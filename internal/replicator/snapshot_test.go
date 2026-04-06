package replicator

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func createTestDB(t *testing.T, dir, name string) string {
	t.Helper()
	dbPath := filepath.Join(dir, name)
	db, err := sql.Open("sqlite", dbPath+"?_pragma=journal_mode(wal)")
	if err != nil {
		t.Fatalf("failed to create test db: %v", err)
	}
	defer db.Close()

	db.SetMaxOpenConns(1)
	db.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)")
	db.Exec("INSERT INTO users (name) VALUES ('alice'), ('bob')")
	return dbPath
}

func TestCreateSnapshot(t *testing.T) {
	dir := t.TempDir()
	srcPath := createTestDB(t, dir, "source.sqlite")
	snapPath := filepath.Join(dir, "snapshots", "snap-v1.sqlite")

	size, err := CreateSnapshot(srcPath, snapPath)
	if err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}

	if size <= 0 {
		t.Error("expected positive snapshot size")
	}

	if _, err := os.Stat(snapPath); os.IsNotExist(err) {
		t.Fatal("snapshot file not created")
	}

	// Verify snapshot is a valid SQLite DB with the same data
	db, err := sql.Open("sqlite", snapPath)
	if err != nil {
		t.Fatalf("failed to open snapshot: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatalf("failed to query snapshot: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 rows, got %d", count)
	}
}

func TestCreateSnapshot_InvalidSource(t *testing.T) {
	dir := t.TempDir()
	_, err := CreateSnapshot(filepath.Join(dir, "nonexistent.sqlite"), filepath.Join(dir, "snap.sqlite"))
	if err == nil {
		t.Error("expected error for nonexistent source")
	}
}

func TestRestoreSnapshot(t *testing.T) {
	dir := t.TempDir()
	srcPath := createTestDB(t, dir, "source.sqlite")

	snapPath := filepath.Join(dir, "snap.sqlite")
	if _, err := CreateSnapshot(srcPath, snapPath); err != nil {
		t.Fatalf("CreateSnapshot failed: %v", err)
	}

	targetPath := filepath.Join(dir, "restored", "target.sqlite")
	if err := RestoreSnapshot(snapPath, targetPath); err != nil {
		t.Fatalf("RestoreSnapshot failed: %v", err)
	}

	// Verify restored file is valid
	db, err := sql.Open("sqlite", targetPath)
	if err != nil {
		t.Fatalf("failed to open restored: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatalf("failed to query restored: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2 rows, got %d", count)
	}

	// Verify sidecar files are removed
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(targetPath + suffix); err == nil {
			t.Errorf("sidecar file %s should not exist", suffix)
		}
	}
}

func TestRestoreSnapshot_StaleSidecars(t *testing.T) {
	dir := t.TempDir()
	srcPath := createTestDB(t, dir, "source.sqlite")

	snapPath := filepath.Join(dir, "snap.sqlite")
	if _, err := CreateSnapshot(srcPath, snapPath); err != nil {
		t.Fatal(err)
	}

	targetPath := filepath.Join(dir, "target.sqlite")

	// Create stale sidecar files
	os.WriteFile(targetPath+"-wal", []byte("stale"), 0644)
	os.WriteFile(targetPath+"-shm", []byte("stale"), 0644)

	if err := RestoreSnapshot(snapPath, targetPath); err != nil {
		t.Fatal(err)
	}

	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(targetPath + suffix); err == nil {
			t.Errorf("stale sidecar %s should have been removed", suffix)
		}
	}
}

func TestRestoreSnapshot_NotFound(t *testing.T) {
	dir := t.TempDir()
	err := RestoreSnapshot(filepath.Join(dir, "missing.sqlite"), filepath.Join(dir, "target.sqlite"))
	if err == nil {
		t.Error("expected error for missing snapshot")
	}
}

func TestPruneSnapshotFiles(t *testing.T) {
	dir := t.TempDir()
	paths := make([]string, 3)
	for i := range paths {
		p := filepath.Join(dir, fmt.Sprintf("snap-%d.sqlite", i))
		os.WriteFile(p, []byte("data"), 0644)
		paths[i] = p
	}

	PruneSnapshotFiles(paths)

	for _, p := range paths {
		if _, err := os.Stat(p); err == nil {
			t.Errorf("file %s should have been deleted", p)
		}
	}
}
