package ports

import "context"

type TextField struct {
	Name  string
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
	Field  string
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
