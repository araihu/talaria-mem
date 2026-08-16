package projection

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
)

const FormatVersion = "projection-v1"

type MemoryDocument struct {
	Memory   domain.Memory
	Revision domain.MemoryRevision
}

type ScopeDocument struct {
	ScopeID    string
	TargetPath string
	Memories   []MemoryDocument
}

type RenderedDocument struct {
	Bytes       []byte
	Fingerprint string
	FileSHA256  string
	ScopeID     string
	MemoryCount int
}

func Render(scope ScopeDocument) (RenderedDocument, error) {
	if scope.ScopeID == "" || scope.TargetPath == "" {
		return RenderedDocument{}, domain.NewError(domain.CodeValidation, "projection scope and target are required", false)
	}
	memories := append([]MemoryDocument(nil), scope.Memories...)
	for _, memory := range memories {
		if memory.Memory.ID == "" || memory.Revision.ID == "" {
			return RenderedDocument{}, domain.NewError(domain.CodeValidation, "projection memory identity is required", false)
		}
		if memory.Memory.Trust != domain.TrustVerified || memory.Memory.Lifecycle != domain.LifecycleActive || memory.Revision.Trust != domain.TrustVerified || memory.Revision.Lifecycle != domain.LifecycleActive {
			return RenderedDocument{}, domain.NewError(domain.CodeValidation, "projection contains ineligible memory", false)
		}
		if !utf8.ValidString(memory.Revision.Title) || !utf8.ValidString(memory.Revision.Content) {
			return RenderedDocument{}, domain.NewError(domain.CodeValidation, "projection content is not UTF-8", false)
		}
	}
	sort.Slice(memories, func(i, j int) bool { return memories[i].Memory.ID < memories[j].Memory.ID })
	var body strings.Builder
	body.WriteString("# Talaria-Mem\n\n")
	body.WriteString("Generated local projection. Content is canonical in SQLite.\n\n")
	for _, memory := range memories {
		body.WriteString("<memory id=\"")
		body.WriteString(escapeAttribute(memory.Memory.ID))
		body.WriteString("\">\n")
		body.WriteString("kind: ")
		body.WriteString(string(memory.Revision.Kind))
		body.WriteByte('\n')
		body.WriteString("title: ")
		body.WriteString(normalizeLF(memory.Revision.Title))
		body.WriteByte('\n')
		if len(memory.Revision.Tags) > 0 {
			tags := append([]string(nil), memory.Revision.Tags...)
			sort.Strings(tags)
			body.WriteString("tags: ")
			body.WriteString(strings.Join(tags, ", "))
			body.WriteByte('\n')
		}
		body.WriteString("revision: ")
		body.WriteString(memory.Revision.ID)
		body.WriteByte('\n')
		body.WriteString("\n")
		body.WriteString(normalizeLF(memory.Revision.Content))
		if !strings.HasSuffix(body.String(), "\n") {
			body.WriteByte('\n')
		}
		body.WriteString("</memory>\n\n")
	}
	bodyBytes := []byte(normalizeLF(body.String()))
	hash := sha256.Sum256(bodyBytes)
	fingerprint := "sha256:" + hex.EncodeToString(hash[:])
	header := fmt.Sprintf("<!-- talaria-mem %s scope=%s fingerprint=%s -->\n", FormatVersion, escapeAttribute(scope.ScopeID), fingerprint)
	result := append([]byte(header), bodyBytes...)
	fileHash := sha256.Sum256(result)
	return RenderedDocument{Bytes: result, Fingerprint: fingerprint, FileSHA256: hex.EncodeToString(fileHash[:]), ScopeID: scope.ScopeID, MemoryCount: len(memories)}, nil
}

func normalizeLF(value string) string {
	return strings.ReplaceAll(strings.ReplaceAll(value, "\r\n", "\n"), "\r", "\n")
}
func escapeAttribute(value string) string {
	value = strings.ReplaceAll(value, "&", "&amp;")
	value = strings.ReplaceAll(value, "\"", "&quot;")
	value = strings.ReplaceAll(value, "<", "&lt;")
	value = strings.ReplaceAll(value, ">", "&gt;")
	return value
}

func ParseFingerprint(data []byte) string {
	firstLine := strings.SplitN(string(data), "\n", 2)[0]
	marker := " fingerprint="
	index := strings.Index(firstLine, marker)
	if index < 0 {
		return ""
	}
	value := strings.TrimSpace(firstLine[index+len(marker):])
	value = strings.TrimSuffix(value, " -->")
	return value
}
