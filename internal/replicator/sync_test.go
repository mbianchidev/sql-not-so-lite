package replicator

import (
	"database/sql"
	"os"
	"path/filepath"
	"reflect"
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

func TestOpenReadOnlyUsesEscapedReadOnlyURI(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "source # files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "read only.sqlite")
	source, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, value TEXT); INSERT INTO items (value) VALUES ('one')"); err != nil {
		source.Close()
		t.Fatal(err)
	}
	if err := source.Close(); err != nil {
		t.Fatal(err)
	}

	readOnly, err := OpenReadOnly(path)
	if err != nil {
		t.Fatal(err)
	}
	defer readOnly.Close()

	var value string
	if err := readOnly.QueryRow("SELECT value FROM items").Scan(&value); err != nil {
		t.Fatalf("read escaped database path: %v", err)
	}
	if value != "one" {
		t.Fatalf("value = %q, want one", value)
	}
	if _, err := readOnly.Exec("PRAGMA query_only=0; INSERT INTO items (value) VALUES ('two')"); err == nil {
		t.Fatal("expected read-only URI to reject writes after disabling query_only")
	}
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

func TestDifferentialSyncSkipsUnchangedTables(t *testing.T) {
	dir := t.TempDir()
	srcPath := filepath.Join(dir, "source.sqlite")
	replicaPath := filepath.Join(dir, "replica.sqlite")
	source, err := sql.Open("sqlite", srcPath+"?_pragma=journal_mode(wal)")
	if err != nil {
		t.Fatal(err)
	}
	defer source.Close()
	if _, err := source.Exec(`
		CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);
		CREATE TABLE logs (id INTEGER PRIMARY KEY, message TEXT);
		INSERT INTO users (name) VALUES ('alice');
		INSERT INTO logs (message) VALUES ('started');
		CREATE INDEX users_name_idx ON users(name);
		CREATE TRIGGER users_name_required
		BEFORE INSERT ON users
		WHEN NEW.name = ''
		BEGIN
			SELECT RAISE(ABORT, 'name required');
		END;
	`); err != nil {
		t.Fatal(err)
	}
	if _, err := InitialSync(srcPath, replicaPath); err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec("INSERT INTO users (name) VALUES ('bob')"); err != nil {
		t.Fatal(err)
	}

	changed, err := DifferentialSync(srcPath, replicaPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"users"}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed tables = %v, want %v", changed, want)
	}

	replica, err := sql.Open("sqlite", replicaPath)
	if err != nil {
		t.Fatal(err)
	}
	defer replica.Close()
	var count int
	if err := replica.QueryRow("SELECT COUNT(*) FROM users").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("replica user count = %d, want 2", count)
	}
	for _, objectName := range []string{"users_name_idx", "users_name_required"} {
		var exists int
		if err := replica.QueryRow(
			"SELECT COUNT(*) FROM sqlite_master WHERE name = ?",
			objectName,
		).Scan(&exists); err != nil {
			t.Fatal(err)
		}
		if exists != 1 {
			t.Fatalf("schema object %q was not preserved", objectName)
		}
	}
}

func TestDifferentialSyncReturnsNoChanges(t *testing.T) {
	dir := t.TempDir()
	srcPath := createTestDB(t, dir, "source.sqlite")
	replicaPath := filepath.Join(dir, "replica.sqlite")
	if _, err := InitialSync(srcPath, replicaPath); err != nil {
		t.Fatal(err)
	}

	changed, err := DifferentialSync(srcPath, replicaPath)
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 0 {
		t.Fatalf("changed tables = %v, want none", changed)
	}
}

func TestDifferentialSyncRemovesDeletedTables(t *testing.T) {
	dir := t.TempDir()
	srcPath := createTestDB(t, dir, "source.sqlite")
	source, err := sql.Open("sqlite", srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec("CREATE TABLE obsolete (id INTEGER PRIMARY KEY)"); err != nil {
		source.Close()
		t.Fatal(err)
	}
	source.Close()
	replicaPath := filepath.Join(dir, "replica.sqlite")
	if _, err := InitialSync(srcPath, replicaPath); err != nil {
		t.Fatal(err)
	}
	source, err = sql.Open("sqlite", srcPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec("DROP TABLE obsolete"); err != nil {
		source.Close()
		t.Fatal(err)
	}
	source.Close()

	changed, err := DifferentialSync(srcPath, replicaPath)
	if err != nil {
		t.Fatal(err)
	}
	if want := []string{"obsolete"}; !reflect.DeepEqual(changed, want) {
		t.Fatalf("changed tables = %v, want %v", changed, want)
	}
	replica, err := sql.Open("sqlite", replicaPath)
	if err != nil {
		t.Fatal(err)
	}
	defer replica.Close()
	var count int
	if err := replica.QueryRow(
		"SELECT COUNT(*) FROM sqlite_master WHERE type = 'table' AND name = 'obsolete'",
	).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatal("deleted table still exists in replica")
	}
}
