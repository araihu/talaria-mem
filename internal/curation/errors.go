package curation

import "fmt"

type ErrorClass string

const (
	ErrorUnavailable       ErrorClass = "unavailable"
	ErrorTimeout           ErrorClass = "timeout"
	ErrorRateLimit         ErrorClass = "rate_limit"
	ErrorAuthentication    ErrorClass = "authentication"
	ErrorScannerRefusal    ErrorClass = "scanner_refusal"
	ErrorInvalidOutput     ErrorClass = "invalid_output"
	ErrorPolicyRefusal     ErrorClass = "policy_refusal"
	ErrorSuspiciousContent ErrorClass = "suspicious_content"
	ErrorPersistence       ErrorClass = "persistence"
	ErrorDomainValidation  ErrorClass = "domain_validation"
)

func (class ErrorClass) Valid() bool {
	switch class {
	case ErrorUnavailable, ErrorTimeout, ErrorRateLimit, ErrorAuthentication,
		ErrorScannerRefusal, ErrorInvalidOutput, ErrorPolicyRefusal,
		ErrorSuspiciousContent, ErrorPersistence, ErrorDomainValidation:
		return true
	default:
		return false
	}
}

func (class ErrorClass) Retryable() bool {
	switch class {
	case ErrorUnavailable, ErrorTimeout, ErrorRateLimit, ErrorAuthentication:
		return true
	default:
		return false
	}
}

type ProviderError struct {
	Class ErrorClass
	Cause error
}

func NewProviderError(class ErrorClass, cause error) error {
	if !class.Valid() {
		return fmt.Errorf("invalid curation error class %q", class)
	}
	return &ProviderError{Class: class, Cause: cause}
}

func (err *ProviderError) Error() string {
	if err == nil {
		return "<nil>"
	}
	if err.Cause == nil {
		return string(err.Class)
	}
	return fmt.Sprintf("%s: %v", err.Class, err.Cause)
}

func (err *ProviderError) Unwrap() error {
	if err == nil {
		return nil
	}
	return err.Cause
}

var ErrCurationDisabled = fmt.Errorf("automatic curation disabled")
var ErrNoProvider = fmt.Errorf("no curation provider available")
