package schema

import (
	"database/sql"
	"path/filepath"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "test.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestExtractSchema(t *testing.T) {
	db := openTestDB(t)

	// Create tables and an index
	for _, stmt := range []string{
		`CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT NOT NULL, email TEXT)`,
		`CREATE INDEX idx_users_email ON users(email)`,
		`CREATE TABLE posts (id INTEGER PRIMARY KEY, user_id INTEGER, title TEXT, FOREIGN KEY(user_id) REFERENCES users(id))`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatalf("exec %q: %v", stmt, err)
		}
	}

	schema, err := ExtractSchema(db)
	if err != nil {
		t.Fatalf("ExtractSchema: %v", err)
	}

	// Should contain all three objects
	if !strings.Contains(schema, "users") {
		t.Error("schema missing 'users' table")
	}
	if !strings.Contains(schema, "posts") {
		t.Error("schema missing 'posts' table")
	}
	if !strings.Contains(schema, "idx_users_email") {
		t.Error("schema missing 'idx_users_email' index")
	}

	// Verify sorted order: idx_users_email < posts < users
	parts := strings.Split(schema, ";\n")
	if len(parts) != 3 {
		t.Fatalf("expected 3 statements, got %d", len(parts))
	}
	if !strings.Contains(parts[0], "idx_users_email") {
		t.Errorf("first statement should be idx_users_email, got: %s", parts[0])
	}
	if !strings.Contains(parts[1], "posts") {
		t.Errorf("second statement should be posts, got: %s", parts[1])
	}
	if !strings.Contains(parts[2], "users") {
		t.Errorf("third statement should be users, got: %s", parts[2])
	}
}

func TestExtractSchema_Empty(t *testing.T) {
	db := openTestDB(t)

	schema, err := ExtractSchema(db)
	if err != nil {
		t.Fatalf("ExtractSchema: %v", err)
	}
	if schema != "" {
		t.Errorf("expected empty schema, got: %q", schema)
	}
}

func TestNormalizeSchema(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "collapse whitespace",
			input: "CREATE TABLE  users (\n  id   INTEGER,\n  name  TEXT\n)",
			want:  "CREATE TABLE users ( id INTEGER, name TEXT )",
		},
		{
			name:  "remove IF NOT EXISTS",
			input: "CREATE TABLE IF NOT EXISTS users (id INTEGER)",
			want:  "CREATE TABLE users (id INTEGER)",
		},
		{
			name:  "sort statements",
			input: "CREATE TABLE users (id INTEGER);\nCREATE TABLE accounts (id INTEGER)",
			want:  "CREATE TABLE accounts (id INTEGER);\nCREATE TABLE users (id INTEGER)",
		},
		{
			name:  "trim and skip empty",
			input: "  CREATE TABLE a (id INTEGER) ;  ; CREATE TABLE b (id INTEGER)  ",
			want:  "CREATE TABLE a (id INTEGER);\nCREATE TABLE b (id INTEGER)",
		},
		{
			name:  "case insensitive IF NOT EXISTS removal",
			input: "CREATE TABLE if not exists Foo (id INTEGER)",
			want:  "CREATE TABLE Foo (id INTEGER)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := NormalizeSchema(tt.input)
			if got != tt.want {
				t.Errorf("NormalizeSchema(%q)\ngot:  %q\nwant: %q", tt.input, got, tt.want)
			}
		})
	}
}

func TestHashSchema(t *testing.T) {
	input := "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)"

	h1 := HashSchema(input)
	h2 := HashSchema(input)
	if h1 != h2 {
		t.Error("same input produced different hashes")
	}
	if len(h1) != 64 {
		t.Errorf("expected 64-char hex digest, got %d chars", len(h1))
	}

	// Different input should give different hash
	h3 := HashSchema(input + " NOT NULL")
	if h1 == h3 {
		t.Error("different inputs produced same hash")
	}
}

func TestDiffSchemas_AddedTable(t *testing.T) {
	oldSQL := "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)"
	newSQL := "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT);\nCREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT)"

	diff, err := DiffSchemas(oldSQL, newSQL)
	if err != nil {
		t.Fatalf("DiffSchemas: %v", err)
	}
	if len(diff.Added) != 1 {
		t.Fatalf("expected 1 added, got %d", len(diff.Added))
	}
	if diff.Added[0].Name != "posts" {
		t.Errorf("expected added table 'posts', got %q", diff.Added[0].Name)
	}
	if diff.Added[0].Type != "table" {
		t.Errorf("expected type 'table', got %q", diff.Added[0].Type)
	}
	if len(diff.Dropped) != 0 {
		t.Errorf("expected 0 dropped, got %d", len(diff.Dropped))
	}
	if len(diff.Modified) != 0 {
		t.Errorf("expected 0 modified, got %d", len(diff.Modified))
	}
}

func TestDiffSchemas_DroppedTable(t *testing.T) {
	oldSQL := "CREATE TABLE users (id INTEGER);\nCREATE TABLE old_cache (id INTEGER)"
	newSQL := "CREATE TABLE users (id INTEGER)"

	diff, err := DiffSchemas(oldSQL, newSQL)
	if err != nil {
		t.Fatalf("DiffSchemas: %v", err)
	}
	if len(diff.Dropped) != 1 {
		t.Fatalf("expected 1 dropped, got %d", len(diff.Dropped))
	}
	if diff.Dropped[0].Name != "old_cache" {
		t.Errorf("expected dropped 'old_cache', got %q", diff.Dropped[0].Name)
	}
}

func TestDiffSchemas_ModifiedTable(t *testing.T) {
	oldSQL := "CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT)"
	newSQL := "CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, description TEXT)"

	diff, err := DiffSchemas(oldSQL, newSQL)
	if err != nil {
		t.Fatalf("DiffSchemas: %v", err)
	}
	if len(diff.Modified) != 1 {
		t.Fatalf("expected 1 modified, got %d", len(diff.Modified))
	}
	if diff.Modified[0].Name != "products" {
		t.Errorf("expected modified 'products', got %q", diff.Modified[0].Name)
	}
}

func TestDiffSchemas_NoChanges(t *testing.T) {
	schema := "CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)"

	diff, err := DiffSchemas(schema, schema)
	if err != nil {
		t.Fatalf("DiffSchemas: %v", err)
	}
	if len(diff.Added) != 0 || len(diff.Dropped) != 0 || len(diff.Modified) != 0 {
		t.Errorf("expected empty diff, got added=%d dropped=%d modified=%d",
			len(diff.Added), len(diff.Dropped), len(diff.Modified))
	}
}

func TestDiffSchemas_Complex(t *testing.T) {
	oldSQL := strings.Join([]string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT)",
		"CREATE TABLE sessions (id INTEGER PRIMARY KEY, token TEXT)",
		"CREATE INDEX idx_sessions_token ON sessions(token)",
	}, ";\n")

	newSQL := strings.Join([]string{
		"CREATE TABLE users (id INTEGER PRIMARY KEY, name TEXT, email TEXT)",
		"CREATE TABLE posts (id INTEGER PRIMARY KEY, title TEXT)",
		"CREATE INDEX idx_posts_title ON posts(title)",
	}, ";\n")

	diff, err := DiffSchemas(oldSQL, newSQL)
	if err != nil {
		t.Fatalf("DiffSchemas: %v", err)
	}

	// Added: posts table, idx_posts_title
	if len(diff.Added) != 2 {
		t.Errorf("expected 2 added, got %d", len(diff.Added))
	}
	// Dropped: sessions table, idx_sessions_token
	if len(diff.Dropped) != 2 {
		t.Errorf("expected 2 dropped, got %d", len(diff.Dropped))
	}
	// Modified: users (added email column)
	if len(diff.Modified) != 1 {
		t.Errorf("expected 1 modified, got %d", len(diff.Modified))
	}
}

func TestSummary(t *testing.T) {
	tests := []struct {
		name string
		diff SchemaDiff
		want string
	}{
		{
			name: "no changes",
			diff: SchemaDiff{},
			want: "no changes",
		},
		{
			name: "added table and index",
			diff: SchemaDiff{
				Added: []SchemaObject{
					{Type: "table", Name: "users", SQL: "CREATE TABLE users (id INTEGER)"},
					{Type: "index", Name: "idx_users_email", SQL: "CREATE INDEX idx_users_email ON users(email)"},
				},
			},
			want: "added table users; added index idx_users_email",
		},
		{
			name: "dropped table",
			diff: SchemaDiff{
				Dropped: []SchemaObject{
					{Type: "table", Name: "old_cache", SQL: "CREATE TABLE old_cache (id INTEGER)"},
				},
			},
			want: "dropped table old_cache",
		},
		{
			name: "modified table with column added",
			diff: SchemaDiff{
				Modified: []ModifiedObject{
					{
						Type:   "table",
						Name:   "products",
						OldSQL: "CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT)",
						NewSQL: "CREATE TABLE products (id INTEGER PRIMARY KEY, name TEXT, description TEXT)",
					},
				},
			},
			want: "modified table products (column added: description TEXT)",
		},
		{
			name: "mixed changes",
			diff: SchemaDiff{
				Added: []SchemaObject{
					{Type: "table", Name: "users"},
				},
				Dropped: []SchemaObject{
					{Type: "table", Name: "old_cache"},
				},
			},
			want: "added table users; dropped table old_cache",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tt.diff.Summary()
			if got != tt.want {
				t.Errorf("Summary()\ngot:  %q\nwant: %q", got, tt.want)
			}
		})
	}
}
