# Log

Newest first. Each entry: what changed, why, and what was left out on purpose.

## 2026-11-12 — Paged lists are connections with cursors

- Every list that can grow returned a bare array with `limit` and `offset`. Nothing said that
  a list was cut: an editor that read 500 of 800 cues and saved would have replaced the track
  with those 500. Offsets also shifted when rows were added, so the last asset of one page came
  back as the first of the next (system review, finding GQL-11). The eight paged fields now
  return `{ nodes pageInfo { hasNextPage endCursor } }` and take `first` and `after`:
  `assets`, `files`, `users`, `programs`, `projects`, `Asset.versions`, `Program.projects`,
  `SubtitleTrack.cues`.
- **Breaking API change**, made now because nothing outside this repository uses the API:
  `limit` and `offset` are gone, and each of those fields has a new return type. The Yaak
  requests and the examples in `api.md` are updated.
- A cursor is the sort key of the last item (id, code, version number, cue sequence), encoded
  so that clients treat it as opaque. Each list already sorted by a unique key, so no query
  changed its order; `OFFSET` became `key > after`, and one extra row is read to know whether
  a next page exists. Nested lists still load in one statement per batch.
- Cost is unchanged for the same data: the connection field counts its selection once per
  item, `nodes` adds nothing. `pageInfo` adds its fields once per item, not once per page:
  gqlgen ignores a cost lower than the selection's.
- Left out: a total count, paging backwards, and choosing the order. The short nested lists
  (renditions, files, hashes, copies, links, subtitle tracks) stay plain lists; subtitle track
  history is still unpaged and can grow with every edit. `editSubtitles` does not check that
  the caller had read the whole track: a client must page until `hasNextPage` is false.

## 2026-11-12 — Audit records name the request and the client

- An audit record said who changed what, but not with which request or from where, and the
  request log's `remote` is the proxy (system review, finding SEC-08). Every request now has
  an id, returned as `X-Request-Id` and written as `request` on its request line and on the
  audit records and refusals it causes, with `client`, the address it came from. The request
  line of `/query` also names the `user` and `token`.
- New setting `AUTH_CLIENT_HEADER`: the header in which the proxy reports its client. Read
  only from a trusted proxy, only recorded. Unset, `client` is the peer.
- The id is made here and one sent by the caller is ignored: a caller could otherwise reuse an
  id to blur which request did what.
- Left out, by decision: logging `NOT_FOUND` answers. Trying ids one has no access to stays
  invisible in the log. Also left out: an append-only store for audit lines; the log remains
  the only copy.

## 2026-11-11 — A pool size chosen on purpose, and nothing waits without end

- The pool was pgx's default, the CPU count and at least 4, while one GraphQL request runs
  several statements at once: on a 2-CPU container one deep query filled it (system review,
  finding PERF-07). It is now 16 unless `DATABASE_URL` says `pool_max_conns`.
- Every connection sets `statement_timeout` (35 s) and
  `idle_in_transaction_session_timeout` (1 min). A request already had its 30 s deadline; the
  sweeper and the CLI had none. The sweep also gets 30 s of its own, so one that waits on a
  lock gives its connection back.
- `/healthz` waits at most 2 s for the database and then answers 503. It pinged through the
  same pool with no limit, so a full pool made the probe hang.
- Left out: a load test. 16 is a judgement, not a measurement; the review's suggestion stands
  (the deep query at 20 requests a second with the pool at 4 and at 16, watching
  `pool.Stat().EmptyAcquireCount()`). Splitting liveness from readiness: one endpoint that
  always answers in 2 s was enough for the failure described.

## 2026-11-11 — Every error has a code, and says what a client acts on

- **Code.** A `Date` or `Time` that could not be read answered with a message and no
  `extensions` at all, against what `api.md` promised (system review, finding GQL-05). The
  scalars now return `INVALID_INPUT` (`Time` is gqlgen's own, wrapped). The presenter answers
  `INTERNAL`, and logs the cause, for anything else that arrives without a code, so the
  promise no longer depends on each error's origin. gqlgen's three codes for a request that
  never reaches a service are in the error table.
- **Detail.** `CONFLICT` meant a stale revision, a value already taken, a delete blocked by
  references or a superseded subtitle revision, told apart only by the English message; an
  `INVALID_INPUT` did not name the field (finding GQL-06). `extensions` now has `reason`
  (`STALE_REVISION`, `ALREADY_EXISTS`, `IN_USE`), `currentRevision` for the stale case and
  `field`. They ride on a typed domain error (`domain.Detail`), not on the message, and the
  codes are unchanged, so nothing a client relied on moved.
- A reference to something that does not exist names its field from the foreign key's name
  (`asset_type_id_fkey` on `asset` is `typeId`): the API names fields after columns.
- Left out: the field behind `ALREADY_EXISTS` (a unique constraint can span columns, and its
  name does not say which input was the taken one); a `field` for errors about several fields
  or about subtitle content; the conflicts of file registration, which stay without a reason.

## 2026-11-11 — Origin columns named for what they hold, copies know their age

- `asset_project.linked_by` and `asset_group_member.added_by` held an origin (folder or manual;
  rule or person), while `*_by` names a user everywhere else. They are now `link_origin` and
  `member_origin` (system review, finding DB-13; migration `00015`). **API change:**
  `AssetProjectLink.linkedBy` is now `linkOrigin`. Renamed rather than kept beside the new
  name because nothing outside this repository uses the API yet.
- `file_copy.created_at` (`FileCopy.createdAt`): when a copy was registered. Existing rows take
  the time from their id, which starts with its creation time.
- `subtitle_track.language` is a copy of its rendition's language, and `updateRendition`
  changed only the original. A trigger on `rendition` now carries the change to its tracks,
  and the migration brings existing tracks back in line.
- Left out: the other timestamp names (`computed_at`, `added_at`, `granted_at`,
  `refreshed_at`) say what happened and stay; `user_group` and `rendition_file` still have no
  `created_at`; dropping `subtitle_track.language` instead of syncing it would change every
  track query and the loaders for no gain to a client.

## 2026-11-11 — The database counts revisions

- `revision` was incremented by each UPDATE statement that remembered to; the codebase guide
  had to warn that a new query must do it too. One that forgot, or a fix by hand in psql, left
  the counter where it was, and an update based on the old row was then accepted (system
  review, finding DB-09). Migration `00014` adds a `bump_revision` trigger to program, project,
  asset, asset version, rendition and storage location, and the queries no longer write the
  column. The check stays the UPDATE's own `WHERE revision = ...`, which sees the old value.
- Asset version, rendition and storage location get `updated_at`, kept by the same trigger as
  elsewhere, and `updatedAt` in the API. Rows that existed start at their `created_at`.
- A consequence to know: every UPDATE of such a row now counts, also one that changes nothing
  and one the database makes itself (`ON DELETE SET NULL` on `asset_version.created_by`).
  Before, only the statements that said so counted.
- Left out: comparing old and new row to skip an UPDATE that changes nothing. A full
  replacement with the same values counted as an edit before, and still does.

## 2026-11-11 — Indexes for what a delete looks up

- Deleting a file or a subtitle track makes the database find the rows that point at it, once
  per deleted row. `subtitle_track.parent_id`, `subtitle_track.source_file_id` and
  `timeline.source_file_id` had no index, so each lookup read the whole table, and
  `subtitle_track` gets a row per edit (system review, findings DB-06 and PERF-06). Migration
  `00013` adds partial indexes (`WHERE ... IS NOT NULL`: most rows have no parent or source file).
- `TestDeletedTablesAreReferencedThroughIndexes` reads the catalog and fails when a foreign key
  to a table the API deletes from has no index on the referencing column.
- Left out: the `created_by`, `granted_by` and `added_by` columns that point at `app_user`.
  Users are never deleted. Not measured: the gain was not timed on a large table.

## 2026-11-11 — An email is unique whatever its case

- `app_user.email` was unique as written but looked up with `lower()`, so `Ann@x.org` and
  `ann@x.org` could be two accounts and a grant by email, or `admin grant <email>`, went to
  whichever the lookup returned first (system review, finding DB-04). Sign-in checked before
  writing; creating a user did not, and two first sign-ins at once could both pass the check.
  Migration `00012` replaces the constraint with a unique index on `lower(email)`. The lookup
  now uses that index: before, every sign-in that missed the cache and every grant by email
  scanned the table.
- Where accounts already share an address, the migration leaves it with the oldest and clears
  it on the others: what sign-in does with a token that brings another account's address. The
  cleared addresses are not kept anywhere, so the down migration does not restore them.
- Left out: two first sign-ins at the same moment with the same address in different case. The
  database now refuses the second one, which answers `INTERNAL`; signing in again works, and
  that account is then stored without the address. `citext` was not used: it needs an
  extension for what one index does.

## 2026-11-11 — Queries have a cost, a deadline and paged nested lists

- **Cost.** The limit of 2000 selected fields counted a list as one field, so
  `assets(limit: 500)` with unpaged nested lists passed it while asking for hundreds of
  thousands of rows (architecture review, finding 08). A list now costs its selection once per
  item it may return, and the limit is 1 000 000: enough for one asset with everything down to
  its cues (543 560) or 100 assets with versions, renditions and files (860 201), not for 500
  assets with those three levels (4 301 001). Lists without a `limit` count with an assumed
  size. The cost assumes full lists (50 versions per asset by default), which real data is far
  from; a limit of 250 000 was tried first and refused the single-asset query above.
- **Deadline.** A `/query` request is stopped after 30 seconds through its context, and answers
  `TIMEOUT`. A new code rather than `UNAVAILABLE`: that one says another system did not answer
  and trying again may help; here the same request will be too slow again. The HTTP read
  timeout never stopped a statement that was already running.
- **Paging.** `Asset.versions` and `Program.projects` take `limit` and `offset`, per parent,
  still one statement per batch. Without arguments they return 50 versions and 200 projects:
  a client written before the arguments existed keeps getting everything unless an asset has
  more than 50 versions or a program more than 200 projects.
- Left out: paging renditions, files, hashes and copies (short by nature; they count with an
  assumed size, so the cost of a query over them is an estimate, not a bound), a limit on the
  size of the answer in bytes, per-user rate limits, and a load test at production scale: the
  numbers come from arithmetic, not from measurement. A query with very many aliased fields is
  still parsed and validated before its cost is known; the 8 MB body limit is what bounds that.

## 2026-11-11 — Updates can name the revision they were based on

- Program, project, asset, asset version, rendition and storage location have a `revision`
  that every update of their own fields increments, and each `update...` mutation takes an
  optional `expectedRevision`. A stale one is refused with `CONFLICT` and changes nothing. Why:
  updates replace every field, so two people editing the same object overwrote each other
  without either noticing (architecture review, finding 09). Subtitle edits already refused
  stale revisions; this is the same rule for the rest.
- The check is the UPDATE's own `WHERE revision = ...`, not a read followed by a write, so of
  several edits sent at once on one revision exactly one is accepted.
- `expectedRevision` is an argument, not an input field: the inputs are shared with `create...`
  and bound to the services' input structs. It is optional, so existing clients keep working
  with last-write-wins; an interactive client should always send it.
- Left out: patch inputs that tell "left out" from "cleared". Full replacement stays, now
  spelled out on every `update...` mutation (an omitted optional field is cleared, an omitted
  status becomes `PLANNED` / `DRAFT`). Changing that is a redesign of the API's inputs that
  has not been decided. Also left out: making `expectedRevision` required, and revisions on
  files, copies, grants and users, which have no full-replacement update.

## 2026-11-11 — The audit log records what happened

- Services now write `audit: change` after a successful write (`access.Audited`), with the
  actor, an action name, the target and the details that matter: a grant's scope, recipient,
  level and expiry, the new value of `is_admin`, a service token's account and expiry. Why: the
  log was written by a GraphQL hook before the mutation ran, so it recorded attempts, looked
  the same for success and failure, kept only top-level `id` arguments (nothing of a grant's
  input, nothing for `setUserAdmin`), and missed the command line (architecture review,
  finding 10).
- The hook (`audit: mutation`) is gone; `audit: denied` stays where it was, in the error
  presenter, because only the API can be refused: the command line acts as the system.
- A transaction over several services holds their records until it commits
  (`access.HoldAudit`), so a rolled-back Vidispine import logs nothing.
- Left out: an audit table (the log is the only store, so retention is the log pipeline's),
  a request id to tie the records of one operation together, refusals that look like
  `NOT_FOUND`, and dev-data scenarios, whose records are dropped on purpose.

## 2026-11-11 — Asset access expires by the clock, not by the sweeper

- `asset_access` now has one row per principal, asset and level with `expires_at`, and every
  read of it (`AssetLevels`, `FileLevels`, `ListAssets`) checks that against `now()`. Why: the
  table held only the highest level, so expired access lasted until the sweeper rebuilt it: a
  minute when it worked, indefinitely when it kept failing, and from start until the first
  sweep (architecture review, finding 05).
- One row per level, not one row with the highest level and its expiry: when an EDIT grant
  runs out, a VIEW grant that does not must count again without a rebuild. A level's
  `expires_at` is the latest end among the grants giving it, NULL if any of them is permanent.
- Migration 10 rebuilds the table in SQL; `TestAssetAccessExpiryMigration` checks that it gives
  what the service's refresh gives, and that Down collapses it again.
- The sweeper stays as cleanup and now also runs once at start.
- Left out: a view that hides expired rows, so that a new query could not forget the check.
  The debug UI's reset and stats read every table in `public`, and a view there needs care.
  The rule is written next to the queries and in `security.md` instead. Also left out:
  monitoring of the sweeper, which no longer guards anything.

## 2026-11-11 — Machine clients: service accounts and service tokens

- Another system can now call the API as itself. An administrator creates a **service account**
  (`createServiceAccount`, or `mediabrain service create`) and a **token** for it
  (`createServiceToken`, `mediabrain service token`); the system sends
  `Authorization: Bearer mbs_...`. Details in [security.md](security.md#machine-clients).
- *A service account is an `app_user` of kind `service`*, not a table of its own. Grants, groups,
  `created_by` and every permission query work for it without a change, and there is one kind of
  principal to reason about.
- *MediaBrain issues the tokens*, rather than accepting client-credentials tokens from the
  identity provider. It needs nothing from the identity provider, and expiry, revocation and
  "last used" are in one place. `ServiceSignIn` sits behind `auth.Users`, so identity-provider
  tokens can be mapped to the same accounts later.
- *Only the SHA-256 of a token is stored.* A token is 256 random bits, so a slow password hash
  would protect nothing and cost every uncached request.
- *Machine requests still come through the auth proxy*, which has to pass them on; MediaBrain
  checks the token. `AUTH_SERVICE_HEADER` (default `Authorization`) is for proxies that keep
  `Authorization` for themselves.
- *A service account cannot manage credentials or administrators*, even as an administrator
  (`access.RequirePersonAdmin`), so a leaked token cannot make itself permanent. `setUserAdmin`
  now follows the same rule.
- Dev data: the `access` scenario creates the accounts `ingest` (administrator) and `reader`
  (view on project 1) and prints a token for each; the debug UI marks service accounts.
- Left out: roles narrower than administrator for machines (an ingest tool is an administrator
  because registering files is), scopes per token, rate limits per token, identity-provider
  tokens, and anything outbound (webhooks, events).

## 2026-11-11 — An asset no longer lists links to hidden projects

- `asset.projects` leaves out links to projects the caller may not see. Why: only the nested
  `project` was filtered, so someone with a grant on just the asset still read the hidden
  project's id, the link's role and when it was made (architecture review, finding 06). That
  contradicted the rule that an asset grant does not reveal its project.
- The visibility rule is not copied: `asset.Service.LinksFor` asks `ListVisibleProjects`, the
  same query the project lists use, at the cost of one more statement per batch.
- `AssetProjectLink.project` stays nullable in the schema so clients do not break.
- Left out: `unlinkAssetFromProject` still needs only `EDIT` on the asset, and
  `AssetFilter.projectId` still filters by any project id. Both tell someone who already knows
  a hidden project's id whether an asset they can see is linked to it; ids cannot be guessed.

## 2026-11-11 — The user cache ends with the token

- A cached user is now forgotten when its token stops being accepted (`exp` plus the verifier's
  30 s leeway), or after one minute, whichever comes first. Why: a cache hit skips verification,
  and the entry did not know the token's expiry, so a token verified just before it expired
  kept working for up to a minute more (architecture review, finding 03).
- `access.Identity` carries `Expires`, set by the verifier. An identity without it is not cached:
  nothing says how long it holds.
- Left out: the one-minute delay before a change to `is_admin`, `active` or group membership
  applies is unchanged and documented. A withdrawn signing key also still works from the cache
  for up to a minute.

## 2026-11-11 — Signing keys are trusted for a bounded time

- The JWKS cache now has a maximum age of 5 minutes: an older key set is fetched again before
  any key in it is used. Why: a key the verifier already knew was trusted until the process
  restarted or some token happened to name an unknown key, so withdrawing a compromised key at
  the identity provider did not stop its tokens (architecture review, finding 02: a day-old
  cache accepted a key the endpoint no longer listed).
- When the refresh fails, the cached keys stay in use for up to 1 hour after they were fetched,
  with a warning per failed attempt; after that tokens are refused until a fetch succeeds. Why:
  a short outage of the identity provider should not sign everyone out, but an attacker who can
  keep the endpoint unreachable must not keep a withdrawn key alive.
- Fetches are at least 10 s apart, failed ones included (before, every request retried a
  failing endpoint), and the cache mutex is no longer held during the request: one caller
  fetches, the others wait for its result, and a caller that gives up does not cancel it.
- The verifier's clock can be replaced in tests (`internal/auth/verifier_cache_test.go`).
- Left out: settings for the two limits (constants until someone needs other values), and
  refreshing in the background before the cache expires (one request in five minutes waits for
  the fetch). The one-minute user cache still serves a token past its expiry; that is finding
  03 and a separate change.

## 2026-11-11 — Attaching a file needs DOWNLOAD_ORIGINAL on it

- `attachFile`, and `importSubtitles` with a `sourceFileId`, now need `DOWNLOAD_ORIGINAL` on the
  file, not just sight of it (`access.RequireFileReuse`). Why: a file's level is the highest
  over every asset that uses it, so someone with `VIEW` on asset A and `EDIT` on asset B could
  attach A's file to B and then read its storage paths, and so could everyone else with
  download rights on B (architecture review, finding 01).
  `TestAttachingAFileNeedsDownloadOriginal` reproduces it.
- A file the caller cannot see is still `INVALID_INPUT`; one they can only view is `FORBIDDEN`.
- Left out: who may widen a file's audience at all. Someone with download rights on a file can
  still attach it to an asset whose other users had no access to it. Whether that needs a
  right of its own (or grants on files) is an open policy decision.

## 2026-11-11 — Debug mode stays on the developer's machine

From the architecture review (finding 04, "development defaults expose privileged services").

- The example setup listens on loopback: `HTTP_ADDR=127.0.0.1:8080` in `.env.example`, and the
  dev Postgres is published as `127.0.0.1:5432`. Why: with `DEBUG=true` and `DEBUG_USER` every
  request is an administrator, `/debug/api/reset` wipes the data, and the database password is
  in the repository; all three were reachable from the local network.
- With `DEBUG=true` the server refuses to start on a non-loopback `HTTP_ADDR`
  (`ErrDebugNotLocal`). `DEBUG_ALLOW_REMOTE=true` is the explicit way out. An existing `.env`
  with `HTTP_ADDR=:8080` must be changed; the error says how. The default of `HTTP_ADDR` stays
  `:8080`, which is what a deployment (debug off) wants.
- A POST to the debug API must be same-origin (`http.CrossOriginProtection`: `Sec-Fetch-Site`,
  or `Origin` against `Host`) and carry `X-Debug-Token`, a per-process random token from
  `GET /debug/api/token`. Why both: the origin check relies on headers only browsers send, the
  token on something another site cannot read. The page fetches the token itself and fetches
  it again after a restart.
- Without `DEBUG_ALLOW_REMOTE`, a request whose `Host` is not local is refused on every route.
  Why: DNS rebinding makes another site same-origin, which defeats both checks above and would
  also reach `/query` as `DEBUG_USER`. The review did not list this; it follows from the fix.
- `/query` is unchanged: it only takes `application/json`, so a cross-site form cannot use the
  debug cookie or `DEBUG_USER`.
- Left out: no browser-based cross-origin exploit was run, in the review or here; the tests
  send the headers a browser would. The dev database keeps its fixed credentials, now on
  loopback only. Debug GETs (stats, users) are not behind the token. The token is not tied to
  a browser session: anything that can read `GET /debug/api/token` locally can use it.
  See [security.md](security.md#debug-bypass-development-only).

## 2026-11-11 — Migration lock, image and a production compose file

- Migrations now run under a Postgres advisory lock (goose's session locker). Why: with
  `AUTO_MIGRATE=true`, instances starting together could apply the same migration twice; eight
  concurrent `MigrateUp` calls on an empty database failed about one run in five ("relation
  already exists"). `TestConcurrentMigrateUp` covers it. The wait is polled every second rather
  than goose's default five, so a second instance starts promptly.
- `Dockerfile`: static binary on alpine, non-root. Alpine rather than distroless because the
  compose healthcheck calls `wget` on `/healthz`.
- `deploy/docker-compose.prod.yml` with `deploy/.env.prod.example`: Postgres, a one-shot
  `migrate`, and replicas of the server with debug off and the `AUTH_*` settings required.
  `make docker-build`, `make prod-config`.
- An external database is named with `MEDIABRAIN_DATABASE_URL`, not `DATABASE_URL`: compose
  reads the shell's environment, and a developer's shell usually has `DATABASE_URL` set to the
  dev database.
- No host port and no load balancer: `/query` only accepts the auth proxy, which is specific to
  each deployment. It joins the compose network and uses Docker's DNS for the replicas.
- Left out: a setting for the pool size (`pool_max_conns` in the URL does it), and electing one
  instance to run the grant sweeper (running it everywhere is harmless).
  See [architecture.md](architecture.md#running-several-instances).

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
