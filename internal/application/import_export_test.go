package application

import (
	"context"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/guilhermecastro/talaria-mem/internal/domain"
	"github.com/guilhermecastro/talaria-mem/internal/ports"
)

type batchImporterFixture struct {
	calls int
	items []MemoryRecord
	err   error
}

func (fixture *batchImporterFixture) ImportBatch(_ context.Context, _ string, records []MemoryRecord) ([]MutationResult, error) {
	fixture.calls++
	fixture.items = append([]MemoryRecord(nil), records...)
	if fixture.err != nil {
		return nil, fixture.err
	}
	result := make([]MutationResult, len(records))
	for index := range records {
		result[index] = MutationResult{MemoryID: records[index].MemoryID, RevisionID: records[index].RevisionID, WorkspaceID: records[index].WorkspaceID}
	}
	return result, nil
}

type exportSourceFixture struct{ records []MemoryRecord }

func (fixture exportSourceFixture) ListForExport(context.Context, string, bool) ([]MemoryRecord, error) {
	return append([]MemoryRecord(nil), fixture.records...), nil
}

type allowScanner struct{ status ports.ScanStatus }

func (scanner allowScanner) Scan(context.Context, []ports.TextField) ports.ScanResult {
	status := scanner.status
	if status == "" {
		status = ports.ScanClean
	}
	return ports.ScanResult{Status: status, Generation: "test"}
}

type managedStoreFixture struct {
	files map[string][]byte
}

func (store *managedStoreFixture) CreateTemp(_ context.Context, directory, prefix string, _ fs.FileMode) (ports.ManagedTempFile, error) {
	path := filepath.Join(directory, prefix+"tmp")
	return ports.ManagedTempFile{Path: path, Writer: &bufferWriteCloser{store: store, path: path}}, nil
}
func (*managedStoreFixture) SyncFile(context.Context, string) error { return nil }
func (store *managedStoreFixture) ReplaceNoFollow(_ context.Context, temp, target string, _ *ports.FileFingerprint) error {
	store.files[target] = append([]byte(nil), store.files[temp]...)
	delete(store.files, temp)
	return nil
}
func (*managedStoreFixture) SyncParent(context.Context, string) error { return nil }
func (*managedStoreFixture) CleanupStaleTemps(context.Context, string, string, time.Time) error {
	return nil
}
func (store *managedStoreFixture) FingerprintNoFollow(_ context.Context, path string) (ports.FileFingerprint, error) {
	data, ok := store.files[path]
	if !ok {
		return ports.FileFingerprint{}, os.ErrNotExist
	}
	return ports.FileFingerprint{SHA256Hex: contentFingerprint(data), Size: int64(len(data)), Mode: ports.ManagedFileMode}, nil
}

type bufferWriteCloser struct {
	store *managedStoreFixture
	path  string
	data  []byte
}

func (writer *bufferWriteCloser) Write(data []byte) (int, error) {
	writer.data = append(writer.data, data...)
	writer.store.files[writer.path] = append([]byte(nil), writer.data...)
	return len(data), nil
}
func (*bufferWriteCloser) Close() error { return nil }

func importRecordFixture() MemoryRecord {
	return MemoryRecord{Version: MemoryWireVersion, MemoryID: "018f1f61-7b5c-7abc-8def-0123456789ab", RevisionID: "018f1f61-7b5c-7abc-8def-1123456789ab", RevisionNumber: 1, WorkspaceID: "workspace", Kind: domain.MemoryKindProcedure, Title: "procedure", Content: "run command", Tags: []string{"ops"}, Provenance: domain.Provenance{Actor: "cli", Source: "fixture"}, CreatedAt: time.Date(2026, 8, 16, 12, 0, 0, 0, time.UTC)}
}

func TestImportJSONLIsDryRunByDefaultAndBatchAtomic(t *testing.T) {
	record := importRecordFixture()
	payload, err := RenderRecords(ExportJSONL, []MemoryRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	importer := &batchImporterFixture{}
	guard := NewContentOutputGuard(allowScanner{}, nil, nil)
	service := NewImportExportService(importer, nil, guard, nil, nil)
	result, err := service.Import(context.Background(), ImportRequest{WorkspaceID: "workspace", Format: ExportJSONL, Data: payload, DryRun: true})
	if err != nil {
		t.Fatal(err)
	}
	if !result.DryRun || result.Count != 1 || importer.calls != 0 {
		t.Fatalf("dry-run result=%+v calls=%d", result, importer.calls)
	}
	result, err = service.Import(context.Background(), ImportRequest{WorkspaceID: "workspace", Format: ExportJSONL, Data: payload})
	if err != nil {
		t.Fatal(err)
	}
	if result.DryRun || result.Count != 1 || importer.calls != 1 {
		t.Fatalf("apply result=%+v calls=%d", result, importer.calls)
	}
}

func TestImportRejectsStandingInstructionAndScannerFinding(t *testing.T) {
	record := importRecordFixture()
	record.Kind = domain.MemoryKindStandingInstruction
	payload, err := RenderRecords(ExportJSONL, []MemoryRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	service := NewImportExportService(&batchImporterFixture{}, nil, NewContentOutputGuard(allowScanner{}, nil, nil), nil, nil)
	if _, err := service.Import(context.Background(), ImportRequest{WorkspaceID: "workspace", Format: ExportJSONL, Data: payload, DryRun: true}); !domain.IsCode(err, domain.CodeValidation) {
		t.Fatalf("standing instruction error=%v", err)
	}
	record.Kind = domain.MemoryKindProcedure
	payload, err = RenderRecords(ExportJSONL, []MemoryRecord{record})
	if err != nil {
		t.Fatal(err)
	}
	finding := NewImportExportService(&batchImporterFixture{}, nil, NewContentOutputGuard(allowScanner{status: ports.ScanFinding}, nil, nil), nil, nil)
	if _, err := finding.Import(context.Background(), ImportRequest{WorkspaceID: "workspace", Format: ExportJSONL, Data: payload, DryRun: true}); !domain.IsCode(err, domain.CodeSecretRefusal) && !domain.IsCode(err, domain.CodeQuarantine) {
		t.Fatalf("finding error=%v", err)
	}
}

func TestExportMarkdownDeterministicAndManaged(t *testing.T) {
	first := importRecordFixture()
	second := first
	second.MemoryID = "018f1f61-7b5c-7abc-8def-2123456789ab"
	store := &managedStoreFixture{files: make(map[string][]byte)}
	service := NewImportExportService(nil, exportSourceFixture{records: []MemoryRecord{second, first}}, NewContentOutputGuard(allowScanner{}, nil, nil), store, nil)
	result, err := service.Export(context.Background(), ExportRequest{WorkspaceID: "workspace", Format: ExportMarkdown, OutputPath: "/tmp/export.md"})
	if err != nil {
		t.Fatal(err)
	}
	if result.Count != 2 || result.Fingerprint == "" || len(store.files["/tmp/export.md"]) == 0 {
		t.Fatalf("export result=%+v files=%v", result, store.files)
	}
	if !strings.Contains(string(store.files["/tmp/export.md"]), MarkdownHeaderPrefix) {
		t.Fatal("markdown header missing")
	}
	records, err := ParseRecords(ExportMarkdown, store.files["/tmp/export.md"])
	if err != nil || len(records) != 2 || records[0].MemoryID != first.MemoryID {
		t.Fatalf("round-trip records=%+v err=%v", records, err)
	}
}
