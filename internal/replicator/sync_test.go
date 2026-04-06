package replicator

import (
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func setupSourceAndReplica(t *testing.T) (srcPath, repPath string, srcDB, repDB *sql.DB) {
	t.Helper()
	dir := t.TempDir()
	srcPath = filepath.Join(dir, "source.sqlite")
	repPath = filepath.Join(dir, "replica.sqlite")

	var err error
	srcDB, err = sql.Open("sqlite", srcPath+"?_pragma=journal_mode(wal)")
	if err != nil {
		t.Fatalf("failed to create source: %v", err)
	}
	srcDB.SetMaxOpenConns(1)
	t.Cleanup(func() { srcDB.Close() })

	srcDB.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)")
	srcDB.Exec("INSERT INTO users (name, email) VALUES ('alice', 'a@b.com'), ('bob', 'b@b.com')")
	srcDB.Exec("CREATE TABLE logs (id INTEGER PRIMARY KEY, msg TEXT)")
	srcDB.Exec("INSERT INTO logs (msg) VALUES ('hello'), ('world')")

	repDB, err = sql.Open("sqlite", repPath+"?_pragma=journal_mode(wal)")
	if err != nil {
		t.Fatalf("failed to create replica: %v", err)
	}
	repDB.SetMaxOpenConns(1)
	t.Cleanup(func() { repDB.Close() })

	// Create empty tables in replica
	repDB.Exec("CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)")
	repDB.Exec("CREATE TABLE logs (id INTEGER PRIMARY KEY, msg TEXT)")

	return
}

func TestSyncTable(t *testing.T) {
	_, _, srcDB, repDB := setupSourceAndReplica(t)

	if err := SyncTable(srcDB, repDB, "users"); err != nil {
		t.Fatalf("SyncTable failed: %v", err)
	}

	var count int
	repDB.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 rows, got %d", count)
	}

	var name string
	repDB.QueryRow("SELECT name FROM users WHERE id = 1").Scan(&name)
	if name != "alice" {
		t.Errorf("expected 'alice', got %q", name)
	}
}

func TestSyncTables(t *testing.T) {
	_, _, srcDB, repDB := setupSourceAndReplica(t)

	if err := SyncTables(srcDB, repDB, []string{"users", "logs"}); err != nil {
		t.Fatalf("SyncTables failed: %v", err)
	}

	var userCount, logCount int
	repDB.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount)
	repDB.QueryRow("SELECT COUNT(*) FROM logs").Scan(&logCount)

	if userCount != 2 {
		t.Errorf("users: expected 2, got %d", userCount)
	}
	if logCount != 2 {
		t.Errorf("logs: expected 2, got %d", logCount)
	}
}

func TestFullSync(t *testing.T) {
	_, _, srcDB, repDB := setupSourceAndReplica(t)

	if err := FullSync(srcDB, repDB); err != nil {
		t.Fatalf("FullSync failed: %v", err)
	}

	var userCount, logCount int
	repDB.QueryRow("SELECT COUNT(*) FROM users").Scan(&userCount)
	repDB.QueryRow("SELECT COUNT(*) FROM logs").Scan(&logCount)

	if userCount != 2 || logCount != 2 {
		t.Errorf("expected 2+2 rows, got %d+%d", userCount, logCount)
	}
}

func TestFullSync_IncrementalChanges(t *testing.T) {
	_, _, srcDB, repDB := setupSourceAndReplica(t)

	// Initial sync
	FullSync(srcDB, repDB)

	// Add data to source
	srcDB.Exec("INSERT INTO users (name, email) VALUES ('charlie', 'c@c.com')")

	// Re-sync
	if err := FullSync(srcDB, repDB); err != nil {
		t.Fatalf("re-sync failed: %v", err)
	}

	var count int
	repDB.QueryRow("SELECT COUNT(*) FROM users").Scan(&count)
	if count != 3 {
		t.Errorf("expected 3 rows after re-sync, got %d", count)
	}
}

func TestInitialSync(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.sqlite")

	db, _ := sql.Open("sqlite", srcPath+"?_pragma=journal_mode(wal)")
	db.SetMaxOpenConns(1)
	db.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, val TEXT)")
	db.Exec("INSERT INTO items (val) VALUES ('one'), ('two')")
	db.Close()

	repPath := filepath.Join(dir, "replica.sqlite")
	tables, err := InitialSync(srcPath, repPath)
	if err != nil {
		t.Fatalf("InitialSync failed: %v", err)
	}

	if len(tables) != 1 || tables[0] != "items" {
		t.Errorf("expected [items], got %v", tables)
	}

	if _, err := os.Stat(repPath); os.IsNotExist(err) {
		t.Fatal("replica not created")
	}

	repDB, _ := sql.Open("sqlite", repPath)
	defer repDB.Close()
	var count int
	repDB.QueryRow("SELECT COUNT(*) FROM items").Scan(&count)
	if count != 2 {
		t.Errorf("expected 2 rows in replica, got %d", count)
	}
}

func TestOpenReadOnly(t *testing.T) {
	dir := t.TempDir()
	dbPath := createTestDB(t, dir, "test.sqlite")

	db, err := OpenReadOnly(dbPath)
	if err != nil {
		t.Fatalf("OpenReadOnly failed: %v", err)
	}
	defer db.Close()

	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatalf("query failed: %v", err)
	}
	if count != 2 {
		t.Errorf("expected 2, got %d", count)
	}
}

func TestSyncTable_NonExistentTable(t *testing.T) {
	_, _, srcDB, repDB := setupSourceAndReplica(t)

	err := SyncTable(srcDB, repDB, "nonexistent")
	if err == nil {
		t.Error("expected error for non-existent table")
	}
}
