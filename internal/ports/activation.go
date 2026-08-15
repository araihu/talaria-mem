package ports

// T2 RED stubs the replaceable contracts while leaving transition behavior
// intentionally incorrect.
type KeyDeriver interface{}
type ManagedFileStore interface{}
type ActivationJournal interface{}

type ActivationPhase string

const (
	ActivationPending ActivationPhase = "pending"
	ActivationActive  ActivationPhase = "active"
)

type ActivationRecord struct {
	Phase ActivationPhase
}

func ValidateActivationTransition(ActivationRecord, ActivationPhase) error { return nil }
