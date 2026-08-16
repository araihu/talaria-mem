package lifecycle

import (
	"context"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
)

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
}

type StatusService struct {
	Readiness *Readiness
	Database  string
	Freelist  func(context.Context) (int64, error)
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
	return StatusReport{Version: "talaria.status.v1", Ready: readiness.Ready, Readiness: readiness, Storage: storage}, nil
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
