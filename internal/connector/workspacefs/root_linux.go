package workspacefs

import (
	"errors"
	"os"
	"path"
	"strconv"
	"strings"

	"golang.org/x/sys/unix"
)

const safeOpenFlags = unix.O_NOFOLLOW | unix.O_NONBLOCK

// Separate helper processes and hardlink aliases share this inode lock.
// It is advisory for unrelated terminal/editor processes; validate rechecks
// their edits and namespace replacement immediately before the first mutation.
func lockFile(file *os.File) error { return unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB) }

// ValidateInstallation rejects mutable root anchors and helper installations.
// Workspace contents may be writable; all ancestors and the helper must be
// root-owned and not writable by group/other. ACL/mount administration remains
// an operator responsibility, as for the VM's SSH server itself.
func ValidateInstallation(directory, executable string) error {
	if directory == "/" || path.Clean(directory) != directory || !strings.HasPrefix(directory, "/") {
		return errors.New("invalid installation")
	}
	if err := trustedPath(path.Dir(directory), false); err != nil {
		return err
	}
	return trustedPath(executable, true)
}
func trustedPath(p string, file bool) error {
	if !strings.HasPrefix(p, "/") || path.Clean(p) != p {
		return errors.New("invalid installation")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return err
	}
	defer func() { unix.Close(fd) }()
	parts := strings.Split(strings.TrimPrefix(p, "/"), "/")
	for i, part := range parts {
		if part == "" {
			continue
		}
		flags := unix.O_RDONLY | unix.O_NOFOLLOW | unix.O_CLOEXEC | unix.O_NONBLOCK
		if !file || i < len(parts)-1 {
			flags |= unix.O_DIRECTORY
		}
		next, e := unix.Openat(fd, part, flags, 0)
		if e != nil {
			return errors.New("untrusted installation")
		}
		unix.Close(fd)
		fd = next
		var st unix.Stat_t
		if unix.Fstat(fd, &st) != nil || st.Uid != 0 || st.Mode&0022 != 0 {
			return errors.New("untrusted installation")
		}
		if file && i == len(parts)-1 && (st.Mode&unix.S_IFMT != unix.S_IFREG || st.Mode&0111 == 0) {
			return errors.New("untrusted installation")
		}
	}
	return nil
}

func openRoot(directory string) (*os.Root, error) {
	if !strings.HasPrefix(directory, "/") || directory == "/" {
		return nil, errors.New("invalid root")
	}
	fd, err := unix.Open("/", unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil, err
	}
	defer func() { unix.Close(fd) }()
	for _, part := range strings.Split(strings.TrimPrefix(directory, "/"), "/") {
		if part == "" || part == "." || part == ".." {
			return nil, errors.New("invalid root")
		}
		next, err := unix.Openat(fd, part, unix.O_RDONLY|unix.O_DIRECTORY|unix.O_NOFOLLOW|unix.O_CLOEXEC, 0)
		if err != nil {
			return nil, err
		}
		unix.Close(fd)
		fd = next
	}
	// Pin the already-open directory; no pathname re-resolution race.
	return os.OpenRoot("/proc/self/fd/" + strconv.Itoa(fd))
}
