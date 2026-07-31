package server

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/service"
	"github.com/mbianchidev/sql-not-so-lite/internal/store"
)

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
}
