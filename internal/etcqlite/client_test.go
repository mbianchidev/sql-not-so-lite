package etcqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mbianchidev/sql-not-so-lite/internal/config"
)

const projectionFixtureDDL = `
CREATE TABLE projection_metadata (
	kind TEXT NOT NULL, format INTEGER NOT NULL,
	cluster_id BLOB NOT NULL, member_id BLOB NOT NULL,
	revision INTEGER NOT NULL, compact_revision INTEGER NOT NULL,
	source_schema INTEGER NOT NULL, includes_data INTEGER NOT NULL
);
CREATE TABLE key_metadata (
	ordinal INTEGER PRIMARY KEY, key_bytes INTEGER NOT NULL, value_bytes INTEGER NOT NULL,
	create_revision INTEGER NOT NULL, mod_revision INTEGER NOT NULL,
	version INTEGER NOT NULL, lease INTEGER NOT NULL
);
CREATE TABLE lease_metadata (
	id INTEGER PRIMARY KEY, granted_ttl INTEGER NOT NULL,
	generation INTEGER NOT NULL, checkpoint_ttl INTEGER
);
INSERT INTO projection_metadata VALUES ('read-only-inspection',1,x'ffffffffffffffff',x'8000000000000001',42,10,2,0);
INSERT INTO key_metadata VALUES (1,3,4,12,42,3,7);
INSERT INTO lease_metadata VALUES (7,60,1,NULL);
`

func projectionFixture(t *testing.T, extra string) []byte {
	t.Helper()
	path := filepath.Join(t.TempDir(), "projection.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(projectionFixtureDDL + extra); err != nil {
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

func discoveryFixture() Discovery {
	return Discovery{
		Format: 1, Kind: "etcqlite",
		ClusterID: "18446744073709551615", MemberID: "9223372036854775809",
		Revision: 40, SchemaVersion: 2, EngineVersion: "fixture", APIVersion: "3.7.3",
		Build: strings.Repeat("a", 40), Capabilities: []string{"native-snapshot-v1", "read-only-projection-v1"},
		ProjectionPath: "/etcqlite/v1/projections", SnapshotPath: "/etcqlite/v1/snapshots",
		ETag: `"ffffffffffffffff:8000000000000001"`,
	}
}

func TestProjectionContractPrivacyAndReadOnly(t *testing.T) {
	ctx := context.Background()
	metadataOnly := projectionFixture(t, "")
	withData := projectionFixture(t, `
		UPDATE projection_metadata SET includes_data=1;
		CREATE TABLE key_values (ordinal INTEGER PRIMARY KEY, key BLOB NOT NULL, value BLOB NOT NULL);
		INSERT INTO key_values VALUES (1,x'00ff01',x'0002feff');
	`)
	var projectionRequests []map[string]bool
	producer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "synthetic-etcd-token" {
			t.Error("missing raw etcd token, or incorrectly added a Bearer prefix")
		}
		switch r.URL.Path {
		case "/etcqlite/v1/discovery":
			if r.Method != http.MethodGet {
				t.Error("discovery must be GET")
			}
			w.Header().Set("ETag", discoveryFixture().ETag)
			json.NewEncoder(w).Encode(discoveryFixture())
		case "/etcqlite/v1/projections":
			if r.Method != http.MethodPost || r.Header.Get("If-Match") != discoveryFixture().ETag {
				t.Error("projection must be POST with the exact discovery ETag")
			}
			var body map[string]bool
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			projectionRequests = append(projectionRequests, body)
			w.Header().Set("Content-Type", "application/vnd.sqlite3")
			if body["include_data"] && body["ack_sensitive_data"] {
				w.Write(withData)
			} else {
				w.Write(metadataOnly)
			}
		default:
			t.Errorf("unexpected producer request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer producer.Close()
	client, err := NewClient(config.EtcqliteConfig{Endpoint: producer.URL, AuthToken: "synthetic-etcd-token"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	discovery, err := client.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if discovery.ClusterID != discoveryFixture().ClusterID || discovery.MemberID != discoveryFixture().MemberID {
		t.Fatal("high-bit identities were not preserved as decimal strings")
	}
	dir := filepath.Join(t.TempDir(), "private")
	for _, includeData := range []bool{false, true} {
		projection, err := client.Projection(ctx, discovery, includeData, includeData, dir)
		if err != nil {
			t.Fatal(err)
		}
		if projection.Metadata.Revision != 42 || projection.Metadata.IncludesData != includeData {
			t.Fatalf("incorrect projection metadata: %+v", projection.Metadata)
		}
		info, err := os.Stat(projection.Path)
		if err != nil || info.Mode().Perm() != 0o400 {
			t.Fatalf("projection must be private and immutable: %v, %v", info, err)
		}
		info, err = os.Stat(dir)
		if err != nil || info.Mode().Perm() != 0o700 {
			t.Fatalf("projection directory must be private: %v, %v", info, err)
		}
		db, err := OpenProjection(ctx, projection.Path, projection.Metadata)
		if err != nil {
			t.Fatal(err)
		}
		for _, statement := range []string{
			"DELETE FROM key_metadata",
			"PRAGMA query_only=OFF; DELETE FROM key_metadata",
			"CREATE TABLE injected(value TEXT)",
		} {
			if _, err := db.ExecContext(ctx, statement); err == nil {
				t.Errorf("projection accepted a write: %s", statement)
			}
		}
		if err := CheckOrdinaryDatabase(ctx, db); !errors.Is(err, ErrProtected) {
			t.Fatalf("projection accepted as generic database: %v", err)
		}
		if includeData {
			var key, value []byte
			if err := db.QueryRowContext(ctx, "SELECT key,value FROM key_values").Scan(&key, &value); err != nil {
				t.Fatal(err)
			}
			if string(key) != "\x00\xff\x01" || string(value) != "\x00\x02\xfe\xff" {
				t.Fatal("binary fixture was changed")
			}
		}
		db.Close()
		for _, suffix := range []string{"-wal", "-shm"} {
			if _, err := os.Stat(projection.Path + suffix); !os.IsNotExist(err) {
				t.Fatalf("inspector created a sidecar: %s", suffix)
			}
		}
	}
	if len(projectionRequests) != 2 || len(projectionRequests[0]) != 0 ||
		!projectionRequests[1]["include_data"] || !projectionRequests[1]["ack_sensitive_data"] {
		t.Fatalf("wrong consent contract: %+v", projectionRequests)
	}
	for _, flags := range [][2]bool{{true, false}, {false, true}} {
		if _, err := client.Projection(ctx, discovery, flags[0], flags[1], dir); !errors.Is(err, ErrConsent) {
			t.Fatalf("partial consent accepted: %v", err)
		}
	}
	if len(projectionRequests) != 2 {
		t.Fatal("partial consent reached the producer")
	}
}

func TestRejectUnsafeProjectionArtifacts(t *testing.T) {
	for _, extra := range []string{
		"CREATE TABLE auth_users(password_hash BLOB);",
		"ALTER TABLE key_metadata ADD COLUMN token TEXT;",
		"UPDATE projection_metadata SET kind='native-snapshot';",
		"UPDATE projection_metadata SET cluster_id=x'00';",
		"UPDATE projection_metadata SET source_schema=99;",
		"UPDATE projection_metadata SET compact_revision=-2;",
		"UPDATE projection_metadata SET compact_revision=43;",
		"UPDATE projection_metadata SET includes_data=1;",
		"UPDATE key_metadata SET version='synthetic-secret';",
		"CREATE VIEW credentials AS SELECT * FROM key_metadata;",
		"CREATE TRIGGER command AFTER INSERT ON key_metadata BEGIN SELECT 1; END;",
		"CREATE TABLE key_values(ordinal INTEGER PRIMARY KEY,key BLOB,value BLOB);",
		"INSERT INTO projection_metadata SELECT * FROM projection_metadata;",
	} {
		t.Run(extra, func(t *testing.T) {
			data := projectionFixture(t, extra)
			producer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Write(data)
			}))
			defer producer.Close()
			client, err := NewClient(config.EtcqliteConfig{Endpoint: producer.URL, AuthToken: "synthetic-token"})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			dir := filepath.Join(t.TempDir(), "private")
			discovery := discoveryFixture()
			if _, err := client.Projection(context.Background(), &discovery, false, false, dir); !errors.Is(err, ErrInvalidProjection) {
				t.Fatalf("unsafe artifact accepted: %v", err)
			}
			files, err := os.ReadDir(dir)
			if err != nil || len(files) != 0 {
				t.Fatalf("failed download was not cleaned up: %v, %v", files, err)
			}
		})
	}
}

func TestProjectionIdentityChangeAndRedirectAreNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusPreconditionFailed, http.StatusPreconditionRequired, http.StatusUnauthorized, http.StatusFound} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			producer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.Header().Set("Location", "/etcqlite/v1/snapshots")
				w.WriteHeader(status)
				w.Write([]byte("synthetic-secret-not-for-display"))
			}))
			defer producer.Close()
			client, err := NewClient(config.EtcqliteConfig{Endpoint: producer.URL, AuthToken: "synthetic-token"})
			if err != nil {
				t.Fatal(err)
			}
			defer client.Close()
			discovery := discoveryFixture()
			_, err = client.Projection(context.Background(), &discovery, false, false, t.TempDir())
			if err == nil || calls != 1 || strings.Contains(err.Error(), "synthetic-secret") {
				t.Fatalf("failure was hidden, retried or leaked response data: calls=%d err=%v", calls, err)
			}
			if status == http.StatusPreconditionFailed && !errors.Is(err, ErrIdentityChanged) {
				t.Fatalf("identity change not identified: %v", err)
			}
		})
	}
}

func TestDiscoveryRejectsInvalidIdentityAndCapabilities(t *testing.T) {
	for _, alter := range []func(*Discovery){
		func(d *Discovery) { d.ClusterID = "18446744073709551616" },
		func(d *Discovery) { d.ClusterID = "0001" },
		func(d *Discovery) { d.MemberID = "-1" },
		func(d *Discovery) { d.Format = 2 },
		func(d *Discovery) { d.Revision = -1 },
		func(d *Discovery) { d.EngineVersion = "" },
		func(d *Discovery) { d.APIVersion = "" },
		func(d *Discovery) { d.Build = "" },
		func(d *Discovery) { zero := uint32(0); d.ConfigurationID = &zero },
		func(d *Discovery) { d.Capabilities = []string{"native-snapshot-v1"} },
		func(d *Discovery) { d.ProjectionPath = "/etcqlite/v1/snapshots" },
		func(d *Discovery) { d.ETag = `"different-identity"` },
	} {
		discovery := discoveryFixture()
		alter(&discovery)
		producer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("ETag", discovery.ETag)
			json.NewEncoder(w).Encode(discovery)
		}))
		client, err := NewClient(config.EtcqliteConfig{Endpoint: producer.URL, AuthToken: "synthetic-token"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Discover(context.Background()); err == nil {
			t.Errorf("invalid discovery accepted: %+v", discovery)
		}
		client.Close()
		producer.Close()
	}
}

func TestProtectedNativeSchemaDoesNotReadSensitiveRows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "renamed-recovery.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}

	defer db.Close()
	_, err = db.Exec(`
		CREATE TABLE node_state (
			id INTEGER PRIMARY KEY, cluster_id BLOB, member_id BLOB,
			name TEXT, mode TEXT, runtime_epoch INTEGER, checksum BLOB
		);
		CREATE TABLE app_state (
			id INTEGER PRIMARY KEY, schema_version INTEGER, codec_version INTEGER,
			revision INTEGER, compacted_revision INTEGER, auth_revision BLOB,
			auth_enabled INTEGER, configuration_id INTEGER, configuration_base INTEGER,
			applied_index INTEGER, logical_bytes INTEGER, quota_bytes INTEGER,
			downgrade_target TEXT, checksum BLOB
		);
		CREATE TABLE auth_users(password_hash BLOB);
		INSERT INTO auth_users VALUES (x'001122');
	`)
	if err != nil {
		t.Fatal(err)
	}
	if err := CheckOrdinaryFile(context.Background(), path); !errors.Is(err, ErrProtected) {
		t.Fatalf("native recovery file accepted as ordinary SQLite: %v", err)
	}
}

func TestOrdinaryMetadataTableNameIsNotProtected(t *testing.T) {
	path := filepath.Join(t.TempDir(), "node.db")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE projection_metadata(note TEXT); INSERT INTO projection_metadata VALUES ('synthetic ordinary application')`); err != nil {
		t.Fatal(err)
	}
	if err := CheckOrdinaryFile(context.Background(), path); err != nil {
		t.Fatalf("ordinary SQLite behavior was changed by a name collision: %v", err)
	}
}

func TestProjectionDownloadCeiling(t *testing.T) {
	producer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Length", "67108865")
		w.WriteHeader(http.StatusOK)
	}))
	defer producer.Close()
	client, err := NewClient(config.EtcqliteConfig{Endpoint: producer.URL, AuthToken: "synthetic-token"})
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	discovery := discoveryFixture()
	dir := filepath.Join(t.TempDir(), "private")
	if _, err := client.Projection(context.Background(), &discovery, false, false, dir); err == nil || !strings.Contains(err.Error(), "64 MiB") {
		t.Fatalf("oversized projection was not rejected: %v", err)
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("oversized projection created an inspection cache")
	}
}
