package service

import (
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"math"
	"net/url"
	"slices"
	"strconv"
	"strings"

	"modernc.org/sqlite"
)

var (
	ErrRecordInput    = errors.New("invalid record request")
	ErrRecordNotFound = errors.New("record not found")
	ErrRecordConflict = errors.New("record conflict")
	ErrRecordReadOnly = errors.New("table is read-only")
)

type RecordColumn struct {
	Name      string `json:"name"`
	Type      string `json:"type"`
	Nullable  bool   `json:"nullable"`
	Generated bool   `json:"generated"`
	sqlName   string
	keyOrder  int
}

type RecordTable struct {
	Name     string         `json:"name"`
	Columns  []RecordColumn `json:"columns"`
	Keys     []string       `json:"keys"`
	ReadOnly bool           `json:"read_only"`
}

type recordReader interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
	QueryRowContext(context.Context, string, ...any) *sql.Row
}

func inputError(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrRecordInput, fmt.Sprintf(format, args...))
}

func GetRecordTable(ctx context.Context, db recordReader, name string) (*RecordTable, error) {
	if err := validateExistingIdentifier("table", name); err != nil {
		return nil, inputError("%v", err)
	}
	if strings.HasPrefix(strings.ToLower(name), "sqlite_") {
		return nil, fmt.Errorf("%w: internal SQLite tables are not exposed", ErrRecordNotFound)
	}
	var actual, kind, ddl string
	err := db.QueryRowContext(ctx, `SELECT name,type,coalesce(sql,'') FROM sqlite_schema WHERE name=? COLLATE NOCASE AND type IN ('table','view')`, name).Scan(&actual, &kind, &ddl)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: table %q", ErrRecordNotFound, name)
	}
	if err != nil {
		return nil, fmt.Errorf("inspect table: %w", err)
	}
	table := &RecordTable{Name: actual, Columns: []RecordColumn{}, Keys: []string{}, ReadOnly: kind == "view" || strings.HasPrefix(strings.ToUpper(ddl), "CREATE VIRTUAL TABLE")}
	rows, err := db.QueryContext(ctx, "PRAGMA table_xinfo("+quoteIdentifier(actual)+")")
	if err != nil {
		return nil, fmt.Errorf("inspect columns: %w", err)
	}
	defer rows.Close()
	names := map[string]bool{}
	for rows.Next() {
		var cid, required, pk, hidden int
		var column RecordColumn
		var def sql.NullString
		if err := rows.Scan(&cid, &column.Name, &column.Type, &required, &def, &pk, &hidden); err != nil {
			return nil, fmt.Errorf("inspect column: %w", err)
		}
		names[strings.ToLower(column.Name)] = true
		if hidden == 1 {
			continue
		}
		column.Nullable = required == 0 && pk == 0
		column.Generated = hidden > 1
		column.sqlName = column.Name
		column.keyOrder = pk
		table.Columns = append(table.Columns, column)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("read columns: %w", err)
	}
	keys := slices.Clone(table.Columns)
	slices.SortFunc(keys, func(a, b RecordColumn) int { return a.keyOrder - b.keyOrder })
	for _, column := range keys {
		if column.keyOrder > 0 {
			table.Keys = append(table.Keys, column.Name)
		}
	}
	if len(table.Keys) == 0 && kind == "table" && !strings.Contains(strings.ToUpper(ddl), "WITHOUT ROWID") {
		for _, alias := range []string{"rowid", "_rowid_", "oid"} {
			if names[alias] {
				continue
			}
			key := "__sqnsl_rowid"
			for names[strings.ToLower(key)] {
				key += "_"
			}
			table.Columns = append(table.Columns, RecordColumn{Name: key, Type: "INTEGER", Generated: true, sqlName: alias})
			table.Keys = append(table.Keys, key)
			break
		}
	}
	return table, nil
}

func recordColumn(table *RecordTable, name string) (RecordColumn, error) {
	for _, column := range table.Columns {
		if strings.EqualFold(column.Name, name) {
			return column, nil
		}
	}
	return RecordColumn{}, inputError("unknown field %q", name)
}

func projection(table *RecordTable, names []string) (string, error) {
	if names == nil {
		for _, column := range table.Columns {
			names = append(names, column.Name)
		}
	}
	parts := []string{}
	seen := map[string]bool{}
	for _, name := range names {
		column, err := recordColumn(table, strings.TrimSpace(name))
		if err != nil {
			return "", err
		}
		if seen[strings.ToLower(column.Name)] {
			return "", inputError("duplicate selected field %q", name)
		}
		seen[strings.ToLower(column.Name)] = true
		parts = append(parts, quoteIdentifier(column.sqlName)+" AS "+quoteIdentifier(column.Name))
	}
	if len(parts) == 0 {
		return "", inputError("select at least one field")
	}
	return strings.Join(parts, ","), nil
}

func nativeRecordValue(column RecordColumn, value any) (any, error) {
	switch typed := value.(type) {
	case nil, bool, int64, float64:
		return value, nil
	case json.Number:
		if integer, err := typed.Int64(); err == nil {
			return integer, nil
		}
		number, err := typed.Float64()
		if err != nil || math.IsInf(number, 0) || math.IsNaN(number) {
			return nil, inputError("invalid number for %q", column.Name)
		}
		if strings.Contains(strings.ToUpper(column.Type), "INT") {
			return nil, inputError("integer outside int64 range for %q", column.Name)
		}
		return number, nil
	case string:
		if strings.EqualFold(column.Type, "BLOB") {
			data, err := base64.StdEncoding.DecodeString(typed)
			if err != nil {
				return nil, inputError("field %q requires a base64 string", column.Name)
			}
			return data, nil
		}
		return typed, nil
	default:
		return nil, inputError("field %q requires a scalar JSON value", column.Name)
	}
}

func filterValue(column RecordColumn, raw string) (any, error) {
	kind := strings.ToUpper(column.Type)
	if strings.Contains(kind, "INT") {
		value, err := strconv.ParseInt(raw, 10, 64)
		if err != nil {
			return nil, inputError("field %q requires an integer", column.Name)
		}
		return value, nil
	}
	if strings.Contains(kind, "REAL") || strings.Contains(kind, "FLOA") || strings.Contains(kind, "DOUB") || strings.Contains(kind, "NUMERIC") {
		value, err := strconv.ParseFloat(raw, 64)
		if err != nil || math.IsNaN(value) || math.IsInf(value, 0) {
			return nil, inputError("field %q requires a finite number", column.Name)
		}
		return value, nil
	}
	if kind == "BOOLEAN" {
		switch raw {
		case "true", "1":
			return int64(1), nil
		case "false", "0":
			return int64(0), nil
		default:
			return nil, inputError("field %q requires true or false", column.Name)
		}
	}
	return nativeRecordValue(column, raw)
}

func recordWhere(table *RecordTable, params url.Values) (string, []any, error) {
	keys := make([]string, 0, len(params))
	for key := range params {
		keys = append(keys, key)
	}
	slices.Sort(keys)
	clauses := []string{}
	args := []any{}
	for _, key := range keys {
		if key == "select" || key == "order" || key == "limit" || key == "offset" {
			continue
		}
		column, err := recordColumn(table, strings.TrimPrefix(key, "filter."))
		if err != nil {
			return "", nil, err
		}
		for _, raw := range params[key] {
			if len(clauses) >= 50 {
				return "", nil, inputError("at most 50 filters are allowed")
			}
			operator, operand, ok := strings.Cut(raw, ".")
			if !ok {
				return "", nil, inputError("filter %q requires an operator, for example eq.value", key)
			}
			field := quoteIdentifier(column.sqlName)
			if operator == "is" {
				switch operand {
				case "null":
					clauses = append(clauses, field+" IS NULL")
				case "notnull":
					clauses = append(clauses, field+" IS NOT NULL")
				default:
					return "", nil, inputError("is filter requires null or notnull")
				}
				continue
			}
			if operator == "in" {
				if !strings.HasPrefix(operand, "(") || !strings.HasSuffix(operand, ")") {
					return "", nil, inputError("in filter requires a parenthesized list")
				}
				values, err := csv.NewReader(strings.NewReader(operand[1 : len(operand)-1])).Read()
				if err != nil || len(values) == 0 || len(values) > 100 {
					return "", nil, inputError("in filter requires between 1 and 100 values")
				}
				binds := []string{}
				for _, rawValue := range values {
					value, err := filterValue(column, rawValue)
					if err != nil {
						return "", nil, err
					}
					args = append(args, value)
					binds = append(binds, "?")
				}
				clauses = append(clauses, field+" IN ("+strings.Join(binds, ",")+")")
				continue
			}
			sqlOperator, ok := map[string]string{"eq": "=", "neq": "<>", "gt": ">", "gte": ">=", "lt": "<", "lte": "<=", "like": "LIKE"}[operator]
			if !ok {
				return "", nil, inputError("unsupported filter operator %q", operator)
			}
			var value any
			if operator == "like" {
				value = operand
			} else {
				value, err = filterValue(column, operand)
				if err != nil {
					return "", nil, err
				}
			}
			clauses = append(clauses, field+" "+sqlOperator+" ?")
			args = append(args, value)
		}
	}
	if len(clauses) == 0 {
		return "", args, nil
	}
	return " WHERE " + strings.Join(clauses, " AND "), args, nil
}

func recordRows(ctx context.Context, db recordReader, statement string, args []any) ([]map[string]any, error) {
	rows, err := db.QueryContext(ctx, statement, args...)
	if err != nil {
		return nil, recordSQLError(err)
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, fmt.Errorf("read column names: %w", err)
	}
	result := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		ptrs := make([]any, len(columns))
		for index := range values {
			ptrs[index] = &values[index]
		}
		if err := rows.Scan(ptrs...); err != nil {
			return nil, fmt.Errorf("read record: %w", err)
		}
		record := map[string]any{}
		for index, name := range columns {
			if number, ok := values[index].(float64); ok && (math.IsInf(number, 0) || math.IsNaN(number)) {
				return nil, fmt.Errorf("%w: field %q contains a non-finite number that JSON cannot represent", ErrRecordConflict, name)
			}
			if data, ok := values[index].([]byte); ok {
				record[name] = base64.StdEncoding.EncodeToString(data)
			} else {
				record[name] = values[index]
			}
		}
		result = append(result, record)
	}
	if err := rows.Err(); err != nil {
		return nil, recordSQLError(err)
	}
	return result, nil
}

func recordSQLError(err error) error {
	var sqliteErr *sqlite.Error
	if errors.As(err, &sqliteErr) && sqliteErr.Code()&255 == 19 {
		return fmt.Errorf("%w: %v", ErrRecordConflict, err)
	}
	return fmt.Errorf("SQLite record operation: %w", err)
}

func ListRecords(ctx context.Context, db *sql.DB, name string, params url.Values) ([]map[string]any, error) {
	table, err := GetRecordTable(ctx, db, name)
	if err != nil {
		return nil, err
	}
	limit, offset := 100, 0
	for key, target := range map[string]*int{"limit": &limit, "offset": &offset} {
		if values, ok := params[key]; ok {
			if len(values) != 1 {
				return nil, inputError("%s must appear once", key)
			}
			value, err := strconv.Atoi(values[0])
			if err != nil || value < 0 || (key == "limit" && (value < 1 || value > 1000)) || (key == "offset" && value > 1_000_000_000) {
				return nil, inputError("invalid %s", key)
			}
			*target = value
		}
	}
	var selected []string
	if values, ok := params["select"]; ok {
		if len(values) != 1 {
			return nil, inputError("select must appear once")
		}
		selected = strings.Split(values[0], ",")
	}
	columns, err := projection(table, selected)
	if err != nil {
		return nil, err
	}
	where, args, err := recordWhere(table, params)
	if err != nil {
		return nil, err
	}
	order := []string{}
	if values, ok := params["order"]; ok {
		if len(values) != 1 {
			return nil, inputError("order must appear once")
		}
		for _, term := range strings.Split(values[0], ",") {
			parts := strings.Split(term, ".")
			if len(parts) > 2 {
				return nil, inputError("invalid order term %q", term)
			}
			column, err := recordColumn(table, parts[0])
			if err != nil {
				return nil, err
			}
			direction := "ASC"
			if len(parts) == 2 {
				switch parts[1] {
				case "asc":
				case "desc":
					direction = "DESC"
				default:
					return nil, inputError("invalid order direction")
				}
			}
			order = append(order, quoteIdentifier(column.sqlName)+" "+direction)
		}
	}
	for _, key := range table.Keys {
		column, _ := recordColumn(table, key)
		order = append(order, quoteIdentifier(column.sqlName)+" ASC")
	}
	orderSQL := ""
	if len(order) > 0 {
		orderSQL = " ORDER BY " + strings.Join(order, ",")
	}
	args = append(args, limit, offset)
	return recordRows(ctx, db, "SELECT "+columns+" FROM "+quoteIdentifier(table.Name)+where+orderSQL+" LIMIT ? OFFSET ?", args)
}

func MutateRecord(ctx context.Context, db *sql.DB, name, method string, values map[string]any, params url.Values) (map[string]any, error) {
	if method != "POST" && method != "PATCH" && method != "DELETE" {
		return nil, inputError("unsupported record operation")
	}
	for _, key := range []string{"select", "order", "limit", "offset"} {
		if _, ok := params[key]; ok {
			return nil, inputError("%s is only supported for reads", key)
		}
	}
	if method == "POST" && len(params) > 0 {
		return nil, inputError("insert does not accept filters")
	}
	if method == "PATCH" && len(values) == 0 {
		return nil, inputError("provide at least one field to update")
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return nil, recordSQLError(err)
	}
	defer func() {
		if err := tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
			log.Printf("rollback record operation: %v", err)
		}
	}()
	table, err := GetRecordTable(ctx, tx, name)
	if err != nil {
		return nil, err
	}
	if table.ReadOnly {
		return nil, ErrRecordReadOnly
	}
	columns, err := projection(table, nil)
	if err != nil {
		return nil, err
	}
	where, filters, err := recordWhere(table, params)
	if err != nil {
		return nil, err
	}
	if method != "POST" {
		if where == "" {
			return nil, inputError("update and delete require a filter")
		}
		matches, err := recordRows(ctx, tx, "SELECT "+columns+" FROM "+quoteIdentifier(table.Name)+where+" LIMIT 2", filters)
		if err != nil {
			return nil, err
		}
		if len(matches) == 0 {
			return nil, ErrRecordNotFound
		}
		if len(matches) > 1 {
			return nil, fmt.Errorf("%w: filter matched multiple rows; use a unique key", ErrRecordConflict)
		}
	}
	names := make([]string, 0, len(values))
	for name := range values {
		names = append(names, name)
	}
	slices.Sort(names)
	fieldSQL, binds, args := []string{}, []string{}, []any{}
	seen := map[string]bool{}
	for _, name := range names {
		column, err := recordColumn(table, name)
		if err != nil {
			return nil, err
		}
		if column.Generated {
			return nil, inputError("field %q is generated", name)
		}
		key := strings.ToLower(column.Name)
		if seen[key] {
			return nil, inputError("duplicate field %q", name)
		}
		seen[key] = true
		value, err := nativeRecordValue(column, values[name])
		if err != nil {
			return nil, err
		}
		fieldSQL = append(fieldSQL, quoteIdentifier(column.sqlName))
		binds = append(binds, "?")
		args = append(args, value)
	}
	target := quoteIdentifier(table.Name)
	var statement string
	switch method {
	case "POST":
		statement = "INSERT INTO " + target + " DEFAULT VALUES"
		if len(names) > 0 {
			statement = "INSERT INTO " + target + " (" + strings.Join(fieldSQL, ",") + ") VALUES (" + strings.Join(binds, ",") + ")"
		}
	case "PATCH":
		for index := range fieldSQL {
			fieldSQL[index] += "=?"
		}
		statement = "UPDATE " + target + " SET " + strings.Join(fieldSQL, ",") + where
		args = append(args, filters...)
	case "DELETE":
		statement = "DELETE FROM " + target + where
		args = filters
	}
	result, err := recordRows(ctx, tx, statement+" RETURNING "+columns, args)
	if err != nil {
		return nil, err
	}
	if len(result) == 0 {
		return nil, ErrRecordNotFound
	}
	if len(result) != 1 {
		return nil, fmt.Errorf("%w: operation affected multiple rows", ErrRecordConflict)
	}
	if err := tx.Commit(); err != nil {
		return nil, recordSQLError(err)
	}
	return result[0], nil
}
