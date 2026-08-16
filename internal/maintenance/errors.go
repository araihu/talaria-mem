package maintenance

import (
	"context"
	"errors"
	"fmt"
)

// The maintenance package deliberately keeps its error vocabulary small.  A
// caller may log the error code and operation ID, but never needs to inspect a
// path, database byte, or scanner match to decide whether an operation can be
// retried.
var (
	ErrReceiptInvalid       = errors.New("maintenance receipt is invalid")
	ErrReceiptExpired       = errors.New("maintenance receipt is expired")
	ErrReceiptUsed          = errors.New("maintenance receipt has already been consumed")
	ErrReceiptDrift         = errors.New("maintenance receipt target changed")
	ErrUnsafePath           = errors.New("maintenance path is unsafe")
	ErrLockHeld             = errors.New("maintenance lock is held")
	ErrJournalConflict      = errors.New("maintenance journal compare-and-swap conflict")
	ErrJournalInvalid       = errors.New("maintenance journal transition is invalid")
	ErrScannerUnavailable   = errors.New("scanner unavailable")
	ErrMigrationRejected    = errors.New("migration contains non-transactional maintenance")
	ErrMigrationUnready     = errors.New("migration left storage unready")
	ErrIntegrityFailed      = errors.New("maintenance integrity check failed")
	ErrInventoryUnready     = errors.New("backup inventory is not ready")
	ErrOperationIncomplete  = errors.New("maintenance operation is incomplete")
	ErrOperationAlreadyDone = errors.New("maintenance operation is already complete")
)

// SafeError is a bounded non-content diagnostic suitable for a journal.  It
// intentionally drops wrapped filesystem/database details: those details may
// include a path supplied by a caller or a driver message containing data.
func SafeError(err error) string {
	if err == nil {
		return ""
	}
	// Never persist the wrapped error text.  Driver and scanner errors are not
	// guaranteed to be content-free, so journals receive only this stable
	// category even when a test double returns a canary string.
	if errors.Is(err, context.Canceled) {
		return "operation canceled"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "operation timed out"
	}
	return "maintenance operation failed"
}

func maintenanceError(err error) error {
	if err == nil {
		return nil
	}
	return fmt.Errorf("maintenance: %w", err)
}
