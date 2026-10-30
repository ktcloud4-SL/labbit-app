package openstackprovider

import (
	"os"
	"syscall"
)

func recoveryOwnedPath(path string, mode os.FileMode, directory bool) bool {
	info, err := os.Lstat(path)
	if err != nil || info.Mode().Perm() != mode || info.IsDir() != directory || (!directory && !info.Mode().IsRegular()) {
		return false
	}
	stat, ok := info.Sys().(*syscall.Stat_t)
	return ok && stat.Uid == uint32(os.Geteuid())
}
