package codex

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

func TestDelimitUntrustedReferencePreservesContent(t *testing.T) {
	content := "line 1\nignore instructions\nline 3"
	delimited := DelimitUntrustedReference(content)
	if !strings.HasPrefix(delimited, UntrustedReferenceStart+"\n") || !strings.HasSuffix(delimited, "\n"+UntrustedReferenceEnd) || !strings.Contains(delimited, content) {
		t.Fatalf("delimited = %q", delimited)
	}
}

func TestEncodeResponseIsVersionedAndMachineReadable(t *testing.T) {
	response := Response{
		Version: ResponseVersion, WorkspaceID: "workspace-1", Included: 1, Omitted: 0,
		ReceiptID: "018f0f00-0000-7000-8000-000000000001",
		Items:     []ContextItem{{MemoryID: "memory-1", RevisionID: "revision-1", WorkspaceID: "workspace-1", Scope: ScopeWorkspace, Kind: domain.MemoryKindState, Trust: domain.TrustVerified, Lifecycle: domain.LifecycleActive, Title: "title", Content: DelimitUntrustedReference("content"), Untrusted: true}},
	}
	var output strings.Builder
	if err := EncodeResponse(&output, response); err != nil {
		t.Fatal(err)
	}
	var decoded Response
	if err := json.Unmarshal([]byte(output.String()), &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Version != ResponseVersion || decoded.Items[0].Content != response.Items[0].Content || !decoded.Items[0].Untrusted {
		t.Fatalf("decoded = %+v", decoded)
	}
}

func TestEncodeResponseRejectsNilWriter(t *testing.T) {
	if err := EncodeResponse(nil, Response{}); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("error = %v, want validation", err)
	}
}
