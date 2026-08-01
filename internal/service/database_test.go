package service

import (
	"context"
	"reflect"
	"strings"
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

func TestCreateTableAndAddColumn(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()

	if _, err := svc.CreateDatabase(ctx, "builder"); err != nil {
		t.Fatal(err)
	}

	if err := svc.CreateTable(ctx, "builder", CreateTableRequest{
		Name: "users",
		Columns: []ColumnDefinition{
			{Name: "id", Type: "INTEGER", PrimaryKey: true},
			{Name: "name", Type: "TEXT", NotNull: true},
		},
	}); err != nil {
		t.Fatalf("CreateTable failed: %v", err)
	}

	defaultValue := "active"
	if err := svc.AddColumn(ctx, "builder", "users", ColumnDefinition{
		Name:         "status",
		Type:         "TEXT",
		NotNull:      true,
		DefaultValue: &defaultValue,
	}); err != nil {
		t.Fatalf("AddColumn failed: %v", err)
	}

	tables, err := svc.GetSchema(ctx, "builder")
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || len(tables[0].Columns) != 3 {
		t.Fatalf("unexpected schema: %+v", tables)
	}
	if tables[0].Columns[2].Name != "status" || tables[0].Columns[2].Nullable {
		t.Errorf("unexpected added column: %+v", tables[0].Columns[2])
	}
	if tables[0].Columns[0].Nullable {
		t.Errorf("primary key column should be NOT NULL: %+v", tables[0].Columns[0])
	}

	if _, err := svc.Execute(ctx, "builder", `CREATE TABLE "user-data" (id INTEGER)`, nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.AddColumn(ctx, "builder", "user-data", ColumnDefinition{Name: "email-address", Type: "TEXT"}); err != nil {
		t.Fatalf("AddColumn with quoted identifiers failed: %v", err)
	}
}

func TestInsertRowSupportsValuesNullAndDefaults(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()

	if _, err := svc.CreateDatabase(ctx, "rows"); err != nil {
		t.Fatal(err)
	}
	defaultStatus := "active"
	if err := svc.CreateTable(ctx, "rows", CreateTableRequest{
		Name: "users",
		Columns: []ColumnDefinition{
			{Name: "id", Type: "INTEGER", PrimaryKey: true},
			{Name: "name", Type: "TEXT", NotNull: true},
			{Name: "note", Type: "TEXT"},
			{Name: "status", Type: "TEXT", DefaultValue: &defaultStatus},
		},
	}); err != nil {
		t.Fatal(err)
	}

	name := "Ada"
	result, err := svc.InsertRow(ctx, "rows", "users", InsertRowRequest{
		Columns: []string{"name", "note"},
		Values:  []*string{&name, nil},
	})
	if err != nil {
		t.Fatalf("InsertRow failed: %v", err)
	}
	if result.RowsAffected != 1 || result.LastInsertID == 0 {
		t.Fatalf("unexpected insert result: %+v", result)
	}

	rows, err := svc.Query(ctx, "rows", `SELECT id, name, note, status FROM "users"`, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 1 {
		t.Fatalf("expected one row, got %+v", rows.Rows)
	}
	want := []string{"1", "Ada", "NULL", "active"}
	for i, value := range want {
		if rows.Rows[0][i] != value {
			t.Fatalf("row[%d] = %q, want %q", i, rows.Rows[0][i], value)
		}
	}

	if err := svc.CreateTable(ctx, "rows", CreateTableRequest{
		Name: "settings",
		Columns: []ColumnDefinition{
			{Name: "id", Type: "INTEGER", PrimaryKey: true},
			{Name: "status", Type: "TEXT", DefaultValue: &defaultStatus},
		},
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.InsertRow(ctx, "rows", "settings", InsertRowRequest{}); err != nil {
		t.Fatalf("DEFAULT VALUES insert failed: %v", err)
	}
}

func TestInsertRowValidatesColumnsAndValues(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()
	if _, err := svc.CreateDatabase(ctx, "row-validation"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CreateTable(ctx, "row-validation", CreateTableRequest{
		Name:    "items",
		Columns: []ColumnDefinition{{Name: "name", Type: "TEXT"}},
	}); err != nil {
		t.Fatal(err)
	}

	value := "test"
	tests := []InsertRowRequest{
		{Columns: []string{"name"}, Values: nil},
		{Columns: []string{"missing"}, Values: []*string{&value}},
		{Columns: []string{"name", "NAME"}, Values: []*string{&value, &value}},
	}
	for _, req := range tests {
		if _, err := svc.InsertRow(ctx, "row-validation", "items", req); err == nil {
			t.Fatalf("expected validation error for %+v", req)
		}
	}

	if err := svc.CreateTable(ctx, "row-validation", CreateTableRequest{
		Name: "unicode",
		Columns: []ColumnDefinition{
			{Name: "Ä", Type: "TEXT"},
			{Name: "ä", Type: "TEXT"},
		},
	}); err != nil {
		t.Fatalf("create Unicode columns: %v", err)
	}
	upper, lower := "upper", "lower"
	if _, err := svc.InsertRow(ctx, "row-validation", "unicode", InsertRowRequest{
		Columns: []string{"Ä", "ä"},
		Values:  []*string{&upper, &lower},
	}); err != nil {
		t.Fatalf("insert Unicode columns: %v", err)
	}
}

func TestInsertRowSafelyReadsQuotedTableName(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()
	if _, err := svc.CreateDatabase(ctx, "quoted-row"); err != nil {
		t.Fatal(err)
	}
	if err := svc.CreateTable(ctx, "quoted-row", CreateTableRequest{
		Name:    "safe",
		Columns: []ColumnDefinition{{Name: "value", Type: "TEXT"}},
	}); err != nil {
		t.Fatal(err)
	}
	tableName := `odd'); DROP TABLE safe; --`
	if err := svc.CreateTable(ctx, "quoted-row", CreateTableRequest{
		Name:    tableName,
		Columns: []ColumnDefinition{{Name: "value", Type: "TEXT"}},
	}); err != nil {
		t.Fatal(err)
	}

	value := "kept"
	if _, err := svc.InsertRow(ctx, "quoted-row", tableName, InsertRowRequest{
		Columns: []string{"value"},
		Values:  []*string{&value},
	}); err != nil {
		t.Fatalf("insert quoted table: %v", err)
	}
	if _, err := svc.Query(ctx, "quoted-row", `SELECT * FROM "safe"`, nil, 10, 0); err != nil {
		t.Fatalf("safe table was modified: %v", err)
	}
}

func TestSchemaMutationValidation(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()
	if _, err := svc.CreateDatabase(ctx, "validation"); err != nil {
		t.Fatal(err)
	}

	err := svc.CreateTable(ctx, "validation", CreateTableRequest{
		Name:    "sqlite_internal",
		Columns: []ColumnDefinition{{Name: "id", Type: "INTEGER"}},
	})
	if err == nil {
		t.Fatal("expected reserved table name error")
	}

	err = svc.CreateTable(ctx, "validation", CreateTableRequest{
		Name: "users",
		Columns: []ColumnDefinition{
			{Name: "id", Type: "INTEGER", PrimaryKey: true},
			{Name: "other_id", Type: "INTEGER", PrimaryKey: true},
		},
	})
	if err == nil {
		t.Fatal("expected multiple primary key error")
	}

	if err := svc.CreateTable(ctx, "validation", CreateTableRequest{
		Name:    "users",
		Columns: []ColumnDefinition{{Name: "id", Type: "INTEGER", PrimaryKey: true}},
	}); err != nil {
		t.Fatal(err)
	}
	err = svc.AddColumn(ctx, "validation", "users", ColumnDefinition{
		Name:    "email",
		Type:    "TEXT",
		NotNull: true,
	})
	if err == nil {
		t.Fatal("expected missing default error")
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

func TestEditColumnRebuildsDataAndPreservesSchemaObjects(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()
	if _, err := svc.CreateDatabase(ctx, "column-edit"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(ctx, "column-edit", `
		CREATE TABLE users (
			id INTEGER NOT NULL PRIMARY KEY,
			nickname TEXT
		);
		CREATE INDEX users_nickname_idx ON users(nickname);
		CREATE TRIGGER users_nickname_guard
		BEFORE INSERT ON users
		WHEN NEW.nickname = 'blocked'
		BEGIN
			SELECT RAISE(ABORT, 'blocked nickname');
		END;
		INSERT INTO users (id, nickname) VALUES (1, NULL), (2, '42');
	`, nil); err != nil {
		t.Fatal(err)
	}
	defaultValue := "0"
	if err := svc.EditColumn(ctx, "column-edit", "users", EditColumnRequest{
		OriginalName: "nickname",
		Name:         "score",
		Type:         "INTEGER",
		Nullable:     false,
		DefaultValue: &defaultValue,
	}); err != nil {
		t.Fatalf("EditColumn failed: %v", err)
	}

	schema, err := svc.GetSchema(ctx, "column-edit")
	if err != nil {
		t.Fatal(err)
	}
	if len(schema) != 1 || len(schema[0].Columns) != 2 {
		t.Fatalf("unexpected schema: %+v", schema)
	}
	column := schema[0].Columns[1]
	if column.Name != "score" || column.Type != "INTEGER" || column.Nullable {
		t.Fatalf("edited column = %+v", column)
	}
	rows, err := svc.Query(
		ctx,
		"column-edit",
		"SELECT id, score, typeof(score) FROM users ORDER BY id",
		nil,
		10,
		0,
	)
	if err != nil {
		t.Fatal(err)
	}
	want := [][]string{{"1", "0", "integer"}, {"2", "42", "integer"}}
	if !reflect.DeepEqual(rows.Rows, want) {
		t.Fatalf("rows = %v, want %v", rows.Rows, want)
	}
	for _, objectName := range []string{"users_nickname_idx", "users_nickname_guard"} {
		result, err := svc.Query(
			ctx,
			"column-edit",
			"SELECT sql FROM sqlite_master WHERE name = ?",
			[]string{objectName},
			1,
			0,
		)
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Rows) != 1 || !strings.Contains(result.Rows[0][0], "score") {
			t.Fatalf("schema object %q was not updated: %+v", objectName, result.Rows)
		}
	}
	if _, err := svc.Execute(
		ctx,
		"column-edit",
		"INSERT INTO users (id, score) VALUES (3, 'blocked')",
		nil,
	); err == nil {
		t.Fatal("preserved trigger did not reject the row")
	}
}

func TestEditColumnRejectsNotNullWithoutDefault(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()
	if _, err := svc.CreateDatabase(ctx, "column-null"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(
		ctx,
		"column-null",
		"CREATE TABLE items (value TEXT); INSERT INTO items (value) VALUES (NULL)",
		nil,
	); err != nil {
		t.Fatal(err)
	}

	err := svc.EditColumn(ctx, "column-null", "items", EditColumnRequest{
		OriginalName: "value",
		Name:         "value",
		Type:         "TEXT",
		Nullable:     false,
	})
	if err == nil || !strings.Contains(err.Error(), "contains NULL") {
		t.Fatalf("error = %v, want NULL validation", err)
	}
	schema, schemaErr := svc.GetSchema(ctx, "column-null")
	if schemaErr != nil {
		t.Fatal(schemaErr)
	}
	if !schema[0].Columns[0].Nullable {
		t.Fatal("failed edit changed the schema")
	}
}

func TestEditColumnRejectsUnsupportedConstraints(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()
	if _, err := svc.CreateDatabase(ctx, "column-constraint"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(
		ctx,
		"column-constraint",
		"CREATE TABLE items (code TEXT UNIQUE)",
		nil,
	); err != nil {
		t.Fatal(err)
	}
	err := svc.EditColumn(ctx, "column-constraint", "items", EditColumnRequest{
		OriginalName: "code",
		Name:         "code",
		Type:         "INTEGER",
		Nullable:     true,
	})
	if err == nil || !strings.Contains(err.Error(), "UNIQUE") {
		t.Fatalf("error = %v, want unsupported UNIQUE constraint", err)
	}
}

func TestEditColumnSupportsQuotedIdentifiers(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()
	if _, err := svc.CreateDatabase(ctx, "column-quoted"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(
		ctx,
		"column-quoted",
		`CREATE TABLE "user-data" ("display-name" TEXT); INSERT INTO "user-data" VALUES ('Ada')`,
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if err := svc.EditColumn(ctx, "column-quoted", "user-data", EditColumnRequest{
		OriginalName: "display-name",
		Name:         "display name",
		Type:         "TEXT",
		Nullable:     true,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := svc.Query(
		ctx,
		"column-quoted",
		`SELECT "display name" FROM "user-data"`,
		nil,
		1,
		0,
	)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "Ada" {
		t.Fatalf("quoted edit result = %+v, error = %v", rows, err)
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

func TestEditColumnCanRelaxNullabilityAndRemoveDefault(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()
	if _, err := svc.CreateDatabase(ctx, "column-relax"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(
		ctx,
		"column-relax",
		"CREATE TABLE items (status TEXT NOT NULL DEFAULT 'active'); INSERT INTO items DEFAULT VALUES",
		nil,
	); err != nil {
		t.Fatal(err)
	}
	if err := svc.EditColumn(ctx, "column-relax", "items", EditColumnRequest{
		OriginalName: "status",
		Name:         "status",
		Type:         "TEXT",
		Nullable:     true,
		DefaultValue: nil,
	}); err != nil {
		t.Fatal(err)
	}
	schema, err := svc.GetSchema(ctx, "column-relax")
	if err != nil {
		t.Fatal(err)
	}
	column := schema[0].Columns[0]
	if !column.Nullable || column.DefaultValue != "" {
		t.Fatalf("column = %+v, want nullable without default", column)
	}
	if _, err := svc.Execute(ctx, "column-relax", "INSERT INTO items (status) VALUES (NULL)", nil); err != nil {
		t.Fatalf("nullable column rejected NULL: %v", err)
	}
}

func TestEditColumnRestoresForeignKeyMode(t *testing.T) {
	svc := setupTestService(t)
	ctx := context.Background()
	if _, err := svc.CreateDatabase(ctx, "column-fk-mode"); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(ctx, "column-fk-mode", "PRAGMA foreign_keys = ON", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.Execute(ctx, "column-fk-mode", "CREATE TABLE items (value TEXT)", nil); err != nil {
		t.Fatal(err)
	}
	if err := svc.EditColumn(ctx, "column-fk-mode", "items", EditColumnRequest{
		OriginalName: "value",
		Name:         "renamed",
		Type:         "TEXT",
		Nullable:     true,
	}); err != nil {
		t.Fatal(err)
	}
	result, err := svc.Query(ctx, "column-fk-mode", "PRAGMA foreign_keys", nil, 1, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Rows) != 1 || result.Rows[0][0] != "1" {
		t.Fatalf("foreign key mode = %+v, want enabled", result.Rows)
	}
}
