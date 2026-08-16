//go:build !aix && !darwin && !dragonfly && !freebsd && !linux && !netbsd && !openbsd && !solaris

package security

import "os"

func openProtected(path string, flags int, mode os.FileMode) (*os.File, error) {
	return os.OpenFile(path, flags, mode.Perm())
}
