package main

import (
	"bytes"
	"strings"
	"testing"
)

func cli(t *testing.T, args ...string) (int, string, string) {
	t.Helper()
	var out, errOut bytes.Buffer
	code := run(args, &out, &errOut)
	return code, out.String(), errOut.String()
}

func entry(name string) []string {
	d := "../../corpus/" + name
	return []string{"--migrations", d + "/migrations", "--connector", d + "/connector.json", "--sink", d + "/sink"}
}

func TestDisableHidesARuleAndSaysSo(t *testing.T) {
	// column-never-captured reports two source-column-not-captured infos
	// and nothing else.
	code, out, _ := cli(t, append(entry("column-never-captured"), "--disable", "source-column-not-captured")...)
	want := "ok: nothing to report outside the disabled rules\n" +
		"not shown (--disable): source-column-not-captured 2\n"
	if code != 0 || out != want {
		t.Fatalf("exit %d, output:\n%s\nwant:\n%s", code, out, want)
	}
}

func TestADisabledRuleDoesNotFailTheRun(t *testing.T) {
	// include-list-typo: one sink-column-not-captured error and one
	// captured-column-missing warning; exit 1 at the default --fail-on error.
	if code, _, _ := cli(t, entry("include-list-typo")...); code != 1 {
		t.Fatalf("without --disable: exit %d, want 1", code)
	}
	code, out, _ := cli(t, append(entry("include-list-typo"), "--disable", "sink-column-not-captured")...)
	if code != 0 {
		t.Errorf("exit %d, want 0: the only error was disabled", code)
	}
	if !strings.Contains(out, "warning captured-column-missing") || strings.Contains(out, "sink-column-not-captured include-list") {
		t.Errorf("output:\n%s", out)
	}
	if !strings.HasSuffix(out, "0 error(s), 1 warning(s), 0 info\nnot shown (--disable): sink-column-not-captured 1\n") {
		t.Errorf("summary:\n%s", out)
	}
}

func TestDisableTakesCommasAndRepeats(t *testing.T) {
	a, outA, _ := cli(t, append(entry("include-list-typo"), "--disable", "sink-column-not-captured,captured-column-missing")...)
	b, outB, _ := cli(t, append(entry("include-list-typo"), "--disable", "sink-column-not-captured", "--disable", " captured-column-missing ")...)
	want := "ok: nothing to report outside the disabled rules\n" +
		"not shown (--disable): sink-column-not-captured 1, captured-column-missing 1\n"
	if a != 0 || b != 0 || outA != want || outB != want {
		t.Fatalf("commas: exit %d\n%s\nrepeats: exit %d\n%s\nwant:\n%s", a, outA, b, outB, want)
	}
}

func TestAnUnknownRuleIsAnErrorNotASilentFilter(t *testing.T) {
	code, out, errOut := cli(t, append(entry("include-list-typo"), "--disable", "source-column-not-capturd")...)
	if code != 2 || out != "" {
		t.Fatalf("exit %d, stdout %q", code, out)
	}
	if !strings.Contains(errOut, `unknown rule "source-column-not-capturd"`) || !strings.Contains(errOut, "source-column-not-captured") {
		t.Errorf("stderr: %s", errOut)
	}
}

func TestDisablingTheDiffRuleSkipsTheBase(t *testing.T) {
	// A ref that does not exist fails the base load; with the diff rule
	// disabled the base is never read, exactly as without --base.
	if code, _, _ := cli(t, append(entry("clean"), "--base", "no-such-ref-cdclint")...); code != 2 {
		t.Fatalf("the base should have been read and failed: exit %d", code)
	}
	code, out, errOut := cli(t, append(entry("clean"), "--base", "no-such-ref-cdclint", "--disable", "schema-before-connector")...)
	if code != 0 || out != "ok: source, connector and sink agree\n" {
		t.Fatalf("exit %d, stdout %q, stderr %q", code, out, errOut)
	}
}

func TestJSONKeepsItsShapeAndNotesOnStderr(t *testing.T) {
	code, out, errOut := cli(t, append(entry("include-list-typo"), "--format", "json", "--disable", "sink-column-not-captured")...)
	if code != 0 || !strings.HasPrefix(strings.TrimSpace(out), "[") || strings.Contains(out, "sink-column-not-captured") {
		t.Fatalf("exit %d, stdout:\n%s", code, out)
	}
	if errOut != "cdclint: not shown (--disable): sink-column-not-captured 1\n" {
		t.Errorf("stderr %q", errOut)
	}
}
