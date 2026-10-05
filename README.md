# dbcompat

`dbcompat` proves whether two versions of PostgreSQL query contracts can coexist
with two versions of a schema during a rolling deployment.

It executes a deterministic 2×2 matrix:

| Query contract | Previous schema | Next schema |
|---|---:|---:|
| Previous | baseline | migration-first compatibility |
| Next | code-first / rollback compatibility | baseline |

Unlike a migration linter, `dbcompat` asks PostgreSQL to prepare and execute the
actual versioned queries. A failed baseline is an invalid contract (exit 2); a
failed required cross-version cell is a deployment incompatibility (exit 1).

## Quick start

Requirements: Go 1.25+, Docker, and permission to start local containers.

```console
go install github.com/ssivitskii/dbcompat/cmd/dbcompat@latest
dbcompat -config examples/additive/dbcompat.json -output reports
```

The command writes `report.json`, `report.xml` (JUnit), and `report.md` together.
Reports contain hashes and query IDs, never SQL, passwords, or connection strings.

## Configuration

```json
{
  "version": 1,
  "postgresImage": "postgres:17.6",
  "schemas": {
    "previous": "schema.previous.sql",
    "next": "schema.next.sql"
  },
  "querySets": {
    "previous": "queries.previous.json",
    "next": "queries.next.json"
  },
  "requiredCells": [
    "previous_previous",
    "previous_next",
    "next_previous",
    "next_next"
  ],
  "timeouts": { "startup": "30s", "query": "3s" }
}
```

Paths must resolve inside the configuration directory, including after symlink
resolution. Output paths are subject to the same boundary and may not contain
existing symlink components. Unknown fields, duplicate cells, unsupported
versions, and psql backslash directives are rejected. Omit `requiredCells` to
require all cells. Both matching-version baselines are always mandatory;
`requiredCells` can make either cross-version cell optional. Optional failures
remain visible but do not affect the exit code.

Each query manifest is explicit and reviewable:

```json
{
  "version": 1,
  "queries": [{
    "id": "get-account",
    "sql": "SELECT id, name FROM accounts WHERE id = $1",
    "args": [{ "type": "int8", "value": 1 }]
  }]
}
```

Argument types are `text`, `int8`, `float8`, `bool`, `uuid`, `timestamptz`,
`json`, and `null`. JSON literal `null` is rejected for every non-null type.
Nulls require their PostgreSQL intent and an explicit value, for example
`{"type":"null","as":"text","value":null}`. Bare JSON numbers are rejected
for `json` arguments to avoid an ambiguous integer/decimal representation.

## Execution and security boundary

- Two disposable containers are created, one per schema.
- Ports bind to random loopback ports only.
- Containers have CPU, memory, and process limits and an exact per-run label.
- `dbcompat` removes only container IDs returned by its own `docker run` calls.
- Queries use a fresh connection and read-only transaction. Each query is the
  single prepared statement in that transaction; rows are consumed and the
  transaction is rolled back.
- There are no mounts, Docker socket exposure, SQL log dumps, or live-target mode.

The isolated container bootstrap `postgres` role is used for schema setup and
query execution in this MVP. The database is loopback-only, randomly credentialed,
short-lived, and never a live target. Query/schema inputs must nevertheless be
treated as trusted CI repository content. A restricted execution role is planned
for a later release.

Use an immutable image digest in security-sensitive CI. The examples and CI pin
the observed PostgreSQL 17.6 digest, while the configuration excerpt uses the
readable `postgres:17.6` tag. `dbcompat` records the locally resolved image ID
when Docker exposes it.

The integration workflow is intended for an ephemeral GitHub-hosted runner. Do
not run repository-authored schema/query fixtures on a persistent self-hosted
runner that has access to unrelated workloads or secrets.

## Exit codes

| Code | Meaning |
|---:|---|
| 0 | all required compatibility cells passed |
| 1 | required cross-version compatibility failed |
| 2 | invalid input or a baseline contract failed |
| 3 | Docker, image, startup, or schema environment failed |
| 4 | internal/reporting failure |

## Development

```console
gofmt -w .
go vet ./...
go test ./...
go test -race ./...
go build ./cmd/dbcompat
go test -tags=integration -timeout=10m ./internal/integration
```

Integration tests skip when Docker is unavailable. See `examples/additive`,
`examples/premature-drop`, and `examples/code-first` for passing and asymmetric
`42703` failure cases.

## Non-goals

This MVP is not a migration runner, ORM query discoverer, production database
scanner, load/lock simulator, or multi-database abstraction.
