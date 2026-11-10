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
  package per area: `project`, `file`, `asset`, `subtitle`, plus `access` for users and grants.
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
  the auth middleware, config, the loader.
- There is deliberately **no in-memory fake of the database**. The permission rules are SQL; a
  fake would test a second implementation of them.

## Known limits

- Paging is `limit`/`offset`. Ids are time-ordered, so cursor paging can be added later.
- Subscriptions and file upload/download are not implemented.
