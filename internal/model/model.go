// Package model holds the three schemas cdclint compares and the finding it
// produces when they disagree. Every reader produces one of these; every
// rule consumes them. Nothing in here knows about files or flags.
package model

import (
	"fmt"
	"sort"
	"strings"
)

// Pos is where something was declared: a file path as given on the command
// line and a 1-based line. Findings point at declarations, not at abstract
// objects, because the fix is an edit.
type Pos struct {
	File string
	Line int
}

func (p Pos) String() string {
	if p.File == "" {
		return ""
	}
	if p.Line == 0 {
		return p.File
	}
	return fmt.Sprintf("%s:%d", p.File, p.Line)
}

// Column is one column of a source table. Type is the declared type text,
// kept verbatim; rules that compare types normalise it themselves.
type Column struct {
	Name string
	Type string
	Pos  Pos
}

// ReplicaIdentity is Postgres' REPLICA IDENTITY setting for a table, which
// decides what a change event carries for the old row.
type ReplicaIdentity string

const (
	ReplicaDefault ReplicaIdentity = "DEFAULT"
	ReplicaFull    ReplicaIdentity = "FULL"
	ReplicaNothing ReplicaIdentity = "NOTHING"
	ReplicaIndex   ReplicaIdentity = "USING INDEX"
)

// Table is one source table after every migration has been applied in
// order. Columns keeps declaration order.
type Table struct {
	Schema          string
	Name            string
	Columns         []Column
	PrimaryKey      []string
	ReplicaIdentity ReplicaIdentity
	// KeyUnknown is set when a statement the reader does not run may have
	// given the table its primary key or replica identity: a DO block that
	// alters it (Mattermost adds reactions' key that way), or PRIMARY KEY
	// USING INDEX. Rules that depend on the key make no finding then.
	KeyUnknown bool
	Pos        Pos
}

// Qualified is schema.name, the form Debezium matches include lists against.
func (t *Table) Qualified() string { return t.Schema + "." + t.Name }

// Column returns the named column, case-insensitively, or nil.
func (t *Table) Column(name string) *Column {
	for i := range t.Columns {
		if strings.EqualFold(t.Columns[i].Name, name) {
			return &t.Columns[i]
		}
	}
	return nil
}

// Source is the whole source schema.
type Source struct {
	Tables []*Table
}

// Table returns the table with this schema and name, case-insensitively.
// An unqualified name matches any schema; the first match wins.
func (s *Source) Table(schema, name string) *Table {
	for _, t := range s.Tables {
		if strings.EqualFold(t.Name, name) && (schema == "" || strings.EqualFold(t.Schema, schema)) {
			return t
		}
	}
	return nil
}

// Contract is what the capture connector will emit. It answers the two
// questions every rule asks: is this table captured, is this column
// captured, and one the sinks need: what topic does this table land on.
type Contract interface {
	// Pos is where the contract was declared, for findings.
	Pos() Pos
	// CapturesTable reports whether change events for schema.table are
	// emitted at all.
	CapturesTable(schema, table string) bool
	// CapturesColumn reports whether the column is present in those events.
	// It is only meaningful when CapturesTable is true.
	CapturesColumn(schema, table, column string) bool
	// Topic is the topic events for schema.table are routed to.
	Topic(schema, table string) string
	// ColumnListSetting names the config key a column would have to be added
	// to (or removed from), so a finding can say exactly what to edit.
	ColumnListSetting() string
	// TableListSetting is the same for tables.
	TableListSetting() string
}

// Mapping says how a sink table was tied to a source table, so the finding
// can say whether it trusted a connector config or guessed by name.
type Mapping string

const (
	MapByTopic     Mapping = "topic"      // the sink named the topic it reads
	MapByConnector Mapping = "connector"  // a sink connector config mapped topic to table
	MapByName      Mapping = "table-name" // no mapping given; same table name assumed
)

// SinkTable is one warehouse table that reads the stream, with the columns
// it expects to receive. Topic is set when the sink itself names it; Source
// is resolved by the engine.
type SinkTable struct {
	Sink    string // "clickhouse", "bigquery", "snowflake", "iceberg"
	Name    string // as declared, qualified
	Table   string // the last segment of Name
	Columns []Column
	Topic   string
	Pos     Pos
}

// Sink is everything one sink reader found.
type Sink struct {
	Tables []*SinkTable
}

// Severity orders findings and decides the exit code.
type Severity int

const (
	Info Severity = iota
	Warning
	Error
)

func (s Severity) String() string {
	switch s {
	case Error:
		return "error"
	case Warning:
		return "warning"
	default:
		return "info"
	}
}

// Finding is one disagreement. Message is the sentence, Fix the edit, and
// both are written for a person reading a CI log with the files open.
type Finding struct {
	Rule     string
	Severity Severity
	Pos      Pos
	Message  string
	Fix      string
}

// Sort orders findings for stable output: by file, line, rule, message.
func Sort(fs []Finding) {
	sort.SliceStable(fs, func(i, j int) bool {
		a, b := fs[i], fs[j]
		if a.Pos.File != b.Pos.File {
			return a.Pos.File < b.Pos.File
		}
		if a.Pos.Line != b.Pos.Line {
			return a.Pos.Line < b.Pos.Line
		}
		if a.Rule != b.Rule {
			return a.Rule < b.Rule
		}
		return a.Message < b.Message
	})
}

// Format renders one finding in the text output shape.
func (f Finding) Format() string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s", f.Severity, f.Rule)
	if p := f.Pos.String(); p != "" {
		fmt.Fprintf(&b, " %s", p)
	}
	b.WriteString("\n")
	for _, line := range strings.Split(f.Message, "\n") {
		fmt.Fprintf(&b, "  %s\n", line)
	}
	if f.Fix != "" {
		fmt.Fprintf(&b, "  fix: %s\n", f.Fix)
	}
	return b.String()
}

// ShapeKind says what a change event's value looks like when a sink reads it.
type ShapeKind int

const (
	// ShapeUnknown: a transform cdclint does not model changed the value, or
	// the connector is not one whose output it knows. Sink columns are taken
	// as the row's column names, as they always were.
	ShapeUnknown ShapeKind = iota
	// ShapeEnvelope: Debezium's change event, before/after/source/op/ts_ms.
	ShapeEnvelope
	// ShapeRow: the row itself, unwrapped from the envelope.
	ShapeRow
	// ShapeFlattened: the envelope flattened into after<d>column,
	// before<d>column, source<d>field, op, ts_ms.
	ShapeFlattened
)

// Shape is the value a sink receives after every transform on the way.
type Shape struct {
	Kind ShapeKind
	// Delimiter joins envelope and column names when Kind is ShapeFlattened.
	Delimiter string
	// Transform and File name the flatten transform, for findings.
	Transform string
	File      string
}
