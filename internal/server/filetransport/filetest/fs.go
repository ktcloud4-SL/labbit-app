// Package filetest는 Workspace File transport 계약(contracts/connector/README.md §7a)을 따르는 contract peer(fake Connector)다.
// 실제 WebSocket으로 SaaS의 Connector Control WSS에 HELLO(capability 포함)를 보내고, FILE_OPEN을 받으면 File Data WSS를 열어
// 메모리 안의 FS로 Tree/Read/Save에 응답한다. 실제 SSH/SFTP, VM filesystem root, symlink containment는 구현하지 않는다(LBT-21 범위).
//
// 이 peer가 통과시킨 test는 SaaS가 계약대로 동작한다는 증거이지 실제 Workspace VM이나 SFTP가 동작한다는 증거가 아니다.
// test가 계약을 어기는 Connector를 흉내 낼 수 있도록 Behavior로 frame을 바꿀 수 있다.
package filetest

import (
	"crypto/sha256"
	"encoding/hex"
	"sort"
	"strings"
	"sync"
)

// FS는 peer가 Workspace VM 대신 쓰는 메모리 파일 시스템이다. goroutine에 안전하다.
type FS struct {
	mu    sync.Mutex
	files map[string][]byte
	dirs  map[string]bool
}

// NewFS는 root 디렉터리만 있는 FS를 만든다.
func NewFS() *FS {
	return &FS{files: map[string][]byte{}, dirs: map[string]bool{"": true}}
}

// Put은 파일을 만들거나 교체한다. 상위 디렉터리를 모두 만든다.
func (f *FS) Put(path string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mkdirLocked(parent(path))
	f.files[path] = append([]byte(nil), content...)
}

// Mkdir은 디렉터리와 그 상위 디렉터리를 만든다.
func (f *FS) Mkdir(path string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.mkdirLocked(path)
}

func (f *FS) mkdirLocked(path string) {
	for path != "" {
		f.dirs[path] = true
		path = parent(path)
	}
}

func parent(path string) string {
	i := strings.LastIndexByte(path, '/')
	if i < 0 {
		return ""
	}
	return path[:i]
}

// Content는 파일의 현재 본문이다. 없으면 ok가 false다.
func (f *FS) Content(path string) ([]byte, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	content, ok := f.files[path]
	return append([]byte(nil), content...), ok
}

// Revision은 파일 한 시점의 opaque revision이다. 본문의 digest이며 계약상 SaaS는 해석하지 않는다.
func Revision(content []byte) string {
	sum := sha256.Sum256(content)
	return hex.EncodeToString(sum[:16])
}

// RevisionOf는 파일의 현재 revision이다. 없으면 빈 문자열이다.
func (f *FS) RevisionOf(path string) string {
	content, ok := f.Content(path)
	if !ok {
		return ""
	}
	return Revision(content)
}

type kind int

const (
	kindMissing kind = iota
	kindFile
	kindDirectory
)

func (f *FS) kindOf(path string) kind {
	if _, ok := f.files[path]; ok {
		return kindFile
	}
	if f.dirs[path] {
		return kindDirectory
	}
	return kindMissing
}

// entry는 디렉터리 항목 하나다.
type entry struct {
	Name string `json:"name"`
	Kind string `json:"kind"`
}

// failure는 FAILED 결과의 error.code다.
type failure string

const (
	failNotFound         failure = "NOT_FOUND"
	failNotAFile         failure = "NOT_A_FILE"
	failNotADirectory    failure = "NOT_A_DIRECTORY"
	failTooLarge         failure = "TOO_LARGE"
	failRevisionConflict failure = "REVISION_CONFLICT"
	failInvalidPath      failure = "INVALID_PATH"
)

// canonical은 Connector의 심층 방어다. SaaS가 canonical하지 않은 경로를 보내면 root에 붙이지 않고 거절한다.
func canonical(path string, allowRoot bool) bool {
	if path == "" {
		return allowRoot
	}
	if strings.HasPrefix(path, "/") || strings.HasSuffix(path, "/") || strings.ContainsAny(path, "\\\x00") {
		return false
	}
	for _, segment := range strings.Split(path, "/") {
		if segment == "" || segment == "." || segment == ".." {
			return false
		}
	}
	return true
}

func (f *FS) tree(path string) ([]entry, failure) {
	if !canonical(path, true) {
		return nil, failInvalidPath
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.kindOf(path) {
	case kindMissing:
		return nil, failNotFound
	case kindFile:
		return nil, failNotADirectory
	}
	prefix := path
	if prefix != "" {
		prefix += "/"
	}
	seen := map[string]entry{}
	for name := range f.files {
		if rest, ok := strings.CutPrefix(name, prefix); ok && !strings.Contains(rest, "/") {
			seen[rest] = entry{Name: rest, Kind: "file"}
		}
	}
	for name := range f.dirs {
		if name == "" {
			continue
		}
		if rest, ok := strings.CutPrefix(name, prefix); ok && !strings.Contains(rest, "/") {
			seen[rest] = entry{Name: rest, Kind: "directory"}
		}
	}
	out := make([]entry, 0, len(seen))
	for _, e := range seen {
		out = append(out, e)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, ""
}

func (f *FS) read(path string, maxBytes int64) ([]byte, string, failure) {
	if !canonical(path, false) {
		return nil, "", failInvalidPath
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.kindOf(path) {
	case kindMissing:
		return nil, "", failNotFound
	case kindDirectory:
		return nil, "", failNotAFile
	}
	content := f.files[path]
	if int64(len(content)) > maxBytes {
		return nil, "", failTooLarge
	}
	return append([]byte(nil), content...), Revision(content), ""
}

// save는 현재 revision이 expected와 같을 때만 교체한다. 파일을 만들지 않는다. 비교와 쓰기는 하나의 잠금 안에서 일어난다.
func (f *FS) save(path, expected string, content []byte) (string, failure) {
	if !canonical(path, false) {
		return "", failInvalidPath
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	switch f.kindOf(path) {
	case kindMissing:
		return "", failNotFound
	case kindDirectory:
		return "", failNotAFile
	}
	if Revision(f.files[path]) != expected {
		return "", failRevisionConflict
	}
	f.files[path] = append([]byte(nil), content...)
	return Revision(content), ""
}
