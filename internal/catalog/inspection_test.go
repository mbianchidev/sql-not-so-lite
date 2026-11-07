package catalog

import (
	"path/filepath"
	"testing"
	"time"
)

func TestInspectionPersistenceReplacementAndPreferences(t *testing.T) {
	dir := t.TempDir()
	cat, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC().Truncate(time.Second)
	d := &DiscoveredDB{
		Name: "Etcqlite", SourcePath: filepath.Join(dir, "first.sqlite"),
		Inspection: &EtcqliteInspection{
			Endpoint:  "http://127.0.0.1:2379",
			ClusterID: "18446744073709551615", MemberID: "9223372036854775809",
			Revision: "9007199254740993", CompactRevision: "9007199254740990",
			SchemaVersion: 2, State: "stale", FetchedAt: now,
		},
	}
	id, err := cat.UpsertDiscovered(d)
	if err != nil {
		t.Fatal(err)
	}
	if err := cat.UpdateFavorite(id, true); err != nil {
		t.Fatal(err)
	}
	if err := cat.Close(); err != nil {
		t.Fatal(err)
	}
	cat, err = Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	d, err = cat.GetDiscovered(id)
	if err != nil || d.Inspection == nil || d.Inspection.Revision != "9007199254740993" || !d.Favorite {
		t.Fatalf("inspection metadata or preferences not persisted: %+v, %v", d, err)
	}
	d.SourcePath = filepath.Join(dir, "second.sqlite")
	d.Inspection.IncludesData = true
	d.Available = true
	if err := cat.UpdateInspection(d); err != nil {
		t.Fatal(err)
	}
	dbs, err := cat.ListDiscovered()
	if err != nil || len(dbs) != 1 || dbs[0].ID != id || dbs[0].SourcePath != d.SourcePath ||
		!dbs[0].Favorite || !dbs[0].Inspection.IncludesData {
		t.Fatalf("replacement lost stable catalog identity: %+v, %v", dbs, err)
	}
}
