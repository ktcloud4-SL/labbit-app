package drcontrol

import (
	"io"
	"os"
	"path/filepath"
)

// Trusted executable/config directories remain an operator prerequisite. Refuse
// links and broad permissions on the exact open file; do not chmod user files.
func ReadProtected(path string) ([]byte, error) {
	if !filepath.IsAbs(path) {
		return nil, ErrInput
	}
	before, e := os.Lstat(path)
	if e != nil || !before.Mode().IsRegular() || before.Size() > MaxDocumentBytes {
		return nil, ErrDenied
	}
	f, e := os.Open(path)
	if e != nil {
		return nil, ErrDenied
	}
	defer f.Close()
	after, e := f.Stat()
	if e != nil || !os.SameFile(before, after) || protectedFile(f, path) != nil {
		return nil, ErrDenied
	}
	b, e := io.ReadAll(io.LimitReader(f, MaxDocumentBytes+1))
	if e != nil || len(b) > MaxDocumentBytes {
		return nil, ErrDenied
	}
	return b, nil
}
