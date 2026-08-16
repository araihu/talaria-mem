package scanner

import "errors"

var (
	ErrUnsafeRules       = errors.New("scanner rules enable an unsafe feature")
	ErrInvalidRules      = errors.New("scanner rules are invalid")
	ErrComparativeFailed = errors.New("scanner comparative fixture failed")
)
