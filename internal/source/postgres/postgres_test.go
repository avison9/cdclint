package postgres

import (
	"testing"

	"github.com/avison9/cdclint/internal/model"
)

func TestAppliesMigrationsInOrder(t *testing.T) {
	src := &model.Source{}
	must := func(file, sql string) {
		t.Helper()
		if err := Apply(src, file, sql); err != nil {
			t.Fatal(err)
		}
	}
	must("0001.sql", `
CREATE TABLE IF NOT EXISTS reports (
    id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    reporter_user_id UUID NOT NULL REFERENCES users(id),
    "quoted col" TEXT,
    location GEOGRAPHY(POINT, 4326) NOT NULL,
    CONSTRAINT reports_check CHECK (char_length(description) < 2000)
);
CREATE TABLE public.users (id UUID, PRIMARY KEY (id));
ALTER TABLE reports REPLICA IDENTITY FULL;`)
	must("0002.sql", `
ALTER TABLE reports ADD COLUMN IF NOT EXISTS reference_number BIGINT, DROP COLUMN "quoted col";
ALTER TABLE ONLY reports RENAME COLUMN location TO geom;
ALTER TABLE reports ALTER COLUMN reference_number SET DATA TYPE INTEGER;
ALTER TABLE reports ADD CONSTRAINT x UNIQUE (reference_number);
DROP TABLE IF EXISTS users;`)

	r := src.Table("public", "reports")
	if r == nil {
		t.Fatal("reports missing")
	}
	var names []string
	for _, c := range r.Columns {
		names = append(names, c.Name+":"+c.Type)
	}
	want := "[id:UUID reporter_user_id:UUID geom:GEOGRAPHY(POINT, 4326) reference_number:INTEGER]"
	if got := formatSlice(names); got != want {
		t.Errorf("columns = %s, want %s", got, want)
	}
	if r.ReplicaIdentity != model.ReplicaFull {
		t.Errorf("replica identity = %s, want FULL", r.ReplicaIdentity)
	}
	if len(r.PrimaryKey) != 1 || r.PrimaryKey[0] != "id" {
		t.Errorf("primary key = %v", r.PrimaryKey)
	}
	if src.Table("public", "users") != nil {
		t.Error("users should have been dropped")
	}
	if r.Columns[3].Pos.File != "0002.sql" || r.Columns[3].Pos.Line != 2 {
		t.Errorf("reference_number pos = %+v", r.Columns[3].Pos)
	}
}

func formatSlice(s []string) string {
	out := "["
	for i, x := range s {
		if i > 0 {
			out += " "
		}
		out += x
	}
	return out + "]"
}

func TestANameGluedToAMultiLineColumnListDoesNotPanic(t *testing.T) {
	// Mattermost 000016_create_reactions.up.sql, as written.
	src := &model.Source{}
	text := "CREATE TABLE IF NOT EXISTS reactions(\n    userid VARCHAR(26) NOT NULL,\n    postid VARCHAR(26) NOT NULL\n);"
	if err := Apply(src, "000016_create_reactions.up.sql", text); err != nil {
		t.Fatal(err)
	}
	r := src.Table("public", "reactions")
	if r == nil || len(r.Columns) != 2 || r.Columns[0].Name != "userid" || r.Columns[1].Name != "postid" {
		t.Fatalf("public.reactions = %+v", r)
	}
	if r.Columns[1].Pos.Line != 3 {
		t.Errorf("postid line = %d, want 3", r.Columns[1].Pos.Line)
	}
}

func TestColumnsAnAlterAddsAreOnTheirOwnLines(t *testing.T) {
	src := &model.Source{}
	text := "CREATE TABLE reports (id UUID);\n" +
		"ALTER TABLE reports\n" +
		"    ADD COLUMN IF NOT EXISTS cleared_at TIMESTAMPTZ,\n" +
		"    -- a comment between actions\n" +
		"    ADD COLUMN cleared_by UUID REFERENCES users(id),\n" +
		"    ADD COLUMN reports_note TEXT;\n" +
		"ALTER TABLE reports ADD COLUMN one_line INT;\n"
	if err := Apply(src, "0141.sql", text); err != nil {
		t.Fatal(err)
	}
	r := src.Table("public", "reports")
	for name, want := range map[string]int{"cleared_at": 3, "cleared_by": 5, "reports_note": 6, "one_line": 7} {
		if c := r.Column(name); c == nil || c.Pos.Line != want {
			t.Errorf("%s at %+v, want line %d", name, c, want)
		}
	}
}
