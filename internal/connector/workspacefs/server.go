// Package workspacefs implements the VM-side, rooted SFTP endpoint. A filesystem
// Root, rather than a client-side check/open sequence, enforces confinement.
package workspacefs

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"io"
	"io/fs"
	"os"
	"strings"
	"sync"

	"github.com/pkg/sftp"
)

// Private SSH/SFTP status codes; these never cross the SaaS File protocol.
const (
	InvalidPath      uint32 = 100
	NotAFile         uint32 = 101
	NotADirectory    uint32 = 102
	TooLarge         uint32 = 103
	Unavailable      uint32 = 104
	RevisionConflict uint32 = 105
	MaxListingBytes         = 1 << 20
)

type filesystem struct {
	root     *os.Root
	mu       sync.Mutex
	writers  map[string]*writeHandle
	maxBytes int64
}

// Serve serves one private SFTP stream and closes every handle on disconnect.
// The configured root's parent and helper executable must be administrator-owned.
func Serve(stream io.ReadWriteCloser, directory string) error {
	return ServeWithLimit(stream, directory, 1<<20)
}

func ServeWithLimit(stream io.ReadWriteCloser, directory string, maxBytes int64) error {
	if maxBytes < 1 || maxBytes == int64(^uint64(0)>>1) {
		return errors.New("invalid file limit")
	}
	root, err := openRoot(directory)
	if err != nil {
		return errors.New("workspace root unavailable")
	}
	defer root.Close()
	f := &filesystem{root: root, writers: make(map[string]*writeHandle), maxBytes: maxBytes}
	server := sftp.NewRequestServer(stream, sftp.Handlers{FileGet: f, FilePut: f, FileCmd: f, FileList: f})
	defer server.Close()
	return server.Serve()
}

func status(code uint32) error { return &sftp.StatusError{Code: code} }
func safe(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return sftp.ErrSSHFxNoSuchFile
	case errors.Is(err, fs.ErrPermission):
		return sftp.ErrSSHFxPermissionDenied
	default:
		return status(InvalidPath)
	}
}

// RequestServer presents virtual absolute paths. It is never given VM paths.
func (f *filesystem) name(p string) (string, error) {
	if !strings.HasPrefix(p, "/") || strings.ContainsAny(p, "\\\x00") {
		return "", status(InvalidPath)
	}
	p = strings.TrimPrefix(p, "/")
	if p == "" {
		return ".", nil
	}
	for _, segment := range strings.Split(p, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return "", status(InvalidPath)
		}
	}
	return p, nil
}

func (f *filesystem) checked(p string) (string, os.FileInfo, error) {
	n, err := f.name(p)
	if err != nil {
		return "", nil, err
	}
	var info os.FileInfo
	current := ""
	for _, part := range strings.Split(n, "/") {
		if current != "" {
			current += "/"
		}
		current += part
		info, err = f.root.Lstat(current)
		if err != nil {
			return "", nil, safe(err)
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", nil, status(InvalidPath)
		}
	}
	return n, info, nil
}

func (f *filesystem) open(r *sftp.Request, write bool) (*os.File, error) {
	n, info, err := f.checked(r.Filepath)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, status(NotAFile)
	}
	flags := os.O_RDONLY
	if write {
		p := r.Pflags()
		if p.Creat || p.Trunc || p.Append || p.Excl {
			return nil, sftp.ErrSSHFxOpUnsupported
		}
		flags = os.O_RDWR
	}
	file, err := f.root.OpenFile(n, flags|safeOpenFlags, 0)
	if err != nil {
		return nil, safe(err)
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, status(NotAFile)
	}
	return file, nil
}
func (f *filesystem) Fileread(r *sftp.Request) (io.ReaderAt, error) { return f.open(r, false) }
func (f *filesystem) Filewrite(r *sftp.Request) (io.WriterAt, error) {
	return nil, sftp.ErrSSHFxOpUnsupported
}
func (f *filesystem) OpenFile(r *sftp.Request) (sftp.WriterAtReaderAt, error) {
	file, err := f.open(r, true)
	if err != nil {
		return nil, err
	}
	if err := lockFile(file); err != nil {
		file.Close()
		return nil, status(Unavailable)
	}
	initial, err := fileDigest(file, f.maxBytes)
	if err != nil {
		file.Close()
		return nil, err
	}
	name, err := f.name(r.Filepath)
	if err != nil {
		file.Close()
		return nil, err
	}
	w := &writeHandle{File: file, owner: f, name: r.Filepath, relative: name, initial: initial}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.writers[r.Filepath] != nil {
		file.Close()
		return nil, status(Unavailable)
	}
	f.writers[r.Filepath] = w
	return w, nil
}

type writeHandle struct {
	*os.File
	owner    *filesystem
	name     string
	relative string
	initial  [32]byte
	started  bool
	mu       sync.Mutex
}

func (w *writeHandle) Close() error {
	w.owner.mu.Lock()
	defer w.owner.mu.Unlock()
	if w.owner.writers[w.name] == w {
		delete(w.owner.writers, w.name)
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	var err error
	if w.started {
		err = w.validate(false)
	}
	closeErr := w.File.Close()
	if err != nil {
		return err
	}
	return safe(closeErr)
}

func fileDigest(file *os.File, limit int64) ([32]byte, error) {
	h := sha256.New()
	n, err := io.Copy(h, io.NewSectionReader(file, 0, limit+1))
	if err != nil {
		return [32]byte{}, safe(err)
	}
	if n > limit {
		return [32]byte{}, status(TooLarge)
	}
	var digest [32]byte
	copy(digest[:], h.Sum(nil))
	return digest, nil
}
func (w *writeHandle) validate(compare bool) error {
	current, err := w.owner.root.Lstat(w.relative)
	if err != nil {
		return status(RevisionConflict)
	}
	opened, err := w.File.Stat()
	if err != nil {
		return status(Unavailable)
	}
	if !current.Mode().IsRegular() || !os.SameFile(current, opened) {
		return status(RevisionConflict)
	}
	if compare {
		digest, err := fileDigest(w.File, w.owner.maxBytes)
		if err != nil {
			return err
		}
		if digest != w.initial {
			return status(RevisionConflict)
		}
	}
	return nil
}
func (w *writeHandle) WriteAt(p []byte, offset int64) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if offset < 0 || offset > w.owner.maxBytes || int64(len(p)) > w.owner.maxBytes-offset {
		return 0, status(TooLarge)
	}
	if err := w.validate(!w.started); err != nil {
		return 0, err
	}
	w.started = true
	n, err := w.File.WriteAt(p, offset)
	return n, safe(err)
}
func (w *writeHandle) Truncate(size int64) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if size < 0 || size > w.owner.maxBytes {
		return status(TooLarge)
	}
	if err := w.validate(!w.started); err != nil {
		return err
	}
	w.started = true
	return safe(w.File.Truncate(size))
}
func (f *filesystem) Filecmd(r *sftp.Request) error {
	// FSETSTAT is routed by RequestServer via a pathname. Use the already-open
	// writer, never reopen a possibly replaced pathname for truncation.
	if r.Method != "Setstat" || !r.AttrFlags().Size || r.Flags != 1 {
		return sftp.ErrSSHFxOpUnsupported
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	file := f.writers[r.Filepath]
	if file == nil || r.Attributes().Size > uint64(^uint64(0)>>1) {
		return sftp.ErrSSHFxOpUnsupported
	}
	return file.Truncate(int64(r.Attributes().Size))
}

type listing []os.FileInfo

func (l listing) ListAt(dst []os.FileInfo, offset int64) (int, error) {
	if offset < 0 || offset >= int64(len(l)) {
		return 0, io.EOF
	}
	n := copy(dst, l[offset:])
	if n < len(dst) {
		return n, io.EOF
	}
	return n, nil
}
func (f *filesystem) Lstat(r *sftp.Request) (sftp.ListerAt, error) {
	n, err := f.name(r.Filepath)
	if err != nil {
		return nil, err
	}
	info, err := f.root.Lstat(n)
	if err != nil {
		return nil, safe(err)
	}
	return listing{info}, nil
}
func (f *filesystem) Filelist(r *sftp.Request) (sftp.ListerAt, error) {
	n, info, err := f.checked(r.Filepath)
	if err != nil {
		return nil, err
	}
	if r.Method == "Stat" {
		return listing{info}, nil
	}
	if r.Method != "List" {
		return nil, sftp.ErrSSHFxOpUnsupported
	}
	if !info.IsDir() {
		return nil, status(NotADirectory)
	}
	dir, err := f.root.OpenFile(n, os.O_RDONLY|safeOpenFlags, 0)
	if err != nil {
		return nil, safe(err)
	}
	defer dir.Close()
	actual, err := dir.Stat()
	if err != nil || !actual.IsDir() {
		return nil, status(NotADirectory)
	}
	var result listing
	budget := 2 // JSON array brackets; commas and encoded entries follow.
	for {
		entries, err := dir.ReadDir(128)
		for _, entry := range entries {
			if !CanonicalPath(entry.Name(), false) || strings.Contains(entry.Name(), "/") {
				continue
			}
			// Resolve through Root, never via DirEntry.Info's VM absolute path.
			info, e := f.root.Lstat(n + "/" + entry.Name())
			if e != nil {
				return nil, safe(e)
			}
			if !info.IsDir() && !info.Mode().IsRegular() {
				continue
			}
			kind := "file"
			if info.IsDir() {
				kind = "directory"
			}
			encoded, e := json.Marshal(struct {
				Name string `json:"name"`
				Kind string `json:"kind"`
			}{info.Name(), kind})
			if e != nil {
				return nil, status(InvalidPath)
			}
			if len(result) > 0 {
				budget++
			}
			budget += len(encoded)
			if budget > MaxListingBytes {
				return nil, status(TooLarge)
			}
			result = append(result, info)
		}
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return nil, safe(err)
		}
	}
	return result, nil
}
