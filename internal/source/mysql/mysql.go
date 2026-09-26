// Package mysql reads a directory of MySQL or MariaDB migrations, applies
// them in filename order, and returns the schema they leave behind. Like the
// Postgres reader it reads only the statements that shape a table's columns
// (CREATE, ALTER, RENAME and DROP TABLE, and USE) and ignores indexes,
// routines, triggers, grants and data.
//
// MySQL has no schemas. Debezium names a table databaseName.tableName and a
// column databaseName.tableName.columnName, so the database goes where the
// Postgres reader puts the schema, and the rules, include lists and topic
// names work unchanged. Replica identity is a Postgres setting and is left
// empty here.
package mysql

import (
	"fmt"
	"strings"

	"github.com/avison9/cdclint/internal/ddl"
	"github.com/avison9/cdclint/internal/migrate"
	"github.com/avison9/cdclint/internal/model"
	"github.com/avison9/cdclint/internal/source"
	"github.com/avison9/cdclint/internal/sqlsplit"
)

// ReadDir applies every *.sql file in dir, sorted by name. database is the
// database an unqualified table belongs to until a USE statement names
// another; see ReadFiles.
func ReadDir(dir, database string) (*model.Source, []string, error) {
	named, files, err := source.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	src, err := ReadFiles(named, database)
	return src, files, err
}

// ReadFiles applies migrations already in memory, in the order given, and
// leaves out down migrations as the Postgres reader does (package migrate). A
// migration runner connects to one database and runs unqualified statements
// in it, and that name is in neither the files nor the statements; database
// supplies it (the caller takes it from the connector). A table the reader
// cannot place in a database is an error, because every finding about it
// would name the wrong table.
func ReadFiles(files []source.NamedFile, database string) (*model.Source, error) {
	r := &reader{src: &model.Source{}, db: database}
	for _, f := range files {
		if migrate.Down(f.Path) {
			continue
		}
		if err := r.apply(f.Path, f.Text); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	return r.src, nil
}

type reader struct {
	src *model.Source
	db  string
}

func (r *reader) apply(file, text string) error {
	for _, st := range sqlsplit.SplitMySQL(migrate.Up(text)) {
		if err := r.statement(file, st); err != nil {
			return err
		}
	}
	return nil
}

func (r *reader) statement(file string, st sqlsplit.Statement) error {
	body := doubleEscapedQuotes(st.Text)
	w := ddl.Words(body)
	// MySQL has no ADD COLUMN IF NOT EXISTS, so re-runnable migrations put
	// the DDL in a string and run it through a prepared statement:
	//   SET @s = (SELECT IF(<column exists>, 'SELECT 1', 'ALTER TABLE t ADD c INT'));
	//   PREPARE stmt FROM @s; EXECUTE stmt;
	// The IF only skips the DDL when its effect is already there, and adding
	// a column that exists or dropping one that does not is a no-op here, so
	// applying every table statement found in such a string gives the end
	// state the migration guarantees. 100 of Mattermost's 140 MySQL
	// migrations are written this way.
	if (ddl.HasPrefixFold(w, "SET") && len(w) > 1 && strings.HasPrefix(w[1], "@")) || ddl.HasPrefixFold(w, "PREPARE") {
		for _, lit := range stringLiterals(st) {
			if err := r.statement(file, lit); err != nil {
				return err
			}
		}
		return nil
	}
	pos := model.Pos{File: file, Line: st.Line}
	var err error
	switch {
	case ddl.HasPrefixFold(w, "USE") && len(w) >= 2:
		r.db = ddl.Unquote(w[1])
	case ddl.HasPrefixFold(w, "CREATE", "TEMPORARY"):
		// Session-scoped and never written to the binlog as rows.
	case ddl.HasPrefixFold(w, "CREATE", "TABLE"):
		err = r.createTable(body, w, pos)
	case ddl.HasPrefixFold(w, "ALTER") && tableKeyword(w) > 0:
		err = r.alterTable(body, w, tableKeyword(w), pos)
	case ddl.HasPrefixFold(w, "RENAME", "TABLE"):
		err = r.renameTables(w[2:])
	case ddl.HasPrefixFold(w, "DROP", "TABLE"), ddl.HasPrefixFold(w, "DROP", "TEMPORARY", "TABLE"):
		err = r.dropTables(w)
	}
	if err != nil {
		return fmt.Errorf("line %d: %w", st.Line, err)
	}
	return nil
}

// stringLiterals returns the single-quoted literals in st that hold a table
// statement, as statements of their own, each on the line it starts on.
func stringLiterals(st sqlsplit.Statement) []sqlsplit.Statement {
	var out []sqlsplit.Statement
	s := st.Text
	line := st.Line
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '\n':
			line++
		case '`', '"':
			if end := strings.IndexByte(s[i+1:], s[i]); end >= 0 {
				line += strings.Count(s[i:i+1+end], "\n")
				i += end + 1
			}
		case '\'':
			var lit strings.Builder
			start := line
			j := i + 1
			for ; j < len(s); j++ {
				c := s[j]
				if c == '\\' && j+1 < len(s) {
					lit.WriteByte(s[j+1])
					j++
					continue
				}
				if c == '\'' {
					if j+1 < len(s) && s[j+1] == '\'' {
						lit.WriteByte('\'')
						j++
						continue
					}
					break
				}
				if c == '\n' {
					line++
				}
				lit.WriteByte(c)
			}
			i = j
			text := strings.TrimSuffix(strings.TrimSpace(lit.String()), ";")
			lw := ddl.Words(text)
			if ddl.HasPrefixFold(lw, "ALTER") || ddl.HasPrefixFold(lw, "CREATE", "TABLE") ||
				ddl.HasPrefixFold(lw, "DROP", "TABLE") || ddl.HasPrefixFold(lw, "RENAME", "TABLE") {
				out = append(out, sqlsplit.Statement{Text: text, Line: start})
			}
		}
	}
	return out
}

// tableKeyword finds TABLE in ALTER [ONLINE] [IGNORE] TABLE and returns its
// index, or 0 when the statement alters something else.
func tableKeyword(w []string) int {
	for i := 1; i < len(w) && i <= 3; i++ {
		switch strings.ToUpper(w[i]) {
		case "TABLE":
			return i
		case "ONLINE", "OFFLINE", "IGNORE":
		default:
			return 0
		}
	}
	return 0
}

// qualify splits db.table, placing an unqualified table in the current
// database.
func (r *reader) qualify(name string) (db, table string, err error) {
	parts := ddl.SplitName(name)
	if len(parts) >= 2 {
		return parts[len(parts)-2], parts[len(parts)-1], nil
	}
	if r.db == "" {
		return "", "", fmt.Errorf("table %s names no database and the migrations do not say which one they run in: "+
			"give the connector a database.include.list naming that one database, or start the first migration with USE <database>;", parts[0])
	}
	return r.db, parts[0], nil
}

func (r *reader) createTable(text string, w []string, pos model.Pos) error {
	i := 2
	if ddl.HasPrefixFold(w[i:], "IF", "NOT", "EXISTS") {
		i += 3
	}
	if i >= len(w) {
		return nil
	}
	// The name may be glued to the paren: "reports(" is legal.
	name := w[i]
	if p := strings.IndexByte(name, '('); p > 0 {
		name = name[:p]
	}
	db, table, err := r.qualify(name)
	if err != nil {
		return err
	}
	if r.src.Table(db, table) != nil {
		// CREATE TABLE IF NOT EXISTS for a table that exists is a no-op; a
		// plain CREATE would have failed. Either way keep the first.
		return nil
	}
	t := &model.Table{Schema: db, Name: table, Pos: pos}
	rest := w[i+1:]
	if strings.HasPrefix(w[i], name+"(") {
		rest = append([]string{w[i][len(name):]}, rest...)
	}
	// CREATE TABLE t LIKE s, or (LIKE s): the same columns as s.
	if ddl.HasPrefixFold(rest, "LIKE") && len(rest) >= 2 {
		return r.createLike(t, rest[1])
	}
	if len(rest) > 0 && strings.HasPrefix(rest[0], "(") {
		inner, _, _ := ddl.Body(rest[0])
		if iw := ddl.Words(inner); ddl.HasPrefixFold(iw, "LIKE") && len(iw) >= 2 {
			return r.createLike(t, iw[1])
		}
	}
	// The column list is the first parenthesis after TABLE. Searching for
	// the name's word instead fails when the name is glued to a paren that
	// opens a multi-line list ("orders(\n  id ..."): the word splitter has
	// folded that list's whitespace, so the word is not in the text.
	body, _, ok := ddl.Body(text[strings.Index(strings.ToUpper(text), "TABLE")+len("TABLE"):])
	if !ok {
		// CREATE TABLE ... AS SELECT without a column list: its columns are
		// the query's, which a file reader cannot know. Leaving the table
		// out is better than inventing its columns.
		return nil
	}
	cursor := 0
	for _, item := range ddl.SplitTop(body) {
		line := pos.Line + ddl.ItemLine(text, item, &cursor) - 1
		iw := ddl.Words(item)
		if len(iw) == 0 {
			continue
		}
		if isKey(iw) {
			if pk := primaryKey(iw, item); pk != nil {
				t.PrimaryKey = pk
			}
			continue
		}
		col := model.Column{Name: ddl.Unquote(iw[0]), Type: strings.Join(typeWords(iw[1:]), " "), Pos: model.Pos{File: pos.File, Line: line}}
		if containsFold(iw, "PRIMARY") {
			t.PrimaryKey = []string{col.Name}
		}
		t.Columns = append(t.Columns, col)
	}
	r.src.Tables = append(r.src.Tables, t)
	return nil
}

func (r *reader) createLike(t *model.Table, like string) error {
	db, table, err := r.qualify(like)
	if err != nil {
		return err
	}
	from := r.src.Table(db, table)
	if from == nil {
		return nil
	}
	t.Columns = append([]model.Column(nil), from.Columns...)
	t.PrimaryKey = append([]string(nil), from.PrimaryKey...)
	r.src.Tables = append(r.src.Tables, t)
	return nil
}

// isKey reports whether a table element defines an index or a constraint
// rather than a column. SPATIAL is not reserved, so a column may be named
// spatial; it only starts an index when INDEX, KEY or a column list follows.
func isKey(iw []string) bool {
	// A keyword may be glued to its column list: UNIQUE(a, b).
	first := iw[0]
	if p := strings.IndexByte(first, '('); p > 0 {
		first = first[:p]
	}
	switch strings.ToUpper(first) {
	case "PRIMARY", "KEY", "INDEX", "UNIQUE", "FULLTEXT", "CONSTRAINT", "FOREIGN", "CHECK":
		return true
	case "SPATIAL":
		return len(iw) > 1 && (strings.EqualFold(iw[1], "INDEX") || strings.EqualFold(iw[1], "KEY") || strings.HasPrefix(iw[1], "("))
	}
	return false
}

// primaryKey returns the key's columns when the element is a primary key.
// Key parts may carry a prefix length or an order: name(10) DESC.
func primaryKey(iw []string, item string) []string {
	if !containsFold(iw, "PRIMARY") {
		return nil
	}
	body, _, ok := ddl.Body(item)
	if !ok {
		return nil
	}
	var cols []string
	for _, c := range ddl.SplitTop(body) {
		name := ddl.Words(c)[0]
		if p := strings.IndexByte(name, '('); p > 0 {
			name = name[:p]
		}
		cols = append(cols, ddl.Unquote(name))
	}
	return cols
}

// typeWords keeps the type part of a column definition: everything up to the
// first attribute keyword. UNSIGNED and ZEROFILL belong to the type.
func typeWords(w []string) []string {
	for i, x := range w {
		switch strings.ToUpper(x) {
		case "NOT", "NULL", "DEFAULT", "AUTO_INCREMENT", "COMMENT", "PRIMARY", "UNIQUE", "KEY", "REFERENCES",
			"CHECK", "GENERATED", "AS", "COLLATE", "CHARACTER", "CHARSET", "ON", "INVISIBLE", "VISIBLE",
			"STORED", "VIRTUAL", "SRID", "FIRST", "AFTER", "COLUMN_FORMAT", "STORAGE", "CONSTRAINT",
			"ENGINE_ATTRIBUTE", "SECONDARY_ENGINE_ATTRIBUTE":
			return w[:i]
		}
	}
	return w
}

func (r *reader) alterTable(text string, w []string, at int, pos model.Pos) error {
	if at+1 >= len(w) {
		return nil
	}
	db, table, err := r.qualify(w[at+1])
	if err != nil {
		return err
	}
	t := r.src.Table(db, table)
	if t == nil {
		return nil
	}
	// Each added column is placed on its own line: search the statement for
	// its name, after the table's name and after the previous action.
	cursor := 0
	ddl.NameLine(text, table, &cursor)
	// Actions are comma-separated after the name; each starts with a verb.
	for _, a := range ddl.SplitTop(strings.Join(w[at+2:], " ")) {
		aw := ddl.Words(a)
		if len(aw) < 2 {
			continue
		}
		verb := strings.ToUpper(aw[0])
		j := 1
		if strings.EqualFold(aw[1], "COLUMN") && verb != "RENAME" {
			j = 2
		}
		if j >= len(aw) {
			continue
		}
		switch verb {
		case "ADD":
			if j == 1 && isKey(aw[1:]) || strings.EqualFold(aw[1], "PARTITION") || strings.EqualFold(aw[1], "PERIOD") {
				continue
			}
			if ddl.HasPrefixFold(aw[j:], "IF", "NOT", "EXISTS") {
				j += 3
			}
			if j >= len(aw) {
				continue
			}
			if strings.HasPrefix(aw[j], "(") {
				// ADD [COLUMN] (a INT, b INT): several columns at once.
				inner, _, _ := ddl.Body(aw[j])
				for _, def := range ddl.SplitTop(inner) {
					if dw := ddl.Words(def); len(dw) > 0 && !isKey(dw) {
						addColumn(t, dw, linePos(text, dw[0], pos, &cursor))
					}
				}
				continue
			}
			addColumn(t, aw[j:], linePos(text, aw[j], pos, &cursor))
		case "DROP":
			if j == 1 && (isKey(aw[1:]) || strings.EqualFold(aw[1], "PARTITION") || strings.EqualFold(aw[1], "DEFAULT")) {
				continue
			}
			if ddl.HasPrefixFold(aw[j:], "IF", "EXISTS") {
				j += 2
			}
			if j < len(aw) {
				removeColumn(t, ddl.Unquote(aw[j]))
			}
		case "CHANGE":
			// CHANGE [COLUMN] old new definition [FIRST | AFTER col]
			if j+1 < len(aw) {
				if c := t.Column(ddl.Unquote(aw[j])); c != nil {
					c.Name = ddl.Unquote(aw[j+1])
					c.Type = strings.Join(typeWords(aw[j+2:]), " ")
					c.Pos = pos
				}
			}
		case "MODIFY":
			if c := t.Column(ddl.Unquote(aw[j])); c != nil {
				c.Type = strings.Join(typeWords(aw[j+1:]), " ")
			}
		case "RENAME":
			switch {
			case strings.EqualFold(aw[1], "COLUMN") && len(aw) >= 5 && strings.EqualFold(aw[3], "TO"):
				if c := t.Column(ddl.Unquote(aw[2])); c != nil {
					c.Name = ddl.Unquote(aw[4])
					c.Pos = pos
				}
			case strings.EqualFold(aw[1], "INDEX"), strings.EqualFold(aw[1], "KEY"):
			default:
				k := 1
				if strings.EqualFold(aw[1], "TO") || strings.EqualFold(aw[1], "AS") {
					k = 2
				}
				if k < len(aw) {
					if err := r.renameTable(t, aw[k]); err != nil {
						return err
					}
				}
			}
		}
	}
	return nil
}

// linePos is pos moved to the line in text where the column name next
// appears, or pos itself when it cannot be found.
func linePos(text, name string, pos model.Pos, cursor *int) model.Pos {
	if line := ddl.NameLine(text, ddl.Unquote(name), cursor); line > 0 {
		pos.Line += line - 1
	}
	return pos
}

// addColumn adds one column definition, honouring FIRST and AFTER.
func addColumn(t *model.Table, dw []string, pos model.Pos) {
	name := ddl.Unquote(dw[0])
	if t.Column(name) != nil {
		return
	}
	col := model.Column{Name: name, Type: strings.Join(typeWords(dw[1:]), " "), Pos: pos}
	at := len(t.Columns)
	for k := 1; k < len(dw); k++ {
		switch {
		case strings.EqualFold(dw[k], "FIRST"):
			at = 0
		case strings.EqualFold(dw[k], "AFTER") && k+1 < len(dw):
			for i, c := range t.Columns {
				if strings.EqualFold(c.Name, ddl.Unquote(dw[k+1])) {
					at = i + 1
				}
			}
		}
	}
	t.Columns = append(t.Columns, model.Column{})
	copy(t.Columns[at+1:], t.Columns[at:])
	t.Columns[at] = col
}

func removeColumn(t *model.Table, name string) {
	for i := range t.Columns {
		if strings.EqualFold(t.Columns[i].Name, name) {
			t.Columns = append(t.Columns[:i], t.Columns[i+1:]...)
			return
		}
	}
}

// renameTables handles RENAME TABLE a TO b [, c TO d].
func (r *reader) renameTables(w []string) error {
	for _, pair := range ddl.SplitTop(strings.Join(w, " ")) {
		pw := ddl.Words(pair)
		if len(pw) < 3 || !strings.EqualFold(pw[1], "TO") {
			continue
		}
		db, table, err := r.qualify(pw[0])
		if err != nil {
			return err
		}
		if t := r.src.Table(db, table); t != nil {
			if err := r.renameTable(t, pw[2]); err != nil {
				return err
			}
		}
	}
	return nil
}

// renameTable renames t, possibly moving it to another database.
func (r *reader) renameTable(t *model.Table, to string) error {
	db, table, err := r.qualify(to)
	if err != nil {
		return err
	}
	t.Schema, t.Name = db, table
	return nil
}

func (r *reader) dropTables(w []string) error {
	i := 2
	if strings.EqualFold(w[1], "TEMPORARY") {
		return nil
	}
	if ddl.HasPrefixFold(w[i:], "IF", "EXISTS") {
		i += 2
	}
	for _, name := range ddl.SplitTop(strings.Join(w[i:], " ")) {
		nw := ddl.Words(name)
		if len(nw) == 0 || strings.EqualFold(nw[0], "RESTRICT") || strings.EqualFold(nw[0], "CASCADE") {
			continue
		}
		db, table, err := r.qualify(nw[0])
		if err != nil {
			return err
		}
		for j, t := range r.src.Tables {
			if strings.EqualFold(t.Schema, db) && strings.EqualFold(t.Name, table) {
				r.src.Tables = append(r.src.Tables[:j], r.src.Tables[j+1:]...)
				break
			}
		}
	}
	return nil
}

func containsFold(w []string, kw string) bool {
	for _, x := range w {
		if strings.EqualFold(x, kw) {
			return true
		}
	}
	return false
}

// doubleEscapedQuotes rewrites \' and \" inside quoted strings as ” and "",
// which mean the same and which the shared word splitter understands. Both
// forms are two characters, so every offset and line stays where it was.
func doubleEscapedQuotes(s string) string {
	b := []byte(s)
	var q byte
	for i := 0; i < len(b); i++ {
		c := b[i]
		switch {
		case q == 0 && c == '`':
			// An identifier: quotes inside it are just characters.
			if end := strings.IndexByte(s[i+1:], '`'); end >= 0 {
				i += end + 1
			}
		case q == 0 && (c == '\'' || c == '"'):
			q = c
		case q != 0 && c == '\\' && i+1 < len(b):
			if b[i+1] == q {
				b[i] = q
			}
			i++
		case q != 0 && c == q:
			q = 0
		}
	}
	return string(b)
}
