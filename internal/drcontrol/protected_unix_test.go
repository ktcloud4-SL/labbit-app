//go:build !windows

package drcontrol

import (
	"os"
	"path/filepath"
	"testing"
)

func protectTestFile(t *testing.T, path string) {
	t.Helper()
	if os.Chmod(path, 0600) != nil {
		t.Fatal("protect test file")
	}
}
func TestProtectedUnixRejectsBroadAndLinkedFiles(t *testing.T) {
	path := filepath.Join(t.TempDir(), "file")
	if os.WriteFile(path, []byte("fixture"), 0644) != nil {
		t.Fatal("fixture")
	}
	if _, e := ReadProtected(path); e == nil {
		t.Fatal("broad read accepted")
	}
	protectTestFile(t, path)
	if _, e := ReadProtected(path); e != nil {
		t.Fatal(e)
	}
	link := path + ".link"
	if os.Symlink(path, link) != nil {
		t.Fatal("link fixture")
	}
	if _, e := ReadProtected(link); e == nil {
		t.Fatal("symlink accepted")
	}
}
