// Package engine ties the three schemas together: it resolves every sink
// table to the source table that feeds it, then runs the rules over the
// result. Readers produce the schemas; this decides what they mean for each
// other.
package engine

import (
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/avison9/cdclint/internal/model"
	"github.com/avison9/cdclint/internal/sink/clickhouse"
	"github.com/avison9/cdclint/internal/sink/connect"
)

// Input is everything one run reads.
type Input struct {
	Source   *model.Source
	Contract model.Contract
	// Patterns, when the contract can list them, lets captured-column-missing
	// and captured-table-missing check each include pattern against the schema.
	Patterns PatternLister
	Sinks    []*model.Sink
	Views    []clickhouse.View
	Mappers  []*connect.Mapper
	// Base is set when --base was given; it enables the diff-aware rule.
	Base *Base
}

// TableBlocker is implemented by contracts with more than one list that can
// keep a table out (MySQL's database lists besides the table list), so a
// finding names the list to edit and what to put in it.
type TableBlocker interface {
	TableBlock(schema, table string) (setting, entry string)
}

// PatternLister is implemented by contracts whose column and table lists are
// sets of patterns that can be checked one by one.
type PatternLister interface {
	ColumnPatterns() []string
	TablePatterns() []string
}

// Read is one sink column resolved to the source column it expects.
type Read struct {
	Sink    *model.SinkTable
	Column  model.Column
	Table   *model.Table // nil when the sink table maps to no source table
	Mapping model.Mapping
	Topic   string
	Mapper  *connect.Mapper
	// Field is the source column the sink column receives: its own name, or
	// the column under after<d> or before<d> in a flattened envelope. It is
	// empty for a column no field reaches (see Unreached).
	Field string
	// Unreached marks a column named after a source column that receives
	// nothing, because the value arrives under after<d> instead.
	Unreached bool
	Shape     model.Shape
}

// ShapeLister is implemented by contracts that know what their events look
// like after their own transforms.
type ShapeLister interface {
	ValueShape() model.Shape
}

// envelopeFields are the flattened envelope's own fields besides before and
// after (Debezium's change event structure; ts_us and ts_ns since 2.6).
var envelopeFields = map[string]bool{"op": true, "ts_ms": true, "ts_us": true, "ts_ns": true}

// field works out which source column a sink column receives under shape.
// skip is true for fields the connector or sink adds around the row.
func field(shape model.Shape, table *model.Table, name string) (col string, unreached, skip bool) {
	if isMetadata(name) {
		return "", false, true
	}
	if shape.Kind != model.ShapeFlattened {
		return name, false, false
	}
	d := shape.Delimiter
	lower := strings.ToLower(name)
	for _, image := range []string{"after", "before"} {
		if strings.HasPrefix(lower, image+d) {
			return name[len(image)+len(d):], false, false
		}
	}
	if envelopeFields[lower] || strings.HasPrefix(lower, "source"+d) || strings.HasPrefix(lower, "transaction"+d) {
		return "", false, true
	}
	if table.Column(name) != nil {
		return "", true, false
	}
	return name, false, false
}

// isMetadata reports whether a sink column is one the connector or the
// sink adds around the row rather than a field of it.
func isMetadata(name string) bool {
	if strings.HasPrefix(name, "_") {
		return true
	}
	switch strings.ToUpper(name) {
	case "RECORD_METADATA", "RECORD_CONTENT":
		return true
	}
	return false
}

// resolve maps every sink table to a source table. Tables it cannot map are
// returned with Table nil so the topic rule can report the ones that named
// a topic nothing produces.
func resolve(in *Input) []Read {
	var reads []Read
	var source model.Shape
	if sl, ok := in.Contract.(ShapeLister); ok {
		source = sl.ValueShape()
	}
	byTopic := map[string]*model.Table{}
	for _, t := range in.Source.Tables {
		byTopic[in.Contract.Topic(t.Schema, t.Name)] = t
	}
	for _, s := range in.Sinks {
		for _, st := range s.Tables {
			var (
				table   *model.Table
				mapping model.Mapping
				mapper  *connect.Mapper
				topic   = st.Topic
			)
			switch {
			case st.Topic != "":
				table = byTopic[st.Topic]
				mapping = model.MapByTopic
			default:
				for _, m := range in.Mappers {
					if m.Kind != connect.Unknown && string(m.Kind) != st.Sink {
						continue
					}
					for tp, t := range byTopic {
						if name, _, ok := m.TableFor(tp); ok && connect.Matches(st.Name, name) {
							table, mapping, mapper, topic = t, model.MapByConnector, m, tp
							break
						}
					}
					if table != nil {
						break
					}
				}
				// A warehouse table with no connector mapping is assumed to
				// be fed from the source table of the same name. ClickHouse
				// is excluded: its ordinary tables are usually materialized
				// view targets with transformed columns, and only its
				// Kafka-engine tables read the stream directly.
				if table == nil && st.Sink != "clickhouse" {
					if t := in.Source.Table("", st.Table); t != nil {
						table, mapping, topic = t, model.MapByName, in.Contract.Topic(t.Schema, t.Name)
					}
				}
			}
			if table == nil {
				if st.Topic != "" {
					reads = append(reads, Read{Sink: st, Mapping: model.MapByTopic, Topic: st.Topic})
				}
				continue
			}
			shape := source
			if mapper != nil {
				shape = mapper.Reshape(source)
			}
			for _, c := range st.Columns {
				f, unreached, skip := field(shape, table, c.Name)
				if skip {
					continue
				}
				reads = append(reads, Read{Sink: st, Column: c, Table: table, Mapping: mapping, Topic: topic, Mapper: mapper,
					Field: f, Unreached: unreached, Shape: shape})
			}
		}
	}
	return reads
}

// Run resolves and applies every rule, returning sorted findings.
func Run(in *Input) []model.Finding {
	reads := resolve(in)
	var fs []model.Finding
	fs = append(fs, topicTableMapping(in, reads)...)
	fs = append(fs, sinkTableNotCaptured(in, reads)...)
	fs = append(fs, sinkColumnNotCaptured(in, reads)...)
	fs = append(fs, sinkColumnUnknown(in, reads)...)
	fs = append(fs, sinkColumnFlattened(reads)...)
	// The diff rule runs before the inventory so a column it raised as a
	// warning is not listed again as information.
	diff, raised := schemaBeforeConnector(in, reads)
	fs = append(fs, diff...)
	fs = append(fs, sourceColumnNotCaptured(in, reads, raised)...)
	fs = append(fs, capturedColumnMissing(in)...)
	fs = append(fs, capturedTableMissing(in)...)
	fs = append(fs, replicaIdentity(in)...)
	fs = append(fs, mvColumnMatch(in)...)
	model.Sort(fs)
	return fs
}

// leftOut words why the connector does not carry a name, for the list
// mode: an include list that does not match it, or an exclude list that
// does. Until v0.2.2 every message said "is not matched by
// column.exclude.list" for a column the exclude list matched, the
// opposite of what happened, and since v0.2.1 that info line is the only
// message a pattern-excluded column gets.
func leftOut(setting string) string {
	if excludes(setting) {
		return "is matched by " + setting
	}
	return "is not matched by " + setting
}

// edit words the remedy for the contract's list mode: a column is added to
// an include list and removed from an exclude list.
func edit(setting, name string) string {
	if excludes(setting) {
		return fmt.Sprintf("remove %s from %s", name, setting)
	}
	return fmt.Sprintf("add %s to %s", name, setting)
}

func how(r Read) string {
	switch r.Mapping {
	case model.MapByTopic:
		return fmt.Sprintf("reads topic %s", r.Topic)
	case model.MapByConnector:
		return fmt.Sprintf("is fed topic %s by %s", r.Topic, r.Mapper.Pos.File)
	default:
		return fmt.Sprintf("is assumed to be fed from %s.%s by table name (no sink connector config maps it)", r.Table.Schema, r.Table.Name)
	}
}

func topicTableMapping(in *Input, reads []Read) []model.Finding {
	var fs []model.Finding
	seen := map[*model.SinkTable]bool{}
	for _, r := range reads {
		if r.Table != nil || seen[r.Sink] {
			continue
		}
		seen[r.Sink] = true
		fs = append(fs, model.Finding{
			Rule: "topic-table-mapping", Severity: model.Error, Pos: r.Sink.Pos,
			Message: fmt.Sprintf("%s reads topic %s, which no table in the source schema produces through %s", r.Sink.Name, r.Topic, in.Contract.Pos().File),
			Fix:     "check the topic name against topic.prefix and any RegexRouter transform, or add the table it expects to the source migrations",
		})
	}
	return fs
}

func sinkTableNotCaptured(in *Input, reads []Read) []model.Finding {
	var fs []model.Finding
	seen := map[*model.SinkTable]bool{}
	for _, r := range reads {
		if r.Table == nil || seen[r.Sink] || in.Contract.CapturesTable(r.Table.Schema, r.Table.Name) {
			continue
		}
		seen[r.Sink] = true
		setting, entry := in.Contract.TableListSetting(), r.Table.Schema+"."+r.Table.Name
		if tb, ok := in.Contract.(TableBlocker); ok {
			setting, entry = tb.TableBlock(r.Table.Schema, r.Table.Name)
		}
		fs = append(fs, model.Finding{
			Rule: "sink-table-not-captured", Severity: model.Error, Pos: r.Sink.Pos,
			Message: fmt.Sprintf("%s %s, but %s %s in %s\nnothing will ever arrive on that topic", r.Sink.Name, how(r), entry, leftOut(setting), in.Contract.Pos().File),
			Fix:     edit(setting, entry) + " and deploy the connector before the sink schema",
		})
	}
	return fs
}

func sinkColumnNotCaptured(in *Input, reads []Read) []model.Finding {
	var fs []model.Finding
	for _, r := range reads {
		if r.Table == nil || !in.Contract.CapturesTable(r.Table.Schema, r.Table.Name) {
			continue
		}
		src := r.Table.Column(r.Field)
		if r.Unreached || src == nil || in.Contract.CapturesColumn(r.Table.Schema, r.Table.Name, src.Name) {
			continue
		}
		q := fmt.Sprintf("%s.%s.%s", r.Table.Schema, r.Table.Name, src.Name)
		fs = append(fs, model.Finding{
			Rule: "sink-column-not-captured", Severity: model.Error, Pos: r.Column.Pos,
			Message: fmt.Sprintf("%s is read by %s (%s) but %s in %s\nevery row will carry the column's default, with no error anywhere", q, r.Sink.Name, how(r), leftOut(in.Contract.ColumnListSetting()), in.Contract.Pos().File),
			Fix:     edit(in.Contract.ColumnListSetting(), q) + ", deploy the connector, then apply the sink schema; rows already written need a snapshot",
		})
	}
	return fs
}

// sinkColumnFlattened raises a sink table whose columns carry the row's
// names while a flatten transform delivers the envelope: every field arrives
// as after<d>column, the sink matches by name, and the columns are left at
// their defaults (ClickHouse/clickhouse-kafka-connect discussion 182). One
// finding per table, since one transform causes all of them.
func sinkColumnFlattened(reads []Read) []model.Finding {
	var fs []model.Finding
	var order []*model.SinkTable
	names := map[*model.SinkTable][]string{}
	first := map[*model.SinkTable]Read{}
	for _, r := range reads {
		if !r.Unreached {
			continue
		}
		if _, ok := names[r.Sink]; !ok {
			order = append(order, r.Sink)
			first[r.Sink] = r
		}
		names[r.Sink] = append(names[r.Sink], r.Column.Name)
	}
	for _, st := range order {
		r := first[st]
		cols := names[st]
		fs = append(fs, model.Finding{
			Rule: "sink-column-flattened", Severity: model.Error, Pos: st.Pos,
			Message: fmt.Sprintf("%s (%s) reads %s by their names in %s.%s, but %s in %s flattens each change event, so every one arrives as after%s<column>\nevery row will carry the columns' defaults, with no error anywhere",
				st.Name, how(r), strings.Join(cols, ", "), r.Table.Schema, r.Table.Name, r.Shape.Transform, r.Shape.File, r.Shape.Delimiter),
			Fix: fmt.Sprintf("rename them after%s%s and so on, or unwrap the event with io.debezium.transforms.ExtractNewRecordState in place of %s",
				r.Shape.Delimiter, cols[0], r.Shape.Transform),
		})
	}
	return fs
}

func sinkColumnUnknown(in *Input, reads []Read) []model.Finding {
	var fs []model.Finding
	for _, r := range reads {
		if r.Table == nil || r.Unreached || r.Table.Column(r.Field) != nil {
			continue
		}
		fs = append(fs, model.Finding{
			Rule: "sink-column-unknown", Severity: model.Warning, Pos: r.Column.Pos,
			Message: fmt.Sprintf("%s expects a field %s that %s.%s does not have (%s)\nif the connector renames or computes it, this is fine; otherwise it will be the default on every row", r.Sink.Name, r.Column.Name, r.Table.Schema, r.Table.Name, how(r)),
		})
	}
	return fs
}

func sourceColumnNotCaptured(in *Input, reads []Read, raised map[string]bool) []model.Finding {
	var fs []model.Finding
	declared := map[string]bool{}
	for _, r := range reads {
		if r.Table != nil && !r.Unreached {
			declared[strings.ToLower(r.Table.Qualified()+"."+r.Field)] = true
		}
	}
	for _, t := range in.Source.Tables {
		if !in.Contract.CapturesTable(t.Schema, t.Name) {
			continue
		}
		for _, c := range t.Columns {
			if in.Contract.CapturesColumn(t.Schema, t.Name, c.Name) || declared[strings.ToLower(t.Qualified()+"."+c.Name)] {
				continue
			}
			if raised[strings.ToLower(t.Qualified()+"."+c.Name)] {
				continue
			}
			fs = append(fs, model.Finding{
				Rule: "source-column-not-captured", Severity: model.Info, Pos: c.Pos,
				Message: fmt.Sprintf("%s.%s exists and %s in %s; nothing reads it yet\nthe day a sink asks for it is the day it is found missing", t.Qualified(), c.Name, leftOut(in.Contract.ColumnListSetting()), in.Contract.Pos().File),
			})
		}
	}
	return fs
}

// capturedColumnMissing checks each include-list pattern against the
// schema. A pattern that matches nothing is usually a typo or a column that
// was dropped, and Debezium does not complain.
func capturedColumnMissing(in *Input) []model.Finding {
	if in.Patterns == nil {
		return nil
	}
	var fs []model.Finding
	for _, p := range in.Patterns.ColumnPatterns() {
		for _, name := range expand(p) {
			if matchesAny(in, name) {
				continue
			}
			fs = append(fs, model.Finding{
				Rule: "captured-column-missing", Severity: model.Warning, Pos: in.Contract.Pos(),
				Message: fmt.Sprintf("%s in %s matches no column in the source schema", name, in.Contract.ColumnListSetting()),
				Fix:     "remove it, or check the spelling against the migrations",
			})
		}
	}
	return fs
}

// capturedTableMissing checks each table include-list entry against the
// schema. Debezium logs a warning when an entry matches no table and keeps
// running; debezium/dbz#872 asks it to fail instead, and a maintainer
// answered that a table may be created later. That is why this is a warning:
// when a sink reads the table, sink-table-not-captured raises the error.
//
// Two shapes get a pointed fix because they are how people get it wrong in
// practice (Stack Overflow 74103659 and 51345636): an entry without its schema,
// and a shell glob. Both follow from Debezium's documented matching: each
// entry is a regular expression matched against the whole schema.table name,
// never a substring.
func capturedTableMissing(in *Input) []model.Finding {
	if in.Patterns == nil {
		return nil
	}
	var fs []model.Finding
	for _, p := range in.Patterns.TablePatterns() {
		for _, name := range expand(p) {
			m, err := compileAnchored(name)
			if err != nil || tablesMatching(in, func(t *model.Table) bool { return m(t.Qualified()) }) != nil {
				continue
			}
			message := fmt.Sprintf("%s in %s matches no table in the source schema", name, in.Contract.TableListSetting())
			fix := "remove it, or check the spelling against the migrations"
			if bare := tablesMatching(in, func(t *model.Table) bool { return m(t.Name) }); bare != nil {
				message += "\nDebezium matches each entry against the whole schema.table name"
				var qualified []string
				for _, t := range bare {
					qualified = append(qualified, regexp.QuoteMeta(t.Qualified()))
				}
				fix = "write " + strings.Join(qualified, " or ") + ", or remove it"
			} else if re, caught := globReading(in, name); caught != nil {
				message += "\nDebezium reads each entry as a regular expression, where * repeats the character before it"
				var names []string
				for _, t := range caught {
					names = append(names, t.Qualified())
				}
				fix = "write " + re + " to capture " + joinAnd(names) + ", or remove it"
			}
			fs = append(fs, model.Finding{
				Rule: "captured-table-missing", Severity: model.Warning, Pos: in.Contract.Pos(),
				Message: message,
				Fix:     fix,
			})
		}
	}
	return fs
}

// tablesMatching returns the source tables for which match is true, in
// qualified-name order, or nil when there are none.
func tablesMatching(in *Input, match func(*model.Table) bool) []*model.Table {
	var out []*model.Table
	for _, t := range in.Source.Tables {
		if match(t) {
			out = append(out, t)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Qualified() < out[j].Qualified() })
	return out
}

// globReading reads an entry the way its author most likely meant it, as a
// shell glob where * is any run of characters, and returns the regular
// expression that says so and the tables it would capture. An entry that
// already contains .* was written as a regular expression and is left alone.
func globReading(in *Input, entry string) (string, []*model.Table) {
	if !strings.Contains(entry, "*") || strings.Contains(entry, ".*") {
		return "", nil
	}
	re := strings.ReplaceAll(regexp.QuoteMeta(entry), `\*`, ".*")
	m, err := compileAnchored(re)
	if err != nil {
		return "", nil
	}
	return re, tablesMatching(in, func(t *model.Table) bool { return m(t.Qualified()) })
}

// joinAnd lists names as "a", "a and b" or "a, b and c".
func joinAnd(names []string) string {
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " and " + names[len(names)-1]
}

// expand turns the common `schema.table\.(a|b|c)` shape into one pattern
// per alternative so each column is checked on its own. Any other shape is
// returned as is.
func expand(p string) []string {
	open := strings.LastIndex(p, "(")
	if open < 0 || !strings.HasSuffix(p, ")") {
		return []string{p}
	}
	inner := p[open+1 : len(p)-1]
	if strings.ContainsAny(inner, "()[]*+?.\\") {
		return []string{p}
	}
	var out []string
	for _, alt := range strings.Split(inner, "|") {
		out = append(out, p[:open]+alt)
	}
	return out
}

func matchesAny(in *Input, pattern string) bool {
	m, err := compileAnchored(pattern)
	if err != nil {
		return true
	}
	for _, t := range in.Source.Tables {
		for _, c := range t.Columns {
			if m(t.Qualified() + "." + c.Name) {
				return true
			}
		}
	}
	return false
}
