package schema

import (
	"crypto/sha256"
	"database/sql"
	"fmt"
	"sort"
	"strings"
)

type SchemaObject struct {
	Type string // table, index, view, trigger
	Name string
	SQL  string
}

type ModifiedObject struct {
	Type   string
	Name   string
	OldSQL string
	NewSQL string
}

type SchemaDiff struct {
	Added    []SchemaObject
	Dropped  []SchemaObject
	Modified []ModifiedObject
}

// ExtractSchema reads all user-defined schema objects from a SQLite database
// and returns their SQL joined by ";\n", sorted alphabetically by name.
func ExtractSchema(db *sql.DB) (string, error) {
	rows, err := db.Query(
		`SELECT type, name, sql FROM sqlite_master
		 WHERE type IN ('table','index','view','trigger')
		   AND name NOT LIKE 'sqlite_%'
		   AND sql IS NOT NULL
		 ORDER BY name`,
	)
	if err != nil {
		return "", fmt.Errorf("querying sqlite_master: %w", err)
	}
	defer rows.Close()

	var stmts []string
	for rows.Next() {
		var objType, name, objSQL string
		if err := rows.Scan(&objType, &name, &objSQL); err != nil {
			return "", fmt.Errorf("scanning row: %w", err)
		}
		stmts = append(stmts, objSQL)
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("iterating rows: %w", err)
	}
	return strings.Join(stmts, ";\n"), nil
}

// NormalizeSchema normalizes a schema SQL string for consistent hashing:
// collapses whitespace, removes IF NOT EXISTS, trims and sorts statements.
func NormalizeSchema(rawSQL string) string {
	parts := strings.Split(rawSQL, ";")
	var normalized []string
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		// Collapse whitespace
		fields := strings.Fields(s)
		s = strings.Join(fields, " ")
		// Remove IF NOT EXISTS (case-insensitive)
		s = removeIfNotExists(s)
		normalized = append(normalized, s)
	}
	sort.Strings(normalized)
	return strings.Join(normalized, ";\n")
}

func removeIfNotExists(s string) string {
	lower := strings.ToLower(s)
	idx := strings.Index(lower, "if not exists")
	if idx < 0 {
		return s
	}
	return strings.TrimSpace(s[:idx]) + " " + strings.TrimSpace(s[idx+len("if not exists"):])
}

// HashSchema returns the SHA-256 hex digest of the given normalized schema SQL.
func HashSchema(normalizedSQL string) string {
	h := sha256.Sum256([]byte(normalizedSQL))
	return fmt.Sprintf("%x", h)
}

// DiffSchemas compares two schema SQL strings and returns the differences.
func DiffSchemas(oldSQL, newSQL string) (*SchemaDiff, error) {
	oldObjs := parseSchemaObjects(oldSQL)
	newObjs := parseSchemaObjects(newSQL)

	oldMap := make(map[string]SchemaObject, len(oldObjs))
	for _, o := range oldObjs {
		oldMap[o.Name] = o
	}
	newMap := make(map[string]SchemaObject, len(newObjs))
	for _, o := range newObjs {
		newMap[o.Name] = o
	}

	diff := &SchemaDiff{}

	for _, n := range newObjs {
		old, exists := oldMap[n.Name]
		if !exists {
			diff.Added = append(diff.Added, n)
		} else if normalizeForCompare(old.SQL) != normalizeForCompare(n.SQL) {
			diff.Modified = append(diff.Modified, ModifiedObject{
				Type:   n.Type,
				Name:   n.Name,
				OldSQL: old.SQL,
				NewSQL: n.SQL,
			})
		}
	}

	for _, o := range oldObjs {
		if _, exists := newMap[o.Name]; !exists {
			diff.Dropped = append(diff.Dropped, o)
		}
	}

	return diff, nil
}

func normalizeForCompare(s string) string {
	return strings.Join(strings.Fields(strings.TrimSpace(s)), " ")
}

func parseSchemaObjects(rawSQL string) []SchemaObject {
	parts := strings.Split(rawSQL, ";")
	var objs []SchemaObject
	for _, p := range parts {
		s := strings.TrimSpace(p)
		if s == "" {
			continue
		}
		obj := SchemaObject{SQL: s}
		lower := strings.ToLower(s)

		switch {
		case strings.HasPrefix(lower, "create table"):
			obj.Type = "table"
			obj.Name = extractName(s, "table")
		case strings.HasPrefix(lower, "create index"), strings.HasPrefix(lower, "create unique index"):
			obj.Type = "index"
			obj.Name = extractName(s, "index")
		case strings.HasPrefix(lower, "create view"):
			obj.Type = "view"
			obj.Name = extractName(s, "view")
		case strings.HasPrefix(lower, "create trigger"):
			obj.Type = "trigger"
			obj.Name = extractName(s, "trigger")
		default:
			obj.Type = "unknown"
			obj.Name = s
		}
		objs = append(objs, obj)
	}
	return objs
}

// extractName pulls the object name from a CREATE statement.
// It handles CREATE [UNIQUE] TYPE [IF NOT EXISTS] name ...
func extractName(stmt string, objType string) string {
	fields := strings.Fields(stmt)
	// Find the token matching objType (case-insensitive), then take the next
	// non-keyword token as the name.
	for i, f := range fields {
		if strings.EqualFold(f, objType) {
			for j := i + 1; j < len(fields); j++ {
				tok := fields[j]
				upper := strings.ToUpper(tok)
				if upper == "IF" || upper == "NOT" || upper == "EXISTS" {
					continue
				}
				// Strip surrounding quotes or backticks
				tok = strings.Trim(tok, "`\"[]")
				// Strip trailing parenthesis
				tok = strings.TrimRight(tok, "(")
				return tok
			}
		}
	}
	return ""
}

// Summary returns a human-readable description of the schema diff.
func (d *SchemaDiff) Summary() string {
	if len(d.Added) == 0 && len(d.Dropped) == 0 && len(d.Modified) == 0 {
		return "no changes"
	}

	var parts []string
	for _, a := range d.Added {
		parts = append(parts, fmt.Sprintf("added %s %s", a.Type, a.Name))
	}
	for _, dr := range d.Dropped {
		parts = append(parts, fmt.Sprintf("dropped %s %s", dr.Type, dr.Name))
	}
	for _, m := range d.Modified {
		detail := describeModification(m)
		parts = append(parts, fmt.Sprintf("modified %s %s (%s)", m.Type, m.Name, detail))
	}
	return strings.Join(parts, "; ")
}

// describeModification provides a brief description of what changed.
func describeModification(m ModifiedObject) string {
	if m.Type != "table" {
		return "definition changed"
	}

	oldCols := extractColumns(m.OldSQL)
	newCols := extractColumns(m.NewSQL)

	var changes []string
	for col := range newCols {
		if _, exists := oldCols[col]; !exists {
			changes = append(changes, "column added: "+col+" "+newCols[col])
		}
	}
	for col := range oldCols {
		if _, exists := newCols[col]; !exists {
			changes = append(changes, "column dropped: "+col)
		}
	}

	if len(changes) == 0 {
		return "definition changed"
	}
	sort.Strings(changes)
	return strings.Join(changes, ", ")
}

// extractColumns parses column definitions from a CREATE TABLE statement.
// Returns a map of column name → type string.
func extractColumns(createSQL string) map[string]string {
	cols := make(map[string]string)
	start := strings.Index(createSQL, "(")
	end := strings.LastIndex(createSQL, ")")
	if start < 0 || end <= start {
		return cols
	}
	body := createSQL[start+1 : end]

	// Split on commas, but we need to handle nested parentheses
	var defs []string
	depth := 0
	current := strings.Builder{}
	for _, ch := range body {
		switch ch {
		case '(':
			depth++
			current.WriteRune(ch)
		case ')':
			depth--
			current.WriteRune(ch)
		case ',':
			if depth == 0 {
				defs = append(defs, current.String())
				current.Reset()
			} else {
				current.WriteRune(ch)
			}
		default:
			current.WriteRune(ch)
		}
	}
	if current.Len() > 0 {
		defs = append(defs, current.String())
	}

	for _, def := range defs {
		def = strings.TrimSpace(def)
		upper := strings.ToUpper(def)
		// Skip constraints
		if strings.HasPrefix(upper, "PRIMARY") ||
			strings.HasPrefix(upper, "FOREIGN") ||
			strings.HasPrefix(upper, "UNIQUE") ||
			strings.HasPrefix(upper, "CHECK") ||
			strings.HasPrefix(upper, "CONSTRAINT") {
			continue
		}
		fields := strings.Fields(def)
		if len(fields) >= 2 {
			name := strings.Trim(fields[0], "`\"[]")
			colType := fields[1]
			cols[name] = colType
		} else if len(fields) == 1 {
			name := strings.Trim(fields[0], "`\"[]")
			cols[name] = ""
		}
	}
	return cols
}
