package engine

import (
	"fmt"

	"github.com/avison9/cdclint/internal/model"
)

// ReplicaIdentityContract is implemented by contracts that can override a
// table's replica identity (Debezium's replica.identity.autoset.values)
// and name the logical decoding plug-in they read through.
type ReplicaIdentityContract interface {
	ReplicaIdentityOverride(schema, table string) (id model.ReplicaIdentity, set, ok bool)
	Plugin() string
}

// replicaIdentity raises a captured Postgres table with no replica
// identity: REPLICA IDENTITY NOTHING, or DEFAULT with no primary key.
// Both consequences are documented, not inferred. Postgres: "Tables with a
// replica identity defined as NOTHING, DEFAULT without a primary key, or
// USING INDEX with a dropped index, cannot support UPDATE or DELETE
// operations when included in a publication replicating these actions"
// (logical-replication-publication), which is the pgoutput case. Debezium
// (PostgreSQL connector, "Replica identity"): with DEFAULT and no primary
// key "the connector does not emit UPDATE or DELETE events for that
// table", and with NOTHING its update and delete events "do not contain
// any information about the previous value of any table column".
//
// USING INDEX is not judged: whether the index still exists is not in the
// files. Neither is a table whose key may be set where the reader cannot
// see (KeyUnknown), nor one whose connector override cannot be read. A
// warning, not an error: a table that only ever takes inserts loses
// nothing, and the fix says how to record that.
func replicaIdentity(in *Input) []model.Finding {
	rc, _ := in.Contract.(ReplicaIdentityContract)
	var fs []model.Finding
	for _, t := range in.Source.Tables {
		// MySQL tables carry no replica identity; the setting is Postgres'.
		if t.ReplicaIdentity == "" || t.KeyUnknown || !in.Contract.CapturesTable(t.Schema, t.Name) {
			continue
		}
		id, pos, overridden := t.ReplicaIdentity, t.Pos, false
		if rc != nil {
			o, set, ok := rc.ReplicaIdentityOverride(t.Schema, t.Name)
			if !ok {
				continue
			}
			if set {
				// The connector sets it on every start, so the
				// override is what the stream sees, and the
				// connector file is what to edit.
				id, pos, overridden = o, in.Contract.Pos(), true
			}
		}
		keyed := len(t.PrimaryKey) > 0
		var what string
		switch {
		case id == model.ReplicaNothing:
			what = "REPLICA IDENTITY NOTHING"
		case id == model.ReplicaDefault && !keyed:
			what = "REPLICA IDENTITY DEFAULT and no primary key"
		default:
			continue
		}
		q := t.Qualified()
		if overridden {
			what += fmt.Sprintf(", set by replica.identity.autoset.values in %s", in.Contract.Pos().File)
		}
		var consequence string
		switch {
		case rc != nil && rc.Plugin() == "pgoutput":
			consequence = "Postgres rejects UPDATE and DELETE on a table with no replica identity while a publication replicates them, and this connector reads through one (plugin.name pgoutput): the application's own writes to it fail"
		case id == model.ReplicaNothing:
			consequence = "Debezium's update and delete events for it carry no previous values, so a delete has no before to say which row went"
		default:
			consequence = "Debezium emits only its inserts: updates and deletes never reach the stream, and the sink keeps every row as it was first written"
		}
		var fix string
		switch {
		case overridden && keyed:
			fix = fmt.Sprintf("take %s out of replica.identity.autoset.values, or set it to DEFAULT (it has a primary key) or FULL", q)
		case overridden:
			fix = fmt.Sprintf("set %s to FULL in replica.identity.autoset.values, or give it a primary key", q)
		case keyed:
			fix = fmt.Sprintf("ALTER TABLE %s REPLICA IDENTITY DEFAULT; in a new migration (it has a primary key), or FULL", q)
		default:
			fix = fmt.Sprintf("ALTER TABLE %s REPLICA IDENTITY FULL; in a new migration, or give it a primary key", q)
		}
		fs = append(fs, model.Finding{
			Rule: "replica-identity", Severity: model.Warning, Pos: pos,
			Message: fmt.Sprintf("%s is captured with %s, so it has no replica identity\n%s", q, what, consequence),
			Fix:     fix + "; if it only ever takes inserts, say so on the line that creates it: -- cdclint:ignore replica-identity: append-only",
		})
	}
	return fs
}
