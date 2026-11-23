//go:build !windows

package drcontrol

import (
	"os"
	"syscall"
)

func protectedFile(f *os.File, _ string) error {
	st, e := f.Stat()
	if e != nil || st.Mode().Perm()&0077 != 0 {
		return ErrDenied
	}
	s, ok := st.Sys().(*syscall.Stat_t)
	if !ok || s.Uid != uint32(os.Geteuid()) {
		return ErrDenied
	}
	return nil
}
