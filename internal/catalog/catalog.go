package catalog

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

type DiscoveredDB struct {
	ID            int64
	Name          string
	SourcePath    string
	SQLiteVersion string
	PageSize      int
	JournalMode   string
	SizeBytes     int64
	LastModified  time.Time
	FirstSeen     time.Time
	LastScanned   time.Time
	Status        string
	ErrorMessage  string
	GitHubRepo    string
	GitHubURL     string
	Priority      string
}

type ReplicationState struct {
	DatabaseID     int64
	ReplicaName    string
	Salt1          uint32
	Salt2          uint32
	LastFrame      uint32
	PageSize       uint32
	BaseSnapshotID sql.NullInt64
	LastSync       time.Time
	SyncMode       string
}

type Snapshot struct {
	ID            int64
	DatabaseID    int64
	Version       int
	SchemaVersion int
	SnapshotPath  string
	CreatedAt     time.Time
	SizeBytes     int64
	Trigger       string
}

type SchemaVersion struct {
	ID         int64
	DatabaseID int64
	Version    int
	SchemaSQL  string
	SchemaHash string
	DetectedAt time.Time
}

type SchemaTransition struct {
	ID          int64
	DatabaseID  int64
	FromVersion int
	ToVersion   int
	DetectedDDL string
	Summary     string
	DetectedAt  time.Time
}

const schemaDDL = `
CREATE TABLE IF NOT EXISTS discovered_databases (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	name           TEXT NOT NULL UNIQUE,
	source_path    TEXT NOT NULL UNIQUE,
	sqlite_version TEXT,
	page_size      INTEGER,
	journal_mode   TEXT,
	size_bytes     INTEGER DEFAULT 0,
	last_modified  TEXT,
	first_seen     TEXT NOT NULL DEFAULT (datetime('now')),
	last_scanned   TEXT NOT NULL DEFAULT (datetime('now')),
	status         TEXT NOT NULL DEFAULT 'discovered',
	error_message  TEXT,
	github_repo    TEXT,
	github_url     TEXT,
	priority       TEXT DEFAULT 'other',
	CHECK(status IN ('discovered','replicating','paused','error','archived')),
	CHECK(priority IN ('docker','workspace','copilot','app_data','other'))
);

CREATE TABLE IF NOT EXISTS snapshots (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	database_id    INTEGER NOT NULL REFERENCES discovered_databases(id) ON DELETE CASCADE,
	version        INTEGER NOT NULL,
	schema_version INTEGER NOT NULL,
	snapshot_path  TEXT NOT NULL,
	created_at     TEXT NOT NULL DEFAULT (datetime('now')),
	size_bytes     INTEGER DEFAULT 0,
	"trigger"      TEXT NOT NULL DEFAULT 'manual',
	CHECK("trigger" IN ('initial','schema_change','manual','scheduled')),
	UNIQUE(database_id, version)
);

CREATE TABLE IF NOT EXISTS replication_state (
	database_id     INTEGER PRIMARY KEY REFERENCES discovered_databases(id) ON DELETE CASCADE,
	replica_name    TEXT NOT NULL,
	salt1           INTEGER DEFAULT 0,
	salt2           INTEGER DEFAULT 0,
	last_frame      INTEGER DEFAULT 0,
	page_size       INTEGER DEFAULT 0,
	base_snapshot_id INTEGER,
	last_sync       TEXT,
	sync_mode       TEXT DEFAULT 'full',
	FOREIGN KEY (base_snapshot_id) REFERENCES snapshots(id)
);

CREATE TABLE IF NOT EXISTS schema_versions (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	database_id    INTEGER NOT NULL REFERENCES discovered_databases(id) ON DELETE CASCADE,
	version        INTEGER NOT NULL,
	schema_sql     TEXT NOT NULL,
	schema_hash    TEXT NOT NULL,
	detected_at    TEXT NOT NULL DEFAULT (datetime('now')),
	UNIQUE(database_id, version)
);

CREATE TABLE IF NOT EXISTS schema_transitions (
	id             INTEGER PRIMARY KEY AUTOINCREMENT,
	database_id    INTEGER NOT NULL REFERENCES discovered_databases(id) ON DELETE CASCADE,
	from_version   INTEGER NOT NULL,
	to_version     INTEGER NOT NULL,
	detected_ddl   TEXT,
	summary        TEXT,
	detected_at    TEXT NOT NULL DEFAULT (datetime('now')),
	UNIQUE(database_id, from_version, to_version)
);
`

type Catalog struct {
	db *sql.DB
	mu sync.RWMutex
}

func nullStr(s string) interface{} {
	if s == "" {
		return nil
	}
	return s
}

func nullInt(v int) interface{} {
	if v == 0 {
		return nil
	}
	return v
}

func nullTimeStr(t time.Time) interface{} {
	if t.IsZero() {
		return nil
	}
	return t.Format(time.RFC3339)
}

func parseTime(s string) time.Time {
	t, _ := time.Parse(time.RFC3339, s)
	return t
}

func parseNullTime(ns sql.NullString) time.Time {
	if !ns.Valid {
		return time.Time{}
	}
	return parseTime(ns.String)
}

type scanner interface {
	Scan(dest ...interface{}) error
}

const discoveredCols = `id, name, source_path, sqlite_version, page_size, journal_mode,
	size_bytes, last_modified, first_seen, last_scanned, status,
	error_message, github_repo, github_url, priority`

func scanDiscovered(s scanner) (*DiscoveredDB, error) {
	var d DiscoveredDB
	var sqliteVersion, journalMode, lastModified, errorMessage, githubRepo, githubURL, priority sql.NullString
	var pageSize sql.NullInt64
	var firstSeen, lastScanned string

	err := s.Scan(
		&d.ID, &d.Name, &d.SourcePath,
		&sqliteVersion, &pageSize, &journalMode,
		&d.SizeBytes, &lastModified,
		&firstSeen, &lastScanned,
		&d.Status, &errorMessage,
		&githubRepo, &githubURL, &priority,
	)
	if err != nil {
		return nil, err
	}

	d.SQLiteVersion = sqliteVersion.String
	d.PageSize = int(pageSize.Int64)
	d.JournalMode = journalMode.String
	d.LastModified = parseNullTime(lastModified)
	d.FirstSeen = parseTime(firstSeen)
	d.LastScanned = parseTime(lastScanned)
	d.ErrorMessage = errorMessage.String
	d.GitHubRepo = githubRepo.String
	d.GitHubURL = githubURL.String
	d.Priority = priority.String
	if d.Priority == "" {
		d.Priority = "other"
	}
	return &d, nil
}

// Open opens (or creates) the catalog database inside dataDir.
func Open(dataDir string) (*Catalog, error) {
	if err := os.MkdirAll(dataDir, 0o755); err != nil {
		return nil, fmt.Errorf("catalog: mkdir %s: %w", dataDir, err)
	}

	dsn := filepath.Join(dataDir, "catalog.sqlite") +
		"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)"

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("catalog: open: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if _, err := db.Exec(schemaDDL); err != nil {
		db.Close()
		return nil, fmt.Errorf("catalog: schema: %w", err)
	}

	return &Catalog{db: db}, nil
}

func (c *Catalog) Close() error {
	return c.db.Close()
}

// --------------- Discovered databases ---------------

func (c *Catalog) UpsertDiscovered(d *DiscoveredDB) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	now := time.Now().Format(time.RFC3339)
	firstSeen := now
	if !d.FirstSeen.IsZero() {
		firstSeen = d.FirstSeen.Format(time.RFC3339)
	}
	lastScanned := now
	if !d.LastScanned.IsZero() {
		lastScanned = d.LastScanned.Format(time.RFC3339)
	}
	status := d.Status
	if status == "" {
		status = "discovered"
	}
	priority := d.Priority
	if priority == "" {
		priority = "other"
	}

	_, err := c.db.Exec(`
		INSERT INTO discovered_databases
			(name, source_path, sqlite_version, page_size, journal_mode,
			 size_bytes, last_modified, first_seen, last_scanned, status,
			 error_message, github_repo, github_url, priority)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(source_path) DO UPDATE SET
			name           = excluded.name,
			sqlite_version = excluded.sqlite_version,
			page_size      = excluded.page_size,
			journal_mode   = excluded.journal_mode,
			size_bytes     = excluded.size_bytes,
			last_modified  = excluded.last_modified,
			last_scanned   = excluded.last_scanned,
			status         = excluded.status,
			error_message  = excluded.error_message,
			github_repo    = excluded.github_repo,
			github_url     = excluded.github_url,
			priority       = excluded.priority`,
		d.Name, d.SourcePath,
		nullStr(d.SQLiteVersion), nullInt(d.PageSize), nullStr(d.JournalMode),
		d.SizeBytes, nullTimeStr(d.LastModified),
		firstSeen, lastScanned,
		status, nullStr(d.ErrorMessage),
		nullStr(d.GitHubRepo), nullStr(d.GitHubURL), priority,
	)
	if err != nil {
		return 0, fmt.Errorf("catalog: upsert discovered: %w", err)
	}

	var id int64
	err = c.db.QueryRow(`SELECT id FROM discovered_databases WHERE source_path = ?`, d.SourcePath).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("catalog: upsert discovered (get id): %w", err)
	}
	return id, nil
}

func (c *Catalog) GetDiscovered(id int64) (*DiscoveredDB, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return scanDiscovered(c.db.QueryRow(`SELECT `+discoveredCols+` FROM discovered_databases WHERE id = ?`, id))
}

func (c *Catalog) GetDiscoveredByPath(path string) (*DiscoveredDB, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return scanDiscovered(c.db.QueryRow(`SELECT `+discoveredCols+` FROM discovered_databases WHERE source_path = ?`, path))
}

func (c *Catalog) GetDiscoveredByName(name string) (*DiscoveredDB, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return scanDiscovered(c.db.QueryRow(`SELECT `+discoveredCols+` FROM discovered_databases WHERE name = ?`, name))
}

func (c *Catalog) ListDiscovered() ([]DiscoveredDB, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	rows, err := c.db.Query(`SELECT ` + discoveredCols + ` FROM discovered_databases
		ORDER BY CASE priority
			WHEN 'docker' THEN 1
			WHEN 'workspace' THEN 2
			WHEN 'copilot' THEN 3
			WHEN 'app_data' THEN 4
			ELSE 5
		END, name`)
	if err != nil {
		return nil, fmt.Errorf("catalog: list discovered: %w", err)
	}
	defer rows.Close()

	var out []DiscoveredDB
	for rows.Next() {
		d, err := scanDiscovered(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, *d)
	}
	return out, rows.Err()
}

func (c *Catalog) UpdateStatus(id int64, status, errMsg string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.db.Exec(`UPDATE discovered_databases SET status = ?, error_message = ? WHERE id = ?`,
		status, nullStr(errMsg), id)
	return err
}

func (c *Catalog) DeleteDiscovered(id int64) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	_, err := c.db.Exec(`DELETE FROM discovered_databases WHERE id = ?`, id)
	return err
}

// --------------- Replication state ---------------

func (c *Catalog) SetReplicationState(rs *ReplicationState) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	syncMode := rs.SyncMode
	if syncMode == "" {
		syncMode = "full"
	}

	_, err := c.db.Exec(`
		INSERT INTO replication_state
			(database_id, replica_name, salt1, salt2, last_frame, page_size,
			 base_snapshot_id, last_sync, sync_mode)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(database_id) DO UPDATE SET
			replica_name     = excluded.replica_name,
			salt1            = excluded.salt1,
			salt2            = excluded.salt2,
			last_frame       = excluded.last_frame,
			page_size        = excluded.page_size,
			base_snapshot_id = excluded.base_snapshot_id,
			last_sync        = excluded.last_sync,
			sync_mode        = excluded.sync_mode`,
		rs.DatabaseID, rs.ReplicaName,
		rs.Salt1, rs.Salt2, rs.LastFrame, rs.PageSize,
		rs.BaseSnapshotID, nullTimeStr(rs.LastSync), syncMode,
	)
	return err
}

func (c *Catalog) GetReplicationState(dbID int64) (*ReplicationState, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var rs ReplicationState
	var lastSync sql.NullString
	err := c.db.QueryRow(`
		SELECT database_id, replica_name, salt1, salt2, last_frame, page_size,
			   base_snapshot_id, last_sync, sync_mode
		FROM replication_state WHERE database_id = ?`, dbID).
		Scan(&rs.DatabaseID, &rs.ReplicaName,
			&rs.Salt1, &rs.Salt2, &rs.LastFrame, &rs.PageSize,
			&rs.BaseSnapshotID, &lastSync, &rs.SyncMode)
	if err != nil {
		return nil, err
	}
	rs.LastSync = parseNullTime(lastSync)
	return &rs, nil
}

// --------------- Snapshots ---------------

func (c *Catalog) InsertSnapshot(s *Snapshot) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	createdAt := s.CreatedAt
	if createdAt.IsZero() {
		createdAt = time.Now()
	}
	trigger := s.Trigger
	if trigger == "" {
		trigger = "manual"
	}

	res, err := c.db.Exec(`
		INSERT INTO snapshots (database_id, version, schema_version, snapshot_path, created_at, size_bytes, "trigger")
		VALUES (?, ?, ?, ?, ?, ?, ?)`,
		s.DatabaseID, s.Version, s.SchemaVersion, s.SnapshotPath,
		createdAt.Format(time.RFC3339), s.SizeBytes, trigger,
	)
	if err != nil {
		return 0, fmt.Errorf("catalog: insert snapshot: %w", err)
	}
	return res.LastInsertId()
}

func (c *Catalog) ListSnapshots(dbID int64) ([]Snapshot, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	rows, err := c.db.Query(`
		SELECT id, database_id, version, schema_version, snapshot_path, created_at, size_bytes, "trigger"
		FROM snapshots WHERE database_id = ? ORDER BY version DESC`, dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []Snapshot
	for rows.Next() {
		var s Snapshot
		var createdAt string
		if err := rows.Scan(&s.ID, &s.DatabaseID, &s.Version, &s.SchemaVersion,
			&s.SnapshotPath, &createdAt, &s.SizeBytes, &s.Trigger); err != nil {
			return nil, err
		}
		s.CreatedAt = parseTime(createdAt)
		out = append(out, s)
	}
	return out, rows.Err()
}

func (c *Catalog) NextSnapshotVersion(dbID int64) (int, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var v sql.NullInt64
	err := c.db.QueryRow(`SELECT MAX(version) FROM snapshots WHERE database_id = ?`, dbID).Scan(&v)
	if err != nil {
		return 0, err
	}
	if !v.Valid {
		return 1, nil
	}
	return int(v.Int64) + 1, nil
}

func (c *Catalog) PruneSnapshots(dbID int64, keep int) ([]string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	tx, err := c.db.Begin()
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()

	rows, err := tx.Query(`
		SELECT snapshot_path FROM snapshots
		WHERE database_id = ? AND id NOT IN (
			SELECT id FROM snapshots WHERE database_id = ? ORDER BY version DESC LIMIT ?
		)`, dbID, dbID, keep)
	if err != nil {
		return nil, err
	}

	var paths []string
	for rows.Next() {
		var p string
		if err := rows.Scan(&p); err != nil {
			rows.Close()
			return nil, err
		}
		paths = append(paths, p)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}

	_, err = tx.Exec(`
		DELETE FROM snapshots
		WHERE database_id = ? AND id NOT IN (
			SELECT id FROM snapshots WHERE database_id = ? ORDER BY version DESC LIMIT ?
		)`, dbID, dbID, keep)
	if err != nil {
		return nil, err
	}

	return paths, tx.Commit()
}

// --------------- Schema versions ---------------

func (c *Catalog) InsertSchemaVersion(sv *SchemaVersion) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	detectedAt := sv.DetectedAt
	if detectedAt.IsZero() {
		detectedAt = time.Now()
	}

	res, err := c.db.Exec(`
		INSERT INTO schema_versions (database_id, version, schema_sql, schema_hash, detected_at)
		VALUES (?, ?, ?, ?, ?)`,
		sv.DatabaseID, sv.Version, sv.SchemaSQL, sv.SchemaHash,
		detectedAt.Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("catalog: insert schema version: %w", err)
	}
	return res.LastInsertId()
}

func (c *Catalog) LatestSchemaVersion(dbID int64) (*SchemaVersion, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var sv SchemaVersion
	var detectedAt string
	err := c.db.QueryRow(`
		SELECT id, database_id, version, schema_sql, schema_hash, detected_at
		FROM schema_versions WHERE database_id = ? ORDER BY version DESC LIMIT 1`, dbID).
		Scan(&sv.ID, &sv.DatabaseID, &sv.Version, &sv.SchemaSQL, &sv.SchemaHash, &detectedAt)
	if err != nil {
		return nil, err
	}
	sv.DetectedAt = parseTime(detectedAt)
	return &sv, nil
}

func (c *Catalog) ListSchemaVersions(dbID int64) ([]SchemaVersion, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	rows, err := c.db.Query(`
		SELECT id, database_id, version, schema_sql, schema_hash, detected_at
		FROM schema_versions WHERE database_id = ? ORDER BY version DESC`, dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SchemaVersion
	for rows.Next() {
		var sv SchemaVersion
		var detectedAt string
		if err := rows.Scan(&sv.ID, &sv.DatabaseID, &sv.Version, &sv.SchemaSQL, &sv.SchemaHash, &detectedAt); err != nil {
			return nil, err
		}
		sv.DetectedAt = parseTime(detectedAt)
		out = append(out, sv)
	}
	return out, rows.Err()
}

func (c *Catalog) NextSchemaVersion(dbID int64) (int, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	var v sql.NullInt64
	err := c.db.QueryRow(`SELECT MAX(version) FROM schema_versions WHERE database_id = ?`, dbID).Scan(&v)
	if err != nil {
		return 0, err
	}
	if !v.Valid {
		return 1, nil
	}
	return int(v.Int64) + 1, nil
}

// --------------- Schema transitions ---------------

func (c *Catalog) InsertTransition(t *SchemaTransition) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	detectedAt := t.DetectedAt
	if detectedAt.IsZero() {
		detectedAt = time.Now()
	}

	_, err := c.db.Exec(`
		INSERT INTO schema_transitions (database_id, from_version, to_version, detected_ddl, summary, detected_at)
		VALUES (?, ?, ?, ?, ?, ?)`,
		t.DatabaseID, t.FromVersion, t.ToVersion,
		nullStr(t.DetectedDDL), nullStr(t.Summary),
		detectedAt.Format(time.RFC3339),
	)
	return err
}

func (c *Catalog) ListTransitions(dbID int64) ([]SchemaTransition, error) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	rows, err := c.db.Query(`
		SELECT id, database_id, from_version, to_version, detected_ddl, summary, detected_at
		FROM schema_transitions WHERE database_id = ? ORDER BY from_version`, dbID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var out []SchemaTransition
	for rows.Next() {
		var st SchemaTransition
		var ddl, summary sql.NullString
		var detectedAt string
		if err := rows.Scan(&st.ID, &st.DatabaseID, &st.FromVersion, &st.ToVersion,
			&ddl, &summary, &detectedAt); err != nil {
			return nil, err
		}
		st.DetectedDDL = ddl.String
		st.Summary = summary.String
		st.DetectedAt = parseTime(detectedAt)
		out = append(out, st)
	}
	return out, rows.Err()
}
