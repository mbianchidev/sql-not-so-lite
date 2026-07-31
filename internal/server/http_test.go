package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/config"
)

func TestHandleScanReturnsEmptyFilesArray(t *testing.T) {
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
	cfg.Scanner.ScanRoot = t.TempDir()
	server := &HTTPServer{catalog: cat, cfg: cfg}

	req := httptest.NewRequest(http.MethodPost, "/api/scan", nil)
	rec := httptest.NewRecorder()
	server.handleScan(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected status %d, got %d: %s", http.StatusOK, rec.Code, rec.Body.String())
	}

	var response struct {
		Scanned int               `json:"scanned"`
		Files   []json.RawMessage `json:"files"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &response); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if response.Scanned != 0 {
		t.Fatalf("expected no scanned files, got %d", response.Scanned)
	}
	if response.Files == nil {
		t.Fatal("expected files to be an empty array, got null")
	}
}
