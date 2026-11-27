# Yaak workspace

Ready-made requests for the standard operations of the GraphQL API, for [Yaak](https://yaak.app).

## Open it

In Yaak: workspace menu → **Open Existing Workspace** (or *Workspace Settings → Sync → Directory*)
and choose this folder. Yaak keeps the files in sync, so changes you make show up as diffs here.

## Variables (environment "Global Variables")

| Variable | Use |
|---|---|
| `base_url` | Server address, `http://localhost:8080` by default |
| `user` | Who to act as in debug mode (sent as `X-Debug-User`): an email or user id. The default `admin@example.test` is the `DEBUG_USER` of `.env.example`, which the server creates as an administrator when missing, so it works on an empty database. Other users must exist (`make seed s=access` creates one per permission level) |
| `token` | Not used as shipped: every folder and request inherits the workspace's authentication (*Workspace Settings*), so sign-in is set in one place. Kept for a bearer setup of your own |
| `program_id`, `project_id`, `location_id`, `file_id`, `copy_id`, `asset_id`, `version_id`, `rendition_id`, `track_id`, `grant_id` | Ids used by the requests. Paste them in from the response of the matching "Create ..." request |
| `vidispine_item_id` | The Vidispine item to import, e.g. `VX-123` |
| `service_account` | The name of a machine client: its username at the identity provider. Also the username the workspace signs in with on a deployed server (see below) |
| `service_password` | That account's authentik **app password**, for a deployed server. A secret: set it in a private environment of your own, never in "Global Variables", which is committed. Empty for `make dev` |

## Signing in

Sign-in is set once, on the workspace (*Workspace Settings*): every folder and request inherits
it. It is HTTP Basic authentication with `service_account` and `service_password`.

- **`make dev`**: nothing to set. The server ignores the header and requests run as `user`.
- **A deployed server**: point `base_url` at it and put the account's app password into
  `service_password`. authentik's proxy takes the Basic credentials, signs the account in and
  forwards its token, so the account must be in the service group and exist in MediaBrain as a
  service account (`docs/security.md`, "Machine clients").

## Typical run on an empty database

1. `make dev`. Nothing else is needed: requests run as `admin@example.test`.
2. *Programs and projects*: Create program → Create project.
3. *Files and storage*: Create storage location → Register file.
4. *Assets*: Create asset → Create version → Create rendition → Attach file to rendition.
5. *Subtitles*: Import subtitles (or Import ASR transcript) → Track with cues → Edit subtitles → Export track.
6. *Access*: `make seed s=access` adds a persona per permission level (it wipes the data first).
   Grant access to one of them, then change `user` to their email to see what they see.
7. *Vidispine*: with `VIDISPINE_URL`, `VIDISPINE_USERNAME` and `VIDISPINE_PASSWORD` set on the server, put an
   item id into `vidispine_item_id` → Import from Vidispine → Re-import from Vidispine. Without a Vidispine,
   `make seed s=vidispine` creates assets that look imported.
8. *Service accounts*: Create service account → Service accounts. To see what one may do, put
   its id into `user`. `make seed s=access` also creates three accounts.

The update requests carry `"rev": null`; put the `revision` you read there to try a guarded update.

Requests marked "(admin)" need a system administrator. Keep this folder in step with the schema
when operations are added or changed.
