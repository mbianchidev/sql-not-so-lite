package replicator

import (
	"database/sql"
	"fmt"
	"io"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

// CreateSnapshot creates a consistent snapshot of a source SQLite database
// using VACUUM INTO, which produces a complete, defragmented copy.
func CreateSnapshot(sourcePath, destPath string) (int64, error) {
	if err := os.MkdirAll(filepath.Dir(destPath), 0755); err != nil {
		return 0, fmt.Errorf("failed to create snapshot dir: %w", err)
	}

	if _, err := os.Stat(sourcePath); err != nil {
		return 0, fmt.Errorf("source database not found: %w", err)
	}

	db, err := sql.Open("sqlite", sourcePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return 0, fmt.Errorf("failed to open source: %w", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(1)

	if err := db.Ping(); err != nil {
		return 0, fmt.Errorf("failed to connect to source: %w", err)
	}

	_, err = db.Exec(fmt.Sprintf("VACUUM INTO '%s'", destPath))
	if err != nil {
		return 0, fmt.Errorf("VACUUM INTO failed: %w", err)
	}

	info, err := os.Stat(destPath)
	if err != nil {
		return 0, fmt.Errorf("failed to stat snapshot: %w", err)
	}

	return info.Size(), nil
}

// RestoreSnapshot copies a snapshot file to the target path, replacing
// whatever is there. It also removes any stale WAL/SHM sidecar files.
func RestoreSnapshot(snapshotPath, targetPath string) error {
	if _, err := os.Stat(snapshotPath); err != nil {
		return fmt.Errorf("snapshot not found: %w", err)
	}

	if err := os.MkdirAll(filepath.Dir(targetPath), 0755); err != nil {
		return fmt.Errorf("failed to create target dir: %w", err)
	}

	src, err := os.Open(snapshotPath)
	if err != nil {
		return fmt.Errorf("failed to open snapshot: %w", err)
	}
	defer src.Close()

	dst, err := os.Create(targetPath)
	if err != nil {
		return fmt.Errorf("failed to create target: %w", err)
	}
	defer dst.Close()

	if _, err := io.Copy(dst, src); err != nil {
		os.Remove(targetPath)
		return fmt.Errorf("failed to copy snapshot: %w", err)
	}

	if err := dst.Sync(); err != nil {
		return fmt.Errorf("failed to sync target: %w", err)
	}

	// Remove stale sidecar files from previous instance
	for _, suffix := range []string{"-wal", "-shm"} {
		os.Remove(targetPath + suffix)
	}

	return nil
}

// PruneSnapshotFiles deletes snapshot files from disk.
func PruneSnapshotFiles(paths []string) {
	for _, p := range paths {
		os.Remove(p)
	}
}
