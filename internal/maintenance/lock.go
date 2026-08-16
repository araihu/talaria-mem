package maintenance

import (
	"context"
	"errors"
	"os"
	"path/filepath"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"golang.org/x/sys/unix"
)

// Lock is a process-safe, crash-releasing global maintenance lock.  The lock
// inode is retained after Release so a process death never leaves a stale
// marker that blocks recovery; ownership is represented by the kernel flock.
type Lock struct {
	path   string
	file   *os.File
	closed bool
}

func AcquireLock(ctx context.Context, path string) (*Lock, error) {
	if err := contextErr(ctx); err != nil {
		return nil, err
	}
	if path == "" || !filepath.IsAbs(path) || filepath.Clean(path) != path {
		return nil, ErrUnsafePath
	}
	if err := validateManagedParent(path); err != nil {
		return nil, err
	}
	fd, err := unix.Open(path, unix.O_RDWR|unix.O_CREAT|unix.O_CLOEXEC|unix.O_NOFOLLOW, uint32(ManagedFileMode.Perm()))
	if err != nil {
		if errors.Is(err, unix.ELOOP) {
			return nil, ErrUnsafePath
		}
		return nil, errors.Join(ErrLockHeld, domain.NewError(domain.CodeMaintenanceLock, "maintenance lock unavailable", true))
	}
	file := os.NewFile(uintptr(fd), path)
	if file == nil {
		_ = unix.Close(fd)
		return nil, ErrUnsafePath
	}
	closeWithError := func(result error) (*Lock, error) {
		_ = file.Close()
		return nil, result
	}
	info, err := file.Stat()
	if err != nil {
		return closeWithError(ErrUnsafePath)
	}
	if info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || info.Mode().Perm() != ManagedFileMode || !owns(info) {
		return closeWithError(ErrUnsafePath)
	}
	if err := unix.Flock(int(file.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		if errors.Is(err, unix.EWOULDBLOCK) || errors.Is(err, unix.EAGAIN) {
			return closeWithError(errors.Join(ErrLockHeld, domain.NewError(domain.CodeMaintenanceLock, "maintenance lock held", true)))
		}
		return closeWithError(errors.Join(ErrLockHeld, domain.NewError(domain.CodeMaintenanceLock, "maintenance lock unavailable", true)))
	}
	if err := contextErr(ctx); err != nil {
		_ = unix.Flock(int(file.Fd()), unix.LOCK_UN)
		_ = file.Close()
		return nil, err
	}
	return &Lock{path: path, file: file}, nil
}

// NewLock is an explicit constructor for callers that prefer a factory.
func NewLock(ctx context.Context, path string) (*Lock, error) { return AcquireLock(ctx, path) }

func (lock *Lock) Path() string {
	if lock == nil {
		return ""
	}
	return lock.path
}

func (lock *Lock) Release() error {
	if lock == nil || lock.closed {
		return nil
	}
	lock.closed = true
	if lock.file == nil {
		return nil
	}
	unlockErr := unix.Flock(int(lock.file.Fd()), unix.LOCK_UN)
	closeErr := lock.file.Close()
	if unlockErr != nil {
		return unlockErr
	}
	return closeErr
}

func (lock *Lock) Close() error { return lock.Release() }

// WithLock holds the lock for the complete callback, including post-effect
// verification.  It is intentionally not re-entrant: nested maintenance
// operations must share the caller's lock rather than silently bypass it.
func WithLock(ctx context.Context, path string, operation func(context.Context) error) error {
	if operation == nil {
		return ErrUnsafePath
	}
	lock, err := AcquireLock(ctx, path)
	if err != nil {
		return err
	}
	defer lock.Release()
	return operation(ctx)
}
