package engine

import (
	"fmt"
	"strings"

	"github.com/avison9/cdclint/internal/model"
)

// Base is what the same inputs looked like at the pull request's base ref,
// when the run was given one. Only the parts the diff rule compares are
// kept.
type Base struct {
	// Ref names the base in findings: the full commit id when it came
	// from git, or whatever the caller passed.
	Ref    string
	Source *model.Source
	// Contract is the connector as it was at the base. It is nil when the
	// file did not exist there, and also when ConnectorChanged is false,
	// since an unchanged file has the head's decisions and is not parsed
	// twice.
	Contract model.Contract
	// ConnectorChanged is whether the connector file differs from the
	// base as git sees it; a CRLF checkout of an unchanged file is
	// unchanged.
	ConnectorChanged bool
}

// schemaBeforeConnector is the incident itself, judged on the diff: this
// change adds a column to a table the connector captures and leaves the
// column out of the stream without deciding to. The static rules can only
// see the state after the sink change lands, weeks later; this one sees
// the pull request that creates the column, which is the moment the author
// is still there and the fix is one line in the same PR.
//
// It is a WARNING, not an error, because leaving a column off the include
// list is often right (PII, a large blob, a column the warehouse has no
// use for). The finding asks for the decision to be made on purpose.
//
// The decision is judged PER COLUMN. The first release silenced the rule
// whenever the connector file had changed at all, and RefuseRadar's first
// promotion range (#966) showed why that is wrong: #962 grew the
// report_validations include list and #963 added three reports columns,
// and the rule said nothing about reports because "the connector
// changed". An edit for one table says nothing about another, and, one
// step further, adding one new column to the list says nothing about the
// second new column in the same migration. So a new column the head does
// not capture is raised, including when the change narrowed a pattern
// that used to capture everything down to a list that omits it. Two
// things are decided at table level: a table new in the diff, and a
// table the change put on the connector (or the whole connector, when it
// is new at the base), were looked at as a whole and their columns are
// not a surprise.
//
// An exclude-list connector never produces this finding. A new column is
// captured unless a pattern excludes it, and a pattern is a decision:
// either the change named the column there, or a standing pattern
// (public.reports\..*_internal) matched the name it was given. The
// inventory rule still lists the column as information.
//
// A column that a sink already reads is not reported here; the static
// sink-column-not-captured rule reports that as the error it is.
//
// The second return is the set of columns raised, keyed the way
// sourceColumnNotCaptured keys its own, so the inventory does not list a
// column twice.
func schemaBeforeConnector(in *Input, reads []Read) ([]model.Finding, map[string]bool) {
	raised := map[string]bool{}
	if in.Base == nil || in.Base.Source == nil {
		return nil, raised
	}
	if excludes(in.Contract.ColumnListSetting()) {
		return nil, raised
	}
	base := in.Base.Contract
	if !in.Base.ConnectorChanged {
		// git says the file is the same, so the decisions are the head's.
		base = in.Contract
	}
	read := map[string]bool{}
	for _, r := range reads {
		if r.Table != nil && !r.Unreached {
			read[strings.ToLower(r.Table.Qualified()+"."+r.Field)] = true
		}
	}
	var fs []model.Finding
	for _, t := range in.Source.Tables {
		if !in.Contract.CapturesTable(t.Schema, t.Name) {
			continue
		}
		bt := in.Base.Source.Table(t.Schema, t.Name)
		if bt == nil {
			// A table that is new in this diff and already on the include
			// list was added deliberately; its columns are not a surprise.
			continue
		}
		if base == nil || !base.CapturesTable(t.Schema, t.Name) {
			// The change put this table on the connector (or created the
			// connector): the whole table was decided in this change.
			continue
		}
		for _, c := range t.Columns {
			if bt.Column(c.Name) != nil || in.Contract.CapturesColumn(t.Schema, t.Name, c.Name) {
				continue
			}
			if read[strings.ToLower(t.Qualified()+"."+c.Name)] {
				continue
			}
			q := fmt.Sprintf("%s.%s", t.Qualified(), c.Name)
			raised[strings.ToLower(q)] = true
			fs = append(fs, model.Finding{
				Rule: "schema-before-connector", Severity: model.Warning, Pos: c.Pos,
				Message: fmt.Sprintf("this change adds %s to a captured table without adding it to %s in %s (compared with %s)\nthe column will not be in the stream; if a sink is later given it, every row will be the default until a snapshot", q, in.Contract.ColumnListSetting(), in.Contract.Pos().File, short(in.Base.Ref)),
				Fix:     fmt.Sprintf("%s in the same change, or, if it is left off on purpose, say so on the line that adds it: -- cdclint:ignore schema-before-connector: <why>", edit(in.Contract.ColumnListSetting(), q)),
			})
		}
	}
	return fs, raised
}

// excludes reports whether the contract's column list is an exclude list,
// in either of Debezium's spellings.
func excludes(setting string) bool {
	return strings.Contains(setting, "exclude") || strings.Contains(setting, "blacklist")
}

// short abbreviates a full commit id the way git does; anything else (a
// branch name, a path) is shown as given.
func short(ref string) string {
	if len(ref) == 40 && strings.Trim(ref, "0123456789abcdef") == "" {
		return ref[:12]
	}
	return ref
}
