package domain

import (
	"bytes"
	"encoding/json"
	"sort"
	"time"
	"unicode/utf8"

	"golang.org/x/text/unicode/norm"
)

const (
	MaxConcurrentReads   = 8
	MaxConcurrentWriters = 1

	MaxTitleBytes             = 256
	MaxContentBytes           = 8 * 1024
	MaxTags                   = 20
	MaxTagBytes               = 64
	MaxProvenanceLabels       = 10
	MaxProvenanceLabelBytes   = 128
	MaxSourceLocatorBytes     = 512
	MaxQueryBytes             = 1024
	MaxHTTPRequestBodyBytes   = 1024 * 1024
	MaxImportBytes            = 10 * 1024 * 1024
	MaxImportItems            = 1000
	MaxPinnedItemsPerScope    = 5
	MaxPinnedContentPerScope  = 8 * 1024
	MaxSessionStartItems      = 20
	MaxSessionStartBytes      = 32 * 1024
	MaxSearchItems            = 20
	MaxSearchBytes            = 64 * 1024
	IdempotencyRetentionHours = 24

	NormalizationVersion = "normalization-v1"
	FTS5Tokenizer        = "unicode61 remove_diacritics 2"
	FTS5SecureDelete     = true
)

const (
	FTSDeadline      = 2 * time.Second
	MutationDeadline = 5 * time.Second
	IdempotencyTTL   = 24 * time.Hour
)

type FTSConfig struct {
	Tokenizer    string
	SecureDelete bool
}

func CanonicalFTSConfig() FTSConfig {
	return FTSConfig{Tokenizer: FTS5Tokenizer, SecureDelete: FTS5SecureDelete}
}

func ValidateMemoryText(title string, content []byte, tags []string) error {
	if !utf8.ValidString(title) || len(title) > MaxTitleBytes {
		return NewError(CodeValidation, "invalid title", false)
	}
	if !utf8.Valid(content) || len(content) > MaxContentBytes {
		return NewError(CodeValidation, "invalid content", false)
	}
	if len(tags) > MaxTags {
		return NewError(CodeValidation, "too many tags", false)
	}
	for _, tag := range tags {
		if !utf8.ValidString(tag) || len(tag) > MaxTagBytes {
			return NewError(CodeValidation, "invalid tag", false)
		}
	}
	return nil
}

func NormalizeV1(kind, title, content string, tags []string) (string, error) {
	memoryKind := MemoryKind(kind)
	if !memoryKind.Valid() {
		return "", NewError(CodeValidation, "invalid memory kind", false)
	}
	if !utf8.ValidString(title) || !utf8.ValidString(content) {
		return "", NewError(CodeValidation, "normalization requires valid UTF-8", false)
	}
	normalizedTags := make([]string, len(tags))
	for index, tag := range tags {
		if !utf8.ValidString(tag) {
			return "", NewError(CodeValidation, "normalization requires valid UTF-8", false)
		}
		normalizedTags[index] = normalizeV1Text(tag)
	}
	sort.Strings(normalizedTags)
	value := struct {
		Version string   `json:"version"`
		Kind    string   `json:"kind"`
		Title   string   `json:"title"`
		Content string   `json:"content"`
		Tags    []string `json:"tags"`
	}{
		Version: NormalizationVersion,
		Kind:    kind,
		Title:   normalizeV1Text(title),
		Content: normalizeV1Text(content),
		Tags:    normalizedTags,
	}
	encoded, err := json.Marshal(value)
	if err != nil {
		return "", NewError(CodeValidation, "normalization failed", false)
	}
	return string(encoded), nil
}

func normalizeV1Text(value string) string {
	value = string(bytes.ReplaceAll([]byte(value), []byte("\r\n"), []byte("\n")))
	value = string(bytes.ReplaceAll([]byte(value), []byte("\r"), []byte("\n")))
	return norm.NFC.String(value)
}
