# Architecture

```
HTTP  ->  auth middleware  ->  GraphQL resolvers  ->  domain services  ->  sqlc queries  ->  Postgres
          (internal/auth)      (internal/graph)       (internal/<domain>,   (internal/db)
                                                       internal/access)
```

## Layers

- **Resolvers are thin.** They call one service method or one dataloader and return. GraphQL
  types are bound directly to the sqlc models and inputs to the services' input structs
  (`gqlgen.yml`), so there is no mapping layer to keep in sync.
- **Domain services hold the rules**: validation, permission checks, multi-step writes. One
  package per area: `project`, `file`, `asset`, `subtitle`, `vidispineimport` (import from Vidispine, see
  [vidispine.md](vidispine.md)), plus `access` for users and grants.
- **`pkg/`** holds code other Go projects may import (`pkg/vidispine`). It never imports
  `internal/`.
- **`internal/access` answers "may this caller do that?"** Services call it; resolvers never do.
  See [security.md](security.md).
- **`internal/domain`** has the shared error values, paging rule and small input helpers.

## Transactions

Services take a `sqlc.Querier` and a `db.TxRunner`. Writes that span several statements run in
`tx.InTx(...)`, which hands the function a Querier bound to one transaction. `db.PoolTx` opens a
real transaction; `db.SameTx` runs on a Querier that is already inside one (dev-data scenarios
run a whole scenario in a single transaction this way).

## Errors

Services return the values in `internal/domain`. The GraphQL layer turns them into an
`extensions.code`:

| Code | Meaning |
|---|---|
| `NOT_FOUND` | Does not exist, or the caller may not know that it does |
| `INVALID_INPUT` | The request is wrong; the message says what |
| `CONFLICT` | Already exists, still in use, or a newer revision exists |
| `FORBIDDEN` | The caller can see the object but may not do this |
| `UNAUTHENTICATED` | No valid token (also HTTP 401 before GraphQL runs) |
| `UNAVAILABLE` | A system MediaBrain depends on (Vidispine) is not configured or did not answer. The cause is logged, never sent |
| `INTERNAL` | Anything unexpected. The cause is logged, never sent |

Lookups by id (`asset(id:)`, `file(id:)`, ...) return `null` instead of `NOT_FOUND`.
Database constraint violations are mapped in `domain.FromPG`, so the database stays the final
judge of uniqueness and references without leaking its messages.

## Dataloaders (no N+1)

Every nested field (`Asset.versions`, `Rendition.files`, `File.copies`, `SubtitleTrack.cues`, ...)
goes through a per-request loader in `internal/graph/loaders.go`. A loader collects the parent ids
asked for within a millisecond and fetches them with one query that takes a list of ids
(`WHERE asset_id = ANY($1)`). A deeply nested query over any number of assets therefore costs a
fixed number of statements. `e2e/graph_test.go` (`TestNestedQueriesAreBatched`) fails if the count
grows with the number of assets.

The loader is our own (`internal/graph/loader`, about 60 lines) rather than a dependency.

The batch methods on the services (`VersionsFor`, `FilesFor`, ...) do not re-check permissions:
they are only reachable through a parent the caller was already allowed to see. Where the child
needs its own rule (file copies, the project behind a link) the batch method applies it.

## Testing

- **`e2e/`** (needs Docker) runs the real server against a real Postgres. It covers every query
  and mutation, the permission rules as each kind of user, every dev-data scenario, the CLI,
  and two cross-cutting guards: statement counts for nested queries, and a sweep that makes each
  database statement of each operation fail in turn to prove failures are contained.
- **Unit tests** cover what needs no database: subtitle parsing, input rules, token verification,
  the auth middleware, config, the loader, the Vidispine client.
- **Other systems are faked as HTTP servers**, not mocked in Go: `internal/auth/authtest` issues
  tokens, `pkg/vidispine/vidispinetest` is a Vidispine that tests fill with items.
- There is deliberately **no in-memory fake of the database**. The permission rules are SQL; a
  fake would test a second implementation of them.

## Running several instances

Nothing is shared between instances except Postgres, so the server can run as any number of
replicas behind the auth proxy. What each instance keeps to itself:

- **The signed-in user of a token**, for one minute (`internal/auth/middleware.go`). The bound in
  [security.md](security.md) holds per instance; a new token costs one sign-in per instance.
- **The proxy's signing keys** (JWKS), fetched by each instance when it meets an unknown key
  and again once its copy is 5 minutes old.
- **The sweeper for expired grants**, once a minute in every instance. The first one deletes the
  expired grants; the others wait on its row locks and find nothing.
- **A connection pool** of pgx's default size (the larger of 4 and the CPU count). Cap it with
  `pool_max_conns` in `DATABASE_URL` so that replicas times the cap stays below Postgres's
  `max_connections`.

**Migrations take a Postgres advisory lock** (goose's session locker, `internal/db/migrate.go`),
so instances that start together with `AUTO_MIGRATE=true` do not apply the same migration twice:
the others retry every second for up to five minutes, then find nothing pending. The lock belongs
to a database session, so migrations need a direct connection or a session-mode pooler, not
PgBouncer in transaction mode. `e2e/migrations_test.go` (`TestConcurrentMigrateUp`) starts eight
at once.

[`deploy/docker-compose.prod.yml`](../deploy/docker-compose.prod.yml) is the intended shape of a
deployment: `mediabrain migrate up` as a one-shot step, then replicas with `AUTO_MIGRATE=false`,
`DEBUG=false` and the `AUTH_*` settings required, on a network with a fixed subnet that the auth
proxy joins (that subnet is the default `AUTH_TRUSTED_PROXIES`). No host port is published.

## Known limits

- Paging is `limit`/`offset`. Ids are time-ordered, so cursor paging can be added later.
- Subscriptions and file upload/download are not implemented.
