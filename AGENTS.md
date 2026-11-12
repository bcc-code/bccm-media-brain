# AGENTS.md

Guidance for AI coding agents (and humans) working in this repository.

## Project

**MediaBrain** keeps track of media files and their associated metadata.

- **Core:** Go (module `github.com/bcc-media/mediabrain`), single binary in `cmd/mediabrain`.
- **HTTP:** standard library `net/http` (Go 1.22+ `ServeMux` patterns). No web framework.
- **Database:** PostgreSQL, accessed with `pgx/v5` (`pgxpool`).
- **Migrations:** [goose](https://github.com/pressly/goose), SQL files embedded into the binary.
- **Queries:** [sqlc](https://sqlc.dev) generates type-safe Go from SQL.
- **API:** GraphQL via [gqlgen](https://gqlgen.com) (schema-first).
- **Logging:** `log/slog` only. `LOG_FORMAT=json` for structured output, `LOG_LEVEL` for verbosity.
- **Config:** environment variables, with `.env` loaded by the binary itself
  (`godotenv`; real env vars win; override path with `MEDIABRAIN_ENV_FILE`).
- **Web UI:** Vue, planned. `web/` is a placeholder.

**[`docs/`](docs/README.md) records what has been built and why**: architecture, data model, API,
security, and a dated log. Read it before changing behaviour, and update it in the same change.

**Before exploring the code, read [`docs/codebase.md`](docs/codebase.md)**: where things are, the
conventions for services, config, CLI, GraphQL and tests, and the places a new operation must be
added to. Add to it when you learn something a future change would have to rediscover.

## Layout

```
cmd/mediabrain/         thin main -> internal/cli
internal/cli/           subcommands: serve | migrate | seed | reset-data | admin | service | vidispine
internal/app/           wiring (DB, migrations, services, HTTP server), routes, request logging
internal/config/        env + .env config
internal/logging/       slog setup (text/json)
internal/db/            pgx pool, embedded goose migrations
  migrations/           goose SQL migrations (also the schema source for sqlc)
  queries/              sqlc query files
  sqlc/                 GENERATED, do not edit
internal/domain/        shared errors, paging and input rules
internal/access/        principals, permission levels, grants, asset_access refresh; users, groups, service accounts and tokens
internal/auth/          verifies the proxy's JWT or a service token, trusted proxies, debug bypass (authtest/: test issuer)
internal/<domain>/      domain services over sqlc.Querier: project, file, asset, subtitle, vidispineimport
internal/graph/         gqlgen resolvers (hand-written), dataloaders, error presenter, server setup
  schema/*.graphqls     GraphQL schema (source of truth)
  loader/, scalar/      generic batching loader; Date and JSON scalars
  generated/, model/models_gen.go   GENERATED, do not edit
internal/fake/          gofakeit-based, seedable fake input builders (tests + dev data)
internal/devdata/       dev-data SCENARIOS, debug UI (ui/index.html) and its JSON API
pkg/vidispine/          Vidispine REST client other Go projects may import; vidispinetest/: fake server
e2e/                    tests against real Postgres (build tag `e2e`, testcontainers): API, permissions, scenarios
docs/                   what has been built and why
yaak/                   Yaak API-client workspace with requests for the standard operations
deploy/                 docker-compose for local Postgres; docker-compose.prod.yml for a deployment
Dockerfile              image of the binary
scripts/                dev bootstrap, codegen-on-change, coverage gate, git hooks
web/                    future Vue app
```

HTTP routes: `POST /query` (GraphQL, authenticated), `GET /` (playground, only when
`GRAPHQL_PLAYGROUND=true`), `GET /healthz`, `/debug/` (dev-data UI + API, only when `DEBUG=true`).

## Development

`make dev` is the one command you need. It:

1. creates `.env` from `.env.example` if it's missing,
2. starts Postgres via docker compose,
3. runs the server under [air](https://github.com/air-verse/air) (`.air.toml`): on every change
   to `.go`, `.sql`, `.graphqls`, embedded `.html` or codegen config it re-runs sqlc/gqlgen
   (only if their inputs changed, see `scripts/dev-codegen.sh`), then rebuilds and restarts.
   New migrations apply on restart (`AUTO_MIGRATE=true`).

Then open `http://localhost:8080/debug/` to generate data and choose the user to act as, and
`http://localhost:8080/` for the GraphQL playground. The API refuses anonymous requests, but with `DEBUG_USER` set
(as in `.env.example`) requests that name no user run as that administrator, so the playground
and the Yaak workspace work straight away. Choose another user in the debug UI to see what they see.

Live reload caveats:
- Editing a migration that is **already applied** does nothing on restart. Run `make db-reset`
  (rolls back everything and re-applies; wipes data), then re-seed.
- When a build fails, the previous server keeps running. Check the air output.

| Task | Command |
|---|---|
| Dev environment (live reload) | `make dev` |
| Run once, no reload | `make run` |
| Dev data: list / run scenario | `make seed-list` / `make seed s=catalog args="-reset -seed 42 assets=20"` |
| System administrator | `make admin-grant u=me@example.com` / `make admin-revoke u=...` (the user must have signed in once) |
| Machine clients (service accounts) | `make service-create n=ingest args="-admin"` / `make service-token n=ingest` (prints the token once) / `make service-revoke t=TK-...` / `make service-list` |
| Vidispine: import one item / refresh | `make vidispine-import id=VX-123 args="-type 1 -project PJ-..."` / `make vidispine-refresh a=AS-...` (needs `VIDISPINE_*`) |
| Format | `make fmt` |
| Wipe data / rebuild schema | `make reset-data` / `make db-reset` |
| New migration | `make migrate-new name=add_something` |
| Migration status / up / down | `make migrate-status` / `migrate-up` / `migrate-down` |
| Regenerate code | `make generate` |
| Image / check the production compose file | `make docker-build` / `make prod-config` (needs `deploy/.env.prod`) |
| Unit tests | `make test` (no docker) |
| E2E tests | `make test-e2e` (needs docker) |
| **Pre-commit check** | **`make check`** (go vet + unit + e2e + ≥ 90% coverage gate) |

The binary does the same without make: `go run ./cmd/mediabrain help`.
Tools (goose, sqlc, gqlgen, air) are pinned as `tool` directives in `go.mod`. Use
`go tool <name>`, never global installs.

## Dev data and the debug UI

`internal/devdata` holds a registry of **scenarios** (`scenarios.go`). A scenario is a named,
parameterised recipe that creates a specific data situation, for example "a catalog of N assets",
"one persona per permission level" or "edge cases". Scenarios:
- show up automatically in the debug UI (`/debug/`, a form per scenario generated from its
  `Params`) and in `mediabrain seed`,
- run in one transaction, optionally after wiping all tables (`reset`),
- are reproducible: a run reports its seed, and passing the same seed recreates the same data,
- use `internal/fake` for realistic values and go through the domain services (as the system
  principal), so the data respects the same rules as the API.

The debug UI also lists users and lets you act as one of them ("Act as user"): it sets the
`mb_debug_user` cookie that `/query` honours when `DEBUG=true`. `X-Debug-User: <email or id>` does
the same for curl.

A POST to the debug API must send the server's request token, so that another site open in your
browser cannot wipe the data. The page does it by itself; with curl:
`curl -X POST -H "X-Debug-Token: $(curl -s localhost:8080/debug/api/token | jq -r .token)" localhost:8080/debug/api/reset`.

With the `VIDISPINE_*` settings, the **Vidispine** card imports one item or refreshes an imported
asset (as the system principal) and shows the result.

Reset and stats work from the schema (they read every table in `public` except goose's), so they
need no updates. The one exception: reference tables that a migration seeds (`asset_type`,
`language`) are kept by reset. When a migration seeds a new lookup table, add it to
`referenceTables` in `internal/devdata/store.go`.

## Adding/changing data: checklist

1. Add a goose migration in `internal/db/migrations/`.
2. Add/modify SQL in `internal/db/queries/`.
3. Update the GraphQL schema in `internal/graph/schema/`.
4. `make generate` (or just save while `make dev` runs), then implement resolvers. Keep them thin:
   logic lives in a domain service under `internal/<domain>/` that depends on `sqlc.Querier`.
5. **Update dev data:** add fake builders to `internal/fake` and add/extend scenarios in
   `internal/devdata/scenarios.go` so the new entity can be generated from the debug UI, with
   realistic values plus edge cases. Remove or replace scenarios for entities that no longer
   exist.
6. Tests. Everything that touches the database (services, permissions, resolvers) is tested in
   `e2e/` against real Postgres with fake data, as each kind of user. Unit tests cover what needs
   no database (parsers, validation, auth, config). There is no in-memory fake of the database:
   the permission rules live in SQL, so a fake would test a copy of them. `e2e/` runs every
   scenario automatically, so a broken scenario fails `make check`.
7. **Security:** decide who may call the new operation and enforce it in the service with
   `internal/access` (never in a resolver). Add the operation to the permission tests in
   `e2e/access_test.go` and to the fault sweep in `e2e/graph_test.go`. A write returns through
   `access.Audited` so the audit log records it (see `docs/codebase.md`).
8. **No N+1:** nested GraphQL fields load through a dataloader (`internal/graph/loaders.go`)
   backed by a query that takes a list of parent ids. `TestNestedQueriesAreBatched` fails if a
   field queries per item.
9. Update `docs/`, and the requests in `yaak/` when API operations are added or changed.

## Rules for agents

- **Before every commit, run `make check` and make sure it passes.** That covers `go vet`
  (including `-tags e2e`), unit tests, e2e tests and the ≥ 90% coverage gate. Never commit
  with failing vet or tests, and never bypass the hook (`--no-verify`). If docker is unavailable
  and e2e can't run, say so explicitly instead of committing.
- Run `make hooks` once per clone to enable the pre-commit hook that enforces this.
- **Testing is required:** ≥ 90% coverage of hand-written code (generated code is excluded by
  `scripts/coverage.sh`), with unit tests for database-free logic and docker-based tests in
  `e2e/` using fake data from `internal/fake` for everything else.
- **Keep dev data in sync with the model.** Any change to tables, domain services or GraphQL
  types must update `internal/devdata` scenarios (and `internal/fake`) in the same change.
  The debug UI is how humans try the system, so it must never lag behind.
- `/debug/` and `DEBUG=true` are for development only. Never enable them by default outside
  `.env.example`, and never expose dev-data endpoints through GraphQL. Debug mode listens on
  loopback only (the server refuses another `HTTP_ADDR` unless `DEBUG_ALLOW_REMOTE=true`), and
  every POST to the debug API needs the `X-Debug-Token` header; keep both when adding routes.
- Log with `log/slog` only (no `fmt.Print*`/`log.*` for diagnostics). Use structured key/value
  attributes rather than formatted strings, so JSON output stays useful. CLI result output to
  stdout is the only exception.
- Config goes through `internal/config` (env + `.env`). Add new settings there, in
  `.env.example`, and in this file. Current settings: `DATABASE_URL`, `HTTP_ADDR`, `AUTO_MIGRATE`,
  `GRAPHQL_PLAYGROUND`, `DEBUG`, `DEBUG_USER`, `DEBUG_ALLOW_REMOTE`, `LOG_FORMAT`, `LOG_LEVEL`, and for sign-in `AUTH_ISSUER`,
  `AUTH_AUDIENCE`, `AUTH_JWKS_URL`, `AUTH_TRUSTED_PROXIES`, `AUTH_HEADER`, `AUTH_SERVICE_HEADER`, `AUTH_CLIENT_HEADER`, `AUTH_GROUPS_CLAIM`, and for
  the Vidispine import `VIDISPINE_URL`, `VIDISPINE_USERNAME`, `VIDISPINE_PASSWORD` (optional, all or none).
- **Security is not optional.** Every query and mutation is authenticated and checks permissions
  in its domain service. Reading something without access must look exactly like "not found".
  Unexpected errors reach the client only as a generic `INTERNAL` error. The server must refuse to
  start without the `AUTH_*` settings unless `DEBUG=true`.
- **Keep `docs/` current.** Record what was built and why, concisely, in the same change; add a
  dated entry to `docs/log.md`.
- **Comment only what the code cannot say.** No comments that restate a name or a signature.
  Write one when a reader could not work out the reason, the rule or the trap from the code itself.
- Never edit generated files (`internal/db/sqlc/`, `internal/graph/generated/`,
  `internal/graph/model/models_gen.go`). Change the source and run `make generate`.
  gqlgen preserves resolver bodies in `*.resolvers.go`.
- Commit generated code together with its sources so the tree always builds.
- Keep `cmd/` thin. Anything testable belongs under `internal/`.
- Run `gofmt` on changed Go files.
