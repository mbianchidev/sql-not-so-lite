package replicator

import (
	"context"
	"database/sql"
	"fmt"
	"log"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"

	_ "modernc.org/sqlite"
)

type queryer interface {
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

type txQueryer interface {
	queryer
	ExecContext(context.Context, string, ...interface{}) (sql.Result, error)
	PrepareContext(context.Context, string) (*sql.Stmt, error)
}

var replicaLocks sync.Map

func replicaLock(path string) *sync.Mutex {
	key, err := filepath.Abs(path)
	if err != nil {
		key = filepath.Clean(path)
	}
	lock, _ := replicaLocks.LoadOrStore(key, &sync.Mutex{})
	return lock.(*sync.Mutex)
}

func quoteIdentifier(value string) string {
	return `"` + strings.ReplaceAll(value, `"`, `""`) + `"`
}

// SyncTable performs a full replacement sync of a single table from source to replica.
func SyncTable(srcDB, dstDB *sql.DB, tableName string) error {
	return syncTablesContext(context.Background(), srcDB, dstDB, []string{tableName})
}

// SyncTables syncs a list of tables from source to replica in one transaction.
func SyncTables(srcDB, dstDB *sql.DB, tables []string) error {
	return syncTablesContext(context.Background(), srcDB, dstDB, tables)
}

func syncTablesContext(ctx context.Context, srcDB, dstDB *sql.DB, tables []string) error {
	srcTx, err := srcDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("failed to begin source transaction: %w", err)
	}
	defer srcTx.Rollback()

	dstTx, err := dstDB.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("failed to begin replica transaction: %w", err)
	}
	defer dstTx.Rollback()

	for _, table := range tables {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := syncTableContext(ctx, srcTx, dstTx, table); err != nil {
			return fmt.Errorf("sync table %q: %w", table, err)
		}
	}
	if err := dstTx.Commit(); err != nil {
		return fmt.Errorf("commit replica transaction: %w", err)
	}
	return nil
}

func syncTableContext(ctx context.Context, src queryer, dst txQueryer, tableName string) error {
	var createSQL string
	if err := src.QueryRowContext(
		ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?",
		tableName,
	).Scan(&createSQL); err != nil {
		return fmt.Errorf("get table schema: %w", err)
	}
	objects, err := tableObjects(ctx, src, tableName)
	if err != nil {
		return err
	}
	columns, err := tableColumns(ctx, src, tableName)
	if err != nil {
		return err
	}

	escapedTable := quoteIdentifier(tableName)
	if _, err := dst.ExecContext(ctx, "DROP TABLE IF EXISTS "+escapedTable); err != nil {
		return fmt.Errorf("drop table: %w", err)
	}
	if _, err := dst.ExecContext(ctx, createSQL); err != nil {
		return fmt.Errorf("create table: %w", err)
	}

	if len(columns) > 0 {
		quotedColumns := make([]string, len(columns))
		for i, column := range columns {
			quotedColumns[i] = quoteIdentifier(column)
		}
		columnList := strings.Join(quotedColumns, ", ")
		rows, err := src.QueryContext(ctx, "SELECT "+columnList+" FROM "+escapedTable)
		if err != nil {
			return fmt.Errorf("read source rows: %w", err)
		}

		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(columns)), ",")
		stmt, err := dst.PrepareContext(
			ctx,
			"INSERT INTO "+escapedTable+" ("+columnList+") VALUES ("+placeholders+")",
		)
		if err != nil {
			rows.Close()
			return fmt.Errorf("prepare replica insert: %w", err)
		}
		for rows.Next() {
			values, err := scanValues(rows, len(columns))
			if err != nil {
				stmt.Close()
				rows.Close()
				return fmt.Errorf("scan source row: %w", err)
			}
			if _, err := stmt.ExecContext(ctx, values...); err != nil {
				stmt.Close()
				rows.Close()
				return fmt.Errorf("insert replica row: %w", err)
			}
		}
		if err := rows.Err(); err != nil {
			stmt.Close()
			rows.Close()
			return fmt.Errorf("read source rows: %w", err)
		}
		if err := stmt.Close(); err != nil {
			rows.Close()
			return fmt.Errorf("close replica insert: %w", err)
		}
		if err := rows.Close(); err != nil {
			return fmt.Errorf("close source rows: %w", err)
		}
	}

	for _, objectSQL := range objects {
		if _, err := dst.ExecContext(ctx, objectSQL); err != nil {
			return fmt.Errorf("recreate table schema object: %w", err)
		}
	}
	return nil
}

// FullSync syncs all user tables from source to replica.
func FullSync(srcDB, dstDB *sql.DB) error {
	tables, err := listUserTables(context.Background(), srcDB)
	if err != nil {
		return fmt.Errorf("failed to list tables: %w", err)
	}
	log.Printf("replicator: full sync of %d table(s)", len(tables))
	return SyncTables(srcDB, dstDB, tables)
}

// DifferentialSync updates only tables whose schema or rows differ.
func DifferentialSync(sourcePath, replicaPath string) ([]string, error) {
	return DifferentialSyncContext(context.Background(), sourcePath, replicaPath)
}

func DifferentialSyncContext(ctx context.Context, sourcePath, replicaPath string) ([]string, error) {
	lock := replicaLock(replicaPath)
	lock.Lock()
	defer lock.Unlock()

	if _, err := os.Stat(replicaPath); err != nil {
		return nil, fmt.Errorf("replica is not available: %w", err)
	}
	srcDB, err := OpenReadOnly(sourcePath)
	if err != nil {
		return nil, err
	}
	defer srcDB.Close()
	dstDB, err := OpenReplica(replicaPath)
	if err != nil {
		return nil, err
	}
	defer dstDB.Close()

	srcTx, err := srcDB.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return nil, fmt.Errorf("begin source snapshot: %w", err)
	}
	defer srcTx.Rollback()
	dstTx, err := dstDB.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin replica sync: %w", err)
	}
	defer dstTx.Rollback()

	sourceTables, err := listUserTables(ctx, srcTx)
	if err != nil {
		return nil, fmt.Errorf("list source tables: %w", err)
	}
	replicaTables, err := listUserTables(ctx, dstTx)
	if err != nil {
		return nil, fmt.Errorf("list replica tables: %w", err)
	}
	replicaSet := make(map[string]bool, len(replicaTables))
	for _, table := range replicaTables {
		replicaSet[table] = true
	}

	changed := make([]string, 0)
	for _, table := range sourceTables {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		needsSync := !replicaSet[table]
		if !needsSync {
			needsSync, err = tableDiffers(ctx, srcTx, dstTx, table)
			if err != nil {
				return nil, fmt.Errorf("compare table %q: %w", table, err)
			}
		}
		if needsSync {
			if err := syncTableContext(ctx, srcTx, dstTx, table); err != nil {
				return nil, fmt.Errorf("sync table %q: %w", table, err)
			}
			changed = append(changed, table)
		}
		delete(replicaSet, table)
	}

	for table := range replicaSet {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if _, err := dstTx.ExecContext(ctx, "DROP TABLE "+quoteIdentifier(table)); err != nil {
			return nil, fmt.Errorf("drop removed table %q: %w", table, err)
		}
		changed = append(changed, table)
	}
	if err := dstTx.Commit(); err != nil {
		return nil, fmt.Errorf("commit differential sync: %w", err)
	}
	sort.Strings(changed)
	return changed, nil
}

func tableDiffers(ctx context.Context, source, replica queryer, table string) (bool, error) {
	sourceSQL, err := tableSQL(ctx, source, table)
	if err != nil {
		return false, err
	}
	replicaSQL, err := tableSQL(ctx, replica, table)
	if err != nil {
		return false, err
	}
	if sourceSQL != replicaSQL {
		return true, nil
	}
	sourceObjects, err := tableObjects(ctx, source, table)
	if err != nil {
		return false, err
	}
	replicaObjects, err := tableObjects(ctx, replica, table)
	if err != nil {
		return false, err
	}
	if !reflect.DeepEqual(sourceObjects, replicaObjects) {
		return true, nil
	}

	columns, err := tableColumns(ctx, source, table)
	if err != nil {
		return false, err
	}
	equal, err := tableRowsEqual(ctx, source, replica, table, columns)
	return !equal, err
}

func tableSQL(ctx context.Context, db queryer, table string) (string, error) {
	var value string
	if err := db.QueryRowContext(
		ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?",
		table,
	).Scan(&value); err != nil {
		return "", err
	}
	return value, nil
}

func tableObjects(ctx context.Context, db queryer, table string) ([]string, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT sql FROM sqlite_master
		 WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL
		 ORDER BY type, name`,
		table,
	)
	if err != nil {
		return nil, fmt.Errorf("list table schema objects: %w", err)
	}
	defer rows.Close()
	var objects []string
	for rows.Next() {
		var objectSQL string
		if err := rows.Scan(&objectSQL); err != nil {
			return nil, err
		}
		objects = append(objects, objectSQL)
	}
	return objects, rows.Err()
}

func tableColumns(ctx context.Context, db queryer, table string) ([]string, error) {
	rows, err := db.QueryContext(
		ctx,
		"SELECT name FROM pragma_table_xinfo(?) WHERE hidden = 0 ORDER BY cid",
		table,
	)
	if err != nil {
		return nil, fmt.Errorf("list table columns: %w", err)
	}
	defer rows.Close()
	var columns []string
	for rows.Next() {
		var column string
		if err := rows.Scan(&column); err != nil {
			return nil, err
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func tableRowsEqual(
	ctx context.Context,
	source, replica queryer,
	table string,
	columns []string,
) (bool, error) {
	if len(columns) == 0 {
		return true, nil
	}
	quotedColumns := make([]string, len(columns))
	for i, column := range columns {
		quotedColumns[i] = quoteIdentifier(column)
	}
	columnList := strings.Join(quotedColumns, ", ")
	query := "SELECT " + columnList + " FROM " + quoteIdentifier(table) +
		" ORDER BY " + columnList
	sourceRows, err := source.QueryContext(ctx, query)
	if err != nil {
		return false, err
	}
	defer sourceRows.Close()
	replicaRows, err := replica.QueryContext(ctx, query)
	if err != nil {
		return false, err
	}
	defer replicaRows.Close()

	for {
		sourceNext := sourceRows.Next()
		replicaNext := replicaRows.Next()
		if sourceNext != replicaNext {
			return false, nil
		}
		if !sourceNext {
			return true, firstError(sourceRows.Err(), replicaRows.Err())
		}
		sourceValues, err := scanValues(sourceRows, len(columns))
		if err != nil {
			return false, err
		}
		replicaValues, err := scanValues(replicaRows, len(columns))
		if err != nil {
			return false, err
		}
		if !reflect.DeepEqual(sourceValues, replicaValues) {
			return false, nil
		}
	}
}

func scanValues(rows *sql.Rows, count int) ([]interface{}, error) {
	values := make([]interface{}, count)
	destinations := make([]interface{}, count)
	for i := range values {
		destinations[i] = &values[i]
	}
	if err := rows.Scan(destinations...); err != nil {
		return nil, err
	}
	return values, nil
}

func firstError(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
	return nil
}

// InitialSync creates a replica from scratch using VACUUM INTO, then
// returns the list of tables in the source for schema tracking.
func InitialSync(sourcePath, replicaPath string) ([]string, error) {
	lock := replicaLock(replicaPath)
	lock.Lock()
	defer lock.Unlock()

	if _, err := CreateSnapshot(sourcePath, replicaPath); err != nil {
		return nil, fmt.Errorf("initial snapshot failed: %w", err)
	}

	srcDB, err := sql.Open("sqlite", sourcePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, fmt.Errorf("failed to open source: %w", err)
	}
	defer srcDB.Close()
	srcDB.SetMaxOpenConns(1)

	tables, err := listUserTables(context.Background(), srcDB)
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}
	return tables, nil
}

// OpenReadOnly opens a SQLite database in read-only mode without modifying
// journal settings. Used for source databases we don't own.
func OpenReadOnly(path string) (*sql.DB, error) {
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, fmt.Errorf("resolve read-only path: %w", err)
	}
	uriPath := filepath.ToSlash(absolutePath)
	if filepath.VolumeName(absolutePath) != "" && !strings.HasPrefix(uriPath, "/") {
		uriPath = "/" + uriPath
	}
	dsn := (&url.URL{Scheme: "file", Path: uriPath}).String() +
		"?mode=ro&_pragma=busy_timeout(5000)&_pragma=query_only(1)"

	db, err := sql.Open("sqlite", dsn)
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

func listUserTables(ctx context.Context, db queryer) ([]string, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT name FROM pragma_table_list
		 WHERE schema = 'main' AND type = 'table' AND name NOT LIKE 'sqlite_%'
		 ORDER BY name`,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		tables = append(tables, name)
	}
	return tables, rows.Err()
}
