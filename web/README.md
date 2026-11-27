# web

The MediaBrain web UI: [Nuxt](https://nuxt.com) without server rendering,
[Tailwind CSS](https://tailwindcss.com) and [Ark UI](https://ark-ui.com). It talks only to the
API's GraphQL endpoint, `/query`, on its own origin.

Pages so far: **Assets** (`/assets`, the start page): the assets the caller may see, newest
first, 50 at a time with "Load more". For anyone. Administrators also get an "Import from
Vidispine" panel: a `VX-…` id is imported, or re-imported when it was imported before.

**Users** (`/users`): people (not service accounts), searched by name or email, with their groups;
make an administrator or not, switch off and on. Not your own account. Administrators only.

**Service accounts** (`/service-accounts`): list, create, switch off and on, make
an administrator or not.
Administrators only, as the API decides.

**Subtitles** (`/subtitles`): open an asset by its id or a legacy id (`VX-123`), browse its
subtitle tracks and their revisions, and compare two revisions of a track. Read-only, for anyone
who may view the asset.

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
