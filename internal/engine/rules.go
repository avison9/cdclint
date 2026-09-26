package engine

// Rules names every rule Run can report, in the README's order. --disable
// checks names against it, so a typo is an error rather than a filter that
// silently hides nothing, and the corpus test checks that every finding's
// rule is here, so a new rule cannot be missed.
var Rules = []string{
	"sink-column-not-captured",
	"sink-table-not-captured",
	"sink-column-unknown",
	"source-column-not-captured",
	"captured-column-missing",
	"captured-table-missing",
	"topic-table-mapping",
	"sink-column-flattened",
	"mv-column-match",
	"schema-before-connector",
}

// KnownRule reports whether name is one of Rules.
func KnownRule(name string) bool {
	for _, r := range Rules {
		if r == name {
			return true
		}
	}
	return false
}
