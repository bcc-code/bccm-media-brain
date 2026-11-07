# MediaBrain

Tracks media files and their associated metadata.

Go core · PostgreSQL · goose · sqlc · gqlgen (GraphQL) · slog · Vue web UI (planned).

## Quick start

Requires Go and Docker.

```sh
make dev
```

This creates `.env`, starts Postgres, applies migrations and runs the server with live reload.

- GraphQL playground: http://localhost:8080/
- Debug UI (generate dev data from scenarios): http://localhost:8080/debug/

```sh
make seed-list                                  # available scenarios
make seed s=dummies args="-reset count=100"     # generate data from the CLI
```

## Development

```sh
make hooks        # once: pre-commit hook runs `make check`
make help         # all targets
make check        # vet + unit + e2e + ≥90% coverage gate
```

The binary reads `.env` itself, so `go run ./cmd/mediabrain help` works without make.
See [AGENTS.md](AGENTS.md) for layout, conventions and workflows.
