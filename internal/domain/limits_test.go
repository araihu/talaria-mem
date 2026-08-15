package domain

import (
	"testing"
	"time"
)

func TestLimits(t *testing.T) {
	t.Parallel()

	if MaxConcurrentReads != 8 || MaxConcurrentWriters != 1 {
		t.Fatalf("concurrency = %d/%d, want 8/1", MaxConcurrentReads, MaxConcurrentWriters)
	}
	if FTSDeadline != 2*time.Second || MutationDeadline != 5*time.Second {
		t.Fatalf("deadlines = %s/%s, want 2s/5s", FTSDeadline, MutationDeadline)
	}
	if MaxTitleBytes != 256 || MaxContentBytes != 8*1024 || MaxTags != 20 || MaxTagBytes != 64 {
		t.Fatal("memory byte limits drifted")
	}

	if err := ValidateMemoryText("title", make([]byte, MaxContentBytes), nil); err != nil {
		t.Fatalf("exact content byte limit rejected: %v", err)
	}
	if err := ValidateMemoryText("title", make([]byte, MaxContentBytes+1), nil); err == nil {
		t.Fatal("content over byte limit accepted")
	}
}

func TestFTSConfig(t *testing.T) {
	t.Parallel()
	if FTS5Tokenizer != "unicode61 remove_diacritics 2" {
		t.Fatalf("FTS5Tokenizer = %q", FTS5Tokenizer)
	}
	if !FTS5SecureDelete {
		t.Fatal("FTS5 secure-delete disabled")
	}
}
