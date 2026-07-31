package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/mbianchidev/sql-not-so-lite/internal/store"
)

type DatabaseService struct {
	manager *store.Manager
}

func NewDatabaseService(manager *store.Manager) *DatabaseService {
	return &DatabaseService{manager: manager}
}

type DBInfo struct {
	Name       string
	Path       string
	SizeBytes  int64
	Active     bool
	TableCount int64
}

type Column struct {
	Name string
	Type string
}

type QueryResult struct {
	Columns    []Column
	Rows       [][]string
	TotalCount int64
}

type ExecResult struct {
	RowsAffected int64
	LastInsertID int64
}

type TableInfo struct {
	Name     string
	Columns  []ColumnInfo
	Indexes  []IndexInfo
	RowCount int64
}

type ColumnInfo struct {
	Name         string
	Type         string
	Nullable     bool
	DefaultValue string
	PrimaryKey   bool
}

type IndexInfo struct {
	Name    string
	Columns []string
	Unique  bool
}

type ColumnDefinition struct {
	Name         string
	Type         string
	NotNull      bool
	PrimaryKey   bool
	DefaultValue *string
}

type CreateTableRequest struct {
	Name    string
	Columns []ColumnDefinition
}

var sqliteColumnTypes = map[string]struct{}{
	"BLOB":     {},
	"BOOLEAN":  {},
	"DATE":     {},
	"DATETIME": {},
	"INTEGER":  {},
	"NUMERIC":  {},
	"REAL":     {},
	"TEXT":     {},
}

func (s *DatabaseService) CreateDatabase(_ context.Context, name string) (*DBInfo, error) {
	entry, err := s.manager.Create(name)
	if err != nil {
		return nil, err
	}

	size, _ := s.manager.FileSize(name)
	return &DBInfo{
		Name:      entry.Name,
		Path:      entry.Path,
		SizeBytes: size,
		Active:    true,
	}, nil
}

func (s *DatabaseService) ListDatabases(_ context.Context) ([]DBInfo, error) {
	entries := s.manager.List()
	result := make([]DBInfo, 0, len(entries))

	for _, e := range entries {
		size, _ := s.manager.FileSize(e.Name)
		info := DBInfo{
			Name:      e.Name,
			Path:      e.Path,
			SizeBytes: size,
			Active:    e.DB != nil,
		}

		if e.DB != nil {
			count, err := s.countTables(e.DB)
			if err == nil {
				info.TableCount = count
			}
		}

		result = append(result, info)
	}

	return result, nil
}

func (s *DatabaseService) DropDatabase(_ context.Context, name string) error {
	return s.manager.Drop(name)
}

func (s *DatabaseService) GetDatabaseInfo(_ context.Context, name string) (*DBInfo, error) {
	entry, err := s.manager.Get(name)
	if err != nil {
		return nil, err
	}

	size, _ := s.manager.FileSize(name)
	count, _ := s.countTables(entry.DB)

	return &DBInfo{
		Name:       entry.Name,
		Path:       entry.Path,
		SizeBytes:  size,
		Active:     true,
		TableCount: count,
	}, nil
}

func (s *DatabaseService) Execute(_ context.Context, dbName, sqlStr string, params []string) (*ExecResult, error) {
	entry, err := s.manager.Get(dbName)
	if err != nil {
		return nil, err
	}

	args := stringsToInterfaces(params)
	result, err := entry.DB.Exec(sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("execute failed: %w", err)
	}

	rowsAffected, _ := result.RowsAffected()
	lastID, _ := result.LastInsertId()

	return &ExecResult{
		RowsAffected: rowsAffected,
		LastInsertID: lastID,
	}, nil
}

func (s *DatabaseService) CreateTable(_ context.Context, dbName string, req CreateTableRequest) error {
	if err := validateIdentifier("table", req.Name); err != nil {
		return err
	}
	if strings.Contains(req.Name, "/") {
		return fmt.Errorf("table name cannot contain '/'")
	}
	if len(req.Columns) == 0 {
		return fmt.Errorf("table must have at least one column")
	}

	primaryKeys := 0
	columnSQL := make([]string, 0, len(req.Columns))
	seen := make(map[string]struct{}, len(req.Columns))
	for _, column := range req.Columns {
		key := strings.ToLower(column.Name)
		if _, exists := seen[key]; exists {
			return fmt.Errorf("duplicate column %q", column.Name)
		}
		seen[key] = struct{}{}

		if column.PrimaryKey {
			primaryKeys++
		}
		definition, err := buildColumnDefinition(column, false)
		if err != nil {
			return err
		}
		columnSQL = append(columnSQL, definition)
	}
	if primaryKeys > 1 {
		return fmt.Errorf("table can have at most one primary key column")
	}

	entry, err := s.manager.Get(dbName)
	if err != nil {
		return err
	}
	statement := fmt.Sprintf("CREATE TABLE %s (%s)", quoteIdentifier(req.Name), strings.Join(columnSQL, ", "))
	if _, err := entry.DB.Exec(statement); err != nil {
		return fmt.Errorf("create table failed: %w", err)
	}
	return nil
}

func (s *DatabaseService) AddColumn(_ context.Context, dbName, tableName string, column ColumnDefinition) error {
	if err := validateExistingIdentifier("table", tableName); err != nil {
		return err
	}
	if strings.Contains(tableName, "/") {
		return fmt.Errorf("table name cannot contain '/'")
	}
	if column.PrimaryKey {
		return fmt.Errorf("primary key columns cannot be added to an existing table")
	}
	if column.NotNull && column.DefaultValue == nil {
		return fmt.Errorf("a default value is required for a NOT NULL column")
	}

	definition, err := buildColumnDefinition(column, true)
	if err != nil {
		return err
	}
	entry, err := s.manager.Get(dbName)
	if err != nil {
		return err
	}
	statement := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s", quoteIdentifier(tableName), definition)
	if _, err := entry.DB.Exec(statement); err != nil {
		return fmt.Errorf("add column failed: %w", err)
	}
	return nil
}

func (s *DatabaseService) Query(_ context.Context, dbName, sqlStr string, params []string, limit, offset int) (*QueryResult, error) {
	entry, err := s.manager.Get(dbName)
	if err != nil {
		return nil, err
	}

	if limit <= 0 {
		limit = 1000
	}
	if limit > 100000 {
		limit = 100000
	}

	args := stringsToInterfaces(params)
	rows, err := entry.DB.Query(sqlStr, args...)
	if err != nil {
		return nil, fmt.Errorf("query failed: %w", err)
	}
	defer rows.Close()

	colNames, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("failed to get columns: %w", err)
	}

	colTypes, _ := rows.ColumnTypes()
	columns := make([]Column, len(colNames))
	for i, name := range colNames {
		typeName := "TEXT"
		if colTypes != nil && i < len(colTypes) {
			typeName = colTypes[i].DatabaseTypeName()
			if typeName == "" {
				typeName = "TEXT"
			}
		}
		columns[i] = Column{Name: name, Type: typeName}
	}

	var resultRows [][]string
	skipped := 0
	for rows.Next() {
		if skipped < offset {
			skipped++
			// still need to scan to advance the cursor
			vals := make([]interface{}, len(colNames))
			ptrs := make([]interface{}, len(colNames))
			for i := range vals {
				ptrs[i] = &vals[i]
			}
			rows.Scan(ptrs...)
			continue
		}

		if len(resultRows) >= limit {
			break
		}

		vals := make([]interface{}, len(colNames))
		ptrs := make([]interface{}, len(colNames))
		for i := range vals {
			ptrs[i] = &vals[i]
		}

		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}

		row := make([]string, len(colNames))
		for i, v := range vals {
			if v == nil {
				row[i] = "NULL"
			} else {
				row[i] = fmt.Sprintf("%v", v)
			}
		}
		resultRows = append(resultRows, row)
	}

	return &QueryResult{
		Columns:    columns,
		Rows:       resultRows,
		TotalCount: int64(len(resultRows)),
	}, nil
}

func (s *DatabaseService) GetSchema(_ context.Context, dbName string) ([]TableInfo, error) {
	entry, err := s.manager.Get(dbName)
	if err != nil {
		return nil, err
	}

	// Collect table names first, then close rows before querying each table
	rows, err := entry.DB.Query("SELECT name FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%' ORDER BY name")
	if err != nil {
		return nil, fmt.Errorf("failed to list tables: %w", err)
	}

	var tableNames []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			continue
		}
		tableNames = append(tableNames, name)
	}
	rows.Close()

	var tables []TableInfo
	for _, name := range tableNames {
		tableInfo, err := s.getTableInfo(entry.DB, name)
		if err != nil {
			continue
		}
		tables = append(tables, *tableInfo)
	}

	return tables, nil
}

func (s *DatabaseService) getTableInfo(db *sql.DB, tableName string) (*TableInfo, error) {
	info := &TableInfo{Name: tableName}

	// Get columns — collect and close before next query (single-conn DB)
	pragmaRows, err := db.Query(fmt.Sprintf("PRAGMA table_info('%s')", tableName))
	if err != nil {
		return nil, err
	}
	for pragmaRows.Next() {
		var cid int
		var name, colType string
		var notNull int
		var defaultVal sql.NullString
		var pk int

		if err := pragmaRows.Scan(&cid, &name, &colType, &notNull, &defaultVal, &pk); err != nil {
			continue
		}

		info.Columns = append(info.Columns, ColumnInfo{
			Name:         name,
			Type:         colType,
			Nullable:     notNull == 0,
			DefaultValue: defaultVal.String,
			PrimaryKey:   pk > 0,
		})
	}
	pragmaRows.Close()

	// Get indexes — collect index names first, then query columns
	type rawIdx struct {
		name   string
		unique bool
	}
	var rawIndexes []rawIdx

	idxRows, err := db.Query(fmt.Sprintf("PRAGMA index_list('%s')", tableName))
	if err == nil {
		for idxRows.Next() {
			var seq int
			var name string
			var unique int
			var origin, partial string

			if err := idxRows.Scan(&seq, &name, &unique, &origin, &partial); err != nil {
				continue
			}
			rawIndexes = append(rawIndexes, rawIdx{name: name, unique: unique == 1})
		}
		idxRows.Close()
	}

	for _, ri := range rawIndexes {
		idx := IndexInfo{Name: ri.name, Unique: ri.unique}

		colRows, err := db.Query(fmt.Sprintf("PRAGMA index_info('%s')", ri.name))
		if err == nil {
			for colRows.Next() {
				var seqno, cid int
				var colName string
				if err := colRows.Scan(&seqno, &cid, &colName); err != nil {
					continue
				}
				idx.Columns = append(idx.Columns, colName)
			}
			colRows.Close()
		}
		info.Indexes = append(info.Indexes, idx)
	}

	// Get row count
	var count int64
	err = db.QueryRow(fmt.Sprintf("SELECT COUNT(*) FROM \"%s\"", strings.ReplaceAll(tableName, "\"", "\"\""))).Scan(&count)
	if err == nil {
		info.RowCount = count
	}

	return info, nil
}

func (s *DatabaseService) countTables(db *sql.DB) (int64, error) {
	var count int64
	err := db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE type='table' AND name NOT LIKE 'sqlite_%'").Scan(&count)
	return count, err
}

func (s *DatabaseService) ActiveCount() int {
	return s.manager.ActiveCount()
}

func stringsToInterfaces(strs []string) []interface{} {
	if len(strs) == 0 {
		return nil
	}
	result := make([]interface{}, len(strs))
	for i, s := range strs {
		result[i] = s
	}
	return result
}

func buildColumnDefinition(column ColumnDefinition, adding bool) (string, error) {
	if err := validateIdentifier("column", column.Name); err != nil {
		return "", err
	}
	columnType := strings.ToUpper(strings.TrimSpace(column.Type))
	if _, ok := sqliteColumnTypes[columnType]; !ok {
		return "", fmt.Errorf("unsupported column type %q", column.Type)
	}

	parts := []string{quoteIdentifier(column.Name), columnType}
	if column.PrimaryKey {
		parts = append(parts, "NOT NULL", "PRIMARY KEY")
	} else if column.NotNull {
		parts = append(parts, "NOT NULL")
	}
	if column.DefaultValue != nil {
		parts = append(parts, "DEFAULT", quoteLiteral(*column.DefaultValue))
	}
	if adding && column.PrimaryKey {
		return "", fmt.Errorf("primary key columns cannot be added to an existing table")
	}
	return strings.Join(parts, " "), nil
}

func validateIdentifier(kind, name string) error {
	if err := validateExistingIdentifier(kind, name); err != nil {
		return err
	}
	if strings.HasPrefix(strings.ToLower(name), "sqlite_") {
		return fmt.Errorf("%s name cannot use the reserved sqlite_ prefix", kind)
	}
	return nil
}

func validateExistingIdentifier(kind, name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("%s name cannot be empty", kind)
	}
	if strings.ContainsRune(name, '\x00') {
		return fmt.Errorf("%s name contains an invalid null character", kind)
	}
	return nil
}

func quoteIdentifier(name string) string {
	return `"` + strings.ReplaceAll(name, `"`, `""`) + `"`
}

func quoteLiteral(value string) string {
	return `'` + strings.ReplaceAll(value, `'`, `''`) + `'`
}
