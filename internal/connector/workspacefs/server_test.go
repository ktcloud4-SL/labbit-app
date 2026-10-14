package workspacefs

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"

	"github.com/pkg/sftp"
)

func testFS(t *testing.T) (*filesystem, string) {
	t.Helper()
	directory := t.TempDir()
	root, err := openRoot(directory)
	if err != nil {
		t.Fatal("root fixture unavailable")
	}
	t.Cleanup(func() { root.Close() })
	return &filesystem{root: root, writers: make(map[string]*writeHandle), maxBytes: 1 << 20}, directory
}

func TestStorageErrorsAreSafeFailures(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.ENOSPC, syscall.EIO, syscall.EFBIG, syscall.EDQUOT, syscall.EBADF} {
		raw := &os.PathError{Op: "write", Path: "sensitive-vm-path", Err: errno}
		if err := safe(raw); !errors.Is(err, sftp.ErrSSHFxFailure) || strings.Contains(err.Error(), raw.Path) {
			t.Fatal("storage failure was mislabeled as a path rejection or exposed a path")
		}
	}
}
func put(t *testing.T, name, body string) {
	t.Helper()
	if os.WriteFile(name, []byte(body), 0600) != nil {
		t.Fatal("fixture write failed")
	}
}
func expectStatus(t *testing.T, err error, code uint32) {
	t.Helper()
	var status *sftp.StatusError
	if !errors.As(err, &status) || status.Code != code {
		t.Fatal("unexpected safe status")
	}
}
func openWriter(t *testing.T, f *filesystem) *writeHandle {
	t.Helper()
	writer, err := f.OpenFile(&sftp.Request{Filepath: "/file", Flags: 3})
	if err != nil {
		t.Fatal("writer setup failed")
	}
	return writer.(*writeHandle)
}

func TestRegularFileReplacementNeverReportsSaveSuccess(t *testing.T) {
	f, dir := testFS(t)
	put(t, filepath.Join(dir, "file"), "before")
	w := openWriter(t, f)
	defer w.Close()
	buf := make([]byte, 6)
	if _, err := w.ReadAt(buf, 0); err != nil {
		t.Fatal("initial read failed")
	}
	put(t, filepath.Join(dir, "replacement"), "replacement")
	if err := os.Rename(filepath.Join(dir, "file"), filepath.Join(dir, "old")); err != nil {
		t.Fatal("fixture rename failed")
	}
	if err := os.Rename(filepath.Join(dir, "replacement"), filepath.Join(dir, "file")); err != nil {
		t.Fatal("fixture replacement failed")
	}
	_, err := w.WriteAt([]byte("new"), 0)
	expectStatus(t, err, RevisionConflict)
	expectStatus(t, w.Truncate(0), RevisionConflict)
	body, err := os.ReadFile(filepath.Join(dir, "file"))
	if err != nil || string(body) != "replacement" {
		t.Fatal("replacement was modified")
	}
	body, err = os.ReadFile(filepath.Join(dir, "old"))
	if err != nil || string(body) != "before" {
		t.Fatal("unlinked original was modified")
	}
}
func TestInPlaceEditBeforeFirstWriteConflicts(t *testing.T) {
	f, dir := testFS(t)
	put(t, filepath.Join(dir, "file"), "before")
	w := openWriter(t, f)
	defer w.Close()
	put(t, filepath.Join(dir, "file"), "terminal-edit")
	_, err := w.WriteAt([]byte("new"), 0)
	expectStatus(t, err, RevisionConflict)
	body, _ := os.ReadFile(filepath.Join(dir, "file"))
	if string(body) != "terminal-edit" {
		t.Fatal("external edit overwritten")
	}
}
func TestReplacementAfterWriteFailsAtCloseAndTruncate(t *testing.T) {
	f, dir := testFS(t)
	put(t, filepath.Join(dir, "file"), "before")
	w := openWriter(t, f)
	if _, err := w.WriteAt([]byte("new"), 0); err != nil {
		t.Fatal("initial write failed")
	}
	if os.Rename(filepath.Join(dir, "file"), filepath.Join(dir, "old")) != nil {
		t.Fatal("rename failed")
	}
	put(t, filepath.Join(dir, "file"), "replacement")
	expectStatus(t, w.Truncate(0), RevisionConflict)
	expectStatus(t, w.Close(), RevisionConflict)
	body, _ := os.ReadFile(filepath.Join(dir, "file"))
	if string(body) != "replacement" {
		t.Fatal("replacement truncated")
	}
}
func TestTreeBudgetsAllowedJSONEntries(t *testing.T) {
	for _, tc := range []struct {
		name              string
		count, width      int
		invalid, tooLarge bool
	}{
		{name: "large-valid-under-limit", count: 3000, width: 50},
		{name: "invalid-names-are-excluded", count: 3000, width: 80, invalid: true},
		{name: "actual-json-exceeds-limit", count: 5000, width: 200, tooLarge: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f, dir := testFS(t)
			for i := 0; i < tc.count; i++ {
				name := fmt.Sprintf("%06d", i) + strings.Repeat("a", tc.width-6)
				if tc.invalid {
					name = "%2f" + name
				}
				put(t, filepath.Join(dir, name), "")
			}
			l, err := f.Filelist(&sftp.Request{Method: "List", Filepath: "/"})
			if tc.tooLarge {
				expectStatus(t, err, TooLarge)
				return
			}
			if err != nil {
				t.Fatal("permitted Tree failed")
			}
			items := make([]os.FileInfo, tc.count+1)
			n, err := l.ListAt(items, 0)
			if err != nil && !errors.Is(err, io.EOF) {
				t.Fatal("listing failed")
			}
			if tc.invalid {
				if n != 0 {
					t.Fatal("invalid names returned")
				}
				return
			}
			if n != tc.count {
				t.Fatal("listing was truncated")
			}
			entries := make([]struct {
				Name string `json:"name"`
				Kind string `json:"kind"`
			}, n)
			for i, item := range items[:n] {
				entries[i].Name = item.Name()
				entries[i].Kind = "file"
			}
			encoded, err := json.Marshal(entries)
			if err != nil || len(encoded) >= MaxListingBytes {
				t.Fatal("fixture did not remain within JSON limit")
			}
		})
	}
}
