package lifecycle

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

// CurationProviderStatus contains only topology and health metadata. It is
// deliberately incapable of carrying credentials, prompts, locators, or
// provider response text.
type CurationProviderStatus struct {
	Name       string `json:"name"`
	Type       string `json:"type"`
	Configured bool   `json:"configured"`
	Available  bool   `json:"available"`
}

// CurationHealth is the safe observability contract shared by status and
// doctor. Queue age is represented as seconds so no source payload or
// timestamp-derived path can leak through diagnostics.
type CurationHealth struct {
	Enabled             bool                     `json:"enabled"`
	Degraded            bool                     `json:"degraded"`
	Providers           []CurationProviderStatus `json:"providers,omitempty"`
	QueueDepth          int64                    `json:"queue_depth"`
	OldestJobAgeSeconds int64                    `json:"oldest_job_age_seconds,omitempty"`
	Running             int64                    `json:"running"`
	LastErrorClass      string                   `json:"last_error_class,omitempty"`
}

// String is used by bounded diagnostics and tests. Marshaling this closed
// value cannot expose provider secrets because the type has no such fields.
func (health CurationHealth) String() string {
	value, err := json.Marshal(health)
	if err != nil {
		return `{"degraded":true,"last_error_class":"persistence"}`
	}
	return string(value)
}

type CurationHealthSource interface {
	CurationStatus(context.Context) (CurationHealth, error)
}

type StorageStatus struct {
	DatabasePath  string `json:"database_path"`
	DatabaseSize  int64  `json:"database_size"`
	WALPath       string `json:"wal_path"`
	WALSize       int64  `json:"wal_size"`
	SHMPath       string `json:"shm_path"`
	SHMSize       int64  `json:"shm_size"`
	FreelistPages int64  `json:"freelist_pages"`
}

type StatusReport struct {
	Version   string          `json:"version"`
	Ready     bool            `json:"ready"`
	Readiness ReadinessReport `json:"readiness"`
	Storage   StorageStatus   `json:"storage"`
	Curation  CurationHealth  `json:"curation"`
}

type StatusService struct {
	Readiness *Readiness
	Database  string
	Freelist  func(context.Context) (int64, error)
	Curation  CurationHealthSource
}

func NewStatusService(readiness *Readiness, database string) *StatusService {
	return &StatusService{Readiness: readiness, Database: database}
}

func (service *StatusService) Status(ctx context.Context) (StatusReport, error) {
	if service == nil {
		return StatusReport{}, errors.New("status unavailable")
	}
	readiness := ReadinessReport{}
	if service.Readiness != nil {
		var err error
		readiness, err = service.Readiness.Check(ctx)
		if err != nil {
			return StatusReport{}, err
		}
	}
	storage := StorageStatus{DatabasePath: service.Database}
	storage.DatabaseSize = fileSize(service.Database)
	storage.WALPath = service.Database + "-wal"
	storage.WALSize = fileSize(storage.WALPath)
	storage.SHMPath = service.Database + "-shm"
	storage.SHMSize = fileSize(storage.SHMPath)
	if service.Freelist != nil {
		value, err := service.Freelist(ctx)
		if err != nil {
			return StatusReport{}, err
		}
		if value >= 0 {
			storage.FreelistPages = value
		}
	}
	health := CurationHealth{}
	if service.Curation != nil {
		value, err := service.Curation.CurationStatus(ctx)
		if err != nil {
			health = CurationHealth{Degraded: true, LastErrorClass: "persistence"}
		} else {
			health = value
		}
	}
	return StatusReport{Version: "talaria.status.v1", Ready: readiness.Ready, Readiness: readiness, Storage: storage, Curation: health}, nil
}

func (service *StatusService) Check(ctx context.Context) (StatusReport, error) {
	return service.Status(ctx)
}

func fileSize(path string) int64 {
	if path == "" {
		return 0
	}
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&fs.ModeSymlink != 0 || !info.Mode().IsRegular() {
		return 0
	}
	return info.Size()
}

func DatabaseSidecarPaths(database string) (wal, shm string) {
	return database + "-wal", database + "-shm"
}

func DatabasePath(stateDir string) string {
	return filepath.Join(stateDir, "talaria-mem.sqlite3")
}
