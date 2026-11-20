# web

The MediaBrain web UI: [Nuxt](https://nuxt.com) without server rendering,
[Tailwind CSS](https://tailwindcss.com) and [Ark UI](https://ark-ui.com). It talks only to the
API's GraphQL endpoint, `/query`, on its own origin.

Pages so far: **Service accounts** (`/service-accounts`): list, create, switch off and on.
Administrators only, as the API decides.

## Development

```sh
make dev          # the API on 127.0.0.1:8080, in another terminal
make web-install  # once
make web-dev      # http://localhost:3000/admin/
```

The dev server forwards `/query` to the API (`MEDIABRAIN_API` overrides its address). With
`DEBUG_USER` set, as in `.env.example`, the page acts as that administrator.

## Checks

`make web-check` runs the type check, the unit tests (Vitest, coverage of at least 90%) and the
build. The build is static files in `.output/public`.

## Serving

`make build` (and the image) copies the build into `internal/webui/dist` and compiles it into the
binary, which serves it at `/admin/`. A plain `go build` or `make dev` has no `/admin/`; use the
dev server above.

Conventions are in [`docs/codebase.md`](../docs/codebase.md#web-ui).
