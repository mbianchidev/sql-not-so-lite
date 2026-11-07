package etcqlite

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	_ "modernc.org/sqlite"
)

var ErrProtected = errors.New("Etcqlite internal stores, interchange artifacts and projections cannot be managed, exposed, replicated or recovered as generic SQLite databases; use the consented Etcqlite inspector")

type schemaColumn struct {
	name   string
	kind   string
	hidden int
	pk     int
}

var projectionColumns = map[string][]schemaColumn{
	"projection_metadata": {
		{name: "kind", kind: "TEXT"}, {name: "format", kind: "INTEGER"},
		{name: "cluster_id", kind: "BLOB"}, {name: "member_id", kind: "BLOB"},
		{name: "revision", kind: "INTEGER"}, {name: "compact_revision", kind: "INTEGER"},
		{name: "source_schema", kind: "INTEGER"}, {name: "includes_data", kind: "INTEGER"},
	},
	"key_metadata": {
		{name: "ordinal", kind: "INTEGER"}, {name: "key_bytes", kind: "INTEGER"},
		{name: "value_bytes", kind: "INTEGER"}, {name: "create_revision", kind: "INTEGER"},
		{name: "mod_revision", kind: "INTEGER"}, {name: "version", kind: "INTEGER"}, {name: "lease", kind: "INTEGER"},
	},
	"lease_metadata": {
		{name: "id", kind: "INTEGER"}, {name: "granted_ttl", kind: "INTEGER"},
		{name: "generation", kind: "INTEGER"}, {name: "checkpoint_ttl", kind: "INTEGER"},
	},
	"key_values": {
		{name: "ordinal", kind: "INTEGER"}, {name: "key", kind: "BLOB"}, {name: "value", kind: "BLOB"},
	},
}

func openReadOnly(ctx context.Context, path string, immutable bool) (*sql.DB, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	dsn := url.URL{Scheme: "file", Path: filepath.ToSlash(path)}
	query := url.Values{"mode": {"ro"}, "_pragma": {"query_only(1)", "trusted_schema(0)", "busy_timeout(1000)"}}
	if immutable {
		query.Set("immutable", "1")
	}
	dsn.RawQuery = query.Encode()
	db, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func openImmutable(ctx context.Context, path string) (*sql.DB, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0o400 {
		return nil, fmt.Errorf("projection must be an owner-read-only regular file, not a symlink")
	}
	return openReadOnly(ctx, path, true)
}

func OpenProjection(ctx context.Context, path string, expected Metadata) (*sql.DB, error) {
	db, err := openImmutable(ctx, path)
	if err != nil {
		return nil, err
	}
	discovery := &Discovery{
		ClusterID: expected.ClusterID, MemberID: expected.MemberID,
		Revision: expected.Revision, SchemaVersion: expected.SourceSchema,
	}
	actual, err := validateProjection(ctx, db, discovery, expected.IncludesData)
	if err == nil && actual != expected {
		err = ErrInvalidProjection
	}
	if err != nil {
		db.Close()
		return nil, err
	}
	return db, nil
}

func columns(ctx context.Context, db *sql.DB, table string) ([]schemaColumn, error) {
	rows, err := db.QueryContext(ctx, `SELECT name,type,hidden,pk FROM pragma_table_xinfo(?)`, table)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var result []schemaColumn
	for rows.Next() {
		var column schemaColumn
		if err := rows.Scan(&column.name, &column.kind, &column.hidden, &column.pk); err != nil {
			return nil, err
		}
		column.kind = strings.ToUpper(column.kind)
		result = append(result, column)
	}
	return result, rows.Err()
}

func validateProjection(ctx context.Context, db *sql.DB, discovery *Discovery, includeData bool) (Metadata, error) {
	var metadata Metadata
	fail := func() (Metadata, error) { return Metadata{}, ErrInvalidProjection }
	var integrity string
	if err := db.QueryRowContext(ctx, "PRAGMA quick_check").Scan(&integrity); err != nil || integrity != "ok" {
		return fail()
	}
	rows, err := db.QueryContext(ctx, `SELECT type,name,tbl_name,sql FROM sqlite_schema`)
	if err != nil {
		return fail()
	}
	var tables []string
	invalid := false
	for rows.Next() {
		var kind, name, table string
		var ddl sql.NullString
		if err := rows.Scan(&kind, &name, &table, &ddl); err != nil {
			rows.Close()
			return fail()
		}
		if kind == "index" && !ddl.Valid && projectionColumns[table] != nil {
			continue
		}
		if kind != "table" || projectionColumns[name] == nil || strings.Contains(strings.ToUpper(ddl.String), "VIRTUAL TABLE") {
			invalid = true
			continue
		}
		tables = append(tables, name)
	}
	err = errors.Join(rows.Err(), rows.Close())
	expectedTables := []string{"key_metadata", "lease_metadata", "projection_metadata"}
	if includeData {
		expectedTables = append(expectedTables, "key_values")
	}
	slices.Sort(tables)
	slices.Sort(expectedTables)
	if err != nil || invalid || !slices.Equal(tables, expectedTables) {
		return fail()
	}
	for _, table := range tables {
		actual, err := columns(ctx, db, table)
		expected := projectionColumns[table]
		if err != nil || len(actual) != len(expected) || !containsColumns(actual, expected) {
			return fail()
		}
		conditions := make([]string, 0, len(expected))
		for _, column := range expected {
			typeName := strings.ToLower(column.kind)
			condition := fmt.Sprintf(`typeof("%s") != '%s'`, column.name, typeName)
			if column.name == "checkpoint_ttl" {
				condition += fmt.Sprintf(` AND "%s" IS NOT NULL`, column.name)
			}
			conditions = append(conditions, "("+condition+")")
		}
		var badValue bool
		if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM "`+table+`" WHERE `+strings.Join(conditions, " OR ")+`)`).Scan(&badValue); err != nil || badValue {
			return fail()
		}
	}
	var count, format, includesData int
	var kind string
	var clusterID, memberID []byte
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM projection_metadata").Scan(&count); err != nil || count != 1 {
		return fail()
	}
	if err := db.QueryRowContext(ctx, `SELECT kind,format,cluster_id,member_id,revision,compact_revision,source_schema,includes_data FROM projection_metadata`).
		Scan(&kind, &format, &clusterID, &memberID, &metadata.Revision, &metadata.CompactRevision, &metadata.SourceSchema, &includesData); err != nil {
		return fail()
	}
	if kind != "read-only-inspection" || format != 1 || len(clusterID) != 8 || len(memberID) != 8 ||
		(includesData != 0 && includesData != 1) || (includesData == 1) != includeData {
		return fail()
	}
	metadata.ClusterID = strconv.FormatUint(binary.BigEndian.Uint64(clusterID), 10)
	metadata.MemberID = strconv.FormatUint(binary.BigEndian.Uint64(memberID), 10)
	metadata.IncludesData = includesData == 1
	if metadata.ClusterID != discovery.ClusterID || metadata.MemberID != discovery.MemberID ||
		metadata.SourceSchema != discovery.SchemaVersion || metadata.Revision < discovery.Revision ||
		metadata.CompactRevision < -1 || metadata.CompactRevision > metadata.Revision {
		return fail()
	}
	var invalidKeys bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM key_metadata WHERE
			key_bytes < 0 OR value_bytes < 0 OR ordinal < 1 OR
			create_revision < 1 OR mod_revision < create_revision OR mod_revision > ? OR version < 1)
	`, metadata.Revision).Scan(&invalidKeys); err != nil || invalidKeys {
		return fail()
	}
	if includeData {
		var mismatch bool
		if err := db.QueryRowContext(ctx, `
			SELECT EXISTS(
				SELECT 1 FROM key_metadata m LEFT JOIN key_values v USING(ordinal)
				WHERE v.ordinal IS NULL OR length(v.key) != m.key_bytes OR length(v.value) != m.value_bytes
			) OR EXISTS(
				SELECT 1 FROM key_values v LEFT JOIN key_metadata m USING(ordinal) WHERE m.ordinal IS NULL
			)
		`).Scan(&mismatch); err != nil || mismatch {
			return fail()
		}
	}
	return metadata, nil
}

func CheckOrdinaryFile(ctx context.Context, path string) error {
	db, err := openReadOnly(ctx, path, false)
	if err != nil {
		return err
	}
	defer db.Close()
	return CheckOrdinaryDatabase(ctx, db)
}

func CheckOrdinaryDatabase(ctx context.Context, db *sql.DB) error {
	var projection, interchange bool
	if err := db.QueryRowContext(ctx, `
		SELECT EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='projection_metadata'),
			EXISTS(SELECT 1 FROM sqlite_schema WHERE type='table' AND name='interchange_meta')
	`).Scan(&projection, &interchange); err != nil {
		return fmt.Errorf("classify SQLite schema: %w", err)
	}
	if interchange {
		actual, err := columns(ctx, db, "interchange_meta")
		if err != nil {
			return fmt.Errorf("classify interchange schema: %w", err)
		}
		signature := []schemaColumn{
			{name: "kind", kind: "TEXT"}, {name: "format", kind: "INTEGER"},
			{name: "reference", kind: "TEXT"}, {name: "revision", kind: "INTEGER"},
			{name: "compact_revision", kind: "INTEGER"},
		}
		if containsColumns(actual, signature) {
			var marker bool
			if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM interchange_meta WHERE kind='bbolt-interchange' AND format=2)`).Scan(&marker); err != nil {
				return fmt.Errorf("classify interchange marker: %w", err)
			}
			if marker {
				return ErrProtected
			}
		}
	}
	if projection {
		actual, err := columns(ctx, db, "projection_metadata")
		if err != nil {
			return fmt.Errorf("classify projection schema: %w", err)
		}
		if containsColumns(actual, projectionColumns["projection_metadata"]) {
			var marker bool
			if err := db.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM projection_metadata WHERE kind='read-only-inspection' AND format=1)`).Scan(&marker); err != nil {
				return fmt.Errorf("classify projection marker: %w", err)
			}
			if marker {
				return ErrProtected
			}
		}
	}
	node, err := columns(ctx, db, "node_state")
	if err != nil {
		return fmt.Errorf("classify Etcqlite node schema: %w", err)
	}
	app, err := columns(ctx, db, "app_state")
	if err != nil {
		return fmt.Errorf("classify Etcqlite application schema: %w", err)
	}
	nodeSignature := []schemaColumn{
		{name: "id", kind: "INTEGER", pk: 1}, {name: "cluster_id", kind: "BLOB"},
		{name: "member_id", kind: "BLOB"}, {name: "name", kind: "TEXT"},
		{name: "mode", kind: "TEXT"}, {name: "runtime_epoch", kind: "INTEGER"}, {name: "checksum", kind: "BLOB"},
	}
	appSignature := []schemaColumn{
		{name: "id", kind: "INTEGER", pk: 1}, {name: "schema_version", kind: "INTEGER"},
		{name: "codec_version", kind: "INTEGER"}, {name: "revision", kind: "INTEGER"},
		{name: "compacted_revision", kind: "INTEGER"}, {name: "auth_revision", kind: "BLOB"},
		{name: "auth_enabled", kind: "INTEGER"}, {name: "checksum", kind: "BLOB"},
	}
	if containsColumns(node, nodeSignature) && containsColumns(app, appSignature) {
		return ErrProtected
	}
	return nil
}

func containsColumns(actual, signature []schemaColumn) bool {
	for _, expected := range signature {
		found := slices.ContainsFunc(actual, func(column schemaColumn) bool {
			return column.name == expected.name && column.kind == expected.kind &&
				column.hidden == 0 && (expected.pk == 0 || column.pk == expected.pk)
		})
		if !found {
			return false
		}
	}
	return true
}
