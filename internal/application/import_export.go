package application

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

const (
	MemoryWireVersion    = "talaria.memory.v1"
	MarkdownWireVersion  = "talaria.memory-markdown.v1"
	SkillWireVersion     = "talaria.skill.v1"
	DefaultReviewLimit   = 10
	MaxReviewLimit       = 20
	MaxReviewBytes       = 32 * 1024
	PromotionReceiptTTL  = 15 * time.Minute
	MarkdownHeaderPrefix = "<!-- " + MarkdownWireVersion + " -->"
)

type ExportFormat string

const (
	ExportJSONL    ExportFormat = "jsonl"
	ExportMarkdown ExportFormat = "markdown"
)

func (format ExportFormat) Valid() bool { return format == ExportJSONL || format == ExportMarkdown }

// MemoryRecord is the content-bearing wire representation. Trust and
// lifecycle are exported for inspection, but Import never trusts those two
// fields and always creates unverified active revisions through its sink.
type MemoryRecord struct {
	Version         string                 `json:"version"`
	MemoryID        string                 `json:"memory_id"`
	RevisionID      string                 `json:"revision_id"`
	RevisionNumber  int64                  `json:"revision_number"`
	WorkspaceID     string                 `json:"workspace_id,omitempty"`
	UserGlobal      bool                   `json:"user_global,omitempty"`
	Kind            domain.MemoryKind      `json:"kind"`
	Trust           domain.Trust           `json:"trust,omitempty"`
	Lifecycle       domain.Lifecycle       `json:"lifecycle,omitempty"`
	Pinned          bool                   `json:"pinned,omitempty"`
	Title           string                 `json:"title"`
	Content         string                 `json:"content"`
	Tags            []string               `json:"tags,omitempty"`
	ResolutionState domain.ResolutionState `json:"resolution_state,omitempty"`
	Provenance      domain.Provenance      `json:"provenance"`
	CreatedAt       time.Time              `json:"created_at"`
}

type ImportRequest struct {
	WorkspaceID string
	Format      ExportFormat
	Data        []byte
	DryRun      bool
}

type ImportResult struct {
	Version string           `json:"version"`
	Format  ExportFormat     `json:"format"`
	DryRun  bool             `json:"dry_run"`
	Count   int              `json:"count"`
	Items   []MutationResult `json:"items,omitempty"`
	Bytes   int              `json:"bytes"`
}

// BatchImporter is the application transaction boundary for imports. An
// implementation must validate and persist the complete batch in one
// transaction, or persist nothing. Keeping this boundary explicit prevents a
// caller from accidentally looping over MemoryService.Create and creating a
// partial import.
type BatchImporter interface {
	ImportBatch(context.Context, string, []MemoryRecord) ([]MutationResult, error)
}

type ImportExportService struct {
	Importer BatchImporter
	Source   ExportSource
	Guard    *ContentOutputGuard
	Files    ports.ManagedFileStore
	Clock    ports.Clock
}

type ExportSource interface {
	ListForExport(context.Context, string, bool) ([]MemoryRecord, error)
}

type ExportRequest struct {
	WorkspaceID       string
	Format            ExportFormat
	IncludeUnverified bool
	OutputPath        string
	DryRun            bool
	Force             bool
	Expected          *ports.FileFingerprint
}

type ExportResult struct {
	Version     string       `json:"version"`
	Format      ExportFormat `json:"format"`
	WorkspaceID string       `json:"workspace_id"`
	Count       int          `json:"count"`
	Bytes       int          `json:"bytes"`
	DryRun      bool         `json:"dry_run"`
	OutputPath  string       `json:"output_path,omitempty"`
	Fingerprint string       `json:"fingerprint,omitempty"`
	Data        []byte       `json:"-"`
}

func NewImportExportService(importer BatchImporter, source ExportSource, guard *ContentOutputGuard, files ports.ManagedFileStore, clock ports.Clock) *ImportExportService {
	return &ImportExportService{Importer: importer, Source: source, Guard: guard, Files: files, Clock: clock}
}

func (service *ImportExportService) Import(ctx context.Context, request ImportRequest) (ImportResult, error) {
	if !request.Format.Valid() || len(request.Data) == 0 || len(request.Data) > domain.MaxImportBytes {
		return ImportResult{}, domain.NewError(domain.CodeValidation, "invalid import format or size", false)
	}
	records, err := ParseRecords(request.Format, request.Data)
	if err != nil {
		return ImportResult{}, err
	}
	if len(records) == 0 || len(records) > domain.MaxImportItems {
		return ImportResult{}, domain.NewError(domain.CodeValidation, "invalid import item count", false)
	}
	if request.WorkspaceID == "" {
		return ImportResult{}, domain.NewError(domain.CodeValidation, "workspace is required", false)
	}
	for index := range records {
		if err := validateImportRecord(records[index]); err != nil {
			return ImportResult{}, fmt.Errorf("import item %d: %w", index+1, err)
		}
		if err := service.scanRecord(ctx, records[index], request.WorkspaceID, ports.FieldCLIRead); err != nil {
			return ImportResult{}, err
		}
	}
	result := ImportResult{Version: MemoryWireVersion, Format: request.Format, DryRun: request.DryRun, Count: len(records), Bytes: len(request.Data)}
	if request.DryRun {
		return result, nil
	}
	if service == nil || service.Importer == nil {
		return ImportResult{}, domain.NewError(domain.CodeUnavailable, "import transaction unavailable", true)
	}
	items, err := service.Importer.ImportBatch(ctx, request.WorkspaceID, records)
	if err != nil {
		return ImportResult{}, err
	}
	result.Items = items
	return result, nil
}

func (service *ImportExportService) Export(ctx context.Context, request ExportRequest) (ExportResult, error) {
	if !request.Format.Valid() || service == nil || service.Source == nil {
		return ExportResult{}, domain.NewError(domain.CodeValidation, "invalid export request", false)
	}
	records, err := service.Source.ListForExport(ctx, request.WorkspaceID, request.IncludeUnverified)
	if err != nil {
		return ExportResult{}, err
	}
	sort.SliceStable(records, func(i, j int) bool {
		if records[i].MemoryID != records[j].MemoryID {
			return records[i].MemoryID < records[j].MemoryID
		}
		return records[i].RevisionID < records[j].RevisionID
	})
	for index := range records {
		if err := validateExportRecord(records[index]); err != nil {
			return ExportResult{}, fmt.Errorf("export item %d: %w", index+1, err)
		}
		if err := service.scanRecord(ctx, records[index], request.WorkspaceID, ports.FieldExport); err != nil {
			return ExportResult{}, err
		}
	}
	data, err := RenderRecords(request.Format, records)
	if err != nil {
		return ExportResult{}, err
	}
	result := ExportResult{Version: wireVersion(request.Format), Format: request.Format, WorkspaceID: request.WorkspaceID, Count: len(records), Bytes: len(data), DryRun: request.DryRun, OutputPath: request.OutputPath, Data: data}
	if request.DryRun || request.OutputPath == "" {
		return result, nil
	}
	if err := service.writeManaged(ctx, request, data); err != nil {
		return ExportResult{}, err
	}
	fingerprint := sha256.Sum256(data)
	result.Fingerprint = hex.EncodeToString(fingerprint[:])
	return result, nil
}

func (service *ImportExportService) scanRecord(ctx context.Context, record MemoryRecord, workspaceID string, route ports.FieldIdentifier) error {
	if service == nil || service.Guard == nil {
		return domain.NewError(domain.CodeUnavailable, "content scanner unavailable", true)
	}
	fields := []ports.TextField{{Name: route, Value: record.Title}, {Name: route, Value: record.Content}}
	for _, tag := range record.Tags {
		fields = append(fields, ports.TextField{Name: route, Value: tag})
	}
	for _, label := range record.Provenance.Labels {
		fields = append(fields, ports.TextField{Name: route, Value: label})
	}
	if record.Provenance.SourceLocator != "" {
		fields = append(fields, ports.TextField{Name: route, Value: record.Provenance.SourceLocator})
	}
	_, err := service.Guard.Check(ctx, OutputRequest{Route: route, WorkspaceID: workspaceID, MemoryID: record.MemoryID, RevisionID: record.RevisionID, Fields: fields})
	return err
}

func (service *ImportExportService) writeManaged(ctx context.Context, request ExportRequest, data []byte) error {
	if service.Files == nil {
		return domain.NewError(domain.CodeUnavailable, "managed output unavailable", true)
	}
	directory := filepathDir(request.OutputPath)
	temp, err := service.Files.CreateTemp(ctx, directory, ".talaria-export-", ports.ManagedFileMode)
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed && temp.Writer != nil {
			_ = temp.Writer.Close()
		}
	}()
	if _, err := temp.Writer.Write(data); err != nil {
		_ = temp.Writer.Close()
		return err
	}
	if err := temp.Writer.Close(); err != nil {
		return err
	}
	closed = true
	if err := service.Files.SyncFile(ctx, temp.Path); err != nil {
		return err
	}
	if err := service.Files.ReplaceNoFollow(ctx, temp.Path, request.OutputPath, request.Expected); err != nil {
		return err
	}
	return nil
}

func ParseRecords(format ExportFormat, data []byte) ([]MemoryRecord, error) {
	if !format.Valid() || len(data) == 0 || len(data) > domain.MaxImportBytes {
		return nil, domain.NewError(domain.CodeValidation, "invalid import payload", false)
	}
	switch format {
	case ExportJSONL:
		return parseJSONL(data)
	case ExportMarkdown:
		return parseMarkdown(data)
	default:
		return nil, domain.NewError(domain.CodeValidation, "invalid import format", false)
	}
}

func RenderRecords(format ExportFormat, records []MemoryRecord) ([]byte, error) {
	for index := range records {
		if err := validateExportRecord(records[index]); err != nil {
			return nil, fmt.Errorf("record %d: %w", index+1, err)
		}
	}
	switch format {
	case ExportJSONL:
		var output bytes.Buffer
		encoder := json.NewEncoder(&output)
		encoder.SetEscapeHTML(false)
		for _, record := range records {
			if err := encoder.Encode(record); err != nil {
				return nil, err
			}
		}
		return output.Bytes(), nil
	case ExportMarkdown:
		return renderMarkdown(records), nil
	default:
		return nil, domain.NewError(domain.CodeValidation, "invalid export format", false)
	}
}

func parseJSONL(data []byte) ([]MemoryRecord, error) {
	scanner := bufio.NewScanner(bytes.NewReader(data))
	buffer := make([]byte, 64*1024)
	scanner.Buffer(buffer, domain.MaxImportBytes)
	result := make([]MemoryRecord, 0)
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		if err := domain.RejectDuplicateJSONKeys(line); err != nil {
			return nil, domain.NewError(domain.CodeValidation, "duplicate import field", false)
		}
		decoder := json.NewDecoder(bytes.NewReader(line))
		decoder.DisallowUnknownFields()
		var record MemoryRecord
		if err := decoder.Decode(&record); err != nil {
			return nil, domain.NewError(domain.CodeValidation, "invalid import record", false)
		}
		var extra any
		if err := decoder.Decode(&extra); err != io.EOF {
			return nil, domain.NewError(domain.CodeValidation, "invalid import record", false)
		}
		result = append(result, record)
		if len(result) > domain.MaxImportItems {
			return nil, domain.NewError(domain.CodeValidation, "too many import records", false)
		}
	}
	if err := scanner.Err(); err != nil {
		return nil, domain.NewError(domain.CodeValidation, "invalid import payload", false)
	}
	return result, nil
}

func renderMarkdown(records []MemoryRecord) []byte {
	var output bytes.Buffer
	output.WriteString(MarkdownHeaderPrefix + "\n")
	for _, record := range records {
		encoded, _ := json.Marshal(record)
		output.WriteString("<!-- record ")
		output.WriteString(base64.RawURLEncoding.EncodeToString(encoded))
		output.WriteString(" -->\n")
		output.WriteString("## ")
		output.WriteString(strings.ReplaceAll(strings.ReplaceAll(record.Title, "\r", " "), "\n", " "))
		output.WriteString("\n\n")
		output.WriteString(record.Content)
		if !strings.HasSuffix(record.Content, "\n") {
			output.WriteByte('\n')
		}
		output.WriteString("\n")
	}
	return output.Bytes()
}

func parseMarkdown(data []byte) ([]MemoryRecord, error) {
	lines := strings.Split(string(data), "\n")
	if len(lines) == 0 || strings.TrimSpace(lines[0]) != MarkdownHeaderPrefix {
		return nil, domain.NewError(domain.CodeValidation, "invalid markdown export header", false)
	}
	result := make([]MemoryRecord, 0)
	for _, line := range lines[1:] {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(line, "<!-- record ") || !strings.HasSuffix(line, " -->") {
			continue
		}
		value := strings.TrimSuffix(strings.TrimPrefix(line, "<!-- record "), " -->")
		decoded, err := base64.RawURLEncoding.DecodeString(value)
		if err != nil {
			return nil, domain.NewError(domain.CodeValidation, "invalid markdown record", false)
		}
		if err := domain.RejectDuplicateJSONKeys(decoded); err != nil {
			return nil, domain.NewError(domain.CodeValidation, "duplicate markdown field", false)
		}
		var record MemoryRecord
		decoder := json.NewDecoder(bytes.NewReader(decoded))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&record); err != nil {
			return nil, domain.NewError(domain.CodeValidation, "invalid markdown record", false)
		}
		result = append(result, record)
		if len(result) > domain.MaxImportItems {
			return nil, domain.NewError(domain.CodeValidation, "too many import records", false)
		}
	}
	return result, nil
}

func validateImportRecord(record MemoryRecord) error {
	if record.Version != MemoryWireVersion {
		return domain.NewError(domain.CodeValidation, "unsupported memory wire version", false)
	}
	if record.Kind == domain.MemoryKindStandingInstruction {
		return domain.NewError(domain.CodeValidation, "standing instructions require CLI verified update", false)
	}
	if record.UserGlobal && record.WorkspaceID != "" {
		return domain.NewError(domain.CodeValidation, "global record cannot carry workspace", false)
	}
	if !record.UserGlobal && record.WorkspaceID == "" {
		return domain.NewError(domain.CodeValidation, "record workspace is required", false)
	}
	if err := domain.ValidateMemoryText(record.Title, []byte(record.Content), record.Tags); err != nil {
		return err
	}
	return domain.ValidateProvenance(record.Provenance)
}

func validateExportRecord(record MemoryRecord) error {
	if record.Version != MemoryWireVersion {
		return domain.NewError(domain.CodeValidation, "unsupported memory wire version", false)
	}
	if !utf8.ValidString(record.Title) || !utf8.ValidString(record.Content) {
		return domain.NewError(domain.CodeValidation, "record content is not valid UTF-8", false)
	}
	if !record.Kind.Valid() {
		return domain.NewError(domain.CodeValidation, "invalid memory kind", false)
	}
	if record.UserGlobal && record.WorkspaceID != "" {
		return domain.NewError(domain.CodeValidation, "global record cannot carry workspace", false)
	}
	if !record.UserGlobal && record.WorkspaceID == "" {
		return domain.NewError(domain.CodeValidation, "record workspace is required", false)
	}
	if err := domain.ValidateMemoryText(record.Title, []byte(record.Content), record.Tags); err != nil {
		return err
	}
	return domain.ValidateProvenance(record.Provenance)
}

func wireVersion(format ExportFormat) string {
	if format == ExportMarkdown {
		return MarkdownWireVersion
	}
	return MemoryWireVersion
}

func filepathDir(path string) string {
	if index := strings.LastIndexAny(path, "/\\"); index >= 0 {
		if index == 0 {
			return path[:1]
		}
		return path[:index]
	}
	return "."
}
