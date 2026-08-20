package lifecycle

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"sort"
	"strings"

	"github.com/guilhermecastro/talaria-mem/internal/security"
)

type PathDiagnostic struct {
	Path      string      `json:"path"`
	Exists    bool        `json:"exists"`
	OwnerOK   bool        `json:"owner_ok"`
	Mode      fs.FileMode `json:"mode"`
	ModeOK    bool        `json:"mode_ok"`
	Symlink   bool        `json:"symlink"`
	Regular   bool        `json:"regular"`
	Directory bool        `json:"directory"`
	Safe      bool        `json:"safe"`
	Size      int64       `json:"size,omitempty"`
	Error     string      `json:"error,omitempty"`
}

type DoctorReport struct {
	Version   string           `json:"version"`
	Healthy   bool             `json:"healthy"`
	Readiness ReadinessReport  `json:"readiness"`
	Paths     []PathDiagnostic `json:"paths"`
	FTS       *FTSReport       `json:"fts,omitempty"`
	Curation  CurationHealth   `json:"curation"`
}

type FTSReport struct {
	Tokenizer      string `json:"tokenizer"`
	SecureDelete   bool   `json:"secure_delete"`
	ExpectedRows   int64  `json:"expected_rows"`
	ActualRows     int64  `json:"actual_rows"`
	ExpectedHash   string `json:"expected_hash,omitempty"`
	ActualHash     string `json:"actual_hash,omitempty"`
	RowsMatch      bool   `json:"rows_match"`
	HashesMatch    bool   `json:"hashes_match"`
	RepairRequired bool   `json:"repair_required"`
}

type FTSRepairer interface {
	Compare(context.Context) (FTSReport, error)
	Repair(context.Context) error
}

type FTSRepairReceiptStore interface {
	Create(context.Context, FTSReport) (string, error)
	Consume(context.Context, string, FTSReport) error
}

type DoctorConfig struct {
	Environment  Environment
	Readiness    *Readiness
	FTS          FTSRepairer
	Receipts     FTSRepairReceiptStore
	RootKeyPath  string
	TokenPath    string
	DatabasePath string
	WALPath      string
	SHMPath      string
	Curation     CurationHealthSource
}

type Doctor struct{ config DoctorConfig }

func NewDoctor(configuration DoctorConfig) *Doctor { return &Doctor{config: configuration} }

func (doctor *Doctor) Check(ctx context.Context) (DoctorReport, error) {
	if doctor == nil {
		return DoctorReport{}, errors.New("doctor unavailable")
	}
	report := DoctorReport{Version: "talaria.doctor.v1", Healthy: true, Paths: make([]PathDiagnostic, 0, 8)}
	if doctor.config.Readiness != nil {
		readiness, err := doctor.config.Readiness.Check(ctx)
		if err != nil {
			return DoctorReport{}, err
		}
		report.Readiness = readiness
		if !readiness.Ready {
			report.Healthy = false
		}
	}
	paths := []struct {
		path     string
		mode     fs.FileMode
		dir      bool
		required bool
	}{
		{doctor.config.Environment.StateDir, ManagedDirectoryMode, true, true},
		{doctor.config.Environment.ConfigDir, ManagedDirectoryMode, true, true},
		{doctor.config.Environment.BackupDir, ManagedDirectoryMode, true, true},
		{doctor.config.RootKeyPath, ManagedFileMode, false, true},
		{doctor.config.TokenPath, ManagedFileMode, false, true},
		{doctor.config.DatabasePath, ManagedFileMode, false, true},
		{doctor.config.WALPath, ManagedFileMode, false, false},
		{doctor.config.SHMPath, ManagedFileMode, false, false},
	}
	for _, item := range paths {
		if item.path == "" {
			continue
		}
		pathReport := inspectPath(item.path, item.mode, item.dir)
		report.Paths = append(report.Paths, pathReport)
		if (item.required && !pathReport.Exists) || (pathReport.Exists && !pathReport.Safe) {
			report.Healthy = false
		}
	}
	if doctor.config.FTS != nil {
		fts, err := doctor.config.FTS.Compare(ctx)
		if err != nil {
			return DoctorReport{}, err
		}
		report.FTS = &fts
		if fts.RepairRequired {
			report.Healthy = false
		}
	}
	if doctor.config.Curation != nil {
		health, err := doctor.config.Curation.CurationStatus(ctx)
		if err != nil {
			report.Curation = CurationHealth{Degraded: true, LastErrorClass: "persistence"}
		} else {
			report.Curation = health
		}
		if report.Curation.Degraded {
			report.Healthy = false
		}
	}
	sort.Slice(report.Paths, func(i, j int) bool { return report.Paths[i].Path < report.Paths[j].Path })
	return report, nil
}

type FTSRepairRequest struct {
	DryRun  bool
	Receipt string
}

type FTSRepairResult struct {
	Version string    `json:"version"`
	DryRun  bool      `json:"dry_run"`
	Receipt string    `json:"receipt,omitempty"`
	Report  FTSReport `json:"report"`
}

func (doctor *Doctor) RepairFTS(ctx context.Context, request FTSRepairRequest) (FTSRepairResult, error) {
	if doctor == nil || doctor.config.FTS == nil {
		return FTSRepairResult{}, errors.New("FTS repair unavailable")
	}
	report, err := doctor.config.FTS.Compare(ctx)
	if err != nil {
		return FTSRepairResult{}, err
	}
	result := FTSRepairResult{Version: "talaria.doctor.fts.v1", DryRun: request.DryRun, Report: report}
	if request.DryRun {
		if doctor.config.Receipts == nil {
			return FTSRepairResult{}, errors.New("FTS repair receipt unavailable")
		}
		receipt, err := doctor.config.Receipts.Create(ctx, report)
		if err != nil {
			return FTSRepairResult{}, err
		}
		result.Receipt = receipt
		return result, nil
	}
	if request.Receipt == "" || doctor.config.Receipts == nil {
		return FTSRepairResult{}, errors.New("FTS repair requires a dry-run receipt")
	}
	if err := doctor.config.Receipts.Consume(ctx, request.Receipt, report); err != nil {
		return FTSRepairResult{}, err
	}
	if err := doctor.config.FTS.Repair(ctx); err != nil {
		return FTSRepairResult{}, err
	}
	result.Report.RepairRequired = false
	return result, nil
}

func inspectPath(path string, expected fs.FileMode, directory bool) PathDiagnostic {
	report := PathDiagnostic{Path: path}
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return report
	}
	if err != nil {
		report.Error = safePathError(err)
		return report
	}
	report.Exists = true
	report.Mode = info.Mode().Perm()
	report.ModeOK = report.Mode == expected.Perm()
	report.Symlink = info.Mode()&os.ModeSymlink != 0
	report.Regular = info.Mode().IsRegular()
	report.Directory = info.IsDir()
	report.OwnerOK = currentUserOwns(info)
	report.Size = info.Size()
	report.Safe = !report.Symlink && report.OwnerOK && report.ModeOK && ((directory && report.Directory) || (!directory && report.Regular))
	return report
}

func safePathError(err error) string {
	if err == nil {
		return ""
	}
	message := strings.TrimSpace(err.Error())
	if len(message) > 128 {
		message = message[:128]
	}
	return message
}

func validateProtectedPath(path string, directory bool) error {
	if path == "" {
		return errors.New("path is required")
	}
	if directory {
		return validateManagedDirectory(path)
	}
	if err := security.ValidateRootKey(path); err != nil {
		return err
	}
	return nil
}
