package service

import (
	"context"
	"testing"

	"github.com/mbianchidev/sql-not-so-lite/internal/store"
)

func setupTestService(t *testing.T) *DatabaseService {
	t.Helper()
	dir := t.TempDir()
	m, err := store.NewManager(dir, 100)
	if err != nil {
		t.Fatalf("failed to create manager: %v", err)
	}
	t.Cleanup(func() { m.CloseAll() })
	return NewDatabaseService(m)
}

func TestCreateAndListDatabases(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()

	info, err := svc.CreateDatabase(ctx, "myapp")
	if err != nil {
		t.Fatalf("CreateDatabase failed: %v", err)
	}
	if info.Name != "myapp" {
		t.Errorf("expected name 'myapp', got %q", info.Name)
	}

	dbs, err := svc.ListDatabases(ctx)
	if err != nil {
		t.Fatalf("ListDatabases failed: %v", err)
	}
	if len(dbs) != 1 {
		t.Fatalf("expected 1 database, got %d", len(dbs))
	}
	if dbs[0].Name != "myapp" {
		t.Errorf("expected name 'myapp', got %q", dbs[0].Name)
	}
}

func TestExecuteAndQuery(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()

	if _, err := svc.CreateDatabase(ctx, "testdb"); err != nil {
		t.Fatalf("CreateDatabase failed: %v", err)
	}

	// Create table
	_, err := svc.Execute(ctx, "testdb", "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)", nil)
	if err != nil {
		t.Fatalf("Execute CREATE TABLE failed: %v", err)
	}

	// Insert data
	result, err := svc.Execute(ctx, "testdb", "INSERT INTO users (name, email) VALUES ('Alice', 'alice@example.com')", nil)
	if err != nil {
		t.Fatalf("Execute INSERT failed: %v", err)
	}
	if result.RowsAffected != 1 {
		t.Errorf("expected 1 row affected, got %d", result.RowsAffected)
	}

	// Query
	qr, err := svc.Query(ctx, "testdb", "SELECT * FROM users", nil, 100, 0)
	if err != nil {
		t.Fatalf("Query failed: %v", err)
	}
	if len(qr.Rows) != 1 {
		t.Fatalf("expected 1 row, got %d", len(qr.Rows))
	}
	if len(qr.Columns) != 3 {
		t.Fatalf("expected 3 columns, got %d", len(qr.Columns))
	}
}

func TestGetSchema(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()

	if _, err := svc.CreateDatabase(ctx, "schemadb"); err != nil {
		t.Fatal(err)
	}

	_, err := svc.Execute(ctx, "schemadb", `
		CREATE TABLE products (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			price REAL DEFAULT 0.0,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)
	`, nil)
	if err != nil {
		t.Fatal(err)
	}

	_, err = svc.Execute(ctx, "schemadb", "CREATE INDEX idx_products_name ON products(name)", nil)
	if err != nil {
		t.Fatal(err)
	}

	tables, err := svc.GetSchema(ctx, "schemadb")
	if err != nil {
		t.Fatal(err)
	}

	if len(tables) != 1 {
		t.Fatalf("expected 1 table, got %d", len(tables))
	}

	tbl := tables[0]
	if tbl.Name != "products" {
		t.Errorf("expected table 'products', got %q", tbl.Name)
	}
	if len(tbl.Columns) != 4 {
		t.Errorf("expected 4 columns, got %d", len(tbl.Columns))
	}
	if len(tbl.Indexes) != 1 {
		t.Errorf("expected 1 index, got %d", len(tbl.Indexes))
	}

	// Check column details
	idCol := tbl.Columns[0]
	if idCol.Name != "id" || !idCol.PrimaryKey {
		t.Errorf("expected 'id' as primary key, got name=%q pk=%v", idCol.Name, idCol.PrimaryKey)
	}

	nameCol := tbl.Columns[1]
	if nameCol.Nullable {
		t.Error("expected 'name' to be NOT NULL")
	}
}

func TestDropDatabase(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()

	if _, err := svc.CreateDatabase(ctx, "dropme"); err != nil {
		t.Fatal(err)
	}

	if err := svc.DropDatabase(ctx, "dropme"); err != nil {
		t.Fatalf("DropDatabase failed: %v", err)
	}

	dbs, err := svc.ListDatabases(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(dbs) != 0 {
		t.Errorf("expected 0 databases after drop, got %d", len(dbs))
	}
}

func TestQueryWithPagination(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()

	if _, err := svc.CreateDatabase(ctx, "pagdb"); err != nil {
		t.Fatal(err)
	}

	svc.Execute(ctx, "pagdb", "CREATE TABLE items (id INTEGER PRIMARY KEY, val TEXT)", nil)
	for i := 0; i < 20; i++ {
		svc.Execute(ctx, "pagdb", "INSERT INTO items (val) VALUES (?)", []string{"item"})
	}

	// Query with limit
	qr, err := svc.Query(ctx, "pagdb", "SELECT * FROM items", nil, 5, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(qr.Rows) != 5 {
		t.Errorf("expected 5 rows with limit, got %d", len(qr.Rows))
	}

	// Query with offset
	qr2, err := svc.Query(ctx, "pagdb", "SELECT * FROM items", nil, 5, 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(qr2.Rows) != 5 {
		t.Errorf("expected 5 rows with offset, got %d", len(qr2.Rows))
	}
}

func TestExecuteOnNonExistentDB(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()

	_, err := svc.Execute(ctx, "ghost", "SELECT 1", nil)
	if err == nil {
		t.Error("expected error executing on non-existent database")
	}
}
