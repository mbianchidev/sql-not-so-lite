package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/config"
	"github.com/mbianchidev/sql-not-so-lite/internal/replicator"
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

type scanStatusResponse struct {
	InProgress bool `json:"in_progress"`
}

type discoveredResponse struct {
	ID        int64 `json:"id"`
	IsReplica bool  `json:"is_replica"`
	Favorite  bool  `json:"favorite"`
	Available bool  `json:"available"`
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
	defer db.Close()
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

func TestHandleScanReturnsSharedStatus(t *testing.T) {
	server := newScanTestServer(t, t.TempDir())
	server.scanStatusMu.Lock()
	server.scanInProgress = true
	server.scanStatusMu.Unlock()

	req := httptest.NewRequest(http.MethodGet, "/api/scan", nil)
	rec := httptest.NewRecorder()
	server.handleScan(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}
	var response scanStatusResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if !response.InProgress {
		t.Fatal("expected scan status to report an active scan")
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

func TestRepeatedScanPreservesFavoriteAndTracksAvailability(t *testing.T) {
	scanRoot := t.TempDir()
	dbPath := filepath.Join(scanRoot, "favorite.sqlite")
	createHTTPTestDatabase(t, dbPath)
	server := newScanTestServer(t, scanRoot)

	if _, err := server.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	resolvedPath := resolvedHTTPTestPath(t, dbPath)
	discovered, err := server.catalog.GetDiscoveredByPath(resolvedPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := server.catalog.UpdateFavorite(discovered.ID, true); err != nil {
		t.Fatal(err)
	}

	if err := os.Remove(dbPath); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	missing, err := server.catalog.GetDiscovered(discovered.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !missing.Favorite || missing.Available {
		t.Fatalf("missing database = %+v, want favorite and unavailable", missing)
	}

	createHTTPTestDatabase(t, dbPath)
	if _, err := server.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	restored, err := server.catalog.GetDiscovered(discovered.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !restored.Favorite || !restored.Available {
		t.Fatalf("restored database = %+v, want favorite and available", restored)
	}
}

func TestScanOnlyChecksAvailabilityInsideRequestedRoots(t *testing.T) {
	firstRoot := t.TempDir()
	secondRoot := t.TempDir()
	firstPath := filepath.Join(firstRoot, "first.sqlite")
	secondPath := filepath.Join(secondRoot, "second.sqlite")
	createHTTPTestDatabase(t, firstPath)
	createHTTPTestDatabase(t, secondPath)
	server := newScanTestServer(t, firstRoot)

	if _, err := server.Scan(context.Background(), []string{firstRoot, secondRoot}); err != nil {
		t.Fatal(err)
	}
	second, err := server.catalog.GetDiscoveredByPath(resolvedHTTPTestPath(t, secondPath))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(secondPath); err != nil {
		t.Fatal(err)
	}
	if _, err := server.Scan(context.Background(), []string{firstRoot}); err != nil {
		t.Fatal(err)
	}
	unchanged, err := server.catalog.GetDiscovered(second.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !unchanged.Available {
		t.Fatal("database outside requested roots was marked unavailable")
	}
}

func TestScanConfirmsFilteredExistingDatabaseByStat(t *testing.T) {
	scanRoot := t.TempDir()
	excludedDir := filepath.Join(scanRoot, "node_modules")
	if err := os.MkdirAll(excludedDir, 0o755); err != nil {
		t.Fatal(err)
	}
	dbPath := filepath.Join(excludedDir, "filtered.sqlite")
	createHTTPTestDatabase(t, dbPath)
	server := newScanTestServer(t, scanRoot)
	resolvedPath := resolvedHTTPTestPath(t, dbPath)
	id, err := server.catalog.UpsertDiscovered(&catalog.DiscoveredDB{
		Name:       "filtered",
		SourcePath: resolvedPath,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.catalog.UpdateAvailability(id, false); err != nil {
		t.Fatal(err)
	}

	if _, err := server.Scan(context.Background(), nil); err != nil {
		t.Fatal(err)
	}
	got, err := server.catalog.GetDiscovered(id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Available {
		t.Fatal("existing filtered database was not confirmed available")
	}
}

func TestHandleDiscoveredPatchFavorite(t *testing.T) {
	server := newScanTestServer(t, t.TempDir())
	id, err := server.catalog.UpsertDiscovered(&catalog.DiscoveredDB{
		Name:       "favorite",
		SourcePath: "/favorite.sqlite",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/discovered/1",
		bytes.NewBufferString(`{"favorite":true}`),
	)
	rec := httptest.NewRecorder()

	server.handleDiscoveredGet(rec, req, id)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	got, err := server.catalog.GetDiscovered(id)
	if err != nil {
		t.Fatal(err)
	}
	if !got.Favorite {
		t.Fatal("favorite was not updated")
	}
}

func TestHandleDiscoveredPatchRejectsInvalidBody(t *testing.T) {
	server := newScanTestServer(t, t.TempDir())
	id, err := server.catalog.UpsertDiscovered(&catalog.DiscoveredDB{
		Name:       "favorite",
		SourcePath: "/favorite.sqlite",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(
		http.MethodPatch,
		"/api/discovered/1",
		bytes.NewBufferString(`{"favorite":"yes"}`),
	)
	rec := httptest.NewRecorder()

	server.handleDiscoveredGet(rec, req, id)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleStartReplicationResumesExistingReplica(t *testing.T) {
	root := t.TempDir()
	sourcePath := filepath.Join(root, "source.sqlite")
	createHTTPTestDatabase(t, sourcePath)
	server := newScanTestServer(t, root)
	server.cfg.Replicator.ReplicaDir = filepath.Join(root, "replicas")
	server.cfg.Replicator.SnapshotDir = filepath.Join(root, "snapshots")
	if err := os.MkdirAll(server.cfg.Replicator.ReplicaDir, 0o755); err != nil {
		t.Fatal(err)
	}
	id, err := server.catalog.UpsertDiscovered(&catalog.DiscoveredDB{
		Name:       "source",
		SourcePath: sourcePath,
		Status:     "paused",
	})
	if err != nil {
		t.Fatal(err)
	}
	replicaPath := filepath.Join(server.cfg.Replicator.ReplicaDir, "source.sqlite")
	if _, err := replicator.InitialSync(sourcePath, replicaPath); err != nil {
		t.Fatal(err)
	}
	snapshotID, err := server.catalog.InsertSnapshot(&catalog.Snapshot{
		DatabaseID:    id,
		Version:       1,
		SchemaVersion: 0,
		SnapshotPath:  filepath.Join(server.cfg.Replicator.SnapshotDir, "source", "v1.sqlite"),
		Trigger:       "initial",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := server.catalog.SetReplicationState(&catalog.ReplicationState{
		DatabaseID:     id,
		ReplicaName:    "source",
		BaseSnapshotID: sql.NullInt64{Int64: snapshotID, Valid: true},
	}); err != nil {
		t.Fatal(err)
	}
	source, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec("INSERT INTO test (value) VALUES ('resumed')"); err != nil {
		source.Close()
		t.Fatal(err)
	}
	source.Close()

	req := httptest.NewRequest(http.MethodPost, "/api/discovered/1/replicate", nil)
	rec := httptest.NewRecorder()
	server.handleStartReplication(rec, req, id)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	replica, err := sql.Open("sqlite", replicaPath)
	if err != nil {
		t.Fatal(err)
	}
	defer replica.Close()
	var count int
	if err := replica.QueryRow("SELECT COUNT(*) FROM test").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("replica row count = %d, want 2", count)
	}
	snapshots, err := server.catalog.ListSnapshots(id)
	if err != nil {
		t.Fatal(err)
	}
	if len(snapshots) != 1 {
		t.Fatalf("snapshot count = %d, want 1", len(snapshots))
	}
	state, err := server.catalog.GetReplicationState(id)
	if err != nil {
		t.Fatal(err)
	}
	if state.SyncMode != "differential" {
		t.Fatalf("sync mode = %q, want differential", state.SyncMode)
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

func TestDiscoveredReadOnlyInspector(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "external.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE items (
			id INTEGER PRIMARY KEY,
			name TEXT NOT NULL,
			score REAL DEFAULT 0
		);
		CREATE INDEX items_lower_name ON items(lower(name));
		INSERT INTO items (name, score) VALUES ('one', 1.5), ('two', 2.5);
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	server := newScanTestServer(t, root)
	id, err := server.catalog.UpsertDiscovered(&catalog.DiscoveredDB{
		Name:       "external",
		SourcePath: path,
		Status:     "discovered",
		Available:  true,
	})
	if err != nil {
		t.Fatal(err)
	}

	schemaRequest := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/discovered/%d/schema", id), nil)
	schemaResponse := httptest.NewRecorder()
	server.handleDiscoveredItem(schemaResponse, schemaRequest)
	if schemaResponse.Code != http.StatusOK {
		t.Fatalf("schema status = %d, body = %s", schemaResponse.Code, schemaResponse.Body.String())
	}
	var schema []service.TableInfo
	if err := json.Unmarshal(schemaResponse.Body.Bytes(), &schema); err != nil {
		t.Fatal(err)
	}
	if len(schema) != 1 || schema[0].Name != "items" || len(schema[0].Columns) != 3 {
		t.Fatalf("unexpected schema: %#v", schema)
	}
	if len(schema[0].Indexes) != 1 || schema[0].Indexes[0].Columns == nil {
		t.Fatalf("unexpected expression index: %#v", schema[0].Indexes)
	}

	tableRequest := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/discovered/%d/table?name=items&limit=1&offset=1", id),
		nil,
	)
	tableResponse := httptest.NewRecorder()
	server.handleDiscoveredItem(tableResponse, tableRequest)
	if tableResponse.Code != http.StatusOK {
		t.Fatalf("table status = %d, body = %s", tableResponse.Code, tableResponse.Body.String())
	}
	tableResult := decodeQueryResult(t, tableResponse)
	if len(tableResult.Rows) != 1 || tableResult.Rows[0][1] != "two" {
		t.Fatalf("unexpected table rows: %#v", tableResult.Rows)
	}

	queryBody := bytes.NewBufferString(`{"sql":"SELECT name, score FROM items ORDER BY id","limit":10}`)
	queryRequest := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/discovered/%d/query", id),
		queryBody,
	)
	queryResponse := httptest.NewRecorder()
	server.handleDiscoveredItem(queryResponse, queryRequest)
	if queryResponse.Code != http.StatusOK {
		t.Fatalf("query status = %d, body = %s", queryResponse.Code, queryResponse.Body.String())
	}
	queryResult := decodeQueryResult(t, queryResponse)
	if len(queryResult.Rows) != 2 || queryResult.Rows[0][0] != "one" {
		t.Fatalf("unexpected query rows: %#v", queryResult.Rows)
	}

	mutationBody := bytes.NewBufferString(`{"sql":"SELECT * FROM items; DELETE FROM items"}`)
	mutationRequest := httptest.NewRequest(
		http.MethodPost,
		fmt.Sprintf("/api/discovered/%d/query", id),
		mutationBody,
	)
	mutationResponse := httptest.NewRecorder()
	server.handleDiscoveredItem(mutationResponse, mutationRequest)
	if mutationResponse.Code != http.StatusBadRequest {
		t.Fatalf("mutation status = %d, body = %s", mutationResponse.Code, mutationResponse.Body.String())
	}

	verify, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer verify.Close()
	var count int
	if err := verify.QueryRow("SELECT COUNT(*) FROM items").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("row count = %d, want 2", count)
	}

	extraPathRequest := httptest.NewRequest(
		http.MethodGet,
		fmt.Sprintf("/api/discovered/%d/schema/extra", id),
		nil,
	)
	extraPathResponse := httptest.NewRecorder()
	server.handleDiscoveredItem(extraPathResponse, extraPathRequest)
	if extraPathResponse.Code != http.StatusNotFound {
		t.Fatalf("extra path status = %d, body = %s", extraPathResponse.Code, extraPathResponse.Body.String())
	}
}

func TestDiscoveredInspectorReportsMissingFile(t *testing.T) {
	server := newScanTestServer(t, t.TempDir())
	id, err := server.catalog.UpsertDiscovered(&catalog.DiscoveredDB{
		Name:       "missing",
		SourcePath: filepath.Join(t.TempDir(), "missing.sqlite"),
		Status:     "discovered",
		Available:  true,
	})
	if err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, fmt.Sprintf("/api/discovered/%d/schema", id), nil)
	rec := httptest.NewRecorder()
	server.handleDiscoveredItem(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestValidateReadOnlyQuery(t *testing.T) {
	for _, query := range []string{
		"SELECT 1",
		"SELECT ';' AS value;",
		"-- comment\nSELECT 1;",
		"WITH values_cte(value) AS (SELECT 1) SELECT value FROM values_cte",
	} {
		if err := validateReadOnlyQuery(query); err != nil {
			t.Errorf("validateReadOnlyQuery(%q) = %v", query, err)
		}
	}
	for _, query := range []string{
		"",
		"DELETE FROM items",
		"PRAGMA query_only = 0",
		"SELECT 1; DELETE FROM items",
		"SELECT 1; SELECT 2",
		"/* unterminated",
	} {
		if err := validateReadOnlyQuery(query); err == nil {
			t.Errorf("validateReadOnlyQuery(%q) unexpectedly succeeded", query)
		}
	}
}

func decodeQueryResult(t *testing.T, rec *httptest.ResponseRecorder) service.QueryResult {
	t.Helper()
	var result service.QueryResult
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("decode query result: %v", err)
	}
	return result
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

func TestHandleColumnsEditsExistingColumn(t *testing.T) {
	server, _ := setupHTTPTestServer(t)
	ctx := context.Background()
	if _, err := server.svc.CreateDatabase(ctx, "edit-column"); err != nil {
		t.Fatal(err)
	}
	if _, err := server.svc.Execute(
		ctx,
		"edit-column",
		"CREATE TABLE items (value TEXT); INSERT INTO items VALUES ('7')",
		nil,
	); err != nil {
		t.Fatal(err)
	}
	body := bytes.NewBufferString(`{
		"OriginalName":"value",
		"Name":"amount",
		"Type":"INTEGER",
		"Nullable":false,
		"DefaultValue":"0"
	}`)
	req := httptest.NewRequest(
		http.MethodPut,
		"/api/databases/edit-column/tables/items/columns",
		body,
	)
	rec := httptest.NewRecorder()

	server.handleColumns(rec, req, "edit-column", "items")

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	rows, err := server.svc.Query(ctx, "edit-column", "SELECT amount FROM items", nil, 1, 0)
	if err != nil || len(rows.Rows) != 1 || rows.Rows[0][0] != "7" {
		t.Fatalf("edited rows = %+v, error = %v", rows, err)
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
