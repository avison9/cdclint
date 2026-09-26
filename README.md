# cdclint

Lint the contract between your database, your Debezium connector and your
sink, before the deploy that silently drops a column.

**Status: v0.3.** Ten rules run against a corpus of real incidents; two
more are next. Reads Postgres and MySQL (or MariaDB) sources. Released for
macOS, Linux and Windows, on Homebrew and on the GitHub Marketplace.

## Why are my columns null?

A change-data-capture pipeline has three schemas that must agree and nothing
that makes them agree:

1. **The source schema.** Postgres or MySQL tables, evolved by migrations, changed
   weekly by the application team.
2. **The capture contract.** The Debezium connector JSON: `table.include.list`,
   `column.include.list`, replica identity, topic naming. Written once by
   whoever set up the pipeline, changed rarely and by hand.
3. **The sink schema.** The warehouse tables that read the stream: ClickHouse
   Kafka-engine tables and materialized views, or BigQuery, Snowflake and
   Iceberg tables behind a Kafka Connect sink connector. Changed when somebody
   downstream needs a column.

Three text files, three authors, three moments in time. Nothing reads them
together, and the failure when they disagree is silent by design:

- Debezium's `column.include.list` drops any column not on it **before the
  message reaches Kafka**. That is the feature that keeps PII out of the
  stream. The consequence is that a new column is absent from every message,
  and every message is still well-formed.
- The sink ignores fields it does not know and fills fields it does not
  receive with the type's default. ClickHouse's Kafka engine writes `0`,
  `''` or `NULL`. The BigQuery, Snowflake and Iceberg sink connectors with
  schema evolution off do the same.

No error, no log line, no metric. Lag is zero, offsets advance, the dashboard
is green, and a column is `0` on every row. It is found weeks later by whoever
first reads the column, who assumes the zeros are real or files the bug against
the wrong layer. The fix is one line in the connector. The repair is a
resnapshot of the table.

cdclint reads the three files in the pull request that changes any of them and
fails it with the line to add.

## Symptoms it prevents

If you arrived here searching for one of these, the cause may be sitting in
your repository, and cdclint reads it:

| what you see | what is usually wrong | rule |
|---|---|---|
| A column is `0`, `''`, `NULL` or `1970-01-01` on every row in ClickHouse, BigQuery, Snowflake or Iceberg, with nothing in the logs | the column is not on the connector's `column.include.list` (or is on its exclude list), so Debezium drops it before Kafka | `sink-column-not-captured` |
| A column added in Postgres or MySQL never shows up downstream | the migration added it and nobody added it to the include list | `schema-before-connector` (with `--base`), `sink-column-not-captured` |
| Listing the columns of one table made another table's columns disappear | `column.include.list` is one list for every captured table | `sink-column-not-captured` |
| "`table.include.list` not working", or the connector is `RUNNING` and no topic appears | the entry has no schema (`orders` for `public.orders`), or is a glob (`public.bg_*`) where Debezium expects a regex (`public\.bg_.*`); Debezium matches each entry against the whole `schema.table` name, never a substring | `captured-table-missing` names the entry and the fix; `sink-table-not-captured` when a sink reads the table |
| A Kafka-engine table or sink receives nothing, or the wrong table fills | the topic it reads is not the one the connector produces (prefix, schema, `RegexRouter`) | `topic-table-mapping` |
| A typo in the include list, and a column quietly missing | the pattern matches no column in the source | `captured-column-missing` |
| A typo in `table.include.list`, and a topic that never appears | the entry matches no table in the source | `captured-table-missing` |
| The Kafka Connect sink writes a row of `0` and empty strings for every change, and no error ("sink replicates zero values or nulls") | a `Flatten` transform keeps Debezium's envelope and names every field `after.<column>`, while the sink table names its columns as Postgres does; sinks match fields to columns by name | `sink-column-flattened` |
| A ClickHouse materialized view writes defaults | a refreshable view's `SELECT` order differs from the target's (it matches by position), or a streaming view's names differ (it matches by name) | `mv-column-match` |

It reads files only: Postgres and MySQL (or MariaDB) sources, no type checks, no connection to
anything running.

## What it checks

| rule | catches | status |
|---|---|---|
| `sink-column-not-captured` | the sink reads a column the connector does not include | v0.1 |
| `sink-table-not-captured` | the sink reads a table the connector does not include | v0.1 |
| `sink-column-unknown` | the sink expects a field the source table does not have (warning; renames and computed fields are legitimate) | v0.1 |
| `source-column-not-captured` | a source column nothing captures and nothing reads yet, so the day something asks for it is the day it is found missing (info) | v0.1 |
| `captured-column-missing` | the include list names a column the source does not have (warning) | v0.1 |
| `captured-table-missing` | a `table.include.list` entry matches no table: a typo, a missing schema (`orders` for `public.orders`), or a shell glob (`public.bg_*`) where Debezium reads a regular expression; the last two get the corrected entry as the fix (warning) | v0.3 |
| `topic-table-mapping` | a Kafka-engine table reads a topic the connector will not produce | v0.1 |
| `sink-column-flattened` | a sink table names its columns as the source does while a `Flatten` transform on the way delivers the envelope as `after.<column>`, so every column stays at its default | v0.3 |
| `mv-column-match` | ClickHouse streaming materialized views match by name, refreshable ones by position. ClickHouse 25.4 and later reject a streaming view that writes a column the target lacks when it is created; a refreshable view's order mismatch was loud on 24.8 and is silent on 26.8 | v0.1 |
| `schema-before-connector` | this change adds a column to a captured table and leaves it out of the stream without deciding to, the trap itself, judged on the diff (warning: leaving PII off is right, so it asks for the decision) | v0.2 |
| `replica-identity` | a captured table's replica identity cannot supply what the sink reads | next |
| `migration-numbering` | duplicate or gapped migration prefixes | next |

Findings name the file, the column, the rule and the fix:

```
error sink-column-not-captured
  public.reports.response_distance_m is read by analytics/schema/0020_report_validations.sql:14
  but is not in cdc/postgres-source.json column.include.list
  fix: add "public.report_validations.response_distance_m" to column.include.list,
       deploy the connector, then apply the sink schema
```

## Install

Per-OS steps, verification and uninstalling are in
[INSTALLING.md](INSTALLING.md). The short version:

Homebrew, on macOS or Linux:

```
brew install avison9/tap/cdclint
```

A release archive for your OS and CPU, from
[Releases](https://github.com/avison9/cdclint/releases): unpack it and put
`cdclint` on your PATH. `checksums.txt` beside the archives is signed with
cosign, keyless, by this repository's release workflow:

```
cosign verify-blob --certificate checksums.txt.pem --signature checksums.txt.sig \
  --certificate-identity-regexp 'github.com/avison9/cdclint' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com checksums.txt
```

With Go:

```
go install github.com/avison9/cdclint/cmd/cdclint@latest
```

## Try it on a known failure

The [corpus](corpus/) holds real failures. This one is a typo in
`column.include.list`:

```
git clone https://github.com/avison9/cdclint && cd cdclint
cdclint --migrations corpus/include-list-typo/migrations \
        --connector corpus/include-list-typo/connector.json \
        --sink corpus/include-list-typo/sink
```

```
warning captured-column-missing corpus/include-list-typo/connector.json
  public.report_validations\.reponded_at in column.include.list matches no column in the source schema
  fix: remove it, or check the spelling against the migrations
error sink-column-not-captured corpus/include-list-typo/sink/0020_report_validations.sql:12
  public.report_validations.responded_at is read by kafka_report_validations (reads topic rr.public.report_validations) but is not matched by column.include.list in corpus/include-list-typo/connector.json
  every row will carry the column's default, with no error anywhere
  fix: add public.report_validations.responded_at to column.include.list, deploy the connector, then apply the sink schema; rows already written need a snapshot
1 error(s), 1 warning(s), 0 info
```

The exit code is 1, which is what fails the pull request.

## Run it

```
cdclint --migrations db/migrations \
        --connector cdc/postgres-source.json \
        --sink analytics/schema
```

For a warehouse behind a Kafka Connect sink connector, pass its config too, so
topics map to tables the way the connector maps them:

```
cdclint --migrations db/migrations \
        --connector cdc/postgres-source.json \
        --sink snowflake:warehouse/ddl \
        --sink-connector cdc/snowflake-sink.json
```

The migrations are applied in filename order, the way every migration runner
does, and the down half of a migration is left out, since a forward migrate
never runs it: golang-migrate's `*.down.sql` files, and the down section of a
goose (`-- +goose Down`), sql-migrate (`-- +migrate Down`) or dbmate
(`-- migrate:down`) file.

Add `--base origin/main` (any git ref) and the diff-aware rule judges the
change itself: a column added to a captured table and left off the include
list is raised while the author is still there. Each new column is judged on
its own: editing the connector for another table, or adding one new column
to the list, says nothing about the next. The action does this
on every pull request by default, against the base branch's tip. Paths on
the command line are relative to the current directory, in the working
tree and at the base alike, so it runs from a subdirectory of a monorepo.

`--disable RULE[,RULE]` leaves rules out: their findings are not shown and do
not fail the run, and the output ends with what was left out, for example
`not shown (--disable): source-column-not-captured 161`, so a filtered run
never reads as a clean one. `--disable source-column-not-captured` is the usual
one, for a repository that has read its inventory of uncaptured columns and
does not want it on every run. Disabling `schema-before-connector` is the same
as leaving out `--base`. An unknown rule name is an error, not a filter that
hides nothing.

### Recording a decision: `cdclint:ignore`

Some findings are decisions, not mistakes: a column holding PII is left off
the include list on purpose. Say so in a comment on the line the finding
points at, with the reason:

```sql
ALTER TABLE users
    ADD COLUMN ssn_hash TEXT, -- cdclint:ignore schema-before-connector: PII, never streamed
    ADD COLUMN nickname TEXT;
```

The finding no longer fails the run and is listed as acknowledged, with the
reason; `nickname`, on the next line, is still raised. A marker after code
covers its own line only; a marker on a line of its own covers the line
below. It works in migrations and in sink DDL, with `--` or MySQL's `#`, and
names one or more rules separated by commas. The reason after the colon is
required, because it is the record of the decision. A marker with no reason,
with a rule name that does not exist, or that covers no finding is itself a
warning (`ignore-marker`). A `schema-before-connector` marker is expected to
go quiet once its pull request merges, since that rule judges the change;
to keep the column out of the inventory afterwards too, name both rules:
`-- cdclint:ignore schema-before-connector,source-column-not-captured: PII`.

Files in, findings out, non-zero exit. No database, no daemon, no credentials.
Under a second on a laptop. `--fail-on warning` or `info` raises the bar;
`--format json` is for anything that wants to post findings somewhere. A
later live mode reads the deployed Postgres, Kafka Connect and sink to report
drift against what is actually running.

## In CI

The action is on the
[GitHub Marketplace](https://github.com/marketplace/actions/cdclint):

```yaml
- uses: avison9/cdclint@v0
  with:
    migrations: db/migrations
    connector: cdc/postgres-source.json
    sink: analytics/schema
```

`@v0` follows the newest 0.x release; it becomes `@v1` at 1.0. The step
fails the pull request when the three files disagree, with the finding and
the fix in the log. Several sinks or sink connectors go one per
line; `fail-on`, `version` and `working-directory` are the other inputs. The
action downloads the release binary for the runner and verifies its
checksum before running it.

## Sources

The migrations are read in filename order, and the connector's class picks
the dialect: `MySqlConnector` and `MariaDbConnector` read MySQL, every other
Debezium source reads Postgres. `--migrations mysql:DIR` or `postgres:DIR`
says it outright.

MySQL has no schemas: Debezium names a table `database.table`, so cdclint
needs the database the migrations run in. It takes it from a `USE db;` in the
migrations, or from the connector when `database.include.list` names one
database (or every `table.include.list` entry starts with the same one), and
stops with that advice when neither says. `database.include.list` and
`database.exclude.list` are applied before the table lists, as Debezium does,
and a finding names whichever list keeps a table out. MySQL's re-runnable
migrations wrap `ALTER TABLE` in a string run through `PREPARE`, because MySQL
has no `ADD COLUMN IF NOT EXISTS`; cdclint reads the DDL in those strings too.

## Sinks, out of the box in v1

| sink | how tables are found | how a table maps to a topic |
|---|---|---|
| **ClickHouse**, Kafka engine | `CREATE TABLE ... ENGINE = Kafka` | `kafka_topic_list` |
| **ClickHouse**, Kafka Connect | table DDL + `ClickHouseSinkConnector` config | `topic2TableMap`, else the topic's table name |
| **BigQuery** | table DDL + `BigQuerySinkConnector` config | `topic2TableMap`, else the topic's table name |
| **Snowflake** | table DDL + `SnowflakeSinkConnector` config | `snowflake.topic2table.map`, else the topic's table name |
| **Iceberg** | table DDL (Spark or Trino form) + `IcebergSinkConnector` config | `iceberg.tables` with route regexes, else the topic's table name |

Without a sink connector config, a sink table maps to the source table of the
same name, and the finding says so. Debezium's `RegexRouter` transform is
applied when computing topic names. Other sources (SQL Server, Oracle) and
sinks are packages behind the same two interfaces.

Transforms on either connector decide what the sink receives, and cdclint
follows the documented ones in order: Debezium's `ExtractNewRecordState` (or the
Iceberg `DebeziumTransform`) unwraps the envelope into the row, and Kafka
Connect's `Flatten` turns the envelope into `after.<column>`, `before.<column>`,
`source.<field>`, `op` and `ts_ms` (its `delimiter` included). A sink table
named that way, as in ClickHouse's own CDC example, is matched through
`after.` and `before.` to the source columns. Routers, key transforms and
`Filter` change no field name. Any other transform that touches the value, or a
modelled one applied under a predicate, leaves the shape unknown, and cdclint
then reads the sink's column names as the row's, as it always has.

## Design

- **One job.** Lint the contract. Not a CDC platform, not a migration runner,
  not monitoring.
- **Pluggable ends.** Postgres or MySQL, and Debezium, as the source and capture
  readers; ClickHouse, BigQuery, Snowflake and Iceberg as sink readers, each
  behind a small interface so the next one is a package, not a rewrite.
- **The corpus is the spec.** [`corpus/`](corpus/) holds one directory per
  real failure shape with the three schemas and the expected findings. Every
  bug report becomes a corpus entry before it becomes a fix.
- **Boring technology.** Go, one static binary, Apache-2.0.

## Where it comes from

Extracted from a Postgres to ClickHouse pipeline that had the rule
"connector before schema" written into its repository and still hit this five
times in six weeks. A team that knows the trap hits it five times; a team that
does not hits it more and diagnoses it slower.

## Contributing

Start with [CONTRIBUTING.md](CONTRIBUTING.md): every change begins as a
corpus entry. AI coding agents read [AGENTS.md](AGENTS.md) first.

## License

Apache-2.0. See [LICENSE](LICENSE).
