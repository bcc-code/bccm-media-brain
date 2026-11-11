# Log

Newest first. Each entry: what changed, why, and what was left out on purpose.

## 2026-11-11 — Cantemo in the debug UI

- `/debug/` has a Cantemo card to import one item and to refresh an imported asset, and shows
  the resulting asset and its metadata. Why: trying a real item should not need a GraphQL
  mutation or the CLI.
- It calls new debug API routes (`/debug/api/cantemo…`) that run `cantemo.Service` as the system
  principal, like scenarios do; `devdata.Store.Cantemo` is the source, set in `app.New`.
- Left out: looking at an item without importing it, and a list of imported assets.

## 2026-11-11 — Import and refresh from Cantemo

**What**
- `importFromCantemo` / `mediabrain cantemo import`: one Cantemo (Vidispine) item becomes an
  asset with its metadata and the file of its original shape. `refreshFromCantemo` /
  `cantemo refresh` replaces `metadata.cantemo`. Administrators only. Details in
  [cantemo.md](cantemo.md).
- `pkg/vidispine`: the Vidispine client, extracted from `bccm-flows` as a package other Go
  projects can import, with a fake server for tests.
- `sha1` hashes, the `UNAVAILABLE` error code, settings `CANTEMO_URL`, `CANTEMO_USERNAME`,
  `CANTEMO_PASSWORD`, dev-data scenario `cantemo`, and [codebase.md](codebase.md): a map of the
  code's conventions, so changes need not start with exploring.

**Why these choices**
- *One at a time, by an administrator.* The mapping can be judged on real items before anything
  is imported in bulk, and only administrators may register files.
- *Refresh touches only `metadata.cantemo`.* Title and description may have been edited here;
  Cantemo's values stay available next to them without overwriting anything.
- *The client lives in this repository under `pkg/`.* No new repository to maintain yet; the
  package imports nothing from `internal/`, so it can move without changing.
- *Read side of the client only.* That is what is used and tested here. Flows' write operations
  move over when flows switches to the package.

**Left out on purpose**
Bulk import, scheduled refresh, proxies and other shapes, subtitle sidecars, refreshing files,
mapping Portal's own fields (description, people, tags). Not yet tried against the real Cantemo.

## 2026-11-10 — Yaak workspace and a default debug user

- `yaak/` holds a Yaak workspace with requests for the standard operations.
- `DEBUG_USER` (debug mode only): requests that name no user act as this email, created as an
  administrator when missing. Why: with `make dev` there is no proxy, and the API refused
  everything until a scenario had created a user and one was picked in the debug UI; a data
  reset brought that state back. Without `DEBUG` the setting does nothing.

## 2026-11-10 — First real slice: assets, files, subtitles, projects, security

**What**

- Replaced the `dummy` placeholder with real code on top of the existing data model: programs
  and projects, storage locations, files (hashes, copies), assets (versions, renditions,
  rendition files, project links) and subtitles (import, edit, export).
- GraphQL API for all of it, with dataloaders for every nested field.
- Sign-in behind an auth proxy, the full grant model, system administrators.
- Dev data: `catalog`, `access`, `subtitle-revisions`, `edge-cases` scenarios; the debug UI can
  create users and act as them.
- Migrations: `00006` drops `dummy`, `00007` adds `app_user.is_admin`.

**Why these choices**

- *Files are registered, not scanned.* Reading disks, hashing and probing belong in an ingest
  tool; MediaBrain needed the model and API first. Identity by hash makes registration safe to
  repeat.
- *Subtitle edits create revisions* because the schema is built for it and it gives history and
  safe concurrent editing with one rule (only the latest revision can be edited).
- *Auth by proxy JWT.* Sign-in is handled centrally; MediaBrain still verifies the token so a
  forged header is useless, and only accepts it from trusted proxy addresses.
- *go-jose instead of go-oidc* for verification: no discovery call at startup, and it was the
  library available offline. The JWKS URL is configured explicitly.
- *Administrators are a flag on the user* rather than an SSO group, so admin rights are decided
  in MediaBrain. The first one is set from the CLI.
- *Permissions in the services, not the resolvers*, so the CLI, scenarios and any future
  transport get the same rules.
- *Dataloaders are mandatory* for nested fields; a test counts statements to keep it that way.
- *No in-memory database fake.* Permission rules are SQL; tests run against real Postgres. A
  probing wrapper around the connection gives statement counts and fault injection instead.
- *sqlc emits pointers and `json.RawMessage`* so models bind straight to GraphQL without
  per-field resolvers.

**Left out on purpose**

Disk scanning and probing, subtitle search, tags/credits/rights/groups/notes/collections,
timelines, service tokens, local group management in the API, cursor paging.
