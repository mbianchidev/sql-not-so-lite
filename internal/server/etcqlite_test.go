package server

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/config"
	"github.com/mbianchidev/sql-not-so-lite/internal/etcqlite"
	"github.com/mbianchidev/sql-not-so-lite/internal/replicator"
	"github.com/mbianchidev/sql-not-so-lite/internal/service"
	"github.com/mbianchidev/sql-not-so-lite/internal/store"
)

func inspectorProjectionFixture(t *testing.T, includeData bool) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "fixture.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE projection_metadata(kind TEXT,format INTEGER,cluster_id BLOB,member_id BLOB,revision INTEGER,compact_revision INTEGER,source_schema INTEGER,includes_data INTEGER);
		CREATE TABLE key_metadata(ordinal INTEGER PRIMARY KEY,key_bytes INTEGER,value_bytes INTEGER,create_revision INTEGER,mod_revision INTEGER,version INTEGER,lease INTEGER);
		CREATE TABLE lease_metadata(id INTEGER PRIMARY KEY,granted_ttl INTEGER,generation INTEGER,checkpoint_ttl INTEGER);
		INSERT INTO projection_metadata VALUES ('read-only-inspection',1,x'ffffffffffffffff',x'8000000000000001',42,10,2,0);
		INSERT INTO key_metadata VALUES (1,3,4,12,42,3,7);
		INSERT INTO lease_metadata VALUES (7,60,1,NULL);
	`)
	if err == nil && includeData {
		_, err = db.Exec(`
			UPDATE projection_metadata SET includes_data=1;
			CREATE TABLE key_values(ordinal INTEGER PRIMARY KEY,key BLOB,value BLOB);
			INSERT INTO key_values VALUES (1,x'00ff01',x'0002feff');
		`)
	}
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return data
}

type inspectorProducer struct {
	server      *httptest.Server
	revision    atomic.Int64
	member      atomic.Uint64
	requests    atomic.Int64
	projections atomic.Int64
}

func newInspectorProducer(t *testing.T) *inspectorProducer {
	t.Helper()
	metadata := inspectorProjectionFixture(t, false)
	raw := inspectorProjectionFixture(t, true)
	producer := &inspectorProducer{}
	producer.revision.Store(42)
	producer.member.Store(9223372036854775809)
	producer.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		producer.requests.Add(1)
		if r.Header.Get("Authorization") != "synthetic-etcd-token" {
			t.Error("producer did not receive its raw etcd token")
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		etag := fmt.Sprintf(`"ffffffffffffffff:%016x"`, producer.member.Load())
		switch r.URL.Path {
		case "/etcqlite/v1/discovery":
			w.Header().Set("ETag", etag)
			json.NewEncoder(w).Encode(map[string]any{
				"format": 1, "kind": "etcqlite", "cluster_id": "18446744073709551615",
				"member_id": strconv.FormatUint(producer.member.Load(), 10), "revision": producer.revision.Load(),
				"schema_version": 2, "engine_version": "fixture", "api_version": "3.7.3",
				"build": strings.Repeat("a", 40), "dirty": false, "configuration_id": 1,
				"capabilities":    []string{"read-only-projection-v1", "native-snapshot-v1"},
				"projection_path": "/etcqlite/v1/projections", "snapshot_path": "/etcqlite/v1/snapshots",
			})
		case "/etcqlite/v1/projections":
			producer.projections.Add(1)
			if r.Method != http.MethodPost || r.Header.Get("If-Match") != etag {
				t.Error("projection identity was not pinned")
				w.WriteHeader(http.StatusPreconditionFailed)
				return
			}
			var body map[string]bool
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if body["include_data"] != body["ack_sensitive_data"] {
				t.Error("partial sensitive consent reached the producer")
			}
			if body["include_data"] {
				w.Write(raw)
			} else {
				w.Write(metadata)
			}
		default:
			t.Errorf("inspector called an unapproved endpoint: %s", r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	t.Cleanup(producer.server.Close)
	return producer
}

func newInspectorHTTPServer(t *testing.T, endpoint string) (*HTTPServer, http.Handler, *store.Manager) {
	t.Helper()
	root := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = root
	cfg.Server.AuthToken = fixtureAdminToken
	cfg.Scanner.ScanRoot = t.TempDir()
	cfg.Replicator.ReplicaDir = filepath.Join(root, "replicas")
	cfg.Replicator.SnapshotDir = filepath.Join(root, "snapshots")
	cfg.Etcqlite.Enabled = true
	cfg.Etcqlite.Endpoint = endpoint
	cfg.Etcqlite.AuthToken = "synthetic-etcd-token"
	manager, err := store.NewManager(root, 10)
	if err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Open(root)
	if err != nil {
		t.Fatal(err)
	}
	server := NewHTTPServer(service.NewDatabaseService(manager), 0, cat, cfg)
	t.Cleanup(func() {
		if err := server.Stop(context.Background()); err != nil {
			t.Error(err)
		}
		manager.CloseAll()
		cat.Close()
	})
	handler, err := server.Handler()
	if err != nil {
		t.Fatal(err)
	}
	return server, handler, manager
}

func fetchInspectorProjection(t *testing.T, handler http.Handler, raw bool, expectedStatus int) discoveredResponse {
	t.Helper()
	body := `{"ack_read_only_inspection":true}`
	if raw {
		body = `{"ack_read_only_inspection":true,"include_data":true,"ack_sensitive_data":true}`
	}
	response := exposureRequest(t, handler, "POST", "/api/etcqlite/projections", fixtureAdminToken, body)
	if response.Code != expectedStatus {
		t.Fatalf("fetch projection: %d %s", response.Code, response.Body.String())
	}
	var result discoveredResponse
	if err := json.Unmarshal(response.Body.Bytes(), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestHTTPInspectorConsentReadOnlyAndSensitiveRevocation(t *testing.T) {
	producer := newInspectorProducer(t)
	server, handler, manager := newInspectorHTTPServer(t, producer.server.URL)
	for _, path := range []string{"/api/etcqlite", "/api/etcqlite/projections"} {
		response := exposureRequest(t, handler, "GET", path, "", "")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("inspector administration is unauthenticated: %s %d", path, response.Code)
		}
	}
	response := exposureRequest(t, handler, "GET", "/api/etcqlite", fixtureAdminToken, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), "awaiting-consent") || producer.requests.Load() != 0 {
		t.Fatal("enabling or viewing the integration contacted the producer before consent")
	}
	for _, body := range []string{
		`{}`, `null`, `{"ack_read_only_inspection":false}`,
		`{"ack_read_only_inspection":true,"include_data":true}`,
		`{"ack_read_only_inspection":true,"ack_sensitive_data":true}`,
		`{"ack_read_only_inspection":true,"endpoint":"http://192.0.2.10"}`,
		`{"ack_read_only_inspection":true} {}`,
	} {
		response := exposureRequest(t, handler, "POST", "/api/etcqlite/projections", fixtureAdminToken, body)
		if response.Code != 400 || producer.requests.Load() != 0 {
			t.Fatalf("invalid or incomplete consent accepted: %d %s", response.Code, body)
		}
	}
	result := fetchInspectorProjection(t, handler, false, 201)
	prefix := "/api/discovered/" + strconv.FormatInt(result.ID, 10)
	response = exposureRequest(t, handler, "GET", prefix+"/schema", fixtureAdminToken, "")
	if response.Code != 200 || strings.Contains(response.Body.String(), "key_values") || strings.Contains(response.Body.String(), "auth_users") {
		t.Fatalf("default schema exposes sensitive data: %d %s", response.Code, response.Body.String())
	}
	response = exposureRequest(t, handler, "POST", prefix+"/query", fixtureAdminToken, `{"sql":"SELECT * FROM key_metadata"}`)
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"42"`) {
		t.Fatalf("metadata was not inspectable: %d %s", response.Code, response.Body.String())
	}
	for _, statement := range []string{
		"DELETE FROM key_metadata", "PRAGMA query_only=OFF", "SELECT 1; DELETE FROM key_metadata",
		"WITH fixture AS (SELECT 1) DELETE FROM key_metadata",
		"SELECT * FROM key_values", "SELECT * FROM auth_users",
	} {
		body, _ := json.Marshal(map[string]string{"sql": statement})
		response = exposureRequest(t, handler, "POST", prefix+"/query", fixtureAdminToken, string(body))
		if response.Code == 200 {
			t.Fatalf("default inspector accepted forbidden SQL: %s", statement)
		}
	}
	for _, request := range []struct{ method, resource string }{
		{"POST", "replicate"}, {"DELETE", "replicate"}, {"POST", "restore"},
		{"GET", "snapshots"}, {"GET", "versions"}, {"GET", "transitions"},
	} {
		response = exposureRequest(t, handler, request.method, prefix+"/"+request.resource, fixtureAdminToken, "{}")
		if response.Code != http.StatusForbidden {
			t.Errorf("projection entered generic recovery/history: %s %d", request.resource, response.Code)
		}
	}
	body := fmt.Sprintf(`{"alias":"forbidden","discovered_id":%d,"tables":["key_metadata"],"writable":false}`, result.ID)
	response = exposureRequest(t, handler, "POST", "/api/exposures", fixtureAdminToken, body)
	if response.Code != http.StatusForbidden {
		t.Fatalf("projection could be exposed as a network CRUD API: %d", response.Code)
	}
	copyPath := filepath.Join(server.cfg.Server.DataDir, "copied_projection.sqlite")
	if err := os.WriteFile(copyPath, inspectorProjectionFixture(t, false), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := manager.Get("copied_projection"); !errors.Is(err, etcqlite.ErrProtected) {
		t.Fatalf("renamed projection was accepted as a writable managed database: %v", err)
	}
	if err := manager.Drop("copied_projection"); !errors.Is(err, etcqlite.ErrProtected) {
		t.Fatalf("managed deletion could remove a protected file: %v", err)
	}
	entries := manager.List()
	for i := range entries {
		if entries[i].Name == "copied_projection" {
			t.Fatal("renamed projection appeared in managed databases")
		}
	}
	fetchInspectorProjection(t, handler, true, 200)
	d, err := server.catalog.GetDiscovered(result.ID)
	if err != nil {
		t.Fatal(err)
	}
	rawPath := d.SourcePath
	response = exposureRequest(t, handler, "GET", prefix+"/table?name=key_values", fixtureAdminToken, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), "hex:00ff01") ||
		!strings.Contains(response.Body.String(), "hex:0002feff") {
		t.Fatalf("explicitly consented binary KV inspection failed: %d %s", response.Code, response.Body.String())
	}
	fetchInspectorProjection(t, handler, false, 200)
	if _, err := os.Stat(rawPath); !os.IsNotExist(err) {
		t.Fatal("metadata refresh did not remove the sensitive projection")
	}
	response = exposureRequest(t, handler, "GET", prefix+"/table?name=key_values", fixtureAdminToken, "")
	if response.Code != 404 {
		t.Fatalf("sensitive KV survived consent revocation: %d", response.Code)
	}
	response = exposureRequest(t, handler, "GET", "/api/discovered", fixtureAdminToken, "")
	if strings.Contains(response.Body.String(), "synthetic-etcd-token") || strings.Contains(response.Body.String(), rawPath) {
		t.Fatal("catalog exposed credentials or private artifact paths")
	}
}

func TestHTTPInspectorStaleDisconnectedIdentityAndMissing(t *testing.T) {
	producer := newInspectorProducer(t)
	server, handler, _ := newInspectorHTTPServer(t, producer.server.URL)
	result := fetchInspectorProjection(t, handler, false, 201)
	producer.revision.Store(50)
	response := exposureRequest(t, handler, "GET", "/api/etcqlite", fixtureAdminToken, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"state":"stale"`) || !strings.Contains(response.Body.String(), `"source_revision":"50"`) {
		t.Fatalf("revision lag was not reported truthfully: %d %s", response.Code, response.Body.String())
	}
	before := producer.projections.Load()
	producer.member.Store(9223372036854775810)
	response = exposureRequest(t, handler, "POST", "/api/etcqlite/projections", fixtureAdminToken, `{"ack_read_only_inspection":true}`)
	if response.Code != http.StatusPreconditionFailed || producer.projections.Load() != before {
		t.Fatal("identity change was silently followed or fetched")
	}
	producer.server.Close()
	response = exposureRequest(t, handler, "GET", "/api/etcqlite", fixtureAdminToken, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"state":"disconnected"`) {
		t.Fatalf("stopped store was reported as connected: %d %s", response.Code, response.Body.String())
	}
	prefix := "/api/discovered/" + strconv.FormatInt(result.ID, 10)
	response = exposureRequest(t, handler, "GET", prefix+"/table?name=key_metadata", fixtureAdminToken, "")
	if response.Code != 200 {
		t.Fatal("a stale metadata projection could not be inspected offline")
	}
	d, err := server.catalog.GetDiscovered(result.ID)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(d.SourcePath); err != nil {
		t.Fatal(err)
	}
	response = exposureRequest(t, handler, "GET", prefix+"/schema", fixtureAdminToken, "")
	if response.Code == 200 {
		t.Fatal("missing projection was reported as available")
	}
	if _, err := os.Stat(d.SourcePath); !os.IsNotExist(err) {
		t.Fatal("missing projection was recreated")
	}
	response = exposureRequest(t, handler, "GET", "/api/health", fixtureAdminToken, "")
	if response.Code != 200 {
		t.Fatal("store failure affected the standalone daemon")
	}
	server.cfg.Etcqlite.Enabled = false
	response = exposureRequest(t, handler, "GET", "/api/etcqlite", fixtureAdminToken, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"enabled":false`) {
		t.Fatal("disabled integration did not remain disconnected")
	}
}

func TestHTTPInspectorRestartRevokesSensitiveConsent(t *testing.T) {
	producer := newInspectorProducer(t)
	server, handler, _ := newInspectorHTTPServer(t, producer.server.URL)
	result := fetchInspectorProjection(t, handler, true, 201)
	d, err := server.catalog.GetDiscovered(result.ID)
	if err != nil {
		t.Fatal(err)
	}
	restarted := NewHTTPServer(server.svc, 0, server.catalog, server.cfg)
	nextHandler, err := restarted.Handler()
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(d.SourcePath); !os.IsNotExist(err) {
		t.Fatal("restart retained a sensitive projection")
	}
	response := exposureRequest(t, nextHandler, "GET", "/api/discovered/"+strconv.FormatInt(result.ID, 10)+"/schema", fixtureAdminToken, "")
	if response.Code == 200 {
		t.Fatal("raw KV consent persisted across restart")
	}
	fetchInspectorProjection(t, nextHandler, false, 200)
}

func TestHTTPInspectorRejectsNativeStoresAcrossGenericPaths(t *testing.T) {
	producer := newInspectorProducer(t)
	server, handler, manager := newInspectorHTTPServer(t, producer.server.URL)
	path := filepath.Join(server.cfg.Server.DataDir, "renamed_native.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
		CREATE TABLE node_state(id INTEGER PRIMARY KEY,cluster_id BLOB,member_id BLOB,name TEXT,mode TEXT,runtime_epoch INTEGER,checksum BLOB);
		CREATE TABLE app_state(id INTEGER PRIMARY KEY,schema_version INTEGER,codec_version INTEGER,revision INTEGER,compacted_revision INTEGER,auth_revision BLOB,auth_enabled INTEGER,checksum BLOB);
		CREATE TABLE auth_users(password_hash BLOB);
		INSERT INTO auth_users VALUES (x'001122');
	`)
	if err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.Sum256(original)
	id, err := server.catalog.UpsertDiscovered(&catalog.DiscoveredDB{Name: "renamed_native", SourcePath: path})
	if err != nil {
		t.Fatal(err)
	}
	prefix := "/api/discovered/" + strconv.FormatInt(id, 10)
	for _, resource := range []string{"schema", "table?name=auth_users", "snapshots"} {
		response := exposureRequest(t, handler, "GET", prefix+"/"+resource, fixtureAdminToken, "")
		if response.Code != 403 {
			t.Fatalf("native store could be inspected through generic paths: %s %d", resource, response.Code)
		}
	}
	if _, err := manager.Get("renamed_native"); !errors.Is(err, etcqlite.ErrProtected) {
		t.Fatalf("native store was opened writable: %v", err)
	}
	replica := filepath.Join(t.TempDir(), "replica.sqlite")
	if _, err := replicator.InitialSync(path, replica); !errors.Is(err, etcqlite.ErrProtected) {
		t.Fatalf("native store was table-replicated: %v", err)
	}
	if err := replicator.RestoreSnapshot(path, replica); !errors.Is(err, etcqlite.ErrProtected) {
		t.Fatalf("native image was treated as a generic recovery snapshot: %v", err)
	}
	after, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(after) != hash {
		t.Fatal("native store was changed during classification")
	}
}

func TestBrokenInspectorCacheDoesNotStopGenericService(t *testing.T) {
	producer := newInspectorProducer(t)
	server, _, _ := newInspectorHTTPServer(t, producer.server.URL)
	server.inspectionReady = false
	if err := os.WriteFile(etcqlite.ProjectionDir(server.cfg.Server.DataDir), []byte("synthetic-invalid-cache"), 0o600); err != nil {
		t.Fatal(err)
	}
	handler, err := server.Handler()
	if err != nil {
		t.Fatalf("broken optional inspector stopped standalone HTTP: %v", err)
	}
	response := exposureRequest(t, handler, "GET", "/api/health", fixtureAdminToken, "")
	if response.Code != 200 {
		t.Fatal("generic service was affected by the inspector cache")
	}
	response = exposureRequest(t, handler, "POST", "/api/etcqlite/projections", fixtureAdminToken, `{"ack_read_only_inspection":true}`)
	if response.Code != 503 || producer.requests.Load() != 0 {
		t.Fatal("broken inspector did not fail closed independently")
	}
}

func TestHTTPInspectorErrorsDoNotExposeCredentialsOrPrivatePaths(t *testing.T) {
	producer := newInspectorProducer(t)
	server, handler, _ := newInspectorHTTPServer(t, producer.server.URL)
	fetchInspectorProjection(t, handler, false, 201)
	privatePath := filepath.Join(t.TempDir(), "synthetic-secret.token")
	server.cfg.Etcqlite.AuthToken = ""
	server.cfg.Etcqlite.AuthTokenFile = privatePath
	response := exposureRequest(t, handler, "GET", "/api/etcqlite", fixtureAdminToken, "")
	if response.Code != 200 || !strings.Contains(response.Body.String(), `"state":"disconnected"`) {
		t.Fatal("missing credentials did not produce a truthful disconnected state")
	}
	if strings.Contains(response.Body.String(), privatePath) || strings.Contains(response.Body.String(), "synthetic-etcd-token") {
		t.Fatal("private credential information leaked through the inspector error")
	}
	response = exposureRequest(t, handler, "GET", "/api/discovered", fixtureAdminToken, "")
	if strings.Contains(response.Body.String(), privatePath) || strings.Contains(response.Body.String(), "synthetic-etcd-token") {
		t.Fatal("private credential information leaked through the catalog")
	}
}

func TestInterchangeArtifactsCannotEnterGenericDatabasePaths(t *testing.T) {
	server, handler, manager := newInspectorHTTPServer(t, "http://127.0.0.1:1")
	path := filepath.Join(server.cfg.Server.DataDir, "renamed_interchange.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE interchange_meta(kind TEXT,format INTEGER,reference TEXT,revision INTEGER,compact_revision INTEGER);
		INSERT INTO interchange_meta VALUES ('bbolt-interchange',2,'3.7.3',42,-1);
		CREATE TABLE interchange_buckets(name BLOB PRIMARY KEY);
		CREATE TABLE interchange_entries(bucket BLOB,key BLOB,value BLOB,PRIMARY KEY(bucket,key));
		INSERT INTO interchange_buckets VALUES (x'61757468');
		INSERT INTO interchange_entries VALUES (x'61757468',x'66697874757265',x'001122ff');
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	original, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	footer := sha256.Sum256(original)
	original = append(original, footer[:]...)
	if err := os.WriteFile(path, original, 0o600); err != nil {
		t.Fatal(err)
	}
	id, err := server.catalog.UpsertDiscovered(&catalog.DiscoveredDB{Name: "renamed_interchange", SourcePath: path})
	if err != nil {
		t.Fatal(err)
	}
	prefix := "/api/discovered/" + strconv.FormatInt(id, 10)
	for _, resource := range []string{"schema", "table?name=interchange_entries", "snapshots"} {
		response := exposureRequest(t, handler, "GET", prefix+"/"+resource, fixtureAdminToken, "")
		if response.Code != 403 {
			t.Fatalf("interchange artifact was exposed through generic inspection: %s %d", resource, response.Code)
		}
	}
	for _, writable := range []bool{false, true} {
		body := fmt.Sprintf(`{"alias":"blocked","discovered_id":%d,"tables":["interchange_entries"],"writable":%t}`, id, writable)
		response := exposureRequest(t, handler, "POST", "/api/exposures", fixtureAdminToken, body)
		if response.Code != 403 {
			t.Fatalf("interchange artifact entered generic CRUD registration: %d", response.Code)
		}
	}
	if err := server.catalog.CreateAPIExposure(&catalog.APIExposure{
		Alias: "legacy", DatabaseID: id, Tables: []string{"interchange_entries"},
		Writable: true, TokenHash: tokenHash("synthetic-scoped-token"),
	}); err != nil {
		t.Fatal(err)
	}
	for _, method := range []string{"GET", "POST"} {
		response := exposureRequest(t, handler, method, "/api/data/legacy/interchange_entries", "synthetic-scoped-token", "{}")
		if response.Code != 403 {
			t.Fatalf("manually registered interchange CRUD API was not blocked: %s %d", method, response.Code)
		}
	}
	if _, err := manager.Get("renamed_interchange"); !errors.Is(err, etcqlite.ErrProtected) {
		t.Fatalf("interchange artifact was opened as a writable managed database: %v", err)
	}
	entries := manager.List()
	for i := range entries {
		if entries[i].Path == path {
			t.Fatal("interchange artifact appeared in the managed database list")
		}
	}
	if err := manager.Drop("renamed_interchange"); !errors.Is(err, etcqlite.ErrProtected) {
		t.Fatalf("managed deletion could remove an interchange artifact: %v", err)
	}
	target := filepath.Join(t.TempDir(), "replica.sqlite")
	if _, err := replicator.InitialSync(path, target); !errors.Is(err, etcqlite.ErrProtected) {
		t.Fatalf("interchange artifact was table-replicated: %v", err)
	}
	if err := replicator.RestoreSnapshot(path, target); !errors.Is(err, etcqlite.ErrProtected) {
		t.Fatalf("interchange artifact was treated as a generic recovery source: %v", err)
	}
	generic := filepath.Join(t.TempDir(), "ordinary.sqlite")
	createHTTPTestDatabase(t, generic)
	if err := replicator.RestoreSnapshot(generic, path); !errors.Is(err, etcqlite.ErrProtected) {
		t.Fatalf("generic recovery could overwrite an interchange artifact: %v", err)
	}
	if _, err := os.Stat(target); !os.IsNotExist(err) {
		t.Fatal("blocked interchange replication/recovery created a target")
	}
	after, err := os.ReadFile(path)
	if err != nil || sha256.Sum256(after) != sha256.Sum256(original) {
		t.Fatal("generic database paths modified the interchange artifact")
	}
}
