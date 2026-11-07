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

> The data model is **not designed yet**. Everything named `dummy` (table, queries,
> `internal/dummy`, GraphQL `Dummy` type, dev-data scenarios, tests) is a placeholder
> that proves the pipeline and will be replaced. Don't build features on top of it.

## Layout

```
cmd/mediabrain/         thin main -> internal/cli
internal/cli/           subcommands: serve | migrate | seed | reset-data
internal/app/           wiring (DB, migrations, services, HTTP server), routes, request logging
internal/config/        env + .env config
internal/logging/       slog setup (text/json)
internal/db/            pgx pool, embedded goose migrations
  migrations/           goose SQL migrations (also the schema source for sqlc)
  queries/              sqlc query files
  sqlc/                 GENERATED, do not edit
internal/<domain>/      domain services over sqlc.Querier (currently: dummy)
internal/graph/         gqlgen resolvers (hand-written) + resolver.go (DI)
  schema/*.graphqls     GraphQL schema (source of truth)
  generated/, model/models_gen.go   GENERATED, do not edit
internal/fake/          gofakeit-based, seedable fake data builders (tests + dev data)
internal/devdata/       dev-data SCENARIOS, debug UI (ui/index.html) and its JSON API
e2e/                    end-to-end tests (build tag `e2e`, Postgres via testcontainers)
deploy/                 docker-compose for local Postgres
scripts/                dev bootstrap, codegen-on-change, coverage gate, git hooks
web/                    future Vue app
```

HTTP routes: `POST /query` (GraphQL), `GET /` (playground), `GET /healthz`,
`/debug/` (dev-data UI + API, only when `DEBUG=true`).

## Development

`make dev` is the one command you need. It:

1. creates `.env` from `.env.example` if it's missing,
2. starts Postgres via docker compose,
3. runs the server under [air](https://github.com/air-verse/air) (`.air.toml`): on every change
   to `.go`, `.sql`, `.graphqls`, embedded `.html` or codegen config it re-runs sqlc/gqlgen
   (only if their inputs changed, see `scripts/dev-codegen.sh`), then rebuilds and restarts.
   New migrations apply on restart (`AUTO_MIGRATE=true`).

Then open `http://localhost:8080/` for the GraphQL playground and `http://localhost:8080/debug/`
to generate data.

Live reload caveats:
- Editing a migration that is **already applied** does nothing on restart. Run `make db-reset`
  (rolls back everything and re-applies; wipes data), then re-seed.
- When a build fails, the previous server keeps running. Check the air output.

| Task | Command |
|---|---|
| Dev environment (live reload) | `make dev` |
| Run once, no reload | `make run` |
| Dev data: list / run scenario | `make seed-list` / `make seed s=dummies args="-reset -seed 42 count=100"` |
| Wipe data / rebuild schema | `make reset-data` / `make db-reset` |
| New migration | `make migrate-new name=add_something` |
| Migration status / up / down | `make migrate-status` / `migrate-up` / `migrate-down` |
| Regenerate code | `make generate` |
| Unit tests | `make test` (no docker) |
| E2E tests | `make test-e2e` (needs docker) |
| **Pre-commit check** | **`make check`** (go vet + unit + e2e + ≥ 90% coverage gate) |

The binary does the same without make: `go run ./cmd/mediabrain help`.
Tools (goose, sqlc, gqlgen, air) are pinned as `tool` directives in `go.mod`. Use
`go tool <name>`, never global installs.

## Dev data and the debug UI

`internal/devdata` holds a registry of **scenarios** (`scenarios.go`). A scenario is a named,
parameterised recipe that creates a specific data situation, for example "N dummies" or "edge
cases". Scenarios:
- show up automatically in the debug UI (`/debug/`, a form per scenario generated from its
  `Params`) and in `mediabrain seed`,
- run in one transaction, optionally after wiping all tables (`reset`),
- are reproducible: a run reports its seed, and passing the same seed recreates the same data,
- use `internal/fake` for realistic values and should go through domain services, so the data
  respects invariants.

Reset and stats work from the schema (they read every table in `public` except goose's), so they
need no updates.

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
6. Tests: unit tests (fake Querier / fake service) **and** e2e coverage with fake data.
   `internal/devdata` tests run every scenario automatically (in memory and against real
   Postgres), so a broken scenario fails `make check`.

## Rules for agents

- **Before every commit, run `make check` and make sure it passes.** That covers `go vet`
  (including `-tags e2e`), unit tests, e2e tests and the ≥ 90% coverage gate. Never commit
  with failing vet or tests, and never bypass the hook (`--no-verify`). If docker is unavailable
  and e2e can't run, say so explicitly instead of committing.
- Run `make hooks` once per clone to enable the pre-commit hook that enforces this.
- **Testing is required:** ≥ 90% coverage of hand-written code (generated code is excluded by
  `scripts/coverage.sh`), with both unit tests and docker-based e2e tests using fake data
  from `internal/fake`.
- **Keep dev data in sync with the model.** Any change to tables, domain services or GraphQL
  types must update `internal/devdata` scenarios (and `internal/fake`) in the same change.
  The debug UI is how humans try the system, so it must never lag behind.
- `/debug/` and `DEBUG=true` are for development only. Never enable them by default outside
  `.env.example`, and never expose dev-data endpoints through GraphQL.
- Log with `log/slog` only (no `fmt.Print*`/`log.*` for diagnostics). Use structured key/value
  attributes rather than formatted strings, so JSON output stays useful. CLI result output to
  stdout is the only exception.
- Config goes through `internal/config` (env + `.env`). Add new settings there, in
  `.env.example`, and in this file.
- Never edit generated files (`internal/db/sqlc/`, `internal/graph/generated/`,
  `internal/graph/model/models_gen.go`). Change the source and run `make generate`.
  gqlgen preserves resolver bodies in `*.resolvers.go`.
- Commit generated code together with its sources so the tree always builds.
- Keep `cmd/` thin. Anything testable belongs under `internal/`.
- Run `gofmt` on changed Go files.
