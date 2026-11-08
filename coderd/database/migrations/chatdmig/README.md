# chatd migrations

This fork (`ibetitsmike/coder`) keeps the Coder Agents tables in their own
Postgres schema, `chatd`. Upstream's numbered migrations still run in
`public`. Fork migrations are nodes named by UUID, and each node lists the
migrations that must run before it. This directory holds that graph and the
tool that keeps it in step with upstream.

The Go code stays at upstream's paths. Only the schema moves, so a daily
upstream merge rarely conflicts.

## What is in chatd

The adopt node,
[`nodes/01a1b58c-…_adopt_chatd_schema.up.sql`](nodes/), moves these objects
from `public` to `chatd`:

- The tables and views `chats`, `chats_*`, `chat_*`, `skills`,
  `mcp_server_configs`, `mcp_server_user_tokens`,
  `organization_member_disabled_skills`, `organization_member_skill_settings`
  and `user_ai_provider_keys`. Indexes, constraints, triggers and owned
  sequences move with each table.
- The sequences named `chat_*` that no table owns.
- The enum types named `chat_*`.
- The functions whose names contain `chat`, `skill` or `mcp_server`.

`SET SCHEMA` changes the catalog only. No row is copied, so the move takes
seconds on a large database.

## Every connection needs chatd on its search path

Upstream's queries and the moved functions name tables without a schema.
They work only when `chatd` is on the search path:

- `coder server` adds it: `ConnectToPostgres` wraps the URL with
  `migrations.WithChatdSearchPath`.
- Tests that use `dbtestutil.Open` get it from `ConnectionParams.DSN`. A URL
  in `CODER_PG_CONNECTION_URL` is used as it is, so add
  `options=-c%20search_path%3Dpublic,chatd` to it yourself.
- For `psql` and `pg_dump`, set
  `PGOPTIONS='-c search_path=public,chatd'`.

Without it, a query fails with `relation "chats" does not exist`, and a
`DELETE FROM organization_members` fails inside a trigger.

## How a database migrates

`migrations.UpAll` replaces `migrations.Up`. It holds a session advisory lock
(`chatdmig.LockID`) for the whole run, then repeats two steps:

1. Apply every graph node that is ready: its parent nodes ran, and core is at
   or past each upstream version that it requires.
2. Move core forward by one upstream version with golang-migrate.

`coder server` calls `migrations.ServerMigrate`. It runs the same loop
without background nodes, then starts `RunBackground` in a goroutine (see
[Node options](#node-options)).

The ledger of applied nodes is `chatd.migration_nodes`. It stores a checksum
of each node. `UpAll` refuses to start if a fork node changed after it ran.
`EnsureCleanAll` fails while a startup node is pending.

### One transaction per migration

Upstream's `migrations.Up` runs every pending version in one transaction, so
each lock that it takes lasts until the whole upgrade ends. `UpAll` commits
each core version and each node on its own:

- A lock lasts only for the migration that took it.
- A server that dies or gives up keeps the versions that it finished, and the
  next start continues from there.
- An upgrade can stop between two versions. The old binary then refuses the
  database (`database needs migration`). To go back, restore the database
  snapshot that the deploy took. `coder-local rollback` does this.

There are no down migrations for nodes.

### Lock timeouts

Postgres queues every later query on a table behind an `ALTER` that waits
for its lock. A migration that waits for a long transaction of an old server
stalls all traffic on that table. So every migration transaction, core or
node, starts with `SET LOCAL lock_timeout`. After a lock timeout or a
deadlock, the transaction rolls back, the waiting queries run, and the
migration tries again after a pause.

`chatdmig.DefaultLockPolicy` waits at most 5 seconds per attempt and makes 10
attempts, with pauses from 1 to 30 seconds. After that, startup fails with
`no lock after 10 attempts` and the error that names the migration. Find the
blocking session with `pg_stat_activity` and `pg_locks`, end it, and start
again.

## Routes: upstream migrations after the split

`routes.txt` names the split, the last upstream migration that ran in core
before the fork existed. Each upstream migration after the split has one
line, `<version> <name> <ledger>`:

| Ledger  | Where it runs                                                                                          |
|---------|--------------------------------------------------------------------------------------------------------|
| `core`  | In golang-migrate, as upstream wrote it.                                                               |
| `chatd` | As a graph node with `search_path` `chatd, public`, so the objects that it creates land in chatd. golang-migrate runs `SELECT 1` for its version. |
| `mixed` | It creates objects in both schemas. A person writes `override/<name>.up.sql`, which runs as its node. |

A routed node runs after the adopt node and after the previous routed node.
Its ID is a UUIDv5 of the upstream name, so a renumbered upstream file keeps
its node.

`chatdmig route` adds a line for each new upstream migration. It classifies
the migration by the names that it creates, compared with the tables in
`../dump.sql`. It follows a renumbered file by name. It exits with code 3
when a mixed migration has no override.

`dump.sql` stays upstream's file. `make gen` still runs only upstream's
migrations, so sqlc and the generated queries do not change.

### Migrations taken early

`early.txt` lists the upstream migrations that the fork took from an open
coder/coder branch before upstream main merged them, one
`<version> <name> <source>` per line. golang-migrate keeps one version per
database, so a database that ran one of them skips every later upstream
migration at or below its version. `chatdmig route` fails when upstream brings
such a migration, and the sync stops. A person then renumbers or moves the
migration before the sync continues.

## Add a fork migration

```sh
go run ./coderd/database/migrations/chatdmig/cmd/chatdmig new chat_tags \
  -requires upstream:organization_skill_member_preferences
```

This writes `nodes/<uuidv7>_chat_tags.up.sql` with a header:

```sql
-- requires: 01a1b58c-24fa-7d7d-91d6-202388cf7cc8_adopt_chatd_schema
-- requires: upstream:organization_skill_member_preferences

ALTER TABLE chats ADD COLUMN tags text[] NOT NULL DEFAULT '{}';
```

- `-- requires:` takes `upstream:<name>` or another node as `<uuid>_<name>`.
  Every node must reach the adopt node through its parents.
- `-- search_path:` is optional. The default is `chatd, public`, so a new
  table lands in chatd. Qualify a core object that a node creates
  (`CREATE TABLE public.foo`).
- Never edit a node after it ran on a database. Add a new node.

### Node options

Startup waits for every startup node, so keep them to quick schema changes.
Three header lines change how a node runs:

| Header                      | Effect                                                                                                         |
|-----------------------------|----------------------------------------------------------------------------------------------------------------|
| `-- phase: background`      | The node runs after the server starts, in `RunBackground`, one server at a time (`chatdmig.BackgroundLockID`). A startup node cannot require it. `EnsureCleanAll` ignores it. |
| `-- repeat: until-no-rows`  | The node's one statement runs again, each time in its own transaction, until it changes no rows. The node is recorded with the last run. |
| `-- transaction: none`      | The node's one statement runs outside a transaction, for `CREATE INDEX CONCURRENTLY`. It has no lock timeout. |

A backfill is a background node that repeats a bounded batch. Each batch
commits, so no lock lasts longer than one batch, and a restart continues
where the last batch stopped:

```sql
-- requires: 0193f0aa-..._chat_tags
-- phase: background
-- repeat: until-no-rows

UPDATE chats SET tags = '{}'
WHERE id IN (SELECT id FROM chats WHERE tags IS NULL LIMIT 1000);
```

The statement must change fewer rows each time, or it never stops. Code that
reads the new column must handle rows that the backfill has not reached.
Tests and `dbtestutil` run `UpAll` without `DeferBackground`, so background
nodes run inline there.

An index on a large table:

```sql
-- requires: 0193f0aa-..._chat_tags
-- transaction: none

CREATE INDEX CONCURRENTLY IF NOT EXISTS chats_tags_idx ON chats USING gin (tags);
```

Use `IF NOT EXISTS`: the node is recorded after the statement, so a crash in
between runs it again. A failed `CREATE INDEX CONCURRENTLY` leaves an invalid
index that `IF NOT EXISTS` would skip. The runner checks for invalid indexes
after the statement and stops with the index name. Drop it with
`DROP INDEX CONCURRENTLY` and start again.

Check the graph and print its order:

```sh
go run ./coderd/database/migrations/chatdmig/cmd/chatdmig check
```

### Fork queries

Put queries for fork columns or tables in a separate sqlc package,
`coderd/database/forkdb`, so that upstream's `queries.sql.go` never conflicts.
Its `sqlc.yaml` reads `../dump.sql` and then the fork nodes in graph order.
The package does not exist yet. Create it with the first fork query.

## Upstream sync

`.github/workflows/fork-sync.yaml` runs at 05:00 UTC and on demand:

1. It disables every workflow that is not `fork-*`. Upstream's workflows
   deploy, release or fail in the fork.
2. It stops if a sync PR is open. It labels a red one `needs-mike`.
3. It merges the newest upstream `main` commit that is at least 24 hours old.
   Upstream fixes a duplicate migration number within hours.
4. It runs `chatdmig route` and commits `routes.txt`.
5. It opens a PR labeled `sync` from `sync/<date>-<sha>`. A clean PR merges
   itself with a merge commit when fork-ci passes.

A person takes over in three cases:

- **Conflict.** The job opens or updates an issue labeled `sync-blocked`.
  Merge upstream by hand on a branch and open a PR.
- **Mixed migration.** The PR is labeled `needs-mike`. Write
  `override/<name>.up.sql` (see [override/README.md](override/README.md)),
  push it to the PR branch and merge the PR.
- **Migration below an early one.** The job fails with the route output,
  which names the upstream migration and the `early.txt` entry. Renumber or
  move the migration on a branch, update `early.txt`, and open a PR (see
  [Migrations taken early](#migrations-taken-early)).

The job pushes with the `FORK_SYNC_TOKEN` secret, a fine-grained token for
this repository with Contents, Pull requests, Issues and Workflows write
access. `GITHUB_TOKEN` cannot push upstream's workflow changes, and a PR that
it opens starts no workflows.

## CI

`.github/workflows/fork-ci.yaml` runs on PRs and on `main`:

| Job          | Checks                                                                                                   |
|--------------|----------------------------------------------------------------------------------------------------------|
| `graph`      | `chatdmig check` and the unit tests of this package.                                                     |
| `schema`     | Upstream's migrations and the routed graph give the same schema, apart from the schema name. An upgrade from 634 (the first deployed version) or from the split equals a fresh database. Lock timeouts, retries and the node options work on a real database. |
| `chat-tests` | `./coderd/x/...`, `./coderd/database/...`, and the chat, skill and MCP tests of `./coderd` and `./enterprise/coderd`. |
| `build`      | `go build ./enterprise/cmd/coder`.                                                                       |

Run the schema tests locally against a Postgres role that can create
databases. `pg_dump` must be at least the server's version:

```sh
CHATDMIG_TEST_DATABASE_URL='postgres://postgres:postgres@127.0.0.1:5432/postgres?sslmode=disable' \
  go test -count=1 ./coderd/database/migrations/chatdmig/
```

## Deploy

Mike's Mac runs the fork's `main`. `coder-local-autodeploy` checks every five
minutes for a new `main` commit with a green fork-ci run. It builds the
commit with `coder-local`, waits until no chat runs, switches the binary
after a database snapshot, and rolls back if the server is not healthy.
