package workspacefs

import (
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/pkg/sftp"
)

func TestRootRejectsSymlinkAndPinsRenamedDirectory(t *testing.T) {
	f, dir := testFS(t)
	outside := t.TempDir()
	put(t, filepath.Join(outside, "file"), "outside")
	link := filepath.Join(dir, "link")
	if os.Symlink(outside, link) != nil {
		t.Fatal("symlink fixture failed")
	}
	if root, err := openRoot(link); err == nil {
		root.Close()
		t.Fatal("symlink root accepted")
	}
	if _, err := f.Fileread(&sftp.Request{Filepath: "/link/file"}); err == nil {
		t.Fatal("outside symlink accepted")
	}
	// Exercise os.Root's atomic escape rejection rather than only pre-checks.
	if file, err := f.root.Open("link/file"); err == nil {
		file.Close()
		t.Fatal("root escaped")
	} else {
		expectStatus(t, safe(err), InvalidPath)
	}
	put(t, filepath.Join(dir, "file"), "inside")
	moved := dir + "-moved"
	if os.Rename(dir, moved) != nil {
		t.Fatal("root rename fixture failed")
	}
	defer os.RemoveAll(moved)
	if os.Symlink(outside, dir) != nil {
		t.Fatal("root replacement fixture failed")
	}
	defer os.Remove(dir)
	reader, err := f.Fileread(&sftp.Request{Filepath: "/file"})
	if err != nil {
		t.Fatal("pinned root unavailable")
	}
	defer reader.(io.Closer).Close()
	body := make([]byte, 6)
	if _, err := reader.ReadAt(body, 0); err != nil || string(body) != "inside" {
		t.Fatal("root pin followed replacement")
	}
}
func TestSymlinkSwapNeverReadsOutsideRoot(t *testing.T) {
	f, dir := testFS(t)
	outside := t.TempDir()
	put(t, filepath.Join(outside, "file"), "outside")
	if os.Mkdir(filepath.Join(dir, "race"), 0700) != nil {
		t.Fatal("directory fixture failed")
	}
	put(t, filepath.Join(dir, "race", "file"), "inside")
	stop := make(chan struct{})
	var wg sync.WaitGroup
	wg.Go(func() {
		for {
			select {
			case <-stop:
				return
			default:
			}
			if os.Rename(filepath.Join(dir, "race"), filepath.Join(dir, "park")) != nil {
				continue
			}
			_ = os.Symlink(outside, filepath.Join(dir, "race"))
			_ = os.Remove(filepath.Join(dir, "race"))
			_ = os.Rename(filepath.Join(dir, "park"), filepath.Join(dir, "race"))
		}
	})
	defer func() { close(stop); wg.Wait() }()
	for range 1000 {
		reader, err := f.Fileread(&sftp.Request{Filepath: "/race/file"})
		if err != nil {
			continue
		}
		b := make([]byte, 16)
		n, _ := reader.ReadAt(b, 0)
		reader.(io.Closer).Close()
		if string(b[:n]) != "inside" {
			t.Fatal("symlink swap escaped Workspace root")
		}
	}
}
func TestSeparateHelpersCannotOpenConcurrentWriters(t *testing.T) {
	f, dir := testFS(t)
	put(t, filepath.Join(dir, "file"), "before")
	w := openWriter(t, f)
	defer w.Close()
	root, err := openRoot(dir)
	if err != nil {
		t.Fatal("second root failed")
	}
	defer root.Close()
	second := &filesystem{root: root, writers: make(map[string]*writeHandle), maxBytes: 1 << 20}
	_, err = second.OpenFile(&sftp.Request{Filepath: "/file", Flags: 3})
	expectStatus(t, err, Unavailable)
}
