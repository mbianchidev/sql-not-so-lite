package daemon

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"

	"github.com/mbianchidev/sql-not-so-lite/internal/catalog"
	"github.com/mbianchidev/sql-not-so-lite/internal/config"
	"github.com/mbianchidev/sql-not-so-lite/internal/replicator"
	_ "modernc.org/sqlite"
)

func TestParseScanInterval(t *testing.T) {
	if got := parseScanInterval("45m"); got != 45*time.Minute {
		t.Fatalf("parseScanInterval(45m) = %s", got)
	}

	for _, value := range []string{"invalid", "0s", "-1m"} {
		if got := parseScanInterval(value); got != 30*time.Minute {
			t.Fatalf("parseScanInterval(%q) = %s, want 30m", value, got)
		}
	}
}

func TestRunPeriodicScannerRunsAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := make(chan struct{}, 1)
	done := make(chan struct{})

	go func() {
		defer close(done)
		runPeriodicScanner(ctx, time.Millisecond, func(context.Context) (int, error) {
			calls <- struct{}{}
			return 1, nil
		})
	}()

	select {
	case <-calls:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("scheduled scan did not run")
	}

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("scheduled scanner did not stop")
	}
}

func TestParseReplicationInterval(t *testing.T) {
	if got := parseReplicationInterval("12s"); got != 12*time.Second {
		t.Fatalf("parseReplicationInterval(12s) = %s", got)
	}
	for _, value := range []string{"invalid", "0s", "-1s"} {
		if got := parseReplicationInterval(value); got != 5*time.Second {
			t.Fatalf("parseReplicationInterval(%q) = %s, want 5s", value, got)
		}
	}
}

func TestRunPeriodicReplicatorRunsImmediatelyAndStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := make(chan struct{}, 1)
	done := make(chan struct{})
	go func() {
		defer close(done)
		runPeriodicReplicator(ctx, time.Hour, func(context.Context) (int, error) {
			calls <- struct{}{}
			return 1, nil
		})
	}()

	select {
	case <-calls:
		cancel()
	case <-time.After(time.Second):
		t.Fatal("replication worker did not run immediately")
	}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("replication worker did not stop")
	}
}

func TestSyncReplicationsResumesCatalogDatabase(t *testing.T) {
	root := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Server.DataDir = filepath.Join(root, "data")
	cfg.Replicator.ReplicaDir = filepath.Join(root, "replicas")
	cfg.Replicator.SnapshotDir = filepath.Join(root, "snapshots")
	if err := cfg.EnsureDirs(); err != nil {
		t.Fatal(err)
	}
	cat, err := catalog.Open(cfg.Server.DataDir)
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()

	sourcePath := filepath.Join(root, "source.sqlite")
	source, err := sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec("CREATE TABLE items (id INTEGER PRIMARY KEY, value TEXT); INSERT INTO items (value) VALUES ('one')"); err != nil {
		source.Close()
		t.Fatal(err)
	}
	source.Close()
	id, err := cat.UpsertDiscovered(&catalog.DiscoveredDB{
		Name:       "source",
		SourcePath: sourcePath,
		Status:     "replicating",
	})
	if err != nil {
		t.Fatal(err)
	}
	replicaPath := filepath.Join(cfg.Replicator.ReplicaDir, "source.sqlite")
	if _, err := replicator.InitialSync(sourcePath, replicaPath); err != nil {
		t.Fatal(err)
	}
	if err := cat.SetReplicationState(&catalog.ReplicationState{
		DatabaseID:  id,
		ReplicaName: "source",
		LastSync:    time.Now().Add(-time.Minute),
		SyncMode:    "full",
	}); err != nil {
		t.Fatal(err)
	}
	source, err = sql.Open("sqlite", sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := source.Exec("INSERT INTO items (value) VALUES ('two')"); err != nil {
		source.Close()
		t.Fatal(err)
	}
	source.Close()

	d := &Daemon{cfg: cfg, catalog: cat}
	if _, err := d.syncReplications(context.Background()); err != nil {
		t.Fatal(err)
	}
	replica, err := sql.Open("sqlite", replicaPath)
	if err != nil {
		t.Fatal(err)
	}
	defer replica.Close()
	var count int
	if err := replica.QueryRow("SELECT COUNT(*) FROM items").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 2 {
		t.Fatalf("replica row count = %d, want 2", count)
	}
	state, err := cat.GetReplicationState(id)
	if err != nil {
		t.Fatal(err)
	}
	if state.SyncMode != "differential" {
		t.Fatalf("sync mode = %q, want differential", state.SyncMode)
	}
}
