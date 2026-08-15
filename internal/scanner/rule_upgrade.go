package scanner

// CandidateRules is the T4 RED boundary value.
type CandidateRules struct{}

// LoadCandidateRules intentionally accepts an uncertain candidate in RED.
func LoadCandidateRules([]byte, string) (CandidateRules, error) { return CandidateRules{}, nil }
