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
	destDir := filepath.Dir(destPath)
	if err := os.MkdirAll(destDir, 0755); err != nil {
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

	tempFile, err := os.CreateTemp(destDir, "."+filepath.Base(destPath)+".tmp-*")
	if err != nil {
		return 0, fmt.Errorf("failed to create snapshot temp file: %w", err)
	}
	tempPath := tempFile.Name()
	if err := tempFile.Close(); err != nil {
		os.Remove(tempPath)
		return 0, fmt.Errorf("failed to close snapshot temp file: %w", err)
	}
	if err := os.Remove(tempPath); err != nil {
		return 0, fmt.Errorf("failed to prepare snapshot temp path: %w", err)
	}
	defer os.Remove(tempPath)

	_, err = db.Exec("VACUUM INTO ?", tempPath)
	if err != nil {
		return 0, fmt.Errorf("VACUUM INTO failed: %w", err)
	}

	info, err := os.Stat(tempPath)
	if err != nil {
		return 0, fmt.Errorf("failed to stat snapshot: %w", err)
	}
	if err := os.Rename(tempPath, destPath); err != nil {
		return 0, fmt.Errorf("failed to replace snapshot: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		os.Remove(destPath + suffix)
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
