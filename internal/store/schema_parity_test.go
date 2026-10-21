package store

import (
	"regexp"
	"sort"
	"strings"
	"testing"
)

// TestMigrationDialectsHaveSameShape compares the SQLite and PostgreSQL
// migration scripts without needing either database.
//
// The two files must describe the same tables, columns, foreign keys and
// indexes: every store method is written once and rebound per dialect, so a
// column that exists in only one of them is a runtime failure waiting to
// happen. Types are intentionally not compared (INTEGER vs BIGSERIAL, TEXT
// defaults and so on).
func TestMigrationDialectsHaveSameShape(t *testing.T) {
	scripts := map[string]string{}
	for _, dialect := range []string{"sqlite", "postgres"} {
		list, err := loadMigrations(dialect)
		if err != nil {
			t.Fatalf("load %s migrations: %v", dialect, err)
		}
		var b strings.Builder
		for _, m := range list {
			// Only CREATE TABLE / CREATE INDEX are understood here. When a
			// later migration starts using ALTER TABLE, extend this parser so
			// the comparison keeps covering the final shape.
			b.WriteString(m.body)
			b.WriteString("\n")
		}
		scripts[dialect] = b.String()
	}

	sqlite := parseSchema(t, scripts["sqlite"])
	postgres := parseSchema(t, scripts["postgres"])

	for _, table := range sortedKeys(sqlite.tables) {
		pgTable, ok := postgres.tables[table]
		if !ok {
			t.Errorf("table %q exists in sqlite but not postgres", table)
			continue
		}
		compareSet(t, "column", table, sqlite.tables[table].columns, pgTable.columns)
		compareSet(t, "foreign key target", table, sqlite.tables[table].refs, pgTable.refs)
	}
	for _, table := range sortedKeys(postgres.tables) {
		if _, ok := sqlite.tables[table]; !ok {
			t.Errorf("table %q exists in postgres but not sqlite", table)
		}
	}
	compareSet(t, "index", "", sqlite.indexes, postgres.indexes)
}

type tableShape struct {
	columns map[string]bool
	refs    map[string]bool
}

type schemaShape struct {
	tables  map[string]*tableShape
	indexes map[string]bool
}

var indexPattern = regexp.MustCompile(`(?i)create\s+(?:unique\s+)?index\s+(?:if\s+not\s+exists\s+)?([a-z0-9_]+)`)

// parseSchema extracts tables, their columns, referenced tables and index
// names from a migration script.
func parseSchema(t *testing.T, script string) *schemaShape {
	t.Helper()
	script = stripLineComments(script)
	shape := &schemaShape{
		tables:  map[string]*tableShape{},
		indexes: map[string]bool{},
	}

	lower := strings.ToLower(script)
	cursor := 0
	for {
		idx := strings.Index(lower[cursor:], "create table")
		if idx < 0 {
			break
		}
		i := cursor + idx + len("create table")
		i = skipSpace(script, i)
		if strings.HasPrefix(lower[i:], "if not exists") {
			i += len("if not exists")
			i = skipSpace(script, i)
		}
		nameStart := i
		for i < len(script) && isIdent(script[i]) {
			i++
		}
		name := script[nameStart:i]
		if name == "" {
			t.Fatalf("cannot read a table name near %q", snippet(script, nameStart))
		}
		for i < len(script) && script[i] != '(' {
			i++
		}
		if i >= len(script) {
			t.Fatalf("table %q has no column list", name)
		}
		open := i
		depth := 0
		close := -1
		for ; i < len(script); i++ {
			switch script[i] {
			case '(':
				depth++
			case ')':
				depth--
				if depth == 0 {
					close = i
				}
			}
			if close >= 0 {
				break
			}
		}
		if close < 0 {
			t.Fatalf("table %q has an unbalanced column list", name)
		}
		shape.tables[name] = parseTableBody(script[open+1 : close])
		cursor = close + 1
	}

	for _, match := range indexPattern.FindAllStringSubmatch(script, -1) {
		shape.indexes[strings.ToLower(match[1])] = true
	}
	return shape
}

// parseTableBody reads one definition per line. Table-level constraints are
// skipped; inline REFERENCES clauses are recorded.
func parseTableBody(body string) *tableShape {
	shape := &tableShape{columns: map[string]bool{}, refs: map[string]bool{}}
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		line = strings.TrimSuffix(line, ",")
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "--") {
			continue
		}
		upper := strings.ToUpper(line)
		if strings.HasPrefix(upper, "PRIMARY KEY") ||
			strings.HasPrefix(upper, "UNIQUE") ||
			strings.HasPrefix(upper, "FOREIGN KEY") ||
			strings.HasPrefix(upper, "CONSTRAINT") ||
			strings.HasPrefix(upper, "CHECK") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 || !isIdent([]byte(fields[0])[0]) {
			continue
		}
		shape.columns[strings.ToLower(fields[0])] = true
		for i, field := range fields {
			if strings.EqualFold(field, "REFERENCES") && i+1 < len(fields) {
				target := fields[i+1]
				if cut := strings.IndexByte(target, '('); cut >= 0 {
					target = target[:cut]
				}
				target = strings.ToLower(strings.Trim(target, "`\","))
				if target != "" {
					shape.refs[target] = true
				}
			}
		}
	}
	return shape
}

func stripLineComments(script string) string {
	lines := strings.Split(script, "\n")
	for i, line := range lines {
		if idx := strings.Index(line, "--"); idx >= 0 {
			lines[i] = line[:idx]
		}
	}
	return strings.Join(lines, "\n")
}

func skipSpace(script string, i int) int {
	for i < len(script) {
		switch script[i] {
		case ' ', '\t', '\r', '\n':
			i++
		default:
			return i
		}
	}
	return i
}

func isIdent(b byte) bool {
	return b == '_' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z' || b >= '0' && b <= '9'
}

func snippet(script string, at int) string {
	end := at + 40
	if end > len(script) {
		end = len(script)
	}
	return script[at:end]
}

func compareSet(t *testing.T, kind, table string, left, right map[string]bool) {
	t.Helper()
	where := ""
	if table != "" {
		where = " in table " + table
	}
	for _, item := range sortedKeys(left) {
		if !right[item] {
			t.Errorf("%s %q%s exists in sqlite but not postgres", kind, item, where)
		}
	}
	for _, item := range sortedKeys(right) {
		if !left[item] {
			t.Errorf("%s %q%s exists in postgres but not sqlite", kind, item, where)
		}
	}
}

func sortedKeys[T any](set map[string]T) []string {
	out := make([]string, 0, len(set))
	for key := range set {
		out = append(out, key)
	}
	sort.Strings(out)
	return out
}

// TestSchemaParserSanity makes sure the parser is not silently returning an
// empty shape (which would make the parity test pass for the wrong reason).
func TestSchemaParserSanity(t *testing.T) {
	list, err := loadMigrations("sqlite")
	if err != nil {
		t.Fatal(err)
	}
	shape := parseSchema(t, list[0].body)
	for _, table := range []string{"users", "folders", "messages", "events", "event_exceptions", "outbox"} {
		if shape.tables[table] == nil {
			t.Fatalf("parser did not see table %q", table)
		}
	}
	if !shape.tables["messages"].columns["conversation_id"] {
		t.Fatal("parser did not see messages.conversation_id")
	}
	if !shape.tables["messages"].refs["folders"] {
		t.Fatal("parser did not see messages -> folders foreign key")
	}
	if !shape.indexes["idx_messages_msgid"] {
		t.Fatal("parser did not see idx_messages_msgid")
	}
}
