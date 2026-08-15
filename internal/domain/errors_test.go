package domain

import (
	"errors"
	"testing"
)

type sqliteError struct{ code int }

func (err sqliteError) Error() string { return "unsafe backend detail" }
func (err sqliteError) Code() int     { return err.code }

func TestSQLiteFull(t *testing.T) {
	t.Parallel()

	err := MapSQLiteError(sqliteError{code: 13})
	if !IsCode(err, CodeStorageFull) {
		t.Fatalf("MapSQLiteError(SQLITE_FULL) = %v", err)
	}
	if IsRetryable(err) {
		t.Fatal("SQLITE_FULL marked retryable")
	}
	if errors.Is(err, sqliteError{code: 13}) {
		t.Fatal("storage-full exposes backend error identity")
	}
	if got := err.Error(); got != "storage capacity exhausted" {
		t.Fatalf("safe diagnostic = %q", got)
	}
}

func TestDuplicateJSONKeysRejected(t *testing.T) {
	t.Parallel()
	if err := RejectDuplicateJSONKeys([]byte(`{"id":"one","id":"two"}`)); err == nil {
		t.Fatal("duplicate JSON key accepted")
	}
	if err := RejectDuplicateJSONKeys([]byte(`{"outer":{"id":"one","id":"two"}}`)); err == nil {
		t.Fatal("nested duplicate JSON key accepted")
	}
	if err := RejectDuplicateJSONKeys([]byte(`{"id":"one","nested":[{"id":"two"}]}`)); err != nil {
		t.Fatalf("distinct-object keys rejected: %v", err)
	}
}
