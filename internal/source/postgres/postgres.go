// Package postgres reads a directory of Postgres migrations, applies them in
// filename order, and returns the schema they leave behind. It reads the
// statements that shape a table's columns and its replica identity and
// ignores everything else: indexes, functions, triggers, policies, data.
//
// Filename order is the order every migration runner uses, so the schema
// this produces is the one the connector sees. A live pg_dump reader can
// produce the same model later; the rules do not care which.
package postgres

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/avison9/cdclint/internal/ddl"
	"github.com/avison9/cdclint/internal/migrate"
	"github.com/avison9/cdclint/internal/model"
	"github.com/avison9/cdclint/internal/source"
	"github.com/avison9/cdclint/internal/sqlsplit"
)

// DefaultSchema is assumed for unqualified table names, the way a fresh
// connection's search_path does.
const DefaultSchema = "public"

// ReadDir applies every *.sql file in dir, sorted by name.
func ReadDir(dir string) (*model.Source, []string, error) {
	named, files, err := source.ReadDir(dir)
	if err != nil {
		return nil, nil, err
	}
	src, err := ReadFiles(named)
	return src, files, err
}

// NamedFile is one migration's content, named by the path findings show.
type NamedFile = source.NamedFile

// ReadFiles applies migrations already in memory, in the order given.
// Down migrations are skipped: a forward migrate never runs them.
func ReadFiles(files []NamedFile) (*model.Source, error) {
	src := &model.Source{}
	for _, f := range files {
		if migrate.Down(f.Path) {
			continue
		}
		if err := Apply(src, f.Path, f.Text); err != nil {
			return nil, fmt.Errorf("%s: %w", f.Path, err)
		}
	}
	return src, nil
}

// Apply runs one file's statements against src, leaving out a down section
// (goose, sql-migrate, dbmate) the way a forward migrate does.
func Apply(src *model.Source, file, text string) error {
	for _, st := range sqlsplit.Split(migrate.Up(text)) {
		pos := model.Pos{File: file, Line: st.Line}
		w := ddl.Words(st.Text)
		switch {
		case ddl.HasPrefixFold(w, "CREATE", "TABLE"), ddl.HasPrefixFold(w, "CREATE", "UNLOGGED", "TABLE"):
			createTable(src, st.Text, pos)
		case ddl.HasPrefixFold(w, "ALTER", "TABLE"):
			alterTable(src, st.Text, w, pos)
		case ddl.HasPrefixFold(w, "DROP", "TABLE"):
			dropTable(src, w)
		case ddl.HasPrefixFold(w, "DO"):
			doBlock(src, st.Text)
		}
	}
	return nil
}

// alterInDo finds the tables a DO block's body alters.
var alterInDo = regexp.MustCompile(`(?i)\bALTER\s+TABLE\s+(?:ONLY\s+)?(?:IF\s+EXISTS\s+)?((?:"[^"]+"|[\w$]+)(?:\.(?:"[^"]+"|[\w$]+))?)`)

// doBlock does not run a DO block: its body is procedural, often guarded
// by a condition on the catalog. It marks the tables the body alters as
// having an unknown key, so a rule that depends on the key stays quiet
// rather than judge a table whose key may be added there (Mattermost
// 000016_create_reactions.up.sql adds reactions' primary key in one).
func doBlock(src *model.Source, text string) {
	for _, m := range alterInDo.FindAllStringSubmatch(text, -1) {
		schema, table := splitQualified(m[1])
		if t := src.Table(schema, table); t != nil {
			t.KeyUnknown = true
		}
	}
}

func splitQualified(name string) (schema, table string) {
	parts := ddl.SplitName(name)
	if len(parts) >= 2 {
		return parts[len(parts)-2], parts[len(parts)-1]
	}
	return DefaultSchema, parts[0]
}

func createTable(src *model.Source, text string, pos model.Pos) {
	w := ddl.Words(text)
	i := 2
	if strings.EqualFold(w[1], "UNLOGGED") {
		i = 3
	}
	if ddl.HasPrefixFold(w[i:], "IF", "NOT", "EXISTS") {
		i += 3
	}
	if i >= len(w) {
		return
	}
	// The name may be glued to the paren: "reports(" is legal.
	name := w[i]
	if p := strings.IndexByte(name, '('); p > 0 {
		name = name[:p]
	}
	schema, table := splitQualified(name)
	// The column list is the first parenthesis after TABLE. Searching for
	// the name's word instead panicked when the name was glued to a paren
	// that opens a multi-line list ("reactions(\n  userid ..."): the word
	// splitter folds that list's whitespace, so the word is not in the text
	// and the index is -1. Found on Mattermost's migrations.
	body, rest, ok := ddl.Body(text[strings.Index(strings.ToUpper(text), "TABLE")+len("TABLE"):])
	if !ok {
		return
	}
	// CREATE TABLE ... (LIKE x) and partitions: PARTITION OF has no column
	// list of its own; both are rare in migrations and skipped.
	if ddl.HasPrefixFold(ddl.Words(rest), "PARTITION", "OF") {
		return
	}
	t := &model.Table{Schema: schema, Name: table, ReplicaIdentity: model.ReplicaDefault, Pos: pos}
	cursor := 0
	for _, item := range ddl.SplitTop(body) {
		line := pos.Line + ddl.ItemLine(text, item, &cursor) - 1
		iw := ddl.Words(item)
		if len(iw) == 0 {
			continue
		}
		switch keyword(iw[0]) {
		case "CONSTRAINT", "PRIMARY", "UNIQUE", "CHECK", "FOREIGN", "EXCLUDE", "LIKE":
			if hasKeywords(iw, "PRIMARY", "KEY") {
				t.PrimaryKey = keyColumns(item)
			}
			if ddl.HasPrefixFold(iw, "CONSTRAINT") && containsFold(iw, "PRIMARY") {
				t.PrimaryKey = keyColumns(item)
			}
			continue
		}
		col := model.Column{Name: ddl.Unquote(iw[0]), Type: strings.Join(typeWords(iw[1:]), " "), Pos: model.Pos{File: pos.File, Line: line}}
		if containsFold(iw, "PRIMARY") {
			t.PrimaryKey = []string{col.Name}
		}
		t.Columns = append(t.Columns, col)
	}
	// A second CREATE TABLE IF NOT EXISTS for an existing table is a no-op
	// in Postgres; a plain CREATE would have failed. Either way keep the
	// first.
	if src.Table(schema, table) == nil {
		src.Tables = append(src.Tables, t)
	}
}

// typeWords keeps the type part of a column definition: everything up to
// the first constraint keyword.
func typeWords(w []string) []string {
	for i, x := range w {
		switch keyword(x) {
		case "NOT", "NULL", "DEFAULT", "PRIMARY", "REFERENCES", "UNIQUE", "CHECK", "GENERATED", "CONSTRAINT", "COLLATE":
			return w[:i]
		}
	}
	return w
}

func keyColumns(item string) []string {
	body, _, ok := ddl.Body(item)
	if !ok {
		return nil
	}
	var cols []string
	for _, c := range ddl.SplitTop(body) {
		cols = append(cols, ddl.Unquote(c))
	}
	return cols
}

// keyword is a word as a keyword: upper case, and cut at an opening
// parenthesis. ddl.Words keeps a parenthesised group glued to the word
// before it, and Postgres needs no space there: Mattermost writes
// UNIQUE(channelid, remoteid) and PRIMARY KEY(id, channelid), which were
// read as a column named "UNIQUE(channelid, remoteid)" and as no primary
// key. Kept to this reader: in the ClickHouse reader a word such as
// Kafka(...) is compared whole on purpose.
func keyword(w string) string {
	if i := strings.IndexByte(w, '('); i >= 0 {
		w = w[:i]
	}
	return strings.ToUpper(w)
}

// hasKeywords is ddl.HasPrefixFold for keywords that may be glued to a
// parenthesis.
func hasKeywords(w []string, kws ...string) bool {
	if len(w) < len(kws) {
		return false
	}
	for i, kw := range kws {
		if keyword(w[i]) != kw {
			return false
		}
	}
	return true
}

func containsFold(w []string, kw string) bool {
	for _, x := range w {
		if strings.EqualFold(x, kw) {
			return true
		}
	}
	return false
}

func alterTable(src *model.Source, text string, w []string, pos model.Pos) {
	i := 2
	for i < len(w) && (strings.EqualFold(w[i], "ONLY") || strings.EqualFold(w[i], "IF") || strings.EqualFold(w[i], "EXISTS")) {
		i++
	}
	if i >= len(w) {
		return
	}
	schema, table := splitQualified(w[i])
	t := src.Table(schema, table)
	if t == nil {
		return
	}
	// Actions are comma-separated after the name; each starts with a verb.
	actions := ddl.SplitTop(strings.Join(w[i+1:], " "))
	// Each added column is placed on its own line: search the statement for
	// its name, after the table's name and after the previous action.
	cursor := 0
	ddl.NameLine(text, table, &cursor)
	for _, a := range actions {
		aw := ddl.Words(a)
		if len(aw) == 0 {
			continue
		}
		switch {
		case ddl.HasPrefixFold(aw, "ADD", "COLUMN"), ddl.HasPrefixFold(aw, "ADD") && !hasKeywords(aw, "ADD", "CONSTRAINT") && !hasKeywords(aw, "ADD", "PRIMARY") && !hasKeywords(aw, "ADD", "UNIQUE") && !hasKeywords(aw, "ADD", "CHECK") && !hasKeywords(aw, "ADD", "FOREIGN") && !hasKeywords(aw, "ADD", "EXCLUDE"):
			j := 1
			if strings.EqualFold(aw[j], "COLUMN") {
				j++
			}
			if ddl.HasPrefixFold(aw[j:], "IF", "NOT", "EXISTS") {
				j += 3
			}
			if j >= len(aw) {
				continue
			}
			name := ddl.Unquote(aw[j])
			if t.Column(name) == nil {
				at := pos
				if line := ddl.NameLine(text, name, &cursor); line > 0 {
					at.Line = pos.Line + line - 1
				}
				t.Columns = append(t.Columns, model.Column{Name: name, Type: strings.Join(typeWords(aw[j+1:]), " "), Pos: at})
			}
		case ddl.HasPrefixFold(aw, "DROP", "COLUMN"), ddl.HasPrefixFold(aw, "DROP") && len(aw) >= 2 && !strings.EqualFold(aw[1], "CONSTRAINT"):
			j := 1
			if strings.EqualFold(aw[j], "COLUMN") {
				j++
			}
			if ddl.HasPrefixFold(aw[j:], "IF", "EXISTS") {
				j += 2
			}
			if j < len(aw) {
				removeColumn(t, ddl.Unquote(aw[j]))
			}
		case ddl.HasPrefixFold(aw, "RENAME", "COLUMN"), ddl.HasPrefixFold(aw, "RENAME") && len(aw) >= 4 && strings.EqualFold(aw[2], "TO"):
			j := 1
			if strings.EqualFold(aw[j], "COLUMN") {
				j++
			}
			if j+2 < len(aw) && strings.EqualFold(aw[j+1], "TO") {
				if c := t.Column(ddl.Unquote(aw[j])); c != nil {
					c.Name = ddl.Unquote(aw[j+2])
					c.Pos = pos
				}
			}
		case hasKeywords(aw, "ADD", "PRIMARY", "KEY"), hasKeywords(aw, "ADD", "CONSTRAINT") && len(aw) > 3 && hasKeywords(aw[3:], "PRIMARY", "KEY"):
			// A key added after the table, as migrations that split
			// constraints out do. USING INDEX names no columns here.
			if cols := keyColumns(a); len(cols) > 0 {
				t.PrimaryKey = cols
			} else {
				t.KeyUnknown = true
			}
		case ddl.HasPrefixFold(aw, "RENAME", "TO"):
			if len(aw) >= 3 {
				t.Name = ddl.Unquote(aw[2])
			}
		case ddl.HasPrefixFold(aw, "REPLICA", "IDENTITY"):
			if len(aw) >= 3 {
				switch strings.ToUpper(aw[2]) {
				case "FULL":
					t.ReplicaIdentity = model.ReplicaFull
				case "NOTHING":
					t.ReplicaIdentity = model.ReplicaNothing
				case "DEFAULT":
					t.ReplicaIdentity = model.ReplicaDefault
				case "USING":
					t.ReplicaIdentity = model.ReplicaIndex
				}
			}
		case ddl.HasPrefixFold(aw, "ALTER", "COLUMN"), ddl.HasPrefixFold(aw, "ALTER"):
			j := 1
			if strings.EqualFold(aw[j], "COLUMN") {
				j++
			}
			if j+1 < len(aw) {
				c := t.Column(ddl.Unquote(aw[j]))
				rest := aw[j+1:]
				if c != nil && (ddl.HasPrefixFold(rest, "TYPE") || ddl.HasPrefixFold(rest, "SET", "DATA", "TYPE")) {
					k := 1
					if strings.EqualFold(rest[0], "SET") {
						k = 3
					}
					c.Type = strings.Join(typeWords(rest[k:]), " ")
				}
			}
		}
	}
}

func removeColumn(t *model.Table, name string) {
	for i := range t.Columns {
		if strings.EqualFold(t.Columns[i].Name, name) {
			t.Columns = append(t.Columns[:i], t.Columns[i+1:]...)
			return
		}
	}
}

func dropTable(src *model.Source, w []string) {
	i := 2
	if ddl.HasPrefixFold(w[i:], "IF", "EXISTS") {
		i += 2
	}
	for _, name := range ddl.SplitTop(strings.Join(w[i:], " ")) {
		nw := ddl.Words(name)
		if len(nw) == 0 {
			continue
		}
		schema, table := splitQualified(nw[0])
		for j, t := range src.Tables {
			if strings.EqualFold(t.Schema, schema) && strings.EqualFold(t.Name, table) {
				src.Tables = append(src.Tables[:j], src.Tables[j+1:]...)
				break
			}
		}
	}
}
