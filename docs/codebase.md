# Codebase map

Where things are and which conventions to follow, so a change does not start with exploring.
`AGENTS.md` has the layout and the checklist; this page has what you only learn by reading the
code. Keep it to pointers: the code is the detail.

## Domain services

- Every service is `Service{q sqlc.Querier; tx db.TxRunner}` with `NewService(q, tx)`
  (`internal/asset/service.go`). The caller comes from `access.From(ctx)`.
- A write over several statements runs in `s.tx.InTx(ctx, func(q sqlc.Querier) error {...})`.
- **Several services in one transaction:** inside `InTx`, build them over `db.SameTx(q)`.
  `internal/vidispineimport/service.go` (`Import`) and `internal/devdata/scenarios.go` (`newServices`) do
  this. Services built with `db.PoolTx` open their own transaction per call.
- Never call another system inside a transaction: read first, then open it
  (`vidispineimport.Service.Import`).
- Database errors go through `domain.FromPG` (`FromPGDelete` for deletes). An unknown foreign key
  or an enum value the database rejects becomes `INVALID_INPUT` there; services do not check
  those in Go first.
- Permission checks: `access.RequireAdmin(ctx)`, `access.RequireAsset(ctx, q, id, level)`,
  `access.RequireProject(...)`. `access.System()` is the administrator principal of the CLI and
  dev data; it has no user id, so `created_by` is NULL.
- **Audit:** every write method returns through `access.Audited(ctx, err, "object.verb", id,
  attrs...)`, which logs `audit: change` when `err` is nil and returns `err`. Code that runs
  several services in one transaction wraps them in `access.HoldAudit` and releases on commit
  (`vidispineimport.Service.Import`); dev-data scenarios run under `access.WithoutAudit`.
  `TestAuditLog` and `TestAuditLogOfAnImport` (`e2e/graph_test.go`) show the expected records.
- Administrator-only today: programs (create/delete), storage locations, all file writes, users
  and groups, Vidispine import and refresh.
- `access.RequirePersonAdmin(ctx)` is `RequireAdmin` minus machine clients. Use it for anything
  that creates credentials or administrators (`internal/access/serviceaccount.go`).
- A caller can be a service account (`Principal.Service`, `Principal.TokenID`). It is an
  `app_user` row, so nothing else in a service has to know.

## Services are constructed in three places

A new service, or a new dependency of one, must be added to all of them:

1. `internal/app/app.go` (`New`): production wiring, and the `graph.Resolver` fields.
2. `e2e/graph_test.go` (`probedServices`): the same over a fault-injecting database.
3. `internal/devdata/scenarios.go` (`services`, `newServices`): for scenarios.

`graph.Resolver` (`internal/graph/resolver.go`) holds concrete `*Service` pointers.

## Data model facts that are easy to miss

- Ids are `XX-<ULID>` and checked by the database, so an external id can never be a primary key.
  External ids go in `asset.legacy_ids` (text array, GIN index, `AssetFilter.legacyId`,
  `GetAssetByLegacyID`). It has no unique constraint; see `LockLegacyID`.
- Free-form JSON: `asset.metadata`, `rendition.metadata`, `rendition.overrides`, `file.probe`.
  All must be JSON objects (`domain.JSONObject`). `SetAssetMetadataKey` replaces one key.
- A file is identified by its hashes; `file.Register` is an idempotent upsert by hash and adds
  copies. A hash is required. Copy paths are relative, without `..`.
- `updateAsset` and the other `update…` operations replace every editable field.
- Subtitle import always adds a revision; only SRT and WebVTT parse.
- `language.alt_codes` holds 3-letter codes, but no query reads it yet.
- The id prefix `ST` is `subtitle_track`; service tokens are `TK`.
- Postgres cannot drop an enum value: a migration that adds one rebuilds the type in its Down
  (`00003_languages.sql`, `00008_vidispine_import.sql`).

## Config

`internal/config/config.go`: flat fields on `Config`, the env name in a trailing comment. Plain
strings are read in the map loop of `LoadFrom`; booleans with `boolEnv`. A group that must be
complete is checked at the end of `LoadFrom` (`ErrVidispineIncomplete`). `Validate` (AUTH required
unless `DEBUG`; `DEBUG` only on a loopback `HTTP_ADDR` unless `DEBUG_ALLOW_REMOTE`) runs only in `app.New`, so CLI commands other than `serve` skip it. New settings
also go in `.env.example` and `AGENTS.md`.

## CLI

`internal/cli/cli.go`: a command must be added to the first `switch` (names that are allowed),
the second `switch` (dispatch), the package comment and the `usage` text. Handlers are
`func(ctx, cfg, args, out) error`, connect to the database themselves, act as `access.System()`,
write results to `out` and return `usageErr(...)` for bad command lines. Flags use
`flag.NewFlagSet` with output discarded. Each command gets a `make` target.

## GraphQL

- One `.graphqls` file per area with `extend type Mutation`; who may call it is a description
  string on the field. Mutations return the object itself (no payload types).
- `gqlgen.yml` binds types to sqlc models and inputs to the services' input structs. A new enum
  value needs an `enum_values` line there.
- Resolvers are one line through a helper in `internal/graph/helpers.go`: `one`, `found` (null
  instead of `NOT_FOUND`), `many`, `plain`, `done`. `mask` turns anything that is not a known
  domain error into `INTERNAL`.
- Error codes are the table in `internal/graph/server.go` (`errorCodes`). A new domain error
  needs a line there, or it surfaces as `INTERNAL`. The message of a known error reaches the
  client, so it must not carry internal detail: log that instead.
- Nested fields load through `internal/graph/loaders.go`.

## Other systems

Outbound HTTP uses `net/http` directly. The consumer declares a small interface
(`auth.Verifier`, `vidispineimport.Source`) and tests run against a real `httptest` server from a
non-test package that e2e can import: `internal/auth/authtest` (token issuer),
`pkg/vidispine/vidispinetest` (fake Vidispine). Code other projects may import lives under
`pkg/` and must not import `internal/`.

## Tests

- `e2e/main_test.go` starts Postgres (testcontainers), the token issuer, the fake Vidispine and the
  app; `appConfig()` is the app's configuration. Globals: `pool`, `baseURL`, `issuer`, `vsServer`.
- Users: `signIn(t)`, `signInAdmin(t)`, `signInService(t, admin)` (a service account with a
  token; a token that was accepted once stays accepted for a minute, so tests of refusal use a
  token for the first time); `u.must`, `u.fails(t, code, query, vars)`, `u.sees`,
  `u.isNull`. Builders in `e2e/fixtures_test.go` (`newProgram`, `newProject`, `newClip`, ...);
  `newWorld` in `e2e/access_test.go`. Tests do not run in parallel.
- A new operation is added to: `TestAdminOnlyOperations` or the level tables of
  `TestPermissionLevels`, `TestNoAccessLooksLikeNotFound` when it reads by id
  (`e2e/access_test.go`), and the `asOwner`/`asAdmin` list of `TestDatabaseFailuresAreContained`
  (`e2e/graph_test.go`). In the sweep, `vars` is called before every attempt and must give fresh
  objects when the operation cannot be repeated.
- New nested fields go in `deepQuery` (`TestNestedQueriesAreBatched`).
- CLI: usage and connection-error tables in `internal/cli/cli_test.go`; success paths in e2e
  (`TestAdminCLI`, `TestVidispineCLI`, `TestCLIAgainstDB`). `startApp` and `freshDB` in
  `e2e/devdata_test.go` run a second app or database.
- `TestStoreScenarios` runs every registered scenario; `fake_test.go` `build()` lists every
  builder for the determinism test.
- Coverage (`scripts/coverage.sh`) counts `./internal/...` except generated code. `pkg/` is not
  counted, and has its own tests.

## Dev data

A scenario is an entry in `registry` (`internal/devdata/scenarios.go`) with `Params` and a
`Run(ctx, env, values)` that goes through the services. `internal/fake` builders return service
input structs and never read the clock. A `fake.Faker` is also an `io.Reader`: a scenario that
creates service tokens passes it to `access.Service.WithRand`, so the same seed gives the same
tokens. A scenario that needs another system uses a stand-in held
in memory (`vidispineItems`).

The debug UI is one file, `internal/devdata/ui/index.html`, over the JSON API in
`internal/devdata/http.go`. The handler sees only the `Backend` interface (`Store` implements
it; `http_test.go` has a fake), so a new debug action is a `Backend` method, a route, a card in
the page, a handler test and a case in `TestDebugAPI` (`e2e/devdata_test.go`). `writeResult`
maps domain errors to HTTP statuses. Every non-GET route is behind `guard` (same-origin and the
`X-Debug-Token` header); in the page, `api()` adds the token to a POST, and tests take it from
`GET /api/token` (`tokenOf`, `debugToken` in e2e). A debug action that changes anything must be a
POST, never a GET.

## Deployment

`Dockerfile` builds the one binary (migrations and the debug UI are embedded, so nothing else is
copied). `deploy/docker-compose.yml` is only the dev Postgres; `deploy/docker-compose.prod.yml`
is the deployment. A new setting that production needs goes in its `environment` block and in
`deploy/.env.prod.example`. Compose substitutes from the shell's environment, so its variables
must not share a name with a dev setting that means something else (`MEDIABRAIN_DATABASE_URL`).

## Docs and Yaak

- `docs/log.md`: newest first, `## YYYY-MM-DD — Title`, then what, why, and what was left out.
- `yaak/`: one YAML file per object, `yaak.rq_<10 characters>.yaml` for a request and `fl_` for a
  folder. Administrator-only requests end in "(admin)". Variables are in the `ev_` file and the
  table in `yaak/README.md`.
