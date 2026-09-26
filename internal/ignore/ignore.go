// Package ignore reads cdclint:ignore markers: a comment in a migration or a
// sink file that records a decision about one finding, so the finding stops
// failing the run while the decision stays in the file, next to the column,
// where the pull request that made it shows it.
//
//	ALTER TABLE users
//	    ADD COLUMN ssn_hash TEXT; -- cdclint:ignore schema-before-connector: PII, never streamed
//
// A marker names one or more rules and gives a reason after a colon. After
// code on the same line it covers that line only; on a line of its own it
// covers the line below. A trailing marker must not reach the next line:
// on a two-column ALTER it would silently acknowledge the column that was
// forgotten. The reason is required: it is the record of the decision.
package ignore

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/avison9/cdclint/internal/migrate"
	"github.com/avison9/cdclint/internal/model"
)

// Rule is the name of the findings this package raises about markers.
const Rule = "ignore-marker"

// Ack is a finding a marker acknowledged, with the marker's reason.
type Ack struct {
	Finding model.Finding
	Reason  string
}

// Marker is one cdclint:ignore comment.
type Marker struct {
	Pos    model.Pos
	Rules  []string
	Reason string
	// Alone is true for a marker on a line of its own, which covers the
	// line below; a marker after code covers its own line.
	Alone bool
	used  bool
}

// Covers reports whether the marker applies to a finding at line.
func (m *Marker) Covers(line int) bool {
	if m.Alone {
		return line == m.Pos.Line+1
	}
	return line == m.Pos.Line
}

var marker = regexp.MustCompile(`(?:--|#)\s*cdclint:ignore\b(.*)$`)

// Scan reads the *.sql files directly in each dir (the readers' own
// selection, down migrations left out) for markers.
func Scan(dirs []string) ([]*Marker, error) {
	var out []*Marker
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		var names []string
		for _, e := range entries {
			if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".sql") && !migrate.Down(e.Name()) {
				names = append(names, e.Name())
			}
		}
		sort.Strings(names)
		for _, n := range names {
			path := filepath.Join(dir, n)
			f, err := os.Open(path)
			if err != nil {
				return nil, err
			}
			sc := bufio.NewScanner(f)
			sc.Buffer(make([]byte, 64*1024), 4*1024*1024)
			for line := 1; sc.Scan(); line++ {
				text := sc.Text()
				if loc := marker.FindStringSubmatchIndex(text); loc != nil {
					mk := parse(model.Pos{File: path, Line: line}, text[loc[2]:loc[3]])
					mk.Alone = strings.TrimSpace(text[:loc[0]]) == ""
					out = append(out, mk)
				}
			}
			err = sc.Err()
			f.Close()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", path, err)
			}
		}
	}
	return out, nil
}

func parse(pos model.Pos, rest string) *Marker {
	m := &Marker{Pos: pos}
	rules := rest
	if i := strings.Index(rest, ":"); i >= 0 {
		rules, m.Reason = rest[:i], strings.TrimSpace(rest[i+1:])
	}
	for _, r := range strings.FieldsFunc(rules, func(c rune) bool { return c == ',' || c == ' ' || c == '\t' }) {
		m.Rules = append(m.Rules, r)
	}
	return m
}

// Apply splits findings into the ones that stand and the ones a marker
// acknowledges, and adds a warning for each marker that is malformed or,
// for a rule that ran and judges the current state, covers nothing.
// ran reports whether a rule ran this time; diffRules are the rules that
// judge the change rather than the state, whose markers go quiet once the
// change that added them has merged and are therefore never unused.
func Apply(findings []model.Finding, markers []*Marker, known, ran func(string) bool, diffRules map[string]bool) (kept []model.Finding, acknowledged []Ack) {
	valid := func(m *Marker) bool { return m.Reason != "" && len(m.Rules) > 0 && allKnown(m.Rules, known) }
	for _, f := range findings {
		var by *Marker
		for _, m := range markers {
			if valid(m) && m.Pos.File == f.Pos.File && m.Covers(f.Pos.Line) && contains(m.Rules, f.Rule) {
				by = m
				break
			}
		}
		if by == nil {
			kept = append(kept, f)
			continue
		}
		by.used = true
		acknowledged = append(acknowledged, Ack{Finding: f, Reason: by.Reason})
	}
	for _, m := range markers {
		switch {
		case len(m.Rules) == 0:
			kept = append(kept, problem(m, "a cdclint:ignore marker names no rule, so it acknowledges nothing",
				"name the rule and the reason: -- cdclint:ignore <rule>: <why>"))
		case !allKnown(m.Rules, known):
			kept = append(kept, problem(m, fmt.Sprintf("cdclint:ignore names %s, which is not a rule, so it acknowledges nothing", strings.Join(unknown(m.Rules, known), ", ")),
				"use one of the rule names cdclint --help lists"))
		case m.Reason == "":
			kept = append(kept, problem(m, fmt.Sprintf("cdclint:ignore %s gives no reason, so it acknowledges nothing; the reason is the record of the decision", strings.Join(m.Rules, ",")),
				fmt.Sprintf("say why after a colon: -- cdclint:ignore %s: <why>", strings.Join(m.Rules, ","))))
		case !m.used && judgedNow(m.Rules, ran, diffRules):
			kept = append(kept, problem(m, fmt.Sprintf("cdclint:ignore %s covers no finding %s", strings.Join(m.Rules, ","), map[bool]string{true: "on the line below it", false: "on its line"}[m.Alone]),
				"put it after the code on the line the finding points at, or alone on the line above it, or remove it if the finding is gone"))
		}
	}
	model.Sort(kept)
	return kept, acknowledged
}

// judgedNow is true when every rule the marker names ran this time and
// judges the current state, so a marker that covers nothing is stale.
func judgedNow(rules []string, ran func(string) bool, diffRules map[string]bool) bool {
	for _, r := range rules {
		if diffRules[r] || !ran(r) {
			return false
		}
	}
	return true
}

func problem(m *Marker, message, fix string) model.Finding {
	return model.Finding{Rule: Rule, Severity: model.Warning, Pos: m.Pos, Message: message, Fix: fix}
}

func allKnown(rules []string, known func(string) bool) bool {
	return len(unknown(rules, known)) == 0
}

func unknown(rules []string, known func(string) bool) []string {
	var out []string
	for _, r := range rules {
		if !known(r) {
			out = append(out, r)
		}
	}
	return out
}

func contains(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}
