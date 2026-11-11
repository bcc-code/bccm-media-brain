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
| Refresh | `refreshFromVidispine(assetId)` | `mediabrain vidispine refresh AS-…` |

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
- **Vidispine is read before the transaction opens**, then everything is written in one
  transaction through the asset and file services, so their rules apply and a failure leaves
  nothing behind.

## Refresh

Replaces `metadata.vidispine` with Vidispine's current field values and nothing else: title,
description, other metadata keys, versions and files stay as they are in MediaBrain. The item is
found through `metadata.vidispine.itemId`, or the `VX-…` legacy id when the metadata was edited away.

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

- Reads: item metadata, shapes and files, storage methods, files of a storage, collection items,
  time code conversion.
- Every call takes a context. Ids are escaped into the URL. Only GET is retried, on network
  errors and 5xx.
- Errors are `*vidispine.Error`; `errors.Is(err, vidispine.ErrNotFound)` and
  `ErrShapeTagNotFound` work.
- `pkg/vidispine/vidispinetest` is a fake Vidispine server for tests.

## Left out on purpose

- Bulk import (collections, searches) and scheduled refresh.
- Other shapes (low-res proxies, encodes) and subtitle sidecars.
- Refresh of files: a changed or newly added original is not picked up.
- The write side of the client (jobs, placeholders, shape import, metadata writes, deletes,
  exports) and flows' field-name constants. They move over when flows switches to this package.
- Trying it against the real Vidispine: the tests run against the fake server and against response
  samples shaped like real ones.
