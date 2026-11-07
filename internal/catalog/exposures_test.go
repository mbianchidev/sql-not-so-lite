package catalog

import (
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestAPIExposuresPersistAndCascadeWithoutLeakingHashes(t *testing.T) {
	root := t.TempDir()
	cat, err := Open(root)
	if err != nil {
		t.Fatal(err)
	}
	id, err := cat.UpsertDiscovered(&DiscoveredDB{Name: "fixture", SourcePath: filepath.Join(root, "fixture.sqlite"), Status: "discovered", Priority: "workspace"})
	if err != nil {
		t.Fatal(err)
	}
	exposure := &APIExposure{Alias: "fixture", DatabaseID: id, Tables: []string{"items"}, TokenHash: strings.Repeat("a", 64)}
	if err := cat.CreateAPIExposure(exposure); err != nil {
		t.Fatal(err)
	}
	if err := cat.Close(); err != nil {
		t.Fatal(err)
	}
	cat, err = Open(root)
	if err != nil {
		t.Fatal(err)
	}
	defer cat.Close()
	read, err := cat.GetAPIExposure("fixture")
	if err != nil {
		t.Fatal(err)
	}
	if read.TokenHash != exposure.TokenHash || len(read.Tables) != 1 {
		t.Fatal("exposure was not persisted")
	}
	data, err := json.Marshal(read)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), read.TokenHash) {
		t.Fatal("token hash leaked through JSON")
	}
	if err := cat.DeleteDiscovered(id); err != nil {
		t.Fatal(err)
	}
	if _, err := cat.GetAPIExposure("fixture"); err != sql.ErrNoRows {
		t.Fatalf("exposure was not revoked: %v", err)
	}
}
