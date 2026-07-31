package server

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/config"
	_ "modernc.org/sqlite"
)

type scanResponse struct {
	Scanned int `json:"scanned"`
	Files   []struct {
		SourcePath string `json:"source_path"`
	} `json:"files"`
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
	return &HTTPServer{catalog: cat, cfg: cfg}
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
