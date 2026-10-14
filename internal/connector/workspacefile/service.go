// Package workspacefile is the Connector's SFTP Tree/Read/Save boundary. File
// Control/Data WSS assembly belongs to LBT-150; this package never logs content.
package workspacefile

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/ktcloud4-SL/labbit-app/internal/connector/vmaccess"
	"github.com/ktcloud4-SL/labbit-app/internal/connector/workspacefs"
	"github.com/pkg/sftp"
	"golang.org/x/crypto/ssh"
)

type Error string

func (e Error) Error() string { return string(e) }

const (
	ErrNotFound         Error = "NOT_FOUND"
	ErrNotAFile         Error = "NOT_A_FILE"
	ErrNotADirectory    Error = "NOT_A_DIRECTORY"
	ErrTooLarge         Error = "TOO_LARGE"
	ErrRevisionConflict Error = "REVISION_CONFLICT"
	ErrPermissionDenied Error = "PERMISSION_DENIED"
	ErrInvalidPath      Error = "INVALID_PATH"
	ErrUnavailable      Error = "UNAVAILABLE"
	ErrInternal         Error = "INTERNAL_ERROR"
)

// WorkspaceValidator checks the designated Workspace VM independently of the
// caller's selected VM. Provider ownership and host-key pins are checked again
// by Manager.OpenSession for each operation, even with a cached connection.
type WorkspaceValidator interface {
	ValidateWorkspace(context.Context, vmaccess.Target) error
}
type Config struct {
	Root         string
	HelperPath   string
	Timeout      time.Duration
	MaxFileBytes int64
}
type Service struct {
	access    *vmaccess.Manager
	validator WorkspaceValidator
	config    Config
	slots     chan struct{}
	mu        sync.Mutex
	locks     map[lockKey]*fileLock
}
type lockKey struct {
	target vmaccess.Target
	path   string
}
type fileLock struct {
	token chan struct{}
	refs  int
}
type Entry struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}
type File struct {
	Content  []byte
	Revision string
}

func New(access *vmaccess.Manager, validator WorkspaceValidator, config Config) (*Service, error) {
	if access == nil || validator == nil || !validAbsolute(config.Root) || config.Root == "/" || !validAbsolute(config.HelperPath) {
		return nil, ErrUnavailable
	}
	if config.Timeout <= 0 {
		config.Timeout = 30 * time.Second
	}
	if config.MaxFileBytes <= 0 {
		config.MaxFileBytes = 1 << 20
	}
	if config.MaxFileBytes == int64(^uint64(0)>>1) {
		return nil, ErrUnavailable
	}
	return &Service{access: access, validator: validator, config: config, slots: make(chan struct{}, 16), locks: make(map[lockKey]*fileLock)}, nil
}
func validAbsolute(p string) bool {
	return strings.HasPrefix(p, "/") && p != "/" && path.Clean(p) == p && !strings.ContainsAny(p, "\x00\r\n\\")
}

// ValidatePath implements the canonical HTTP path rules without decoding or
// rewriting the caller's path. Empty is the Tree root only.
func ValidatePath(p string, directory bool) error {
	if workspacefs.CanonicalPath(p, directory) {
		return nil
	}
	return ErrInvalidPath
}
func revision(b []byte) string { sum := sha256.Sum256(b); return hex.EncodeToString(sum[:]) }
func validRevision(r string) bool {
	if len(r) < 1 || len(r) > 128 {
		return false
	}
	for _, c := range r {
		if !(c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c == '_' || c == '-') {
			return false
		}
	}
	return true
}

func (s *Service) Tree(ctx context.Context, target vmaccess.Target, p string) ([]Entry, error) {
	if err := ValidatePath(p, true); err != nil {
		return nil, err
	}
	v, err := s.run(ctx, target, p, func(c *sftp.Client) (any, error) {
		if err := checkPath(c, p, true); err != nil {
			return nil, err
		}
		infos, err := c.ReadDir("/" + p)
		if err != nil {
			return nil, classify(err)
		}
		entries := make([]Entry, 0, len(infos))
		for _, info := range infos {
			if strings.Contains(info.Name(), "/") || ValidatePath(info.Name(), false) != nil {
				continue
			}
			kind := "file"
			if info.IsDir() {
				kind = "directory"
			} else if !info.Mode().IsRegular() {
				continue
			}
			entries = append(entries, Entry{Name: info.Name(), Kind: kind})
		}
		sort.Slice(entries, func(i, j int) bool { return entries[i].Name < entries[j].Name })
		encoded, err := json.Marshal(entries)
		if err != nil {
			return nil, ErrInternal
		}
		if len(encoded) > workspacefs.MaxListingBytes {
			return nil, ErrTooLarge
		}
		return entries, nil
	})
	if err != nil {
		return nil, err
	}
	return v.([]Entry), nil
}
func (s *Service) Read(ctx context.Context, target vmaccess.Target, p string, maxBytes int64) (File, error) {
	if err := ValidatePath(p, false); err != nil {
		return File{}, err
	}
	if maxBytes < 1 || maxBytes > s.config.MaxFileBytes {
		return File{}, ErrTooLarge
	}
	v, err := s.run(ctx, target, p, func(c *sftp.Client) (any, error) {
		if err := checkPath(c, p, false); err != nil {
			return nil, err
		}
		f, err := c.Open("/" + p)
		if err != nil {
			return nil, classify(err)
		}
		defer f.Close()
		body, err := readBounded(f, maxBytes)
		if err != nil {
			return nil, err
		}
		return File{Content: body, Revision: revision(body)}, nil
	})
	if err != nil {
		return File{}, err
	}
	return v.(File), nil
}
func (s *Service) Save(ctx context.Context, target vmaccess.Target, p, expected string, content []byte) (string, error) {
	if err := ValidatePath(p, false); err != nil {
		return "", err
	}
	if int64(len(content)) > s.config.MaxFileBytes {
		return "", ErrTooLarge
	}
	if !validRevision(expected) {
		return "", ErrRevisionConflict
	}
	// The operation worker may outlive a canceled caller. Own a copy until all
	// remote writes finish and retain the per-file lock until then.
	body := append([]byte(nil), content...)
	v, err := s.run(ctx, target, p, func(c *sftp.Client) (any, error) {
		if err := checkPath(c, p, false); err != nil {
			return nil, err
		}
		f, err := c.OpenFile("/"+p, os.O_RDWR)
		if err != nil {
			return nil, classify(err)
		}
		defer f.Close()
		current, err := readBounded(f, s.config.MaxFileBytes)
		if err != nil {
			return nil, err
		}
		if revision(current) != expected {
			return nil, ErrRevisionConflict
		}
		// Recheck namespace before the first mutation; all writes/truncation use
		// the same open handle. No CREATE/TRUNC flag runs before the comparison.
		if err := checkPath(c, p, false); err != nil {
			return nil, err
		}
		if _, err = f.Seek(0, io.SeekStart); err != nil {
			return nil, classify(err)
		}
		if n, err := f.Write(body); err != nil || n != len(body) {
			if err == nil {
				err = io.ErrShortWrite
			}
			return nil, classify(err)
		}
		if err := f.Truncate(int64(len(body))); err != nil {
			return nil, classify(err)
		}
		if err := f.Close(); err != nil {
			return nil, classify(err)
		}
		return revision(body), nil
	})
	if err != nil {
		return "", err
	}
	return v.(string), nil
}
func readBounded(r io.Reader, limit int64) ([]byte, error) {
	body, err := io.ReadAll(io.LimitReader(r, limit+1))
	if err != nil {
		return nil, classify(err)
	}
	if int64(len(body)) > limit {
		return nil, ErrTooLarge
	}
	return body, nil
}
func checkPath(c *sftp.Client, p string, directory bool) error {
	current := ""
	parts := strings.Split(p, "/")
	for i, part := range parts {
		current += "/" + part
		info, err := c.Lstat(current)
		if err != nil {
			return classify(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return ErrInvalidPath
		}
		if i < len(parts)-1 || directory {
			if !info.IsDir() {
				return ErrNotADirectory
			}
		} else if !info.Mode().IsRegular() {
			return ErrNotAFile
		}
	}
	return nil
}

func classify(err error) error {
	if err == nil {
		return nil
	}
	var own Error
	if errors.As(err, &own) {
		return own
	}
	if errors.Is(err, os.ErrNotExist) {
		return ErrNotFound
	}
	if errors.Is(err, os.ErrPermission) {
		return ErrPermissionDenied
	}
	var status *sftp.StatusError
	if errors.As(err, &status) {
		switch status.Code {
		case 2:
			return ErrNotFound
		case 3:
			return ErrPermissionDenied
		case workspacefs.InvalidPath:
			return ErrInvalidPath
		case workspacefs.NotAFile:
			return ErrNotAFile
		case workspacefs.NotADirectory:
			return ErrNotADirectory
		case workspacefs.TooLarge:
			return ErrTooLarge
		case workspacefs.RevisionConflict:
			return ErrRevisionConflict
		case 6, 7, workspacefs.Unavailable:
			return ErrUnavailable
		default:
			return ErrInternal
		}
	}
	return ErrUnavailable
}

type result struct {
	value any
	err   error
}

func (s *Service) run(parent context.Context, target vmaccess.Target, p string, operation func(*sftp.Client) (any, error)) (any, error) {
	ctx, cancel := context.WithTimeout(parent, s.config.Timeout)
	defer cancel()
	select {
	case s.slots <- struct{}{}:
	case <-ctx.Done():
		return nil, errors.Join(ErrUnavailable, ctx.Err())
	}
	done := make(chan result, 1)
	go func() {
		defer func() { <-s.slots }()
		unlock, err := s.lock(ctx, lockKey{target: target, path: p})
		if err != nil {
			done <- result{err: err}
			return
		}
		defer unlock()
		if err := s.validator.ValidateWorkspace(ctx, target); err != nil {
			done <- result{err: ErrUnavailable}
			return
		}
		session, err := s.access.OpenSession(ctx, target)
		if err != nil {
			done <- result{err: ErrUnavailable}
			return
		}
		defer session.Close()
		var stdin io.WriteCloser
		var stdout io.Reader
		if err = session.Setup(ctx, func(raw *ssh.Session) error {
			var err error
			stdin, err = raw.StdinPipe()
			if err != nil {
				return err
			}
			stdout, err = raw.StdoutPipe()
			if err != nil {
				return err
			}
			return raw.Start(shellQuote(s.config.HelperPath) + " --root " + shellQuote(s.config.Root) + " --max-bytes " + strconv.FormatInt(s.config.MaxFileBytes, 10))
		}); err != nil {
			done <- result{err: ErrUnavailable}
			return
		}
		recvR, recvW := io.Pipe()
		sendR, sendW := io.Pipe()
		closePipes := func() {
			_ = recvR.Close()
			_ = recvW.Close()
			_ = sendR.Close()
			_ = sendW.Close()
			_ = session.Close()
		}
		stop := context.AfterFunc(ctx, closePipes)
		var bridges sync.WaitGroup
		bridges.Go(func() { defer recvW.Close(); _, _ = io.Copy(recvW, stdout) })
		bridges.Go(func() { defer sendR.Close(); _, _ = io.Copy(stdin, sendR) })
		// Keep the bounded slot and file lock while any SSH bridge remains.
		defer func() { stop(); closePipes(); bridges.Wait() }()
		client, err := sftp.NewClientPipe(recvR, sendW, sftp.UseConcurrentReads(false), sftp.UseConcurrentWrites(false))
		if err != nil {
			done <- result{err: ErrUnavailable}
			return
		}
		defer client.Close()
		value, err := operation(client)
		done <- result{value: value, err: classify(err)}
	}()
	select {
	case r := <-done:
		if ctx.Err() != nil {
			return nil, errors.Join(ErrUnavailable, ctx.Err())
		}
		return r.value, r.err
	case <-ctx.Done():
		return nil, errors.Join(ErrUnavailable, ctx.Err())
	}
}
func shellQuote(p string) string { return "'" + strings.ReplaceAll(p, "'", "'\"'\"'") + "'" }
func (s *Service) lock(ctx context.Context, key lockKey) (func(), error) {
	s.mu.Lock()
	l := s.locks[key]
	if l == nil {
		l = &fileLock{token: make(chan struct{}, 1)}
		s.locks[key] = l
	}
	l.refs++
	s.mu.Unlock()
	release := func() {
		s.mu.Lock()
		l.refs--
		if l.refs == 0 {
			delete(s.locks, key)
		}
		s.mu.Unlock()
	}
	select {
	case l.token <- struct{}{}:
		return func() { <-l.token; release() }, nil
	case <-ctx.Done():
		release()
		return nil, errors.Join(ErrUnavailable, ctx.Err())
	}
}
