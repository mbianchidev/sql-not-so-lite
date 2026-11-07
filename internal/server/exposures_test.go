package server

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/config"
)

const fixtureAdminToken = "synthetic-admin-token-for-integration-tests"

func exposureTestHandler(t *testing.T) (*HTTPServer, http.Handler) {
	t.Helper()
	server, _ := setupHTTPTestServer(t)
	server.cfg = config.DefaultConfig()
	server.cfg.Server.AuthToken = fixtureAdminToken
	handler, err := server.Handler()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := server.svc.CreateDatabase(context.Background(), "fixture"); err != nil {
		t.Fatal(err)
	}
	if _, err := server.svc.Execute(context.Background(), "fixture", `CREATE TABLE inventory(id INTEGER PRIMARY KEY, sku TEXT UNIQUE, stock INTEGER DEFAULT 0); INSERT INTO inventory(sku,stock) VALUES ('SKU-001',8); CREATE TABLE hidden(value TEXT)`, nil); err != nil {
		t.Fatal(err)
	}
	return server, handler
}

func exposureRequest(t *testing.T, handler http.Handler, method, path, token, body string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, bytes.NewBufferString(body))
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func createExposure(t *testing.T, handler http.Handler, body string) string {
	t.Helper()
	response := exposureRequest(t, handler, "POST", "/api/exposures", fixtureAdminToken, body)
	if response.Code != 201 {
		t.Fatalf("expose: %d %s", response.Code, response.Body.String())
	}
	var result struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	if len(result.Token) < 32 {
		t.Fatal("weak exposure token")
	}
	return result.Token
}

func TestHTTPCRUDAndAuthorizationBoundaries(t *testing.T) {
	_, handler := exposureTestHandler(t)
	for _, path := range []string{"/api/databases", "/api/databases/fixture/query", "/api/scan", "/api/exposures", "/api/stats"} {
		response := exposureRequest(t, handler, "GET", path, "", "")
		if response.Code != 401 {
			t.Fatalf("%s: %d", path, response.Code)
		}
	}
	token := createExposure(t, handler, `{"alias":"demo","managed_database":"fixture","tables":["inventory"],"writable":true}`)
	for _, test := range []struct {
		method, path, token, body string
		status                    int
	}{
		{"GET", "/api/data/demo/inventory", "", "", 401},
		{"GET", "/api/data/demo/inventory", "wrong", "", 401},
		{"GET", "/api/databases", token, "", 401},
		{"GET", "/api/data/demo/hidden", token, "", 403},
		{"GET", "/api/data/demo/inventory?stock=gt.0", token, "", 200},
		{"POST", "/api/data/demo/inventory", token, `{"sku":"SKU-002","stock":3}`, 201},
		{"PATCH", "/api/data/demo/inventory?id=eq.2", token, `{"stock":5}`, 200},
		{"DELETE", "/api/data/demo/inventory?id=eq.2", token, "", 204},
		{"DELETE", "/api/data/demo/inventory", token, "", 400},
		{"POST", "/api/data/demo/inventory", token, `[{"sku":"bulk"}]`, 400},
		{"POST", "/api/data/demo/inventory", token, `{"sku":"A"} {"sku":"B"}`, 400},
		{"POST", "/api/data/demo/inventory", token, `{"unknown":1}`, 400},
		{"GET", "/api/data/demo", token, "", 200},
	} {
		response := exposureRequest(t, handler, test.method, test.path, test.token, test.body)
		if response.Code != test.status {
			t.Errorf("%s %s: %d %s", test.method, test.path, response.Code, response.Body.String())
		}
	}
	response := exposureRequest(t, handler, "GET", "/api/exposures", fixtureAdminToken, "")
	if strings.Contains(response.Body.String(), token) || strings.Contains(response.Body.String(), "token_hash") {
		t.Fatal("exposure secret leaked")
	}
}

func TestDiscoveredCRUDRequiresExplicitWriteOptIn(t *testing.T) {
	server, handler := exposureTestHandler(t)
	path := filepath.Join(t.TempDir(), "source.sqlite")
	createHTTPTestDatabase(t, path)
	id, err := server.catalog.UpsertDiscovered(&catalog.DiscoveredDB{Name: "source", SourcePath: path, Status: "discovered", Priority: "workspace", Available: true})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := json.Marshal(map[string]any{"alias": "source", "discovered_id": id, "tables": []string{"test"}})
	token := createExposure(t, handler, string(body))
	response := exposureRequest(t, handler, "POST", "/api/data/source/test", token, `{"value":"new"}`)
	if response.Code != 403 {
		t.Fatalf("source writable by default: %d", response.Code)
	}
	response = exposureRequest(t, handler, "PUT", "/api/exposures/source", fixtureAdminToken, `{"tables":["test"],"writable":true}`)
	if response.Code != 200 {
		t.Fatalf("opt-in: %d %s", response.Code, response.Body.String())
	}
	response = exposureRequest(t, handler, "POST", "/api/data/source/test", token, `{"value":"new"}`)
	if response.Code != 201 {
		t.Fatalf("source write: %d %s", response.Code, response.Body.String())
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var count int
	if err := db.QueryRow(`SELECT count(*) FROM test`).Scan(&count); err != nil || count != 2 {
		t.Fatalf("source was not updated: %d, %v", count, err)
	}
	var journal string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journal); err != nil || journal != "delete" {
		t.Fatalf("journal mode changed: %q, %v", journal, err)
	}
	response = exposureRequest(t, handler, "POST", "/api/discovered/"+strconv.FormatInt(id, 10)+"/query", fixtureAdminToken, `{"sql":"DELETE FROM test"}`)
	if response.Code == 200 {
		t.Fatal("read-only discovery inspector became writable")
	}

	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	response = exposureRequest(t, handler, "GET", "/api/data/source/test", token, "")
	if response.Code != 404 {
		t.Fatalf("missing source: %d %s", response.Code, response.Body.String())
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("missing source was recreated")
	}
}

func TestExposureTokenRotationRevocationAndDatabaseDeletion(t *testing.T) {
	server, handler := exposureTestHandler(t)
	token := createExposure(t, handler, `{"alias":"demo","managed_database":"fixture","tables":["inventory"]}`)
	response := exposureRequest(t, handler, "POST", "/api/exposures/demo/token", fixtureAdminToken, "")
	var rotated struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &rotated); err != nil {
		t.Fatal(err)
	}
	if response.Code != 200 || rotated.Token == token {
		t.Fatal("rotation failed")
	}
	if exposureRequest(t, handler, "GET", "/api/data/demo/inventory", token, "").Code != 401 {
		t.Fatal("old token still works")
	}
	if exposureRequest(t, handler, "GET", "/api/data/demo/inventory", rotated.Token, "").Code != 200 {
		t.Fatal("new token failed")
	}
	response = exposureRequest(t, handler, "DELETE", "/api/exposures/demo", fixtureAdminToken, "")
	if response.Code != 204 || exposureRequest(t, handler, "GET", "/api/data/demo/inventory", rotated.Token, "").Code != 404 {
		t.Fatal("revoke failed")
	}
	createExposure(t, handler, `{"alias":"again","managed_database":"fixture","tables":["inventory"]}`)
	response = exposureRequest(t, handler, "DELETE", "/api/databases/fixture", fixtureAdminToken, "")
	if response.Code != 200 {
		t.Fatal(response.Body.String())
	}
	if _, err := server.catalog.GetAPIExposure("again"); err != sql.ErrNoRows {
		t.Fatalf("database deletion did not revoke exposure: %v", err)
	}
}

func TestExposureValidationAndEscapedTablePaths(t *testing.T) {
	server, handler := exposureTestHandler(t)
	for _, body := range []string{
		`{"alias":"bad/path","managed_database":"fixture","tables":["inventory"]}`,
		`{"alias":"demo","managed_database":"fixture","tables":[]}`,
		`{"alias":"demo","managed_database":"fixture","discovered_id":1,"tables":["inventory"]}`,
		`{"alias":"demo","managed_database":"fixture","tables":["inventory","INVENTORY"]}`,
		`{"alias":"demo","managed_database":"fixture","tables":["inventory"],"unexpected":true}`,
	} {
		response := exposureRequest(t, handler, "POST", "/api/exposures", fixtureAdminToken, body)
		if response.Code != 400 {
			t.Fatalf("validation: %d %s", response.Code, response.Body.String())
		}
	}
	if _, err := server.svc.Execute(context.Background(), "fixture", `CREATE TABLE "odd/table" (id INTEGER PRIMARY KEY,value TEXT); CREATE TABLE parent(id INTEGER PRIMARY KEY); CREATE TABLE child(id INTEGER PRIMARY KEY,parent INTEGER REFERENCES parent(id))`, nil); err != nil {
		t.Fatal(err)
	}
	token := createExposure(t, handler, `{"alias":"paths","managed_database":"fixture","tables":["odd/table","child"],"writable":true}`)
	if response := exposureRequest(t, handler, "POST", "/api/data/paths/odd%2Ftable", token, `{"value":"fixture"}`); response.Code != 201 {
		t.Fatalf("escaped table path: %d %s", response.Code, response.Body.String())
	}
	if response := exposureRequest(t, handler, "POST", "/api/data/paths/child", token, `{"parent":123}`); response.Code != 409 {
		t.Fatalf("foreign keys not enforced: %d %s", response.Code, response.Body.String())
	}
}
