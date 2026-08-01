package service

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

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

type InsertRowRequest struct {
	Columns []string
	Values  []*string
}

type EditColumnRequest struct {
	OriginalName string
	Name         string
	Type         string
	Nullable     bool
	DefaultValue *string
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
		key := sqliteIdentifierKey(column.Name)
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

func (s *DatabaseService) EditColumn(
	ctx context.Context,
	dbName, tableName string,
	req EditColumnRequest,
) (resultErr error) {
	if err := validateExistingIdentifier("table", tableName); err != nil {
		return err
	}
	if err := validateExistingIdentifier("column", req.OriginalName); err != nil {
		return err
	}
	if err := validateIdentifier("column", req.Name); err != nil {
		return err
	}
	columnType := strings.ToUpper(strings.TrimSpace(req.Type))
	if _, ok := sqliteColumnTypes[columnType]; !ok {
		return fmt.Errorf("unsupported column type %q", req.Type)
	}

	entry, err := s.manager.Get(dbName)
	if err != nil {
		return err
	}
	conn, err := entry.DB.Conn(ctx)
	if err != nil {
		return fmt.Errorf("edit column: acquire database connection: %w", err)
	}
	defer conn.Close()

	columns, err := loadEditableColumns(ctx, conn, tableName)
	if err != nil {
		return fmt.Errorf("edit column: read table schema: %w", err)
	}
	if len(columns) == 0 {
		return fmt.Errorf("table %q does not exist", tableName)
	}
	targetIndex := -1
	primaryKeys := 0
	for index, column := range columns {
		if column.Hidden != 0 {
			return fmt.Errorf("editing tables with generated or hidden columns is not supported")
		}
		if column.PrimaryKey {
			primaryKeys++
		}
		if sqliteIdentifierKey(column.Name) == sqliteIdentifierKey(req.OriginalName) {
			targetIndex = index
		}
		if index != targetIndex &&
			sqliteIdentifierKey(column.Name) == sqliteIdentifierKey(req.Name) &&
			sqliteIdentifierKey(column.Name) != sqliteIdentifierKey(req.OriginalName) {
			return fmt.Errorf("column %q already exists", req.Name)
		}
	}
	if targetIndex < 0 {
		return fmt.Errorf("column %q does not exist in table %q", req.OriginalName, tableName)
	}
	target := columns[targetIndex]
	if primaryKeys > 1 {
		return fmt.Errorf("editing tables with composite primary keys is not supported")
	}
	if target.PrimaryKey {
		if req.Nullable {
			return fmt.Errorf("primary key columns cannot be nullable")
		}
		if !strings.EqualFold(target.Type, columnType) {
			return fmt.Errorf("primary key column types cannot be changed")
		}
	}
	if err := validateEditableTable(ctx, conn, tableName); err != nil {
		return err
	}
	if !req.Nullable && target.Nullable {
		var nullCount int64
		err := conn.QueryRowContext(
			ctx,
			"SELECT COUNT(*) FROM "+quoteIdentifier(tableName)+
				" WHERE "+quoteIdentifier(target.Name)+" IS NULL",
		).Scan(&nullCount)
		if err != nil {
			return fmt.Errorf("edit column: count NULL values: %w", err)
		}
		if nullCount > 0 && req.DefaultValue == nil {
			return fmt.Errorf(
				"column %q contains NULL values; provide a default before making it NOT NULL",
				target.Name,
			)
		}
	}

	var foreignKeys int
	if err := conn.QueryRowContext(ctx, "PRAGMA foreign_keys").Scan(&foreignKeys); err != nil {
		return fmt.Errorf("edit column: read foreign key mode: %w", err)
	}
	if foreignKeys != 0 {
		if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = OFF"); err != nil {
			return fmt.Errorf("edit column: disable foreign keys: %w", err)
		}
		defer func() {
			if _, err := conn.ExecContext(context.Background(), "PRAGMA foreign_keys = ON"); err != nil && resultErr == nil {
				resultErr = fmt.Errorf("edit column: restore foreign key mode: %w", err)
			}
		}()
	}

	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("edit column: begin transaction: %w", err)
	}
	defer tx.Rollback()

	currentName := target.Name
	if sqliteIdentifierKey(target.Name) != sqliteIdentifierKey(req.Name) || target.Name != req.Name {
		statement := fmt.Sprintf(
			"ALTER TABLE %s RENAME COLUMN %s TO %s",
			quoteIdentifier(tableName),
			quoteIdentifier(target.Name),
			quoteIdentifier(req.Name),
		)
		if _, err := tx.ExecContext(ctx, statement); err != nil {
			return fmt.Errorf("edit column: rename field: %w", err)
		}
		currentName = req.Name
	}

	columns, err = loadEditableColumns(ctx, tx, tableName)
	if err != nil {
		return fmt.Errorf("edit column: reload table schema: %w", err)
	}
	objects, err := loadTableSchemaObjects(ctx, tx, tableName)
	if err != nil {
		return fmt.Errorf("edit column: read indexes and triggers: %w", err)
	}

	definitions := make([]string, 0, len(columns))
	columnNames := make([]string, 0, len(columns))
	selectExpressions := make([]string, 0, len(columns))
	var copyArgs []interface{}
	for _, column := range columns {
		columnNames = append(columnNames, quoteIdentifier(column.Name))
		if sqliteIdentifierKey(column.Name) == sqliteIdentifierKey(currentName) {
			definitions = append(definitions, buildEditedColumnDefinition(
				req.Name,
				columnType,
				!req.Nullable,
				column.PrimaryKey,
				req.DefaultValue,
				true,
			))
			expression := quoteIdentifier(column.Name)
			if !req.Nullable && req.DefaultValue != nil {
				expression = "COALESCE(" + expression + ", ?)"
				copyArgs = append(copyArgs, *req.DefaultValue)
			}
			selectExpressions = append(selectExpressions, expression)
			continue
		}
		definitions = append(definitions, buildEditedColumnDefinition(
			column.Name,
			column.Type,
			!column.Nullable,
			column.PrimaryKey,
			column.DefaultValue,
			false,
		))
		selectExpressions = append(selectExpressions, quoteIdentifier(column.Name))
	}

	tempTable := fmt.Sprintf("__sqnsl_rebuild_%d", time.Now().UnixNano())
	if _, err := tx.ExecContext(
		ctx,
		"CREATE TABLE "+quoteIdentifier(tempTable)+" ("+strings.Join(definitions, ", ")+")",
	); err != nil {
		return fmt.Errorf("edit column: create replacement table: %w", err)
	}
	copyStatement := fmt.Sprintf(
		"INSERT INTO %s (%s) SELECT %s FROM %s",
		quoteIdentifier(tempTable),
		strings.Join(columnNames, ", "),
		strings.Join(selectExpressions, ", "),
		quoteIdentifier(tableName),
	)
	if _, err := tx.ExecContext(ctx, copyStatement, copyArgs...); err != nil {
		return fmt.Errorf("edit column: copy rows: %w", err)
	}

	var sourceCount, replacementCount int64
	if err := tx.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM "+quoteIdentifier(tableName),
	).Scan(&sourceCount); err != nil {
		return fmt.Errorf("edit column: count source rows: %w", err)
	}
	if err := tx.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM "+quoteIdentifier(tempTable),
	).Scan(&replacementCount); err != nil {
		return fmt.Errorf("edit column: count replacement rows: %w", err)
	}
	if sourceCount != replacementCount {
		return fmt.Errorf(
			"edit column: row count changed from %d to %d",
			sourceCount,
			replacementCount,
		)
	}

	if _, err := tx.ExecContext(ctx, "DROP TABLE "+quoteIdentifier(tableName)); err != nil {
		return fmt.Errorf("edit column: replace table: %w", err)
	}
	if _, err := tx.ExecContext(
		ctx,
		"ALTER TABLE "+quoteIdentifier(tempTable)+" RENAME TO "+quoteIdentifier(tableName),
	); err != nil {
		return fmt.Errorf("edit column: restore table name: %w", err)
	}
	for _, objectSQL := range objects {
		if _, err := tx.ExecContext(ctx, objectSQL); err != nil {
			return fmt.Errorf("edit column: recreate index or trigger: %w", err)
		}
	}
	rows, err := tx.QueryContext(ctx, "PRAGMA foreign_key_check")
	if err != nil {
		return fmt.Errorf("edit column: validate foreign keys: %w", err)
	}
	if rows.Next() {
		rows.Close()
		return fmt.Errorf("edit column: rebuild would violate a foreign key")
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return fmt.Errorf("edit column: check foreign keys: %w", err)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("edit column: close foreign key validation: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("edit column: commit rebuild: %w", err)
	}
	return nil
}

type editableColumn struct {
	Name         string
	Type         string
	Nullable     bool
	DefaultValue *string
	PrimaryKey   bool
	Hidden       int
}

type schemaQueryer interface {
	QueryContext(context.Context, string, ...interface{}) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...interface{}) *sql.Row
}

func loadEditableColumns(
	ctx context.Context,
	db schemaQueryer,
	tableName string,
) ([]editableColumn, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT name, type, "notnull", dflt_value, pk, hidden
		 FROM pragma_table_xinfo(?) ORDER BY cid`,
		tableName,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var columns []editableColumn
	for rows.Next() {
		var column editableColumn
		var notNull, primaryKey int
		var defaultValue sql.NullString
		if err := rows.Scan(
			&column.Name,
			&column.Type,
			&notNull,
			&defaultValue,
			&primaryKey,
			&column.Hidden,
		); err != nil {
			return nil, err
		}
		column.Nullable = notNull == 0 && primaryKey == 0
		column.PrimaryKey = primaryKey != 0
		if defaultValue.Valid {
			value := defaultValue.String
			column.DefaultValue = &value
		}
		columns = append(columns, column)
	}
	return columns, rows.Err()
}

func loadTableSchemaObjects(
	ctx context.Context,
	db schemaQueryer,
	tableName string,
) ([]string, error) {
	rows, err := db.QueryContext(
		ctx,
		`SELECT sql FROM sqlite_master
		 WHERE tbl_name = ? AND type IN ('index', 'trigger') AND sql IS NOT NULL
		 ORDER BY type, name`,
		tableName,
	)
	if err != nil {
		return nil, err
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

func validateEditableTable(ctx context.Context, db schemaQueryer, tableName string) error {
	var createSQL string
	if err := db.QueryRowContext(
		ctx,
		"SELECT sql FROM sqlite_master WHERE type = 'table' AND name = ?",
		tableName,
	).Scan(&createSQL); err != nil {
		return fmt.Errorf("edit column: read table definition: %w", err)
	}
	for _, keyword := range []string{"AUTOINCREMENT", "CHECK", "COLLATE", "GENERATED", "STRICT", "WITHOUT"} {
		if containsSQLKeyword(createSQL, keyword) {
			return fmt.Errorf("editing tables that use %s is not supported", keyword)
		}
	}

	var uniqueConstraints int
	if err := db.QueryRowContext(
		ctx,
		`SELECT COUNT(*) FROM pragma_index_list(?) WHERE origin = 'u'`,
		tableName,
	).Scan(&uniqueConstraints); err != nil {
		return fmt.Errorf("edit column: inspect UNIQUE constraints: %w", err)
	}
	if uniqueConstraints > 0 {
		return fmt.Errorf("editing tables with UNIQUE constraints is not supported")
	}
	var foreignKeys int
	if err := db.QueryRowContext(
		ctx,
		"SELECT COUNT(*) FROM pragma_foreign_key_list(?)",
		tableName,
	).Scan(&foreignKeys); err != nil {
		return fmt.Errorf("edit column: inspect foreign keys: %w", err)
	}
	if foreignKeys > 0 {
		return fmt.Errorf("editing tables with FOREIGN KEY constraints is not supported")
	}
	return nil
}

func buildEditedColumnDefinition(
	name, columnType string,
	notNull, primaryKey bool,
	defaultValue *string,
	defaultIsLiteral bool,
) string {
	parts := []string{quoteIdentifier(name), strings.ToUpper(strings.TrimSpace(columnType))}
	if primaryKey {
		parts = append(parts, "NOT NULL", "PRIMARY KEY")
	} else if notNull {
		parts = append(parts, "NOT NULL")
	}
	if defaultValue != nil {
		value := *defaultValue
		if defaultIsLiteral {
			value = quoteLiteral(value)
		}
		parts = append(parts, "DEFAULT", value)
	}
	return strings.Join(parts, " ")
}

func containsSQLKeyword(statement, keyword string) bool {
	keyword = strings.ToUpper(keyword)
	for index := 0; index < len(statement); {
		switch statement[index] {
		case '\'', '"', '`':
			quote := statement[index]
			index++
			for index < len(statement) {
				if statement[index] == quote {
					if index+1 < len(statement) && statement[index+1] == quote {
						index += 2
						continue
					}
					index++
					break
				}
				index++
			}
		case '[':
			index++
			for index < len(statement) && statement[index] != ']' {
				index++
			}
			if index < len(statement) {
				index++
			}
		default:
			if !isSQLWordCharacter(statement[index]) {
				index++
				continue
			}
			start := index
			for index < len(statement) && isSQLWordCharacter(statement[index]) {
				index++
			}
			if strings.ToUpper(statement[start:index]) == keyword {
				return true
			}
		}
	}
	return false
}

func isSQLWordCharacter(value byte) bool {
	return value == '_' ||
		value >= '0' && value <= '9' ||
		value >= 'A' && value <= 'Z' ||
		value >= 'a' && value <= 'z'
}

func (s *DatabaseService) InsertRow(
	ctx context.Context,
	dbName, tableName string,
	req InsertRowRequest,
) (*ExecResult, error) {
	if err := validateExistingIdentifier("table", tableName); err != nil {
		return nil, err
	}
	if strings.Contains(tableName, "/") {
		return nil, fmt.Errorf("table name cannot contain '/'")
	}
	if len(req.Columns) != len(req.Values) {
		return nil, fmt.Errorf("columns and values must have the same length")
	}

	entry, err := s.manager.Get(dbName)
	if err != nil {
		return nil, err
	}
	table, err := s.getTableInfo(entry.DB, tableName)
	if err != nil {
		return nil, fmt.Errorf("read table schema: %w", err)
	}
	if len(table.Columns) == 0 {
		return nil, fmt.Errorf("table %q does not exist", tableName)
	}

	knownColumns := make(map[string]string, len(table.Columns))
	for _, column := range table.Columns {
		knownColumns[sqliteIdentifierKey(column.Name)] = column.Name
	}

	quotedColumns := make([]string, 0, len(req.Columns))
	placeholders := make([]string, 0, len(req.Columns))
	args := make([]any, 0, len(req.Values))
	seen := make(map[string]struct{}, len(req.Columns))
	for index, requestedColumn := range req.Columns {
		if err := validateExistingIdentifier("column", requestedColumn); err != nil {
			return nil, err
		}
		key := sqliteIdentifierKey(requestedColumn)
		if _, exists := seen[key]; exists {
			return nil, fmt.Errorf("duplicate column %q", requestedColumn)
		}
		seen[key] = struct{}{}

		columnName, exists := knownColumns[key]
		if !exists {
			return nil, fmt.Errorf("column %q does not exist in table %q", requestedColumn, tableName)
		}
		quotedColumns = append(quotedColumns, quoteIdentifier(columnName))
		placeholders = append(placeholders, "?")
		if req.Values[index] == nil {
			args = append(args, nil)
		} else {
			args = append(args, *req.Values[index])
		}
	}

	statement := fmt.Sprintf("INSERT INTO %s DEFAULT VALUES", quoteIdentifier(tableName))
	if len(quotedColumns) > 0 {
		statement = fmt.Sprintf(
			"INSERT INTO %s (%s) VALUES (%s)",
			quoteIdentifier(tableName),
			strings.Join(quotedColumns, ", "),
			strings.Join(placeholders, ", "),
		)
	}
	result, err := entry.DB.ExecContext(ctx, statement, args...)
	if err != nil {
		return nil, fmt.Errorf("insert row failed: %w", err)
	}
	rowsAffected, _ := result.RowsAffected()
	lastID, _ := result.LastInsertId()
	return &ExecResult{RowsAffected: rowsAffected, LastInsertID: lastID}, nil
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
	pragmaRows, err := db.Query(
		`SELECT cid, name, type, "notnull", dflt_value, pk FROM pragma_table_info(?)`,
		tableName,
	)
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

	idxRows, err := db.Query(
		`SELECT seq, name, "unique", origin, partial FROM pragma_index_list(?)`,
		tableName,
	)
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

		colRows, err := db.Query(
			`SELECT seqno, cid, name FROM pragma_index_info(?)`,
			ri.name,
		)
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

func sqliteIdentifierKey(value string) string {
	var builder strings.Builder
	builder.Grow(len(value))
	for _, char := range value {
		if char >= 'A' && char <= 'Z' {
			char += 'a' - 'A'
		}
		builder.WriteRune(char)
	}
	return builder.String()
}
