# Vidispine import

The media lives in Cantemo Portal today. MediaBrain reads it through the Vidispine API that Portal
exposes under `/API`, not through Portal's own API, which is why everything here is named
Vidispine. An administrator can bring in **one item at a time** as an asset, and later
**refresh** what Vidispine says about it. Vidispine is only read, never written.

Settings: `VIDISPINE_URL` (the Vidispine API root, e.g. `https://vidispine.example.com/API`),
`VIDISPINE_USERNAME`, `VIDISPINE_PASSWORD`. They are optional as a group: without them the server
runs and both operations answer `UNAVAILABLE`.

| | GraphQL | CLI |
|---|---|---|
| Import | `importFromVidispine(input: {itemId, typeId, projectId})` | `mediabrain vidispine import [-type N] [-project PJ-…] VX-123` |
| Re-import | `refreshFromVidispine(assetId)` | `mediabrain vidispine refresh AS-…` |

Both are for administrators only: the import registers files and creates storage locations, which
only administrators may do. The CLI runs as the system principal.

## Debug UI

With `DEBUG=true`, `/debug/` has a **Vidispine** card: import one item (item id, asset type,
optional project) and refresh an imported asset. It shows the resulting asset with its
`metadata`, which is the quickest way to see how a real item maps. Both run as the system
principal through the same service as the API (`/debug/api/vidispine`, `/vidispine/import`,
`/vidispine/refresh`). Without the `VIDISPINE_*` settings the card says so and the buttons are off;
the `vidispine` scenario then creates assets that look imported.

## What an import creates

| Vidispine | MediaBrain |
|---|---|
| Item id (`VX-123`) | `asset.legacyIds`, and `metadata.vidispine.itemId` |
| `title`, else `originalFilename`, else the item id | `asset.title` |
| Every field value that applies to the whole item | `metadata.vidispine.fields` (name → values) |
| Shape tagged `original` | Version "Vidispine original" with a `master` rendition (`metadata.vidispine` holds the shape and file id) |
| The shape's file: size, SHA-1, mime type, duration, codec, resolution | A file, attached as video, audio or sidecar |
| Shape tagged `lowres_watermarked` | The preview: an `encode` rendition with purpose `preview` in the same version, its file attached as video |
| Shape tagged `Transcribed_Subtitle_SRT` (Mediabanken's transcription) | A subtitle revision: language `nb`, kind `SUBTITLE`, origin `ASR`, source `Mediabanken`, with the SRT file registered and attached as its source |
| The file's storage and path | A copy on the storage location named after the storage id |
| The storage (`VX-42`) | A storage location with that name, created on first use |

Why these choices:

- **The item id is the key.** An item can be imported once; a second import is a `CONFLICT` that
  names the existing asset. Imports of the same id are serialised with an advisory lock, because
  `legacy_ids` is an array and cannot carry a unique constraint.
- **Only whole-item values are kept.** Vidispine's terse metadata also lists values of subclips
  with a time interval; they are not the asset's metadata and are left out. Empty values too.
- **Description is not mapped.** Portal's field names (`portal_mf…`) differ per installation, so
  only fields every Vidispine has are interpreted. Everything else is there raw.
- **Storage locations are created from Vidispine's storage methods**: the `file://` method if
  there is one, otherwise the first. Credentials in the address (`s3://key:secret@bucket/`) are
  removed before it is stored. The name is the storage id; that is how the next import finds the
  location, so do not rename it.
- **Files need a hash.** Vidispine records SHA-1, so `hash_algo` has `sha1`. An original that is
  still being written, has no SHA-1 yet, or no storage path is refused (`INVALID_INPUT`) rather
  than imported without its file. An item without an original file (a placeholder) imports as an
  asset alone.
- **The preview and the transcript are extras.** One that cannot be used (still being written,
  no SHA-1, no storage address, a transcript that cannot be read, is over 5 MB, does not match its
  SHA-1 or is not SRT) is left out with a warning in the log (`vidispine: shape left out`), and the
  rest is imported. Only a Vidispine that does not answer fails the import. The transcript's text
  is read from Vidispine (`GET /API/storage/file/{id}/data`). Vidispine is used for importing
  only: nothing else asks it anything.
- **Transcript cues without a duration are stretched**, not refused: speech recognition writes
  one now and then (end not after start). It gets the time up to the next cue that starts
  later, at most a second, with a warning in the log.
- **The transcript is Norwegian** (`nb`): Mediabanken transcribes Norwegian, and the shape does
  not say.
- **Vidispine is read before the transaction opens**, then everything is written in one
  transaction through the asset and file services, so their rules apply and a failure leaves
  nothing behind.

## Watching the preview

`GET /media/renditions/{id}` (`internal/media`) serves the preview from its copy on the storage
location the import created, at the location's `file://` path joined with the copy's path, not
through Vidispine. So the server needs Vidispine's storage mounted at the same path Vidispine's
`file://` method names (in Docker: the same path inside the container). See [api.md](api.md).
The web UI's asset page plays it with subtitles on top.

## Re-import (refresh)

Replaces `metadata.vidispine` with Vidispine's current field values and **adds what the asset
lacks**: the version "Vidispine original" with a master rendition when a placeholder got its
original since, the preview when there is no `encode`/`preview` rendition, and the transcript as
the next revision unless a revision was already made from that very file. Nothing is replaced or
removed: title, description, other metadata keys and the renditions and files already there stay
as they are, so a preview that was re-rendered in Vidispine is not swapped. The item is found
through `metadata.vidispine.itemId`, or the `VX-…` legacy id when the metadata was edited away.

The web UI's **Assets** page has an "Import from Vidispine" panel for administrators: enter a
`VX-…` id; an item that was not imported is imported (asset type and optional project), one that
was is re-imported.

## Errors

| Situation | Code |
|---|---|
| Vidispine not configured, unreachable or answering with an error | `UNAVAILABLE` (the cause is logged, not returned) |
| Vidispine has no such item | `NOT_FOUND` |
| Item already imported; the copy path holds other content | `CONFLICT` |
| Bad item id, asset not from Vidispine, original file not usable | `INVALID_INPUT` |

## The client: `pkg/vidispine`

A Vidispine REST client that other Go projects can import
(`github.com/bcc-media/mediabrain/pkg/vidispine`). It was extracted from
`bccm-flows/services/vidispine/vsapi` and uses the standard library only (flows' version needs
resty, Temporal and flows-internal packages).

- Reads: item metadata, shapes and files, storage methods, files of a storage, the content of a
  file (`ReadFile`, with a size limit: `ErrTooLarge`), collection items, time code conversion.
- Every call takes a context. Ids are escaped into the URL. Only GET is retried, on network
  errors and 5xx.
- **Redirects stay on the configured host.** Vidispine answers a file's `/data` with a redirect to
  a one-time `/APInoauth/...` URL on the address it was set up with, which is often `localhost`.
  Followed as is, that reaches whatever listens on localhost where MediaBrain runs (MediaBrain
  itself, on 8080). The client keeps the path and replaces scheme and host with
  `VIDISPINE_URL`'s, sends the credentials again, and gives up after 10 redirects.
- Errors are `*vidispine.Error`; `errors.Is(err, vidispine.ErrNotFound)` and
  `ErrShapeTagNotFound` work.
- `pkg/vidispine/vidispinetest` is a fake Vidispine server for tests.

## Left out on purpose

- Bulk import (collections, searches) and scheduled refresh.
- Other shapes (`lowres`, `lowaudio`, other encodes) and other subtitle sidecars.
- Replacing files on re-import: a changed original or preview is not picked up.
- The write side of the client (jobs, placeholders, shape import, metadata writes, deletes,
  exports) and flows' field-name constants. They move over when flows switches to this package.
- Trying it against the real Vidispine: the tests run against the fake server and against response
  samples shaped like real ones.
