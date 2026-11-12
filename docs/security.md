# Security

## Sign-in

MediaBrain does not log users in. **A proxy in front of it does**, and forwards a signed JWT with
every request. MediaBrain:

1. accepts `/query` requests only from **trusted proxy addresses** (`AUTH_TRUSTED_PROXIES`),
2. reads the JWT from `AUTH_HEADER` (default `Authorization`, `Bearer ` optional),
3. **verifies it itself** against the keys at `AUTH_JWKS_URL`: signature (asymmetric algorithms
   only), issuer, audience, expiry. A reachable port and a forged header are not enough,
4. creates or updates the user from the claims (`sub`, `email`, `name`) and syncs their groups
   from `AUTH_GROUPS_CLAIM`. Groups from the token are marked `sso`; leaving a group at the
   identity provider removes the membership at the next sign-in.

The server **refuses to start** without `AUTH_ISSUER`, `AUTH_AUDIENCE`, `AUTH_JWKS_URL` and
`AUTH_TRUSTED_PROXIES`, unless `DEBUG=true`.

Details worth knowing:

- An email already used by another account is not taken over: the new user is stored without it.
  Addresses are compared without regard to case, and a unique index on `lower(email)` holds the
  rule in the database, so no path can store the same address twice.
- Inactive users (`app_user.active = false`) get HTTP 403.
- A verified token's user is cached for one minute, so a change to `is_admin`, `active` or group
  membership can take that long to apply. The cache entry ends when the token does (`exp` plus
  the 30 s leeway the verifier allows), so the cache never makes a token work longer than
  verification would.
- Tokens are verified with go-jose. Keys are fetched on first use and re-fetched (at most every
  10 s) when a token names an unknown key, so key rotation needs no restart.
- **A withdrawn signing key stops working within 5 minutes.** The key set is trusted for that
  long, then fetched again before any key in it is used, known or not. When that fetch fails,
  the last key set stays in use for up to 1 hour after it was fetched (a warning is logged at
  every failed attempt, at most one per 10 s); after that every token is refused until a fetch
  succeeds. So the bound is 5 minutes while the key endpoint answers and 1 hour while it does
  not. The limits are constants in `internal/auth/verifier.go`. One request fetches at a time;
  the others wait for its result.
- This bounds the *key*. A token that was already verified is still served from the user cache
  above for up to one minute after its key is withdrawn.

### Machine clients

Another system (an ingest tool, a transcoder) signs in as a **service account** with a
**service token** that MediaBrain issues itself, so nothing depends on what the identity provider
can do for machines.

- A service account is a user of kind `service`: it has grants, can be in groups and shows up as
  `createdBy` like anyone else, and every permission rule below applies to it unchanged. It has
  no email and cannot sign in through the identity provider: a JWT whose subject equals a service
  account's external id (`service|<name>`) is refused.
- A token is `mbs_` followed by 256 random bits. It is shown once, when it is created; the
  database keeps its SHA-256. It expires (a year by default) and can be revoked. An account can
  hold several, so a token is replaced by creating the next one, switching over and revoking
  the old one.
- The client sends `Authorization: Bearer mbs_...`. **Requests still come through the proxy**
  (`AUTH_TRUSTED_PROXIES` applies), which must let a request to `/query` with such a header
  through without a browser session and pass the header on unchanged. The proxy is only the
  network path here; MediaBrain is what checks the token. When the proxy keeps `Authorization`
  for itself, name another header in `AUTH_SERVICE_HEADER`.
- Unknown, revoked and expired tokens and tokens of a switched-off account get the same HTTP 401.
- An accepted token is cached for one minute like a user's (and never past its expiry), so
  revoking a token or switching its account off (`setServiceAccountActive`) can take that long. `lastUsedAt` is written at most
  once a minute for the same reason.
- **Administrators that are people** manage service accounts and tokens (`createServiceAccount`,
  `createServiceToken`, `revokeServiceToken`, ...) and make administrators. A service account
  cannot, even when it is an administrator: a leaked token must not be able to mint the next
  one or promote someone. The CLI can too (`mediabrain service ...`), which is how the first
  token is made without a browser.
- An ingest tool has to be an administrator today, because registering files is
  administrator-only. That is more than it needs; see "Not covered yet".
- The audit log names the token (`token=TK-...`) next to the account on every change and
  refusal by a machine client.

### Debug bypass (development only)

With `DEBUG=true` a request may name the user to act as, without a token:

- the debug UI (`/debug/`, "Act as user") sets the `mb_debug_user` cookie, so the playground on
  the same origin runs as that user,
- `X-Debug-User: <email or user id>` does the same for curl.

With `DEBUG_USER=<email>` set (it is in `.env.example`), a request that names nobody acts as that
user, who is created as an administrator when missing. That is what makes `make dev` usable
straight away: the playground and API clients such as the Yaak workspace work on an empty or
freshly reset database. Any other user must exist (run a scenario or add one in the debug UI).

A service token is honoured before any of this, and in debug mode from any address, so machine
clients can be tried under `make dev`.

`DEBUG` also exposes the data-wiping debug API. It must never be on in production.

Debug mode means "whoever can send a request is an administrator", so it is kept to the
developer's own machine and their own page:

- **Loopback only.** With `DEBUG=true` the server refuses to start unless `HTTP_ADDR` is
  `localhost` or a loopback IP (`config.Validate`, `ErrDebugNotLocal`). `:8080` listens on every
  interface and is refused. `.env.example` uses `127.0.0.1:8080`, and the dev Postgres, whose
  credentials are public, is published on `127.0.0.1` only. `DEBUG_ALLOW_REMOTE=true` lifts
  the rule, for a container or a shared dev box; it is logged as a warning at start.
- **Local host names only.** Without `DEBUG_ALLOW_REMOTE`, every route refuses a request whose
  `Host` is not `localhost` or a loopback IP (403). Otherwise a site could re-point its own name
  at 127.0.0.1 (DNS rebinding), become same-origin with the server, and use everything below.
- **A POST to the debug API is not cross-origin and carries a token.** The handler
  (`devdata.Handler`) refuses a POST whose `Sec-Fetch-Site` is not `same-origin`/`none` or whose
  `Origin` does not match `Host` (`http.CrossOriginProtection`), and one without the
  `X-Debug-Token` header. The token is random per process and read from `GET /debug/api/token`,
  which is refused to cross-site fetches. No CORS headers are sent anywhere, so another site can
  neither read the token nor send the header (that needs a preflight nobody answers). A plain
  form post, which needs no preflight, has no way to carry it.
- **`/query` needs nothing more.** It only accepts `Content-Type: application/json` (gqlgen's
  POST transport), which a page cannot send to another origin without a preflight, so the cookie
  and `DEBUG_USER` cannot be used by a cross-site form. The `mb_debug_user` cookie is
  `SameSite=Strict`. The Host rule above covers rebinding.

GETs on the debug API (table counts, user list, scenario list) stay open to local tools; another
site cannot read their responses.

## Administrators

`app_user.is_admin` marks system administrators. They bypass grant checks and are the only ones
who manage programs (create/delete), storage locations and files, and who import from Vidispine
(an import registers files and may create a storage location).

- First administrator: `mediabrain admin grant <email>` (`make admin-grant u=...`). The user must
  have signed in once; promoting an address nobody has proven they own would be a takeover path.
- Further ones: `setUserAdmin` (administrators only, and not service accounts).

## Permissions

Access is given by **grants**: a user or group gets a level on a program, a project or an asset.

| Level | Allows (each level includes the ones above it) |
|---|---|
| `VIEW` | Read the asset, versions, renditions, file metadata, subtitles (incl. export) |
| `DOWNLOAD_ORIGINAL` | Also see where files are stored (copies: location and path) |
| `EDIT` | Change asset, versions, renditions; attach/detach files; import and edit subtitles; link to a project |
| `DELETE` | Delete the asset, versions, renditions, subtitle tracks |
| `ADMIN` | Grant and revoke access on that object |

Rules:

- **Inheritance.** A grant on a program applies to its projects; a grant on a project applies to
  the assets linked to it. The highest of all applicable grants (own and via groups) counts.
- **`asset_access`** is the derived table every asset read and list checks. It is rebuilt in the
  same transaction whenever a grant, a project link or a project changes. It has one row per
  principal, asset and level, with the time that level ends (`expires_at`, NULL for never).
- **Expiry.** Grants can have `expiresAt`. Every check compares it to the database clock:
  project and program checks on the grant, asset and file checks on `asset_access.expires_at`.
  Access ends at that moment, and a lower grant that has not expired counts again. A sweeper in
  the server deletes expired grants and their rows at start and once a minute; it only tidies
  up, so a failing sweeper does not prolong access. A new query that reads `asset_access` must
  check `expires_at` itself.
- **No existence leaks.** Without at least `VIEW`, every read and every mutation behaves exactly
  as if the object did not exist (`null` or `NOT_FOUND`). With `VIEW` but too low a level the
  answer is `FORBIDDEN`.
- **Programs and projects.** Creating a project needs `EDIT` on the program. A program is
  visible to anyone who can see one of its projects (with only those projects listed).
  A grant on a single asset does not reveal its project: `asset.projects` lists only the links
  to projects the caller may see, because a link carries the project's id.
- **New assets.** Non-administrators create an asset inside a project they can edit, so every
  asset has someone with access from the start.
- **Files** have no grants of their own. A file is visible through an asset the caller can view.
  Its level is the highest the caller has on any asset that uses it, so attaching a file to
  another asset widens who can see where it is stored. Attaching (`attachFile`, and
  `importSubtitles` with a `sourceFileId`) therefore needs `DOWNLOAD_ORIGINAL` on the file as
  well as `EDIT` on the destination: one can only pass on what one has. A file the caller
  cannot see at all is `INVALID_INPUT`, like any unknown id; one they can only view is
  `FORBIDDEN`.
- **Sharing** (`grantAccess`) takes a user id, a user's email (they must have signed in once) or
  a group id. Listing users and groups is for administrators.

Permission checks live in the domain services (`internal/access`), so they apply the same way
to every caller. Dev-data scenarios and the CLI run as an explicit system principal.

## Hardening

- Schema introspection and the playground are off unless `GRAPHQL_PLAYGROUND=true`.
- **Query cost.** One operation may cost at most 1 000 000, where a list counts its selection
  once per item it may return (`internal/graph/complexity.go`; the sums the limit was chosen
  from are in `complexity_test.go`). The number is roughly how many values the answer could
  hold if every list were full, so nesting lists multiplies instead of adding. A refused
  operation runs no statement.
- **Request deadline.** A `/query` request has 30 seconds (`app.QueryTimeout`), sign-in
  included. The deadline is on the request's context, which every statement runs on: the one in
  progress is cancelled, a transaction rolls back, and the client gets `TIMEOUT`. One warning is
  logged per stopped request. The answer may be written for 15 seconds more.
- **Lists.** Every list that grows without bound is paged, nested ones included
  ([api.md](api.md)). Request bodies are limited to 8 MB (a subtitle file to 5 MB); the server
  has header, read and idle timeouts.
- Unexpected errors reach the client only as `INTERNAL` / "internal error"; the cause is logged.
- Audit log (`slog`):
  - `audit: change` for every change that was made, written by the service after the write
    succeeded: `action` (`grant.create`, `asset.update`, ...), `user` (`system` for the command
    line), `token` for a machine client, `target` and what matters about the change. A grant or
    revoke names scope, recipient, `permission` and expiry; `user.set_admin` the new value;
    a service token its account and expiry. A write that failed or was rolled back leaves no
    record. Metadata, titles and token values are never logged.
  - `audit: denied` for every `FORBIDDEN` answer of the API, with the user and the operation.
  - Refused sign-ins are logged with the reason.
  - **Which request, from where.** Every HTTP request gets an id: the `X-Request-Id` response
    header, `request` on its `http request` line and on every `audit: change`,
    `audit: denied` and refused sign-in it causes. Those records also carry `client`, the
    address the request came from, and the `http request` line of `/query` adds `user` and
    `token`. `client` is what the proxy reports in `AUTH_CLIENT_HEADER`, read only when the
    peer is a trusted proxy and cut at 128 characters; without the setting, or without the
    header, it is the peer's own address, which behind a proxy is the proxy. It is recorded
    and never decides anything. Set the header only if the proxy overwrites it: a proxy that
    passes a client's `X-Forwarded-For` along lets the client write its own audit trail.
    Changes made outside a request (command line, sweeper) have neither field.
  - Not recorded: reads, sign-ins that create or update a user, the sweeper, data generated
    by dev-data scenarios, and attempts on objects the caller cannot see: those answer
    `NOT_FOUND` like any wrong id, by decision (system review, finding SEC-08). The log is
    the only store; there is no audit table.

## How this is tested

`e2e/access_test.go` runs every operation as users with no grant, each level, group and inherited
grants, and expired or revoked grants. `e2e/auth_test.go` and `internal/auth` cover tokens
(missing, malformed, expired, wrong issuer or audience, foreign key, tampered, `alg: none`),
untrusted proxies, inactive users and the debug bypass. `e2e/service_test.go` covers service
accounts: every way a token is refused, grants applying to them, and that an administrator
service account cannot manage credentials. `e2e/graph_test.go` fails every database
statement of every operation in turn and requires a clean `INTERNAL` error each time.

## Vidispine

MediaBrain reads Vidispine with one service account (`VIDISPINE_USERNAME`, `VIDISPINE_PASSWORD`); it
never writes there. Import and refresh are for administrators, so what the account can read is
not exposed to anyone else. Three things are kept away from clients: Vidispine's address and error
texts (logged; the client gets `UNAVAILABLE`), credentials inside Vidispine storage addresses
(removed before the address becomes a storage location's base URI), and item ids are checked
against `VX-<digits>` before they go into a request path.

## Not covered yet

- Roles for machine clients that are narrower than administrator (for example "may register
  files"), and scopes per token.
- Tokens from the identity provider for machines (OAuth2 client credentials).
- Telling other systems about changes (webhooks, events). Machine access is inbound only.
- Grants on asset groups, collections, saved searches and notes (the tables allow them).
- Managing local groups through the API (dev data creates them; SSO groups sync automatically).
- Rate limiting and CORS (no browser client on another origin yet).
