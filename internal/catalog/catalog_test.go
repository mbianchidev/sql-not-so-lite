package catalog

import (
	"database/sql"
	"testing"
	"time"
)

func openTestCatalog(t *testing.T) *Catalog {
	t.Helper()
	cat, err := Open(t.TempDir())
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t.Cleanup(func() { cat.Close() })
	return cat
}

func insertTestDB(t *testing.T, cat *Catalog, name, path string) int64 {
	t.Helper()
	id, err := cat.UpsertDiscovered(&DiscoveredDB{
		Name:       name,
		SourcePath: path,
		Priority:   "other",
	})
	if err != nil {
		t.Fatalf("UpsertDiscovered(%s): %v", name, err)
	}
	return id
}

func TestOpenClose(t *testing.T) {
	dir := t.TempDir()
	cat, err := Open(dir)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := cat.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen the same catalog to verify schema idempotency.
	cat2, err := Open(dir)
	if err != nil {
		t.Fatalf("Reopen: %v", err)
	}
	cat2.Close()
}

func TestUpsertAndGetDiscovered(t *testing.T) {
	cat := openTestCatalog(t)

	now := time.Now().Truncate(time.Second)
	d := &DiscoveredDB{
		Name:          "mydb",
		SourcePath:    "/data/my.db",
		SQLiteVersion: "3.45.0",
		PageSize:      4096,
		JournalMode:   "wal",
		SizeBytes:     1024,
		LastModified:  now,
		Status:        "discovered",
		GitHubRepo:    "owner/repo",
		GitHubURL:     "https://github.com/owner/repo",
		Priority:      "docker",
	}

	id, err := cat.UpsertDiscovered(d)
	if err != nil {
		t.Fatalf("UpsertDiscovered: %v", err)
	}
	if id == 0 {
		t.Fatal("expected non-zero id")
	}

	got, err := cat.GetDiscovered(id)
	if err != nil {
		t.Fatalf("GetDiscovered: %v", err)
	}
	if got.Name != "mydb" {
		t.Errorf("Name = %q, want %q", got.Name, "mydb")
	}
	if got.SourcePath != "/data/my.db" {
		t.Errorf("SourcePath = %q, want %q", got.SourcePath, "/data/my.db")
	}
	if got.SQLiteVersion != "3.45.0" {
		t.Errorf("SQLiteVersion = %q, want %q", got.SQLiteVersion, "3.45.0")
	}
	if got.PageSize != 4096 {
		t.Errorf("PageSize = %d, want %d", got.PageSize, 4096)
	}
	if got.JournalMode != "wal" {
		t.Errorf("JournalMode = %q, want %q", got.JournalMode, "wal")
	}
	if got.SizeBytes != 1024 {
		t.Errorf("SizeBytes = %d, want %d", got.SizeBytes, 1024)
	}
	if got.Status != "discovered" {
		t.Errorf("Status = %q, want %q", got.Status, "discovered")
	}
	if got.GitHubRepo != "owner/repo" {
		t.Errorf("GitHubRepo = %q, want %q", got.GitHubRepo, "owner/repo")
	}
	if got.Priority != "docker" {
		t.Errorf("Priority = %q, want %q", got.Priority, "docker")
	}
	if got.FirstSeen.IsZero() {
		t.Error("FirstSeen is zero")
	}
}

func TestUpsertDiscoveredUpdate(t *testing.T) {
	cat := openTestCatalog(t)

	id1, _ := cat.UpsertDiscovered(&DiscoveredDB{
		Name:       "db1",
		SourcePath: "/a/b.db",
		SizeBytes:  100,
	})

	id2, _ := cat.UpsertDiscovered(&DiscoveredDB{
		Name:       "db1",
		SourcePath: "/a/b.db",
		SizeBytes:  200,
	})

	if id1 != id2 {
		t.Errorf("upsert returned different IDs: %d vs %d", id1, id2)
	}

	got, _ := cat.GetDiscovered(id1)
	if got.SizeBytes != 200 {
		t.Errorf("SizeBytes = %d after upsert, want 200", got.SizeBytes)
	}
}

func TestGetDiscoveredByPath(t *testing.T) {
	cat := openTestCatalog(t)
	insertTestDB(t, cat, "pathdb", "/unique/path.db")

	got, err := cat.GetDiscoveredByPath("/unique/path.db")
	if err != nil {
		t.Fatalf("GetDiscoveredByPath: %v", err)
	}
	if got.Name != "pathdb" {
		t.Errorf("Name = %q, want %q", got.Name, "pathdb")
	}

	_, err = cat.GetDiscoveredByPath("/nonexistent")
	if err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

func TestGetDiscoveredByName(t *testing.T) {
	cat := openTestCatalog(t)
	insertTestDB(t, cat, "namedb", "/name/db.sqlite")

	got, err := cat.GetDiscoveredByName("namedb")
	if err != nil {
		t.Fatalf("GetDiscoveredByName: %v", err)
	}
	if got.SourcePath != "/name/db.sqlite" {
		t.Errorf("SourcePath = %q, want %q", got.SourcePath, "/name/db.sqlite")
	}

	_, err = cat.GetDiscoveredByName("nope")
	if err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

func TestListDiscovered(t *testing.T) {
	cat := openTestCatalog(t)

	insertTestDB(t, cat, "alpha", "/alpha.db")
	insertTestDB(t, cat, "beta", "/beta.db")
	insertTestDB(t, cat, "gamma", "/gamma.db")

	list, err := cat.ListDiscovered()
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("len = %d, want 3", len(list))
	}
	// All same priority ("other"), should be sorted by name.
	if list[0].Name != "alpha" || list[1].Name != "beta" || list[2].Name != "gamma" {
		t.Errorf("order = [%s, %s, %s], want [alpha, beta, gamma]",
			list[0].Name, list[1].Name, list[2].Name)
	}
}

func TestUpdateStatus(t *testing.T) {
	cat := openTestCatalog(t)
	id := insertTestDB(t, cat, "statusdb", "/status.db")

	if err := cat.UpdateStatus(id, "error", "disk full"); err != nil {
		t.Fatalf("UpdateStatus: %v", err)
	}

	got, _ := cat.GetDiscovered(id)
	if got.Status != "error" {
		t.Errorf("Status = %q, want %q", got.Status, "error")
	}
	if got.ErrorMessage != "disk full" {
		t.Errorf("ErrorMessage = %q, want %q", got.ErrorMessage, "disk full")
	}

	// Clear error.
	if err := cat.UpdateStatus(id, "discovered", ""); err != nil {
		t.Fatalf("UpdateStatus (clear): %v", err)
	}
	got, _ = cat.GetDiscovered(id)
	if got.ErrorMessage != "" {
		t.Errorf("ErrorMessage = %q after clear, want empty", got.ErrorMessage)
	}
}

func TestDeleteDiscovered(t *testing.T) {
	cat := openTestCatalog(t)
	id := insertTestDB(t, cat, "deldb", "/del.db")

	if err := cat.DeleteDiscovered(id); err != nil {
		t.Fatalf("DeleteDiscovered: %v", err)
	}

	_, err := cat.GetDiscovered(id)
	if err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows after delete, got %v", err)
	}
}

func TestDeleteDiscoveredCascade(t *testing.T) {
	cat := openTestCatalog(t)
	id := insertTestDB(t, cat, "cascadedb", "/cascade.db")

	// Add related records.
	_, err := cat.InsertSnapshot(&Snapshot{
		DatabaseID:    id,
		Version:       1,
		SchemaVersion: 1,
		SnapshotPath:  "/snap/1.db",
		Trigger:       "initial",
	})
	if err != nil {
		t.Fatalf("InsertSnapshot: %v", err)
	}

	err = cat.SetReplicationState(&ReplicationState{
		DatabaseID:  id,
		ReplicaName: "replica-cascade",
	})
	if err != nil {
		t.Fatalf("SetReplicationState: %v", err)
	}

	_, err = cat.InsertSchemaVersion(&SchemaVersion{
		DatabaseID: id,
		Version:    1,
		SchemaSQL:  "CREATE TABLE t(x);",
		SchemaHash: "abc123",
	})
	if err != nil {
		t.Fatalf("InsertSchemaVersion: %v", err)
	}

	err = cat.InsertTransition(&SchemaTransition{
		DatabaseID:  id,
		FromVersion: 0,
		ToVersion:   1,
		Summary:     "initial schema",
	})
	if err != nil {
		t.Fatalf("InsertTransition: %v", err)
	}

	// Delete the parent — everything should cascade.
	if err := cat.DeleteDiscovered(id); err != nil {
		t.Fatalf("DeleteDiscovered: %v", err)
	}

	if _, err := cat.GetReplicationState(id); err != sql.ErrNoRows {
		t.Errorf("replication_state: expected ErrNoRows, got %v", err)
	}
	snaps, _ := cat.ListSnapshots(id)
	if len(snaps) != 0 {
		t.Errorf("snapshots remaining: %d", len(snaps))
	}
	svs, _ := cat.ListSchemaVersions(id)
	if len(svs) != 0 {
		t.Errorf("schema_versions remaining: %d", len(svs))
	}
	trans, _ := cat.ListTransitions(id)
	if len(trans) != 0 {
		t.Errorf("schema_transitions remaining: %d", len(trans))
	}
}

func TestReplicationState(t *testing.T) {
	cat := openTestCatalog(t)
	id := insertTestDB(t, cat, "repldb", "/repl.db")
	now := time.Now().Truncate(time.Second)

	rs := &ReplicationState{
		DatabaseID:  id,
		ReplicaName: "replica-1",
		Salt1:       111,
		Salt2:       222,
		LastFrame:   42,
		PageSize:    4096,
		LastSync:    now,
		SyncMode:    "incremental",
	}
	if err := cat.SetReplicationState(rs); err != nil {
		t.Fatalf("SetReplicationState: %v", err)
	}

	got, err := cat.GetReplicationState(id)
	if err != nil {
		t.Fatalf("GetReplicationState: %v", err)
	}
	if got.ReplicaName != "replica-1" {
		t.Errorf("ReplicaName = %q", got.ReplicaName)
	}
	if got.Salt1 != 111 || got.Salt2 != 222 {
		t.Errorf("Salt = (%d,%d), want (111,222)", got.Salt1, got.Salt2)
	}
	if got.LastFrame != 42 {
		t.Errorf("LastFrame = %d, want 42", got.LastFrame)
	}
	if got.SyncMode != "incremental" {
		t.Errorf("SyncMode = %q, want %q", got.SyncMode, "incremental")
	}

	// Upsert update.
	rs.LastFrame = 100
	if err := cat.SetReplicationState(rs); err != nil {
		t.Fatalf("SetReplicationState (update): %v", err)
	}
	got, _ = cat.GetReplicationState(id)
	if got.LastFrame != 100 {
		t.Errorf("LastFrame after update = %d, want 100", got.LastFrame)
	}
}

func TestSnapshots(t *testing.T) {
	cat := openTestCatalog(t)
	id := insertTestDB(t, cat, "snapdb", "/snap.db")

	for i := 1; i <= 3; i++ {
		_, err := cat.InsertSnapshot(&Snapshot{
			DatabaseID:    id,
			Version:       i,
			SchemaVersion: 1,
			SnapshotPath:  "/snaps/" + string(rune('0'+i)) + ".db",
			Trigger:       "manual",
		})
		if err != nil {
			t.Fatalf("InsertSnapshot(v%d): %v", i, err)
		}
	}

	snaps, err := cat.ListSnapshots(id)
	if err != nil {
		t.Fatalf("ListSnapshots: %v", err)
	}
	if len(snaps) != 3 {
		t.Fatalf("len = %d, want 3", len(snaps))
	}
	// Ordered by version DESC.
	if snaps[0].Version != 3 || snaps[1].Version != 2 || snaps[2].Version != 1 {
		t.Errorf("versions = [%d,%d,%d], want [3,2,1]",
			snaps[0].Version, snaps[1].Version, snaps[2].Version)
	}
}

func TestNextSnapshotVersion(t *testing.T) {
	cat := openTestCatalog(t)
	id := insertTestDB(t, cat, "nextsnapdb", "/nextsnap.db")

	v, err := cat.NextSnapshotVersion(id)
	if err != nil {
		t.Fatalf("NextSnapshotVersion: %v", err)
	}
	if v != 1 {
		t.Errorf("first version = %d, want 1", v)
	}

	cat.InsertSnapshot(&Snapshot{
		DatabaseID: id, Version: 1, SchemaVersion: 1,
		SnapshotPath: "/s/1.db", Trigger: "initial",
	})
	cat.InsertSnapshot(&Snapshot{
		DatabaseID: id, Version: 2, SchemaVersion: 1,
		SnapshotPath: "/s/2.db", Trigger: "manual",
	})

	v, _ = cat.NextSnapshotVersion(id)
	if v != 3 {
		t.Errorf("after 2 inserts = %d, want 3", v)
	}
}

func TestPruneSnapshots(t *testing.T) {
	cat := openTestCatalog(t)
	id := insertTestDB(t, cat, "prunedb", "/prune.db")

	for i := 1; i <= 5; i++ {
		cat.InsertSnapshot(&Snapshot{
			DatabaseID:    id,
			Version:       i,
			SchemaVersion: 1,
			SnapshotPath:  "/prune/" + string(rune('0'+i)) + ".db",
			Trigger:       "manual",
		})
	}

	paths, err := cat.PruneSnapshots(id, 2)
	if err != nil {
		t.Fatalf("PruneSnapshots: %v", err)
	}
	if len(paths) != 3 {
		t.Fatalf("pruned %d paths, want 3", len(paths))
	}

	remaining, _ := cat.ListSnapshots(id)
	if len(remaining) != 2 {
		t.Fatalf("remaining = %d, want 2", len(remaining))
	}
	// The two newest (version 5 and 4) should remain.
	if remaining[0].Version != 5 || remaining[1].Version != 4 {
		t.Errorf("remaining versions = [%d,%d], want [5,4]",
			remaining[0].Version, remaining[1].Version)
	}
}

func TestSchemaVersions(t *testing.T) {
	cat := openTestCatalog(t)
	id := insertTestDB(t, cat, "schemadb", "/schema.db")

	_, err := cat.InsertSchemaVersion(&SchemaVersion{
		DatabaseID: id, Version: 1,
		SchemaSQL: "CREATE TABLE a(x);", SchemaHash: "hash1",
	})
	if err != nil {
		t.Fatalf("InsertSchemaVersion(1): %v", err)
	}
	_, err = cat.InsertSchemaVersion(&SchemaVersion{
		DatabaseID: id, Version: 2,
		SchemaSQL: "CREATE TABLE a(x); CREATE TABLE b(y);", SchemaHash: "hash2",
	})
	if err != nil {
		t.Fatalf("InsertSchemaVersion(2): %v", err)
	}

	latest, err := cat.LatestSchemaVersion(id)
	if err != nil {
		t.Fatalf("LatestSchemaVersion: %v", err)
	}
	if latest.Version != 2 {
		t.Errorf("latest version = %d, want 2", latest.Version)
	}
	if latest.SchemaHash != "hash2" {
		t.Errorf("latest hash = %q, want %q", latest.SchemaHash, "hash2")
	}

	list, err := cat.ListSchemaVersions(id)
	if err != nil {
		t.Fatalf("ListSchemaVersions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	// Ordered by version DESC.
	if list[0].Version != 2 || list[1].Version != 1 {
		t.Errorf("versions = [%d,%d], want [2,1]", list[0].Version, list[1].Version)
	}
}

func TestNextSchemaVersion(t *testing.T) {
	cat := openTestCatalog(t)
	id := insertTestDB(t, cat, "nextsv", "/nextsv.db")

	v, _ := cat.NextSchemaVersion(id)
	if v != 1 {
		t.Errorf("first = %d, want 1", v)
	}

	cat.InsertSchemaVersion(&SchemaVersion{
		DatabaseID: id, Version: 1,
		SchemaSQL: "x", SchemaHash: "h",
	})
	v, _ = cat.NextSchemaVersion(id)
	if v != 2 {
		t.Errorf("after 1 insert = %d, want 2", v)
	}
}

func TestSchemaTransitions(t *testing.T) {
	cat := openTestCatalog(t)
	id := insertTestDB(t, cat, "transdb", "/trans.db")

	err := cat.InsertTransition(&SchemaTransition{
		DatabaseID:  id,
		FromVersion: 1,
		ToVersion:   2,
		DetectedDDL: "ALTER TABLE a ADD COLUMN y;",
		Summary:     "added column y",
	})
	if err != nil {
		t.Fatalf("InsertTransition: %v", err)
	}

	err = cat.InsertTransition(&SchemaTransition{
		DatabaseID:  id,
		FromVersion: 2,
		ToVersion:   3,
		Summary:     "added table b",
	})
	if err != nil {
		t.Fatalf("InsertTransition(2→3): %v", err)
	}

	list, err := cat.ListTransitions(id)
	if err != nil {
		t.Fatalf("ListTransitions: %v", err)
	}
	if len(list) != 2 {
		t.Fatalf("len = %d, want 2", len(list))
	}
	if list[0].FromVersion != 1 || list[0].ToVersion != 2 {
		t.Errorf("first transition = %d→%d, want 1→2", list[0].FromVersion, list[0].ToVersion)
	}
	if list[0].DetectedDDL != "ALTER TABLE a ADD COLUMN y;" {
		t.Errorf("DDL = %q", list[0].DetectedDDL)
	}
	if list[1].Summary != "added table b" {
		t.Errorf("Summary = %q", list[1].Summary)
	}
}

func TestPriorityOrdering(t *testing.T) {
	cat := openTestCatalog(t)

	entries := []struct {
		name     string
		path     string
		priority string
	}{
		{"zz-other", "/zz.db", "other"},
		{"aa-copilot", "/aa.db", "copilot"},
		{"bb-docker", "/bb.db", "docker"},
		{"cc-workspace", "/cc.db", "workspace"},
		{"dd-app", "/dd.db", "app_data"},
		{"ee-docker", "/ee.db", "docker"},
	}

	for _, e := range entries {
		_, err := cat.UpsertDiscovered(&DiscoveredDB{
			Name:       e.name,
			SourcePath: e.path,
			Priority:   e.priority,
		})
		if err != nil {
			t.Fatalf("UpsertDiscovered(%s): %v", e.name, err)
		}
	}

	list, err := cat.ListDiscovered()
	if err != nil {
		t.Fatalf("ListDiscovered: %v", err)
	}

	expected := []string{
		"bb-docker",    // docker, first alphabetically
		"ee-docker",    // docker, second
		"cc-workspace", // workspace
		"aa-copilot",   // copilot
		"dd-app",       // app_data
		"zz-other",     // other
	}

	if len(list) != len(expected) {
		t.Fatalf("len = %d, want %d", len(list), len(expected))
	}
	for i, want := range expected {
		if list[i].Name != want {
			t.Errorf("list[%d].Name = %q, want %q", i, list[i].Name, want)
		}
	}
}

func TestLatestSchemaVersionNoRows(t *testing.T) {
	cat := openTestCatalog(t)
	id := insertTestDB(t, cat, "emptyschema", "/emptyschema.db")

	_, err := cat.LatestSchemaVersion(id)
	if err != sql.ErrNoRows {
		t.Errorf("expected ErrNoRows, got %v", err)
	}
}

func TestReplicationStateWithBaseSnapshot(t *testing.T) {
	cat := openTestCatalog(t)
	id := insertTestDB(t, cat, "replsnap", "/replsnap.db")

	snapID, err := cat.InsertSnapshot(&Snapshot{
		DatabaseID:    id,
		Version:       1,
		SchemaVersion: 1,
		SnapshotPath:  "/s/base.db",
		Trigger:       "initial",
	})
	if err != nil {
		t.Fatalf("InsertSnapshot: %v", err)
	}

	err = cat.SetReplicationState(&ReplicationState{
		DatabaseID:     id,
		ReplicaName:    "replica-snap",
		BaseSnapshotID: sql.NullInt64{Int64: snapID, Valid: true},
	})
	if err != nil {
		t.Fatalf("SetReplicationState: %v", err)
	}

	got, err := cat.GetReplicationState(id)
	if err != nil {
		t.Fatalf("GetReplicationState: %v", err)
	}
	if !got.BaseSnapshotID.Valid || got.BaseSnapshotID.Int64 != snapID {
		t.Errorf("BaseSnapshotID = %v, want %d", got.BaseSnapshotID, snapID)
	}
}

func TestPruneSnapshotsKeepAll(t *testing.T) {
	cat := openTestCatalog(t)
	id := insertTestDB(t, cat, "keepall", "/keepall.db")

	for i := 1; i <= 3; i++ {
		cat.InsertSnapshot(&Snapshot{
			DatabaseID: id, Version: i, SchemaVersion: 1,
			SnapshotPath: "/k/" + string(rune('0'+i)) + ".db", Trigger: "manual",
		})
	}

	paths, err := cat.PruneSnapshots(id, 10)
	if err != nil {
		t.Fatalf("PruneSnapshots: %v", err)
	}
	if len(paths) != 0 {
		t.Errorf("pruned %d, want 0 (keep > total)", len(paths))
	}

	remaining, _ := cat.ListSnapshots(id)
	if len(remaining) != 3 {
		t.Errorf("remaining = %d, want 3", len(remaining))
	}
}
