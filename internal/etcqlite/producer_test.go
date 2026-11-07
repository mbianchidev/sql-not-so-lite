package etcqlite

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestInstalledProducerProjectionContract(t *testing.T) {
	binary := os.Getenv("SQNSL_ETCQLITE_BINARY")
	if binary == "" {
		t.Skip("set SQNSL_ETCQLITE_BINARY to verify an installed producer using disposable synthetic storage")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	dataDir := filepath.Join(t.TempDir(), "synthetic-source")
	clusterID, memberID := "18446744073709551615", "9223372036854775809"
	command := exec.CommandContext(ctx, binary, "init", "--data-dir", dataDir, "--name", "synthetic-inspection",
		"--cluster-id", clusterID, "--member-id", memberID)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("initialize disposable producer: %v\n%s", err, output)
	}
	for _, includeData := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "projection.sqlite")
		args := []string{"inspect", "--data-dir", dataDir, "--output", path}
		if includeData {
			args = append(args, "--include-data", "--ack-sensitive-data")
		}
		if output, err := exec.CommandContext(ctx, binary, args...).CombinedOutput(); err != nil {
			t.Fatalf("produce disposable projection: %v\n%s", err, output)
		}
		db, err := OpenProjection(ctx, path, Metadata{
			ClusterID: clusterID, MemberID: memberID, Revision: 1, CompactRevision: -1,
			SourceSchema: 2, IncludesData: includeData,
		})
		if err != nil {
			logProjectionContract(t, ctx, path)
			t.Fatalf("installed producer violates the projection contract: %v", err)
		}
		db.Close()
	}
	if err := CheckOrdinaryFile(ctx, filepath.Join(dataDir, "node.db")); !errors.Is(err, ErrProtected) {
		t.Fatalf("installed native store was not protected: %v", err)
	}
}

func logProjectionContract(t *testing.T, ctx context.Context, path string) {
	t.Helper()
	db, err := openImmutable(ctx, path)
	if err != nil {
		t.Logf("cannot read projection schema: %v", err)
		return
	}
	defer db.Close()
	for _, table := range []string{"projection_metadata", "key_metadata", "lease_metadata", "key_values"} {
		shape, err := columns(ctx, db, table)
		t.Logf("projection schema %s: %+v, error=%v", table, shape, err)
	}
	var kind, clusterID, memberID string
	var format, revision, compact, schema, includes int64
	err = db.QueryRowContext(ctx, `SELECT kind,format,hex(cluster_id),hex(member_id),revision,compact_revision,source_schema,includes_data FROM projection_metadata`).
		Scan(&kind, &format, &clusterID, &memberID, &revision, &compact, &schema, &includes)
	t.Logf("non-sensitive projection marker: kind=%s format=%d cluster=%s member=%s revision=%d compact=%d schema=%d includes_data=%d error=%v",
		kind, format, clusterID, memberID, revision, compact, schema, includes, err)
}
