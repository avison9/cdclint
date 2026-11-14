// Package debezium reads a Debezium connector configuration and answers what
// it will emit. The config is the JSON posted to Kafka Connect, either the
// full {"name": ..., "config": {...}} document or the bare config map.
//
// Debezium's include and exclude lists are comma-separated Java regular
// expressions, each matched against the WHOLE qualified name
// (schema.table or schema.table.column), case-insensitively. Go's RE2
// accepts the patterns people actually write in these lists; a pattern it
// rejects is reported as an error rather than silently matching nothing,
// because "matches nothing" is exactly the failure this tool exists to
// catch.
package debezium

import (
	"encoding/json"
	"fmt"
	"os"
	"regexp"
	"strings"

	"github.com/avison9/cdclint/internal/model"
	"github.com/avison9/cdclint/internal/smt"
)

// Contract implements model.Contract for a Postgres, MySQL or SQL Server
// Debezium source connector.
type Contract struct {
	pos            model.Pos
	prefix         string
	tableInclude   []*regexp.Regexp
	tableExclude   []*regexp.Regexp
	dbInclude      []*regexp.Regexp // MySQL and MariaDB: database.include.list
	dbExclude      []*regexp.Regexp
	dbIncludeKey   string
	dbExcludeKey   string
	columnInclude  []*regexp.Regexp
	columnExclude  []*regexp.Regexp
	columnListKey  string
	tableListKey   string
	columnPatterns []string
	tablePatterns  []string
	routers        []router
	autoset        []autoset
	autosetErr     bool
	shape          model.Shape
	Config         map[string]string
	Class          string
	Name           string
}

// autoset is one pair of replica.identity.autoset.values: a regular
// expression over schema.table and the identity the connector sets on the
// tables it captures that match it.
type autoset struct {
	re       *regexp.Regexp
	identity model.ReplicaIdentity
}

type router struct {
	re          *regexp.Regexp
	replacement string
}

// ReadFile parses one connector JSON file.
func ReadFile(path string) (*Contract, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	c, err := Parse(b)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	c.pos = model.Pos{File: path}
	return c, nil
}

// Parse builds a Contract from the JSON bytes.
func Parse(b []byte) (*Contract, error) {
	var doc map[string]json.RawMessage
	if err := json.Unmarshal(b, &doc); err != nil {
		return nil, err
	}
	cfg := map[string]string{}
	name := ""
	if raw, ok := doc["config"]; ok {
		var inner map[string]any
		if err := json.Unmarshal(raw, &inner); err != nil {
			return nil, fmt.Errorf("config: %w", err)
		}
		for k, v := range inner {
			cfg[k] = fmt.Sprint(v)
		}
		if raw, ok := doc["name"]; ok {
			_ = json.Unmarshal(raw, &name)
		}
	} else {
		var flat map[string]any
		if err := json.Unmarshal(b, &flat); err != nil {
			return nil, err
		}
		for k, v := range flat {
			cfg[k] = fmt.Sprint(v)
		}
	}
	c := &Contract{Config: cfg, Name: name, Class: cfg["connector.class"]}
	c.prefix = first(cfg, "topic.prefix", "database.server.name")
	var err error
	// The deprecated *.whitelist / *.blacklist spellings still work in
	// Debezium; read them so an old config is not judged as capturing
	// everything.
	if c.tableInclude, c.tableListKey, err = list(cfg, "table.include.list", "table.whitelist"); err != nil {
		return nil, err
	}
	for _, p := range strings.Split(first(cfg, "table.include.list", "table.whitelist"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.tablePatterns = append(c.tablePatterns, p)
		}
	}
	if c.tableExclude, _, err = list(cfg, "table.exclude.list", "table.blacklist"); err != nil {
		return nil, err
	}
	if c.columnInclude, c.columnListKey, err = list(cfg, "column.include.list", "column.whitelist"); err != nil {
		return nil, err
	}
	for _, p := range strings.Split(first(cfg, "column.include.list", "column.whitelist"), ",") {
		if p = strings.TrimSpace(p); p != "" {
			c.columnPatterns = append(c.columnPatterns, p)
		}
	}
	var excludeKey string
	if c.columnExclude, excludeKey, err = list(cfg, "column.exclude.list", "column.blacklist"); err != nil {
		return nil, err
	}
	if c.columnListKey == "" {
		c.columnListKey = excludeKey
	}
	if c.columnListKey == "" {
		c.columnListKey = "column.include.list"
	}
	c.autoset, c.autosetErr = autosets(cfg["replica.identity.autoset.values"])
	if c.tableListKey == "" {
		c.tableListKey = "table.include.list"
	}
	if c.MySQL() {
		// MySQL has no schemas: Debezium filters databases first, and a
		// table must pass both lists.
		if c.dbInclude, c.dbIncludeKey, err = list(cfg, "database.include.list", "database.whitelist"); err != nil {
			return nil, err
		}
		if c.dbExclude, c.dbExcludeKey, err = list(cfg, "database.exclude.list", "database.blacklist"); err != nil {
			return nil, err
		}
	}
	c.routers = routers(cfg)
	c.shape = smt.Apply(cfg, "", smt.Start(cfg))
	return c, nil
}

func first(cfg map[string]string, keys ...string) string {
	for _, k := range keys {
		if v, ok := cfg[k]; ok && v != "" {
			return v
		}
	}
	return ""
}

// list compiles one comma-separated regex list, returning which key held it.
func list(cfg map[string]string, keys ...string) ([]*regexp.Regexp, string, error) {
	for _, k := range keys {
		v, ok := cfg[k]
		if !ok || strings.TrimSpace(v) == "" {
			continue
		}
		var out []*regexp.Regexp
		for _, p := range strings.Split(v, ",") {
			p = strings.TrimSpace(p)
			if p == "" {
				continue
			}
			re, err := regexp.Compile("(?i)^(?:" + p + ")$")
			if err != nil {
				return nil, k, fmt.Errorf("%s: pattern %q: %w", k, p, err)
			}
			out = append(out, re)
		}
		return out, k, nil
	}
	return nil, "", nil
}

// routers collects RegexRouter transforms in the order transforms= lists
// them, translating Java's $1 back-references to Go's ${1}.
func routers(cfg map[string]string) []router {
	var out []router
	for _, name := range strings.Split(cfg["transforms"], ",") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !strings.HasSuffix(cfg["transforms."+name+".type"], "RegexRouter") {
			continue
		}
		re, err := regexp.Compile("^(?:" + cfg["transforms."+name+".regex"] + ")$")
		if err != nil {
			continue
		}
		repl := regexp.MustCompile(`\$(\d+)`).ReplaceAllString(cfg["transforms."+name+".replacement"], "${$1}")
		out = append(out, router{re: re, replacement: repl})
	}
	return out
}

func anyMatch(res []*regexp.Regexp, s string) bool {
	for _, re := range res {
		if re.MatchString(s) {
			return true
		}
	}
	return false
}

func (c *Contract) Pos() model.Pos { return c.pos }

// ValueShape is what the change events look like after this connector's own
// transforms, for the engine to match sink columns against.
func (c *Contract) ValueShape() model.Shape {
	s := c.shape
	if s.Kind == model.ShapeFlattened {
		s.File = c.pos.File
	}
	return s
}

// MySQL reports whether this is Debezium's MySQL or MariaDB connector, whose
// tables are databaseName.tableName and whose migrations are MySQL's.
func (c *Contract) MySQL() bool {
	lc := strings.ToLower(c.Class)
	return strings.Contains(lc, "mysql") || strings.Contains(lc, "mariadb")
}

// DefaultDatabase is the database unqualified MySQL migrations run in, when
// the connector names exactly one: a database.include.list holding a single
// plain name, or failing that a table.include.list whose entries all start
// with the same plain database name. It is "" when the connector does not
// say, and the reader then needs a USE statement.
func (c *Contract) DefaultDatabase() string {
	if dbs := strings.Split(first(c.Config, "database.include.list", "database.whitelist"), ","); len(dbs) == 1 {
		if name, ok := literal(dbs[0]); ok {
			return name
		}
	}
	common := ""
	for _, p := range strings.Split(first(c.Config, "table.include.list", "table.whitelist"), ",") {
		p = strings.TrimSpace(p)
		dot := strings.Index(p, `\.`)
		if dot < 0 {
			dot = strings.Index(p, ".")
		}
		if dot <= 0 {
			return ""
		}
		name, ok := literal(p[:dot])
		if !ok || common != "" && !strings.EqualFold(common, name) {
			return ""
		}
		common = name
	}
	return common
}

// literal reports whether a list entry is a plain name rather than a pattern.
func literal(p string) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" || strings.ContainsAny(p, `.*+?()[]{}|^$\`) {
		return "", false
	}
	return p, true
}

// TableBlock names the list that keeps schema.table out of the stream and
// the entry that would let it in: the database for a MySQL database list,
// the qualified table otherwise.
func (c *Contract) TableBlock(schema, table string) (setting, entry string) {
	if len(c.dbInclude) > 0 && !anyMatch(c.dbInclude, schema) {
		return c.dbIncludeKey, schema
	}
	if len(c.dbExclude) > 0 && anyMatch(c.dbExclude, schema) {
		return c.dbExcludeKey, schema
	}
	return c.tableListKey, schema + "." + table
}

func (c *Contract) CapturesTable(schema, table string) bool {
	if len(c.dbInclude) > 0 && !anyMatch(c.dbInclude, schema) || len(c.dbExclude) > 0 && anyMatch(c.dbExclude, schema) {
		return false
	}
	q := schema + "." + table
	if len(c.tableInclude) > 0 {
		return anyMatch(c.tableInclude, q)
	}
	if len(c.tableExclude) > 0 {
		return !anyMatch(c.tableExclude, q)
	}
	return true
}

func (c *Contract) CapturesColumn(schema, table, column string) bool {
	q := schema + "." + table + "." + column
	if len(c.columnInclude) > 0 {
		return anyMatch(c.columnInclude, q)
	}
	if len(c.columnExclude) > 0 {
		return !anyMatch(c.columnExclude, q)
	}
	return true
}

// Topic is prefix.schema.table, then every RegexRouter in order, the way
// Kafka Connect applies transforms.
func (c *Contract) Topic(schema, table string) string {
	t := schema + "." + table
	if c.prefix != "" {
		t = c.prefix + "." + t
	}
	for _, r := range c.routers {
		if r.re.MatchString(t) {
			t = r.re.ReplaceAllString(t, r.replacement)
		}
	}
	return t
}

func (c *Contract) ColumnListSetting() string { return c.columnListKey }

// ColumnPatterns returns the include-list patterns as written, for the
// captured-column-missing rule. Exclude lists are not returned: a pattern
// there that matches nothing excludes nothing, which is harmless.
func (c *Contract) ColumnPatterns() []string { return c.columnPatterns }

// TablePatterns returns the table include-list patterns as written, for the
// captured-table-missing rule. Exclude lists are left out for the same
// reason as columns.
func (c *Contract) TablePatterns() []string  { return c.tablePatterns }
func (c *Contract) TableListSetting() string { return c.tableListKey }

// autosets reads replica.identity.autoset.values, documented as
// comma-separated "<regex>:<identity>" pairs, identity one of DEFAULT,
// FULL, NOTHING or INDEX <name>. A pair it cannot read makes the whole
// setting unreadable (bad), so the replica-identity rule makes no finding
// rather than judge a table by a setting it misread.
func autosets(v string) (out []autoset, bad bool) {
	for _, pair := range strings.Split(v, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		i := strings.LastIndex(pair, ":")
		if i <= 0 {
			return nil, true
		}
		// Matched like the include lists: whole name, ignoring case.
		re, err := regexp.Compile("(?i)^(?:" + strings.TrimSpace(pair[:i]) + ")$")
		if err != nil {
			return nil, true
		}
		var id model.ReplicaIdentity
		switch value := strings.ToUpper(strings.TrimSpace(pair[i+1:])); {
		case value == "DEFAULT":
			id = model.ReplicaDefault
		case value == "FULL":
			id = model.ReplicaFull
		case value == "NOTHING":
			id = model.ReplicaNothing
		case strings.HasPrefix(value, "INDEX "):
			id = model.ReplicaIndex
		default:
			return nil, true
		}
		out = append(out, autoset{re, id})
	}
	return out, false
}

// ReplicaIdentityOverride is the identity replica.identity.autoset.values
// gives a captured table. set is false when no pair names the table. ok is
// false when the setting cannot be read or two pairs give the table
// different identities: the documentation does not say which wins.
func (c *Contract) ReplicaIdentityOverride(schema, table string) (id model.ReplicaIdentity, set, ok bool) {
	if c.autosetErr {
		return "", true, false
	}
	q := schema + "." + table
	for _, a := range c.autoset {
		if !a.re.MatchString(q) {
			continue
		}
		if set && a.identity != id {
			return "", true, false
		}
		id, set = a.identity, true
	}
	return id, set, true
}

// Plugin is plugin.name, the logical decoding plug-in; decoderbufs when
// unset, Debezium's documented default.
func (c *Contract) Plugin() string {
	if p := strings.TrimSpace(c.Config["plugin.name"]); p != "" {
		return strings.ToLower(p)
	}
	return "decoderbufs"
}
