package domain

import (
	"encoding/hex"
	"strings"
	"time"
	"unicode/utf8"
)

func ValidateUUIDv7(value string) error {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' {
		return NewError(CodeValidation, "invalid UUIDv7", false)
	}
	compact := strings.ReplaceAll(value, "-", "")
	decoded := make([]byte, 16)
	if _, err := hex.Decode(decoded, []byte(compact)); err != nil {
		return NewError(CodeValidation, "invalid UUIDv7", false)
	}
	if decoded[6]>>4 != 7 || decoded[8]>>6 != 2 {
		return NewError(CodeValidation, "invalid UUIDv7", false)
	}
	return nil
}

func ValidateTimestamp(value time.Time) error {
	if value.IsZero() || value.Location() != time.UTC {
		return NewError(CodeValidation, "timestamp must be nonzero UTC", false)
	}
	return nil
}

func ValidateMemoryRevision(revision MemoryRevision) error {
	if err := ValidateUUIDv7(revision.ID); err != nil {
		return err
	}
	if err := ValidateUUIDv7(revision.MemoryID); err != nil {
		return err
	}
	if revision.Number < 1 {
		return NewError(CodeValidation, "revision number must be positive", false)
	}
	if !revision.Trust.Valid() || !revision.Lifecycle.Valid() {
		return NewError(CodeValidation, "invalid trust or lifecycle", false)
	}
	if _, err := ValidateResolutionState(revision.Kind, revision.ResolutionState); err != nil {
		return err
	}
	if err := ValidateMemoryText(revision.Title, []byte(revision.Content), revision.Tags); err != nil {
		return err
	}
	if err := ValidateProvenance(revision.Provenance); err != nil {
		return err
	}
	return ValidateTimestamp(revision.CreatedAt)
}

// ValidateProvenance applies byte-oriented limits before any provenance value
// is persisted or included in a scanner boundary. Lengths are byte lengths,
// not rune counts, so the limits remain deterministic across clients.
func ValidateProvenance(provenance Provenance) error {
	if len(provenance.Labels) > MaxProvenanceLabels {
		return NewError(CodeValidation, "too many provenance labels", false)
	}
	for _, label := range provenance.Labels {
		if !utf8.ValidString(label) || len(label) > MaxProvenanceLabelBytes {
			return NewError(CodeValidation, "invalid provenance label", false)
		}
	}
	if !utf8.ValidString(provenance.Actor) || !utf8.ValidString(provenance.Source) {
		return NewError(CodeValidation, "invalid provenance", false)
	}
	if !utf8.ValidString(provenance.SourceLocator) || len(provenance.SourceLocator) > MaxSourceLocatorBytes {
		return NewError(CodeValidation, "invalid source locator", false)
	}
	return nil
}
