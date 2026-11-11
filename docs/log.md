# Log

Newest first. Each entry: what changed, why, and what was left out on purpose.

## 2026-11-11 — The import is named after Vidispine, not Cantemo

- Everything that was called Cantemo is now called Vidispine: settings `VIDISPINE_URL`,
  `VIDISPINE_USERNAME`, `VIDISPINE_PASSWORD`; mutations `importFromVidispine` and
  `refreshFromVidispine`; `mediabrain vidispine import|refresh` and `make vidispine-import` /
  `vidispine-refresh`; the metadata key `metadata.vidispine`; the version label "Vidispine
  original"; the `vidispine` scenario and the debug UI card and routes; the package
  `internal/vidispineimport`; [vidispine.md](vidispine.md).
- Why: things are named after the API they talk to. The media lives in Cantemo Portal, but only
  the Vidispine API that Portal exposes under `/API` is called; Portal's own API is not.
- The service package is `vidispineimport` because `pkg/vidispine` is the client and two packages
  with one name would need aliases wherever both are used.
- Not migrated: assets imported before this keep `metadata.cantemo` and are not found by
  refresh through it. Nothing real had been imported; re-seed dev data. The entries below keep
  the names of their time.

## 2026-11-11 — Decided: scrubbing previews are sprite sheets

Nothing is built yet. This records the choice so the implementation does not reopen it.

- **Decision:** the preview a user scrubs through is a set of sprite sheets: frames tiled into a
  few JPEGs, with one coarse sheet covering the whole asset so the first hover works at once and
  finer sheets loaded for the part being scrubbed.
- **Detail adapts to length.** The coarse sheet is always 100 frames spread evenly over the
  asset, never closer together than the source frames, so its interval is `duration / 100`: a
  10 s clip gets a frame every 0.1 s, a clip under 100 frames gets every frame, a one-hour asset
  one every 36 s. Finer sheets hold one frame per second and exist only when that is finer than
  the coarse sheet, so for assets longer than 100 s. Each level stores its interval, and the UI
  finds a tile from the timestamp and that interval, so the numbers can change without changing
  the UI. Why: a fixed interval of one second would give a clip under 10 s fewer than 10 frames.
- **Why sprites:** once a sheet is loaded, dragging needs no requests, so it stays instant on a slow
  connection; an asset has a handful of objects instead of one per frame; and plain cached images
  are cheap in a grid of many assets. Several people tried the demo, and sprites gave the best
  experience.
- **Compared in a throwaway demo** on a 14 min 48 s 1080p film, stills 240x102 at one per
  second, with 200 ms added to every request:

  | Option | Size | Objects | Why not |
  |---|---|---|---|
  | Sprite sheets (9 + 1 coarse) | 3.3 MB | 10 | chosen |
  | Single images | 3.2 MB | 888 | one request per frame shown |
  | SQLite file per asset | 3.9 MB | 1 | the browser cannot read it, so one request per frame again |
  | Video, keyframe every 1 s, 12 fps | 9.8 MB | 1 | every pointer move is a range request and a decode |
  | Video, keyframe every 10 s, 12 fps | 6.9 MB | 1 | as above, and a seek decodes up to 10 s |
  | Video of keyframes only, 1 fps | 4.4 MB | 1 | larger than the same frames as sprites, no motion |

  Generating took 12 to 14 s for every option; decoding the source dominates.
- **Left open:** tile size, who runs ffmpeg and when, where the sheets are stored,
  and the authenticated route that serves them. MediaBrain reads no media today, so all of these
  are new. A preview video for hover-to-play is not ruled out as an addition.

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
  [vidispine.md](vidispine.md).
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
