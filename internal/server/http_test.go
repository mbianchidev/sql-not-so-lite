package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/config"
	"github.com/mbianchidev/sql-not-so-lite/internal/service"
	"github.com/mbianchidev/sql-not-so-lite/internal/store"
	_ "modernc.org/sqlite"
)

type scanResponse struct {
	Scanned int `json:"scanned"`
	Files   []struct {
		SourcePath string `json:"source_path"`
	} `json:"files"`
}

type discoveredResponse struct {
	ID        int64 `json:"id"`
	IsReplica bool  `json:"is_replica"`
}

func newScanTestServer(t *testing.T, scanRoot string) *HTTPServer {
	t.Helper()
	cat, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatalf("open catalog: %v", err)
	}
	t.Cleanup(func() {
		if err := cat.Close(); err != nil {
			t.Errorf("close catalog: %v", err)
		}
	})

	cfg := config.DefaultConfig()
	cfg.Scanner.ScanRoot = scanRoot
	return NewHTTPServer(nil, 0, cat, cfg)
}

func createHTTPTestDatabase(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open test database: %v", err)
	}
	t.Cleanup(func() {
		if err := db.Close(); err != nil {
			t.Errorf("close test database: %v", err)
		}
	})
	if _, err := db.Exec("CREATE TABLE test (id INTEGER PRIMARY KEY, value TEXT)"); err != nil {
		t.Fatalf("create test table: %v", err)
	}
	if _, err := db.Exec("INSERT INTO test (value) VALUES ('fixture')"); err != nil {
		t.Fatalf("insert test row: %v", err)
	}
}

func decodeScanResponse(t *testing.T, rec *httptest.ResponseRecorder) scanResponse {
	t.Helper()
	var response scanResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	return response
}

func resolvedHTTPTestPath(t *testing.T, path string) string {
	t.Helper()
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatalf("resolve test path: %v", err)
	}
	return resolved
}

func TestHandleScanReturnsEmptyFilesArray(t *testing.T) {
	server := newScanTestServer(t, t.TempDir())

	req := httptest.NewRequest(http.MethodPost, "/api/scan", nil)
	rec := httptest.NewRecorder()
	server.handleScan(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	response := decodeScanResponse(t, rec)
	if response.Scanned != 0 {
		t.Fatalf("expected no scanned files, got %d", response.Scanned)
	}
	if response.Files == nil {
		t.Fatal("expected files to be an empty array, got null")
	}
}

func TestHandleScanUsesConfiguredRootRecursively(t *testing.T) {
	scanRoot := t.TempDir()
	nestedDir := filepath.Join(scanRoot, "nested", "deeper")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}

	dbPath := filepath.Join(nestedDir, "default.sqlite")
	createHTTPTestDatabase(t, dbPath)

	server := newScanTestServer(t, scanRoot)
	req := httptest.NewRequest(http.MethodPost, "/api/scan", nil)
	rec := httptest.NewRecorder()
	server.handleScan(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	response := decodeScanResponse(t, rec)
	if response.Scanned != 1 || len(response.Files) != 1 {
		t.Fatalf("expected one scanned file, got scanned=%d files=%d", response.Scanned, len(response.Files))
	}
	expectedPath := resolvedHTTPTestPath(t, dbPath)
	if response.Files[0].SourcePath != expectedPath {
		t.Fatalf("source path = %q, want %q", response.Files[0].SourcePath, expectedPath)
	}
}

func TestHandleScanSkipsSnapshotDirectory(t *testing.T) {
	scanRoot := t.TempDir()
	snapshotDir := filepath.Join(scanRoot, "snapshots")
	if err := os.MkdirAll(snapshotDir, 0o755); err != nil {
		t.Fatalf("create snapshot directory: %v", err)
	}
	createHTTPTestDatabase(t, filepath.Join(snapshotDir, "v1.sqlite"))
	createHTTPTestDatabase(t, filepath.Join(scanRoot, "source.sqlite"))

	server := newScanTestServer(t, scanRoot)
	server.cfg.Replicator.SnapshotDir = snapshotDir
	req := httptest.NewRequest(http.MethodPost, "/api/scan", nil)
	rec := httptest.NewRecorder()
	server.handleScan(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	response := decodeScanResponse(t, rec)
	if response.Scanned != 1 || len(response.Files) != 1 {
		t.Fatalf("expected only source database, got scanned=%d files=%d", response.Scanned, len(response.Files))
	}
	if response.Files[0].SourcePath != resolvedHTTPTestPath(t, filepath.Join(scanRoot, "source.sqlite")) {
		t.Fatalf("unexpected scanned file: %s", response.Files[0].SourcePath)
	}
}

func TestReplicaIsMarkedAndCannotBeReplicated(t *testing.T) {
	scanRoot := t.TempDir()
	replicaDir := filepath.Join(scanRoot, "replicas")
	if err := os.MkdirAll(replicaDir, 0o755); err != nil {
		t.Fatalf("create replica directory: %v", err)
	}
	replicaPath := filepath.Join(replicaDir, "managed.sqlite")
	createHTTPTestDatabase(t, replicaPath)

	server := newScanTestServer(t, scanRoot)
	server.cfg.Replicator.ReplicaDir = replicaDir
	id, err := server.catalog.UpsertDiscovered(&catalog.DiscoveredDB{
		Name:       "managed",
		SourcePath: replicaPath,
		Status:     "discovered",
	})
	if err != nil {
		t.Fatalf("catalog replica: %v", err)
	}

	listReq := httptest.NewRequest(http.MethodGet, "/api/discovered", nil)
	listRec := httptest.NewRecorder()
	server.handleDiscovered(listRec, listReq)
	if listRec.Code != http.StatusOK {
		t.Fatalf("list status = %d, body = %s", listRec.Code, listRec.Body.String())
	}
	var discovered []discoveredResponse
	if err := json.NewDecoder(listRec.Body).Decode(&discovered); err != nil {
		t.Fatalf("decode discovered databases: %v", err)
	}
	if len(discovered) != 1 || !discovered[0].IsReplica {
		t.Fatalf("expected marked replica, got %+v", discovered)
	}

	replicateReq := httptest.NewRequest(http.MethodPost, "/api/discovered/1/replicate", nil)
	replicateRec := httptest.NewRecorder()
	server.handleStartReplication(replicateRec, replicateReq, id)
	if replicateRec.Code != http.StatusConflict {
		t.Fatalf("replicate status = %d, want %d: %s", replicateRec.Code, http.StatusConflict, replicateRec.Body.String())
	}
}

func TestHandleScanUsesRequestedPathRecursively(t *testing.T) {
	requestedRoot := t.TempDir()
	nestedDir := filepath.Join(requestedRoot, "nested", "deeper")
	if err := os.MkdirAll(nestedDir, 0o755); err != nil {
		t.Fatalf("create nested directory: %v", err)
	}
	dbPath := filepath.Join(nestedDir, "fixture.sqlite")
	createHTTPTestDatabase(t, dbPath)

	server := newScanTestServer(t, t.TempDir())
	body, err := json.Marshal(map[string][]string{"paths": {requestedRoot}})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/scan", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleScan(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	response := decodeScanResponse(t, rec)
	if response.Scanned != 1 || len(response.Files) != 1 {
		t.Fatalf("expected one scanned file, got scanned=%d files=%d", response.Scanned, len(response.Files))
	}
	expectedPath := resolvedHTTPTestPath(t, dbPath)
	if response.Files[0].SourcePath != expectedPath {
		t.Fatalf("source path = %q, want %q", response.Files[0].SourcePath, expectedPath)
	}
}

func TestHandleScanResolvesRequestedRootSymlink(t *testing.T) {
	requestedRoot := t.TempDir()
	dbPath := filepath.Join(requestedRoot, "fixture.sqlite")
	createHTTPTestDatabase(t, dbPath)

	symlinkRoot := filepath.Join(t.TempDir(), "scan-root")
	if err := os.Symlink(requestedRoot, symlinkRoot); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	server := newScanTestServer(t, t.TempDir())
	body, err := json.Marshal(map[string][]string{"paths": {symlinkRoot}})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}
	req := httptest.NewRequest(http.MethodPost, "/api/scan", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleScan(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	response := decodeScanResponse(t, rec)
	if response.Scanned != 1 || len(response.Files) != 1 {
		t.Fatalf("expected one scanned file, got scanned=%d files=%d", response.Scanned, len(response.Files))
	}
	expectedPath := resolvedHTTPTestPath(t, dbPath)
	if response.Files[0].SourcePath != expectedPath {
		t.Fatalf("source path = %q, want %q", response.Files[0].SourcePath, expectedPath)
	}
}

func TestHandleScanRejectsMissingPath(t *testing.T) {
	server := newScanTestServer(t, t.TempDir())
	body, err := json.Marshal(map[string][]string{"paths": {filepath.Join(t.TempDir(), "missing")}})
	if err != nil {
		t.Fatalf("encode request: %v", err)
	}

	req := httptest.NewRequest(http.MethodPost, "/api/scan", bytes.NewReader(body))
	rec := httptest.NewRecorder()
	server.handleScan(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected status %d, got %d: %s", http.StatusBadRequest, rec.Code, rec.Body.String())
	}
}

func TestHandleScanRejectsOverlappingScan(t *testing.T) {
	server := newScanTestServer(t, t.TempDir())
	server.scanMu.Lock()
	defer server.scanMu.Unlock()

	req := httptest.NewRequest(http.MethodPost, "/api/scan", nil)
	rec := httptest.NewRecorder()
	server.handleScan(rec, req)

	if rec.Code != http.StatusConflict {
		t.Fatalf("expected status %d, got %d: %s", http.StatusConflict, rec.Code, rec.Body.String())
	}
}

func TestScanStopsAfterServerCancellation(t *testing.T) {
	server := newScanTestServer(t, t.TempDir())
	server.scanCancel()

	_, err := server.Scan(context.Background(), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Scan error = %v, want context.Canceled", err)
	}
}

func setupHTTPTestServer(t *testing.T) (*HTTPServer, *catalog.Catalog) {
	t.Helper()
	dataDir := t.TempDir()
	manager, err := store.NewManager(dataDir, 10)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		manager.CloseAll()
		cat.Close()
	})
	return NewHTTPServer(service.NewDatabaseService(manager), 0, cat, nil), cat
}

func TestCreateDatabaseRegistersCatalog(t *testing.T) {
	server, cat := setupHTTPTestServer(t)
	request := httptest.NewRequest(http.MethodPost, "/api/databases", bytes.NewBufferString(`{"name":"tracked"}`))
	response := httptest.NewRecorder()

	server.handleDatabases(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	var info service.DBInfo
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	discovered, err := cat.GetDiscoveredByPath(info.Path)
	if err != nil {
		t.Fatalf("database not registered: %v", err)
	}
	if discovered.Name != "tracked" || discovered.Priority != "app_data" {
		t.Fatalf("unexpected catalog entry: %+v", discovered)
	}

	deleteRequest := httptest.NewRequest(http.MethodDelete, "/api/databases/tracked", nil)
	deleteResponse := httptest.NewRecorder()
	server.handleDatabase(deleteResponse, deleteRequest)
	if deleteResponse.Code != http.StatusOK {
		t.Fatalf("delete status = %d, body = %s", deleteResponse.Code, deleteResponse.Body.String())
	}
	if _, err := cat.GetDiscoveredByPath(info.Path); err == nil {
		t.Fatal("catalog entry still exists after database deletion")
	}
}

func TestCreateDatabaseDisambiguatesCatalogName(t *testing.T) {
	server, cat := setupHTTPTestServer(t)
	if _, err := cat.UpsertDiscovered(&catalog.DiscoveredDB{
		Name:       "tracked",
		SourcePath: "/external/tracked.sqlite",
	}); err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodPost, "/api/databases", bytes.NewBufferString(`{"name":"tracked"}`))
	response := httptest.NewRecorder()
	server.handleDatabases(response, request)
	if response.Code != http.StatusCreated {
		t.Fatalf("status = %d, body = %s", response.Code, response.Body.String())
	}

	var info service.DBInfo
	if err := json.NewDecoder(response.Body).Decode(&info); err != nil {
		t.Fatal(err)
	}
	discovered, err := cat.GetDiscoveredByPath(info.Path)
	if err != nil {
		t.Fatal(err)
	}
	if discovered.Name != "tracked (2)" {
		t.Fatalf("catalog name = %q, want %q", discovered.Name, "tracked (2)")
	}
}

func TestSchemaMutationRoutes(t *testing.T) {
	server, _ := setupHTTPTestServer(t)
	if _, err := server.svc.CreateDatabase(context.Background(), "schema"); err != nil {
		t.Fatal(err)
	}

	createRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/databases/schema/tables",
		bytes.NewBufferString(`{"Name":"users","Columns":[{"Name":"id","Type":"INTEGER","PrimaryKey":true}]}`),
	)
	createResponse := httptest.NewRecorder()
	server.handleDatabase(createResponse, createRequest)
	if createResponse.Code != http.StatusCreated {
		t.Fatalf("create table status = %d, body = %s", createResponse.Code, createResponse.Body.String())
	}

	columnRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/databases/schema/tables/users/columns",
		bytes.NewBufferString(`{"Name":"email","Type":"TEXT","NotNull":false,"PrimaryKey":false}`),
	)
	columnResponse := httptest.NewRecorder()
	server.handleDatabase(columnResponse, columnRequest)
	if columnResponse.Code != http.StatusCreated {
		t.Fatalf("add column status = %d, body = %s", columnResponse.Code, columnResponse.Body.String())
	}

	tables, err := server.svc.GetSchema(context.Background(), "schema")
	if err != nil {
		t.Fatal(err)
	}
	if len(tables) != 1 || len(tables[0].Columns) != 2 {
		t.Fatalf("unexpected schema: %+v", tables)
	}

	rowRequest := httptest.NewRequest(
		http.MethodPost,
		"/api/databases/schema/tables/users/rows",
		bytes.NewBufferString(`{"Columns":["email"],"Values":["dev@example.com"]}`),
	)
	rowResponse := httptest.NewRecorder()
	server.handleDatabase(rowResponse, rowRequest)
	if rowResponse.Code != http.StatusCreated {
		t.Fatalf("insert row status = %d, body = %s", rowResponse.Code, rowResponse.Body.String())
	}

	rows, err := server.svc.Query(context.Background(), "schema", `SELECT email FROM "users"`, nil, 10, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows.Rows) != 1 || rows.Rows[0][0] != "dev@example.com" {
		t.Fatalf("unexpected inserted rows: %+v", rows.Rows)
	}
}
