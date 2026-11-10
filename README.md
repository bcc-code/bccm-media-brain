# MediaBrain

Tracks media files and their associated metadata.

Go core · PostgreSQL · goose · sqlc · gqlgen (GraphQL) · slog · Vue web UI (planned).

## Quick start

Requires Go and Docker.

```sh
make dev
```

This creates `.env`, starts Postgres, applies migrations and runs the server with live reload.

- Debug UI (generate dev data, choose the user to act as): http://localhost:8080/debug/
- GraphQL playground: http://localhost:8080/

The API refuses anonymous requests. In development (`DEBUG=true`, `DEBUG_USER` set as in
`.env.example`) requests run as the administrator `admin@example.test` unless you pick another
user: run the `access` scenario in the debug UI and click "Act as" to see what that user sees.

```sh
make seed-list                                        # available scenarios
make seed s=catalog args="-reset -seed 42 assets=20"  # generate data from the CLI
```

## Development

```sh
make hooks        # once: pre-commit hook runs `make check`
make help         # all targets
make check        # vet + unit + e2e + ≥90% coverage gate
```

[`yaak/`](yaak/README.md) is a [Yaak](https://yaak.app) workspace with requests for the standard
API operations.

The binary reads `.env` itself, so `go run ./cmd/mediabrain help` works without make.
See [AGENTS.md](AGENTS.md) for layout, conventions and workflows, and [docs/](docs/README.md) for what
has been built and why.
