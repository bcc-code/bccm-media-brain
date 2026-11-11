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
| `token` | Only for a deployed server, and only if its auth proxy accepts a bearer token from API clients: point `base_url` at the proxy and enable the bearer authentication on the folders. Not needed for `make dev` |
| `program_id`, `project_id`, `location_id`, `file_id`, `copy_id`, `asset_id`, `version_id`, `rendition_id`, `track_id`, `grant_id` | Ids used by the requests. Paste them in from the response of the matching "Create ..." request |
| `vidispine_item_id` | The Vidispine item to import, e.g. `VX-123` |
| `service_account`, `service_token`, `service_token_id` | A machine client: its name, the token from "Create service token" and that token's id |

## Typical run on an empty database

1. `make dev`. Nothing else is needed: requests run as `admin@example.test`.
2. *Programs and projects*: Create program → Create project.
3. *Files and storage*: Create storage location → Register file.
4. *Assets*: Create asset → Create version → Create rendition → Attach file to rendition.
5. *Subtitles*: Import subtitles → Track with cues → Edit subtitles → Export track.
6. *Access*: `make seed s=access` adds a persona per permission level (it wipes the data first).
   Grant access to one of them, then change `user` to their email to see what they see.
7. *Vidispine*: with `VIDISPINE_URL`, `VIDISPINE_USERNAME` and `VIDISPINE_PASSWORD` set on the server, put an
   item id into `vidispine_item_id` → Import from Vidispine → Refresh from Vidispine. Without a Vidispine,
   `make seed s=vidispine` creates assets that look imported.
8. *Service accounts*: Create service account → Create service token, paste the token into
   `service_token` → Me, as the service account. `make seed s=access` also creates two accounts
   and prints their tokens.

The update requests carry `"rev": null`; put the `revision` you read there to try a guarded update.

Requests marked "(admin)" need a system administrator. Keep this folder in step with the schema
when operations are added or changed.
