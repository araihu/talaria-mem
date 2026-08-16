package scanner

import (
	"bytes"
	"strings"

	"github.com/pelletier/go-toml/v2"
)

// talariaRuleSchema is deliberately smaller than Betterleaks' input model.
// Candidate bytes are decoded into this Talaria-owned schema before they are
// handed to Betterleaks.  In particular, this schema has no extend, path,
// allowlist, expression, or validation fields.  Unknown fields are rejected
// while the bytes are still inert, so ParseTOML cannot read a local extension
// or evaluate a caller-supplied expression as part of validation.
type talariaRuleSchema struct {
	Title                 string                   `toml:"title"`
	Description           string                   `toml:"description"`
	BetterleaksMinVersion string                   `toml:"betterleaksMinVersion"`
	Rules                 []talariaRuleSchemaEntry `toml:"rules"`
}

type talariaRuleSchemaEntry struct {
	ID              string   `toml:"id"`
	Description     string   `toml:"description"`
	Regex           string   `toml:"regex"`
	Keywords        []string `toml:"keywords"`
	SecretGroup     int      `toml:"secretGroup"`
	Entropy         float64  `toml:"entropy"`
	Tags            []string `toml:"tags"`
	Specificity     *int     `toml:"specificity"`
	SkipReport      bool     `toml:"skipReport"`
	TokenEfficiency bool     `toml:"tokenEfficiency"`
}

func validateTalariaRuleSchema(contents []byte) error {
	if len(bytes.TrimSpace(contents)) == 0 {
		return ErrInvalidRules
	}
	var schema talariaRuleSchema
	decoder := toml.NewDecoder(bytes.NewReader(contents))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&schema); err != nil {
		return ErrInvalidRules
	}
	if strings.TrimSpace(schema.BetterleaksMinVersion) != "" && schema.BetterleaksMinVersion != "1.7.4" {
		return ErrInvalidRules
	}
	if len(schema.Rules) == 0 {
		return ErrInvalidRules
	}
	for _, rule := range schema.Rules {
		if strings.TrimSpace(rule.ID) == "" || strings.TrimSpace(rule.Regex) == "" {
			return ErrInvalidRules
		}
	}
	return nil
}
