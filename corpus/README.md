# Corpus

Each directory is one real failure shape: the source migrations, the
connector JSON and the sink DDL exactly as they disagreed, plus
`expected.txt`, the findings cdclint must produce for it. The corpus is the
specification. A rule is done when its corpus entries pass, and a bug report
becomes a corpus entry before it becomes a fix.

The first three entries reproduce incidents from the project this tool was
extracted from, a Postgres to ClickHouse pipeline through Debezium:

| entry | shape |
|---|---|
| `schema-before-connector` | a migration and a sink change merged without the connector change; every row of the new column landed empty |
| `column-never-captured` | a column added to the source and never put on the include list, found 38 days later when a screen first asked for it |
| `refreshable-mv-positional` | a refreshable materialized view whose SELECT order did not match the target table; loud on ClickHouse 24.8, silent on 26.8 |

The rest exercise one path each:

| entry | path |
|---|---|
| `clean` | everything agrees; the tool must say so and exit 0 |
| `snowflake-topic2table` | Snowflake DDL, table mapped through `snowflake.topic2table.map` |
| `bigquery-regex-router` | BigQuery DDL, topic renamed by a Debezium `RegexRouter`, table derived from the topic |
| `iceberg-exclude-list` | Spark-form Iceberg DDL, `iceberg.tables`, and a connector using `column.exclude.list` |
| `clickhouse-kafka-connect` | a ClickHouse MergeTree table fed by the Kafka Connect sink rather than a Kafka-engine table |
| `kafka-topic-typo` | a Kafka-engine table naming a topic nothing produces |
| `include-list-typo` | an include-list pattern that matches no column, and the read it silently breaks |
| `ignore-marker-pii` | the diff rule with a decision recorded: of two columns added and left off the include list, the one whose line says `cdclint:ignore schema-before-connector: PII` is acknowledged and the forgotten one is raised; a trailing marker does not reach the next line |
| `ignore-marker-mistakes` | markers used wrong: one with no reason, one naming a rule that does not exist, one covering nothing, each an `ignore-marker` warning; and one on the line above a finding, which acknowledges it |
| `golang-migrate-down-files` | Mattermost v10.11.0's `000092_add_createat_to_teammembers.down.sql` drops a column from the wrong table; read in name order it ran just before its up file and deleted `reactions.createat`. Down files are skipped |
| `goose-down-section` | a goose file's down section follows its up section; applied whole, every table was created and dropped again. Down sections are skipped |
| `include-table-no-schema` | Stack Overflow 74103659: `table.include.list` names `ipaddrs` without its schema, the connector runs and no topic appears |
| `include-table-glob` | Stack Overflow 51345636: `public.bg_*` written as a shell glob; as a regular expression it names no table |
| `include-table-typo` | a misspelled `table.include.list` entry, which Debezium only logs as a warning (debezium/dbz#872) |
| `flatten-bare-columns` | ClickHouse/clickhouse-kafka-connect discussion 182: a `Flatten` transform on the Debezium connector sends `after.<column>`, the ClickHouse table names its columns as Postgres does, and every change lands as a row of zeros |
| `flatten-envelope-columns` | ClickHouse's own Postgres CDC example (ClickHouse/examples `cdc/postgresql` at ae417ae): `before.` and `after.` columns resolve to the source columns, so the capture rules see `postcode2`, which the include list added for this entry leaves out. The sink connector config there has no `connector.class`; it is added so the file names its sink |
| `flatten-after-unwrap` | a guard: unwrapping first leaves a flat row, so a `Flatten` after it renames nothing |
| `mysql-column-never-captured` | the column-never-captured incident in MySQL syntax: backticks, `KEY` lines, `ADD COLUMN ... AFTER`, a `database.include.list` naming the database the migrations run in |
| `mysql-change-column` | MySQL renames a column with `CHANGE`; the include list still names the old column and the new one stops arriving |
| `mysql-database-not-included` | a table in a second database (`USE billing`) that `database.include.list` leaves out; the fix names the database list and the database |
| `mysql-diff-adds-column` | the diff rule through the MySQL reader: `base/` without the new column, the change adds it and leaves the connector alone |
| `diff-adds-column-connector-untouched` | the diff rule: `base/` holds the migrations and connector before the change; the change adds a column and leaves the connector alone |
| `diff-connector-captures-one-of-two` | #963 with a hurried fix: two of three new columns go on the include list in the same change; the third is raised, since adding two says nothing about it |
| `diff-connector-touched-other-table` | RefuseRadar #962 and #963 in one range: the connector gains report_validations columns, the migration adds reports columns; the reports ones are raised |
| `diff-table-newly-captured` | the change puts an existing table on the connector: the whole table was decided in this change, its columns are not a surprise |
| `diff-connector-new-at-base` | no `base/connector.json`: the change created the connector, every table was decided at once |
| `diff-exclude-list-excludes-new-column` | an exclude-list connector that names the new column in the same change: a decision, not a surprise |
| `diff-exclude-list-captures-by-default` | an exclude-list connector left alone: the new column is captured by default, nothing to say |
| `diff-exclude-list-pattern-already-matches` | a standing exclude pattern (`.*_m`) swallows the new column by name: the pattern was the decision, no warning |
| `diff-include-list-narrowed` | the change narrows `public.t\..*` to an explicit list that omits the new column: raised, since enumerating says nothing about what was left out |
| `diff-new-captured-table` | a table new in the change and already on the include list: its columns are not a surprise |
| `diff-column-already-read` | a column added in the change that a sink already reads: the static error, not the diff warning |

## Layout of an entry

```
migrations/           source migrations, applied in name order
connector.json        the Debezium source connector, full document or bare config
sink/                 ClickHouse DDL; or sink.<dialect>/ for bigquery, snowflake, iceberg
sink-connector.json   optional Kafka Connect sink connector config
base/                 optional; migrations/ and connector.json before the change, for the diff rule; no connector.json there means the change created it, and one byte-identical to the head's is the unchanged fast path
expected.txt          the exact text output; regenerate with go test ./cmd/cdclint -run TestCorpus -update
PENDING               optional; names the rule the entry waits for, and the test skips it
```
