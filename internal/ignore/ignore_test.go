package ignore

import (
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/avison9/cdclint/internal/model"
)

func scanText(t *testing.T, name, text string) []*Marker {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}
	ms, err := Scan([]string{dir})
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

func TestMarkersAreParsedInBothCommentStyles(t *testing.T) {
	ms := scanText(t, "V2.sql", "ALTER TABLE t ADD c INT; -- cdclint:ignore schema-before-connector, source-column-not-captured: PII\n"+
		"# cdclint:ignore sink-column-not-captured: MySQL comment style\n"+
		"-- cdclint:ignore schema-before-connector\n"+
		"-- a comment that mentions cdclint but is not a marker\n")
	if len(ms) != 3 {
		t.Fatalf("got %d markers, want 3", len(ms))
	}
	if !reflect.DeepEqual(ms[0].Rules, []string{"schema-before-connector", "source-column-not-captured"}) || ms[0].Reason != "PII" || ms[0].Alone {
		t.Errorf("trailing marker = %+v", ms[0])
	}
	if ms[1].Reason != "MySQL comment style" || !ms[1].Alone || ms[1].Pos.Line != 2 {
		t.Errorf("# marker = %+v", ms[1])
	}
	if ms[2].Reason != "" {
		t.Errorf("a marker with no colon has no reason: %+v", ms[2])
	}
}

func TestDownMigrationsAreNotScanned(t *testing.T) {
	if ms := scanText(t, "0001_x.down.sql", "-- cdclint:ignore schema-before-connector: x\n"); len(ms) != 0 {
		t.Errorf("markers in a down file: %d", len(ms))
	}
}

func finding(rule string, line int) model.Finding {
	return model.Finding{Rule: rule, Severity: model.Warning, Pos: model.Pos{File: "f.sql", Line: line}}
}

func known(string) bool { return true }

func TestATrailingMarkerCoversItsLineAndAStandaloneOneTheNext(t *testing.T) {
	trailing := &Marker{Pos: model.Pos{File: "f.sql", Line: 5}, Rules: []string{"r"}, Reason: "why"}
	alone := &Marker{Pos: model.Pos{File: "f.sql", Line: 7}, Rules: []string{"r"}, Reason: "why", Alone: true}
	kept, acked := Apply([]model.Finding{finding("r", 5), finding("r", 6), finding("r", 8)},
		[]*Marker{trailing, alone}, known, known, nil)
	if len(acked) != 2 || acked[0].Finding.Pos.Line != 5 || acked[1].Finding.Pos.Line != 8 {
		t.Errorf("acknowledged %+v", acked)
	}
	if len(kept) != 1 || kept[0].Pos.Line != 6 {
		t.Errorf("kept %+v: the line after a trailing marker must stand", kept)
	}
}

func TestUnusedMarkersAreReportedOnlyForRulesThatJudgedTheState(t *testing.T) {
	diff := &Marker{Pos: model.Pos{File: "f.sql", Line: 1}, Rules: []string{"schema-before-connector"}, Reason: "merged long ago"}
	notRun := &Marker{Pos: model.Pos{File: "f.sql", Line: 2}, Rules: []string{"disabled-rule"}, Reason: "x"}
	stale := &Marker{Pos: model.Pos{File: "f.sql", Line: 3}, Rules: []string{"state-rule"}, Reason: "x"}
	ran := func(r string) bool { return r != "disabled-rule" }
	kept, _ := Apply(nil, []*Marker{diff, notRun, stale}, known, ran, map[string]bool{"schema-before-connector": true})
	if len(kept) != 1 || kept[0].Rule != Rule || kept[0].Pos.Line != 3 {
		t.Errorf("kept %+v, want one ignore-marker warning for the stale state-rule marker", kept)
	}
}
