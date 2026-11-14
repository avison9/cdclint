package debezium

import (
	"testing"

	"github.com/avison9/cdclint/internal/model"
)

func TestIncludeListsAndTopics(t *testing.T) {
	c, err := Parse([]byte(`{"name":"pg","config":{
		"connector.class":"io.debezium.connector.postgresql.PostgresConnector",
		"topic.prefix":"rr",
		"table.include.list":"public.reports,public.users",
		"column.include.list":"public.reports\\.(id|category),public.users.id",
		"transforms":"route",
		"transforms.route.type":"org.apache.kafka.connect.transforms.RegexRouter",
		"transforms.route.regex":"rr\\.public\\.(.*)",
		"transforms.route.replacement":"cdc.$1"
	}}`))
	if err != nil {
		t.Fatal(err)
	}
	if !c.CapturesTable("public", "reports") || c.CapturesTable("public", "media") {
		t.Error("table include list misjudged")
	}
	if !c.CapturesColumn("public", "reports", "id") || !c.CapturesColumn("public", "reports", "CATEGORY") {
		t.Error("column include list misjudged an included column")
	}
	if c.CapturesColumn("public", "reports", "reference_number") {
		t.Error("column include list matched a column it does not list")
	}
	if got := c.Topic("public", "reports"); got != "cdc.reports" {
		t.Errorf("topic = %q, want cdc.reports", got)
	}
	if c.ColumnListSetting() != "column.include.list" {
		t.Errorf("column list key = %q", c.ColumnListSetting())
	}
}

func TestNoListsCapturesEverything(t *testing.T) {
	c, err := Parse([]byte(`{"connector.class":"x","topic.prefix":"p"}`))
	if err != nil {
		t.Fatal(err)
	}
	if !c.CapturesTable("a", "b") || !c.CapturesColumn("a", "b", "c") {
		t.Error("empty lists should capture everything")
	}
	if c.Topic("a", "b") != "p.a.b" {
		t.Errorf("topic = %q", c.Topic("a", "b"))
	}
}

func TestExcludeList(t *testing.T) {
	c, _ := Parse([]byte(`{"column.exclude.list":"public.users.password_hash"}`))
	if c.CapturesColumn("public", "users", "password_hash") || !c.CapturesColumn("public", "users", "id") {
		t.Error("exclude list misjudged")
	}
	if c.ColumnListSetting() != "column.exclude.list" {
		t.Errorf("column list key = %q", c.ColumnListSetting())
	}
}

func TestReplicaIdentityAutosetValues(t *testing.T) {
	// The documented form: comma-separated <regex>:<identity> pairs.
	c, err := Parse([]byte(`{"config":{"replica.identity.autoset.values":"public.audit_.*:FULL, public.orders:NOTHING, s2.t3:INDEX idx_name"}}`))
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		schema, table string
		id            model.ReplicaIdentity
		set           bool
	}{
		{"public", "audit_events", model.ReplicaFull, true},
		{"public", "ORDERS", model.ReplicaNothing, true},
		{"s2", "t3", model.ReplicaIndex, true},
		{"public", "users", "", false},
	} {
		id, set, ok := c.ReplicaIdentityOverride(tc.schema, tc.table)
		if !ok || set != tc.set || id != tc.id {
			t.Errorf("%s.%s: id %q set %v ok %v, want %q %v true", tc.schema, tc.table, id, set, ok, tc.id, tc.set)
		}
	}
	if c.Plugin() != "decoderbufs" {
		t.Errorf("plugin = %q, want the documented default decoderbufs", c.Plugin())
	}
	// Two pairs that disagree about one table, and a pair that cannot be
	// read, both leave the identity unknown rather than guessed.
	two, _ := Parse([]byte(`{"config":{"replica.identity.autoset.values":"public.*:FULL,public.orders:NOTHING"}}`))
	if _, set, ok := two.ReplicaIdentityOverride("public", "orders"); !set || ok {
		t.Errorf("conflicting pairs: set %v ok %v, want set and not ok", set, ok)
	}
	bad, _ := Parse([]byte(`{"config":{"replica.identity.autoset.values":"public.orders:EVERYTHING"}}`))
	if _, _, ok := bad.ReplicaIdentityOverride("public", "users"); ok {
		t.Error("an unreadable setting should not be trusted for any table")
	}
}
