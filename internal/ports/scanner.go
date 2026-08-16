package ports

import (
	"context"
	"regexp"
)

// FieldIdentifier is a closed, non-content metadata vocabulary. Scanner
// results may echo this identifier, so arbitrary caller strings are never
// allowed to become diagnostic fields.
type FieldIdentifier string

const (
	FieldTitle           FieldIdentifier = "title"
	FieldContent         FieldIdentifier = "content"
	FieldTag             FieldIdentifier = "tag"
	FieldProvenanceLabel FieldIdentifier = "provenance_label"
	FieldSourceLocator   FieldIdentifier = "source_locator"
	FieldSessionStart    FieldIdentifier = "session_start_read"
	FieldMCPRead         FieldIdentifier = "mcp_read"
	FieldCLIRead         FieldIdentifier = "cli_read"
	FieldExport          FieldIdentifier = "export"
	FieldProjection      FieldIdentifier = "projection"
	FieldSkillPromotion  FieldIdentifier = "skill_promotion"
	FieldLog             FieldIdentifier = "log"
	FieldError           FieldIdentifier = "error"
)

func (identifier FieldIdentifier) Valid() bool {
	switch identifier {
	case FieldTitle, FieldContent, FieldTag, FieldProvenanceLabel,
		FieldSourceLocator, FieldSessionStart, FieldMCPRead, FieldCLIRead,
		FieldExport, FieldProjection, FieldSkillPromotion, FieldLog, FieldError:
		return true
	default:
		return false
	}
}

var safeRuleID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._:-]{0,63}$`)

// ValidateRuleID bounds third-party rule metadata before it can be echoed in
// a result or receipt. It deliberately excludes whitespace and control bytes.
func ValidateRuleID(value string) bool { return safeRuleID.MatchString(value) }

type TextField struct {
	Name  FieldIdentifier
	Value string
}

type ScanStatus string

const (
	ScanClean        ScanStatus = "clean"
	ScanFinding      ScanStatus = "finding"
	ScanUncertain    ScanStatus = "uncertain"
	ScanTimeout      ScanStatus = "timeout"
	ScanPanic        ScanStatus = "panic"
	ScanCancellation ScanStatus = "cancellation"
	ScanError        ScanStatus = "scanner_error"
)

func (status ScanStatus) Valid() bool {
	switch status {
	case ScanClean, ScanFinding, ScanUncertain, ScanTimeout, ScanPanic, ScanCancellation, ScanError:
		return true
	default:
		return false
	}
}

type Finding struct {
	Field  FieldIdentifier
	RuleID string
	Start  int
	End    int
}

type ScanResult struct {
	Status     ScanStatus
	Findings   []Finding
	Generation string
}

type Scanner interface {
	Scan(ctx context.Context, fields []TextField) ScanResult
}
