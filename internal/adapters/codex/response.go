package codex

import (
	"encoding/json"
	"io"
	"strings"
	"unicode/utf8"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

const (
	ResponseVersion  = "talaria.session-start.v1"
	MaxResponseItems = domain.MaxSessionStartItems
	MaxResponseBytes = domain.MaxSessionStartBytes
	MaxItemBytes     = domain.MaxContentBytes

	// These markers make the model-facing value reference data, not an
	// instruction channel. Metadata remains structured beside the delimited
	// content so consumers can display provenance without parsing the body.
	UntrustedReferenceStart = "[[TALARIA-UNTRUSTED-REFERENCE-BEGIN]]"
	UntrustedReferenceEnd   = "[[TALARIA-UNTRUSTED-REFERENCE-END]]"
)

// Response is the bounded SessionStart context envelope. ReceiptID is safe
// metadata used to explain whole-item omission; it never identifies content.
type Response struct {
	Version     string        `json:"version"`
	WorkspaceID string        `json:"workspace_id"`
	Items       []ContextItem `json:"items"`
	Included    int           `json:"included"`
	Omitted     int           `json:"omitted"`
	ReceiptID   string        `json:"receipt_id"`
	Warning     string        `json:"warning,omitempty"`
}

type SessionStartResponse = Response

// ContextItem is an active, verified, non-quarantined memory selected for the
// hook. Content is wrapped by DelimitUntrustedReference before serialization.
type ContextItem struct {
	MemoryID        string                 `json:"memory_id"`
	RevisionID      string                 `json:"revision_id"`
	WorkspaceID     string                 `json:"workspace_id"`
	Scope           string                 `json:"scope"`
	Kind            domain.MemoryKind      `json:"kind"`
	Trust           domain.Trust           `json:"trust"`
	Lifecycle       domain.Lifecycle       `json:"lifecycle"`
	Pinned          bool                   `json:"pinned"`
	ResolutionState domain.ResolutionState `json:"resolution_state,omitempty"`
	Title           string                 `json:"title"`
	Content         string                 `json:"content"`
	Tags            []string               `json:"tags"`
	Score           float64                `json:"startup_score"`
	Provenance      Provenance             `json:"provenance"`
	Untrusted       bool                   `json:"untrusted_reference"`
}

type MemoryItem = ContextItem

type Provenance struct {
	Actor         string   `json:"actor"`
	Source        string   `json:"source"`
	Labels        []string `json:"labels"`
	SourceLocator string   `json:"source_locator"`
}

// DelimitUntrustedReference adds fixed framing around arbitrary memory text.
// It does not alter or redact the text; scanner gating happens before this
// function is called.
func DelimitUntrustedReference(content string) string {
	return UntrustedReferenceStart + "\n" + content + "\n" + UntrustedReferenceEnd
}

// EncodeResponse writes one JSON response and never includes an error string
// in the output. Callers should enforce MaxSessionStartBytes before calling
// this helper.
func EncodeResponse(writer io.Writer, response Response) error {
	if writer == nil {
		return domain.NewError(domain.CodeValidation, "hook output is required", false)
	}
	if err := validResponse(response); err != nil {
		return err
	}
	encoded, err := json.Marshal(response)
	if err != nil {
		return domain.NewError(domain.CodeUnavailable, "hook output unavailable", true)
	}
	if len(encoded) > domain.MaxSessionStartBytes {
		return domain.NewError(domain.CodeValidation, "hook response is too large", false)
	}
	if _, err := writer.Write(encoded); err != nil {
		return domain.NewError(domain.CodeUnavailable, "hook output unavailable", true)
	}
	return nil
}

func responseBytes(response Response) ([]byte, error) {
	encoded, err := json.Marshal(response)
	if err != nil {
		return nil, domain.NewError(domain.CodeUnavailable, "hook response unavailable", true)
	}
	return encoded, nil
}

func validResponse(response Response) error {
	if response.Version != ResponseVersion || response.WorkspaceID == "" || response.ReceiptID == "" {
		return domain.NewError(domain.CodeUnavailable, "hook response unavailable", true)
	}
	if !utf8.ValidString(response.Warning) || len([]byte(response.Warning)) > 512 || strings.ContainsAny(response.Warning, "\x00\r\n") {
		return domain.NewError(domain.CodeUnavailable, "hook response unavailable", true)
	}
	if response.Included != len(response.Items) || response.Included < 0 || response.Included > domain.MaxSessionStartItems || response.Omitted < 0 {
		return domain.NewError(domain.CodeUnavailable, "hook response unavailable", true)
	}
	for _, item := range response.Items {
		if item.MemoryID == "" || item.RevisionID == "" || item.WorkspaceID == "" || item.Scope == "" || !item.Untrusted {
			return domain.NewError(domain.CodeUnavailable, "hook response unavailable", true)
		}
		if strings.TrimSpace(item.Content) == "" || !strings.HasPrefix(item.Content, UntrustedReferenceStart) || !strings.HasSuffix(item.Content, UntrustedReferenceEnd) {
			return domain.NewError(domain.CodeUnavailable, "hook response unavailable", true)
		}
	}
	return nil
}
