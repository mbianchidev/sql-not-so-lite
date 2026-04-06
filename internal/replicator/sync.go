package replicator

import (
	"database/sql"
	"fmt"
	"log"
	"strings"

	_ "modernc.org/sqlite"
)

// SyncTable performs a full replacement sync of a single table from source to replica.
// It drops and recreates the table in the replica with data from the source.
func SyncTable(srcDB, dstDB *sql.DB, tableName string) error {
	escaped := fmt.Sprintf(`"%s"`, strings.ReplaceAll(tableName, `"`, `""`))

	// Get table schema from source
	var createSQL string
	err := srcDB.QueryRow(
		"SELECT sql FROM sqlite_master WHERE type='table' AND name=?", tableName,
	).Scan(&createSQL)
	if err != nil {
		return fmt.Errorf("failed to get schema for table %s: %w", tableName, err)
	}

	tx, err := dstDB.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Drop and recreate in replica
	tx.Exec(fmt.Sprintf("DROP TABLE IF EXISTS %s", escaped))
	if _, err := tx.Exec(createSQL); err != nil {
		return fmt.Errorf("failed to create table %s in replica: %w", tableName, err)
	}

	// Read all rows from source
	rows, err := srcDB.Query(fmt.Sprintf("SELECT * FROM %s", escaped))
	if err != nil {
		return fmt.Errorf("failed to read source table %s: %w", tableName, err)
	}
	defer rows.Close()

	cols, err := rows.Columns()
	if err != nil {
		return fmt.Errorf("failed to get columns for %s: %w", tableName, err)
	}

	if len(cols) == 0 {
		return tx.Commit()
	}

	placeholders := strings.Repeat("?,", len(cols))
	placeholders = placeholders[:len(placeholders)-1]
	insertSQL := fmt.Sprintf("INSERT INTO %s VALUES (%s)", escaped, placeholders)

	stmt, err := tx.Prepare(insertSQL)
	if err != nil {
		return fmt.Errorf("failed to prepare insert for %s: %w", tableName, err)
	}
	defer stmt.Close()

	for rows.Next() {
		vals := make([]interface{}, len(cols))
		ptrs := make([]interface{}, len(cols))
		for i := range vals {
			ptrs[i] = &vals[i]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return fmt.Errorf("failed to scan row from %s: %w", tableName, err)
		}
		if _, err := stmt.Exec(vals...); err != nil {
			return fmt.Errorf("failed to insert row into %s: %w", tableName, err)
		}
	}

	return tx.Commit()
}

// SyncTables syncs a list of tables from source to replica.
func SyncTables(srcDB, dstDB *sql.DB, tables []string) error {
	for _, t := range tables {
		if err := SyncTable(srcDB, dstDB, t); err != nil {
			return fmt.Errorf("sync table %q: %w", t, err)
		}
	}
	return nil
}

// FullSync syncs all user tables from source to replica.
func FullSync(srcDB, dstDB *sql.DB) error {
	tables, err := listUserTables(srcDB)
	if err != nil {
		return fmt.Errorf("failed to list tables: %w", err)
	}

	log.Printf("replicator: full sync of %d table(s)", len(tables))
	return SyncTables(srcDB, dstDB, tables)
}

// InitialSync creates a replica from scratch using VACUUM INTO, then
// returns the list of tables in the source for schema tracking.
func InitialSync(sourcePath, replicaPath string) ([]string, error) {
	if _, err := CreateSnapshot(sourcePath, replicaPath); err != nil {
		return nil, fmt.Errorf("initial snapshot failed: %w", err)
	}

	srcDB, err := sql.Open("sqlite", sourcePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("failed to open source: %w", err)
	}
	defer srcDB.Close()
	srcDB.SetMaxOpenConns(1)

	tables, err := listUserTables(srcDB)
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}

	return tables, nil
}

// OpenReadOnly opens a SQLite database in read-only mode without modifying
// journal settings. Used for source databases we don't own.
func OpenReadOnly(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=busy_timeout(5000)&_pragma=query_only(1)")
	if err != nil {
		return nil, fmt.Errorf("failed to open read-only: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	return db, nil
}

// OpenReplica opens a replica database for writing.
func OpenReplica(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path+"?_pragma=journal_mode(wal)&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("failed to open replica: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to connect: %w", err)
	}
	return db, nil
}

func listUserTables(db *sql.DB) ([]string, error) {
	rows, err := db.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		tables = append(tables, name)
	}
	return tables, nil
}
