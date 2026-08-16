//go:build aix || darwin || dragonfly || freebsd || linux || netbsd || openbsd || solaris

package filesystem

import (
	"os"

	"golang.org/x/sys/unix"
)

func openManaged(path string, flags int, mode os.FileMode) (*os.File, error) {
	fd, err := unix.Open(path, flags|unix.O_NOFOLLOW, uint32(mode.Perm()))
	if err != nil {
		return nil, err
	}
	return os.NewFile(uintptr(fd), path), nil
}
