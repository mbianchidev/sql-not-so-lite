package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/url"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

func recordTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "fixture.sqlite")+"?_pragma=foreign_keys(1)")
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(1)
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Error(err)
		}
	})
	for _, statement := range []string{
		`CREATE TABLE inventory (id INTEGER PRIMARY KEY, sku TEXT UNIQUE NOT NULL, stock INTEGER DEFAULT 0, note TEXT, payload BLOB, doubled INTEGER GENERATED ALWAYS AS (stock * 2))`,
		`INSERT INTO inventory (sku,stock,note,payload) VALUES ('SKU-001',12,NULL,x'000102'),('SKU-002',4,'NULL',x'')`,
		`CREATE TABLE composite (tenant TEXT, item INTEGER, value TEXT, PRIMARY KEY(tenant,item)) WITHOUT ROWID`,
		`INSERT INTO composite VALUES ('fixture',1,'before')`,
		`CREATE TABLE no_key (value TEXT)`,
		`INSERT INTO no_key VALUES ('duplicate'),('duplicate')`,
		`CREATE VIEW inventory_view AS SELECT id,sku FROM inventory`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	return db
}

func TestRecordsNativeJSONAndFiltering(t *testing.T) {
	db := recordTestDB(t)
	rows, err := ListRecords(context.Background(), db, "inventory", url.Values{"stock": {"gte.10"}, "order": {"id.desc"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 || rows[0]["id"] != int64(1) || rows[0]["note"] != nil || rows[0]["payload"] != "AAEC" {
		t.Fatalf("unexpected rows: %#v", rows)
	}
	rows, err = ListRecords(context.Background(), db, "inventory", url.Values{"select": {"sku,note"}, "id": {"eq.2"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(rows[0]) != 2 || rows[0]["note"] != "NULL" {
		t.Fatalf("lost typed values: %#v", rows)
	}
}

func TestRecordsCreatePatchDeleteAndDefaults(t *testing.T) {
	db := recordTestDB(t)
	ctx := context.Background()
	row, err := MutateRecord(ctx, db, "inventory", "POST", map[string]any{"sku": "SKU-003", "payload": "AAEC"}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if row["stock"] != int64(0) || row["note"] != nil || row["payload"] != "AAEC" {
		t.Fatalf("defaults: %#v", row)
	}
	row, err = MutateRecord(ctx, db, "inventory", "PATCH", map[string]any{"stock": json.Number("9"), "note": "fixture"}, url.Values{"id": {"eq.3"}})
	if err != nil {
		t.Fatal(err)
	}
	if row["stock"] != int64(9) || row["doubled"] != int64(18) {
		t.Fatalf("patch: %#v", row)
	}
	if _, err := MutateRecord(ctx, db, "inventory", "DELETE", nil, url.Values{"id": {"eq.3"}}); err != nil {
		t.Fatal(err)
	}
	rows, err := ListRecords(ctx, db, "inventory", url.Values{"id": {"eq.3"}})
	if err != nil || len(rows) != 0 {
		t.Fatalf("row not deleted: %#v, %v", rows, err)
	}
}

func TestRecordsCompositeKeysAndImplicitRowID(t *testing.T) {
	db := recordTestDB(t)
	ctx := context.Background()
	row, err := MutateRecord(ctx, db, "composite", "PATCH", map[string]any{"value": "after"}, url.Values{"tenant": {"eq.fixture"}, "item": {"eq.1"}})
	if err != nil || row["value"] != "after" {
		t.Fatalf("composite update: %#v, %v", row, err)
	}
	meta, err := GetRecordTable(ctx, db, "no_key")
	if err != nil {
		t.Fatal(err)
	}
	if len(meta.Keys) != 1 {
		t.Fatalf("missing implicit key: %#v", meta)
	}
	rows, err := ListRecords(ctx, db, "no_key", nil)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0][meta.Keys[0]] != int64(1) {
		t.Fatalf("row ID missing: %#v", rows)
	}
	if _, err := MutateRecord(ctx, db, "no_key", "DELETE", nil, url.Values{meta.Keys[0]: {"eq.1"}}); err != nil {
		t.Fatal(err)
	}
}

func TestRecordsRejectUnsafeAndAmbiguousWrites(t *testing.T) {
	db := recordTestDB(t)
	ctx := context.Background()
	for _, test := range []struct {
		method string
		table  string
		body   map[string]any
		query  url.Values
		want   error
	}{
		{"PATCH", "inventory", map[string]any{"stock": json.Number("5")}, nil, ErrRecordInput},
		{"DELETE", "no_key", nil, url.Values{"value": {"eq.duplicate"}}, ErrRecordConflict},
		{"PATCH", "inventory", map[string]any{"doubled": json.Number("4")}, url.Values{"id": {"eq.1"}}, ErrRecordInput},
		{"POST", "inventory", map[string]any{"sku": "SKU-001"}, nil, ErrRecordConflict},
		{"POST", "inventory_view", map[string]any{"sku": "fixture"}, nil, ErrRecordReadOnly},
		{"DELETE", "inventory", nil, url.Values{"id": {"eq.999"}}, ErrRecordNotFound},
		{"PATCH", "inventory", map[string]any{"stock": json.Number("5")}, url.Values{"id": {"eq.1"}, "limit": {"1"}}, ErrRecordInput},
	} {
		_, err := MutateRecord(ctx, db, test.table, test.method, test.body, test.query)
		if !errors.Is(err, test.want) {
			t.Errorf("%s: got %v, want %v", test.method, err, test.want)
		}
	}
	rows, err := ListRecords(ctx, db, "no_key", nil)
	if err != nil || len(rows) != 2 {
		t.Fatalf("ambiguous deletion was not rolled back: %#v, %v", rows, err)
	}
}

func TestRecordsValidateQueriesAndQuoteIdentifiers(t *testing.T) {
	db := recordTestDB(t)
	ctx := context.Background()
	for _, query := range []url.Values{
		{"limit": {"-1"}}, {"limit": {"1001"}}, {"offset": {"invalid"}},
		{"order": {"sku;DROP TABLE inventory"}}, {"select": {"unknown"}},
		{"unknown": {"eq.1"}}, {"sku": {"raw.SQL"}}, {"id": {"eq.1 OR 1=1"}},
	} {
		if _, err := ListRecords(ctx, db, "inventory", query); !errors.Is(err, ErrRecordInput) {
			t.Errorf("query %v: %v", query, err)
		}

	}
	if _, err := db.Exec(`CREATE TABLE "odd/table" ("quote""field" TEXT)`); err != nil {
		t.Fatal(err)
	}
	if _, err := MutateRecord(ctx, db, "odd/table", "POST", map[string]any{`quote"field`: "safe"}, nil); err != nil {
		t.Fatal(err)
	}
	rows, err := ListRecords(ctx, db, "odd/table", url.Values{`quote"field`: {"eq.safe"}})
	if err != nil || len(rows) != 1 {
		t.Fatalf("quoted identifiers: %#v, %v", rows, err)
	}
}

func TestRecordsRejectNonFiniteDatabaseValues(t *testing.T) {
	db := recordTestDB(t)
	if _, err := db.Exec(`INSERT INTO inventory(sku,stock) VALUES ('SKU-INF',1e999)`); err != nil {
		t.Fatal(err)
	}
	if _, err := ListRecords(context.Background(), db, "inventory", url.Values{"sku": {"eq.SKU-INF"}}); !errors.Is(err, ErrRecordConflict) {
		t.Fatalf("non-finite JSON value: %v", err)
	}
}
