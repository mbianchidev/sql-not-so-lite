package service

import (
	"context"
	"strings"
	"testing"
)

func TestProjectionRepresentationAndExactPayloadLimit(t *testing.T) {
	db := recordTestDB(t)
	ctx := context.Background()
	result, err := QueryProjection(ctx, db, `SELECT payload,9223372036854775807 AS revision FROM inventory WHERE id=?`, []string{"1"}, 100, 0)
	if err != nil {
		t.Fatal(err)
	}
	if result.Rows[0][0] != "hex:000102" || result.Rows[0][1] != "9223372036854775807" {
		t.Fatalf("projection lost binary or integer precision: %+v", result)
	}
	ordinary, err := QueryDatabase(ctx, db, `SELECT payload FROM inventory WHERE id=1`, nil, 100, 0)
	if err != nil || ordinary.Rows[0][0] != "[0 1 2]" {
		t.Fatalf("ordinary query representation changed: %+v, %v", ordinary, err)
	}
	result, err = QueryProjection(ctx, db, `SELECT zeroblob(524286)`, nil, 1, 0)
	if err != nil || len(result.Rows[0][0]) != 1024*1024 {
		t.Fatalf("exact 1 MiB cell payload was rejected: %v", err)
	}
	if _, err := QueryProjection(ctx, db, `SELECT zeroblob(524287)`, nil, 1, 0); err == nil || !strings.Contains(err.Error(), "exceed 1 MiB") {
		t.Fatalf("oversized payload was not rejected explicitly: %v", err)
	}
}
