# GraphQL API

`POST /query`. The schema in `internal/graph/schema/*.graphqls` is the reference; with
`GRAPHQL_PLAYGROUND=true` it can be explored at `/`. Every request must be authenticated
([security.md](security.md)).

Conventions:

- **Paged lists are connections.** A list that can grow without bound returns
  `{ nodes pageInfo { hasNextPage endCursor } }` and takes `first` (default 50, at most 500) and
  `after`. For the next page pass the `endCursor` you got as `after`; `hasNextPage` says whether
  there is one, so a list that was cut never looks complete. A cursor belongs to the list that
  gave it, with the same other arguments, and is opaque: do not build or parse one.
  - Root lists: `assets` (newest first), `files` (newest first), `users`, `programs` and
    `projects` (by code).
  - Nested lists, paged within each parent: `Asset.versions` (by number), `Program.projects`
    (by code, default 200) and `SubtitleTrack.cues` (in order, default 500, at most 5000).
  - A page does not move when something is added or removed elsewhere in the list: it is what
    follows the item the cursor names.
  - The other nested lists (renditions of a version, files of a rendition, hashes and copies of
    a file) are short and come whole, as plain lists.
- One operation may cost at most 1 000 000 (`COMPLEXITY_LIMIT_EXCEEDED` otherwise). A field
  costs 1 and a list 1 plus its selection once per item it may return: its `first`, or for a
  list without one an assumed size (10 renditions, 5 files, 3 copies, ...; the table is in
  `internal/graph/complexity.go`). The cost assumes every list is full, so it is far above what
  comes back: one asset with everything down to its cues costs about 544 000, a page of 100
  assets with versions, renditions and files about 860 000. When a query is refused, give the
  nested lists a smaller `first`: `assets(first: 500) { nodes { versions(first: 5) { ... } } }`.
  `nodes` itself costs nothing; `pageInfo` adds its one or two fields per item.
- A request may run for 30 seconds. Then it is stopped, nothing it did is kept, and the answer
  is `TIMEOUT`.
- Lookups by id return `null` when the object does not exist or is not visible.
- `update...` mutations **replace all editable fields**; an optional field left out is cleared,
  and a status left out becomes the default (`PLANNED` for a project, `DRAFT` for a version). So
  a client sends back everything it read, changed or not.
- Objects with an `update...` mutation have a `revision` that counts the edits of their own
  fields. Pass the revision you read as `expectedRevision`: when someone else changed the object
  since, the update is refused with `CONFLICT` and nothing changes. Without `expectedRevision`
  the last write wins. Subtitle tracks have their own revisions (`editSubtitles`).
- Every error carries `extensions.code`; a conflict also says which kind (`reason`, and
  `currentRevision` for a stale one) and an invalid input which `field`
  ([architecture.md](architecture.md#errors)).
- Scalars: `Time` (RFC 3339), `Date` (`YYYY-MM-DD`), `JSON`, `Int64`.

| Area | Queries | Mutations |
|---|---|---|
| Access | `me`, `users`, `userGroups`, `grants` | `grantAccess`, `revokeAccess`, `setUserAdmin` |
| Machine clients ([security.md](security.md#machine-clients)) | `serviceAccounts`, `serviceTokens` | `createServiceAccount`, `setServiceAccountActive`, `createServiceToken`, `revokeServiceToken` |
| Programs, projects | `program(s)`, `project(s)` | `create/update/deleteProgram`, `create/update/deleteProject` |
| Files | `file`, `fileByHash`, `files`, `storageLocations` | `registerFile`, `addFileCopy`, `setFileCopyStatus`, `removeFileCopy`, `deleteFile`, `create/update/deleteStorageLocation` |
| Assets | `asset(s)`, `assetVersion`, `rendition`, `assetTypes`, `languages` | `create/update/deleteAsset`, `linkAssetToProject`, `unlinkAssetFromProject`, `create/update/deleteAssetVersion`, `create/update/deleteRendition`, `attachFile`, `detachFile` |
| Subtitles | `subtitleTrack`, `subtitleTracks` | `importSubtitles`, `editSubtitles`, `deleteSubtitleTrack` |
| Vidispine ([vidispine.md](vidispine.md)) | | `importFromVidispine`, `refreshFromVidispine` |

## Examples

Let another system in (administrators). The token is only in this answer; the system then sends
it as `Authorization: Bearer mbs_...`:

```graphql
mutation { createServiceAccount(input: {name: "ingest", admin: true}) { id } }
mutation { createServiceToken(input: {serviceAccount: "ingest", name: "production"}) { token serviceToken { id expiresAt } } }
```

Register a file (administrators; typically an ingest tool):

```graphql
mutation {
  registerFile(input: {
    sizeBytes: 1073741824, mimeType: "video/quicktime", codec: "prores", width: 1920, height: 1080,
    hashes: [{algo: XXH3_128, value: "0123456789abcdef0123456789abcdef"}],
    copies: [{locationId: "SL-...", path: "JOSH/S01/A001_0001.mov"}]
  }) { id }
}
```

Create an asset in a project and give it a master file:

```graphql
mutation { createAsset(projectId: "PJ-...", input: {typeId: 1, title: "A001 C003"}) { id } }
mutation { createAssetVersion(assetId: "AS-...", input: {label: "Camera original"}) { id } }
mutation { createRendition(versionId: "AV-...", input: {kind: MASTER, language: "nb"}) { id } }
mutation { attachFile(renditionId: "RE-...", fileId: "FI-...", role: VIDEO) { position } }
```

Import subtitles, edit them, export them:

```graphql
mutation { importSubtitles(input: {versionId: "AV-...", language: "en", format: SRT, content: "1\n00:00:01,000 --> ..."}) { id revision } }
mutation { editSubtitles(trackId: "ST-...", cues: [{startMs: 1000, endMs: 2500, text: "Hello"}]) { id revision parentId } }
query    { subtitleTrack(id: "ST-...") { revision cues { nodes { seq startMs endMs text } pageInfo { hasNextPage endCursor } } export(format: VTT) } }
```

Read an asset with everything under it (a fixed number of database queries, however many assets):

```graphql
query {
  assets(filter: {projectId: "PJ-..."}, first: 20) {
    nodes {
      id title myLevel
      versions(first: 5) { nodes { number status renditions { kind language
        files { role file { sizeBytes codec copies { path location { name } } } }
        subtitleTracks { language revision } } } }
    }
    pageInfo { hasNextPage endCursor }
  }
}
```

Share an asset:

```graphql
mutation { grantAccess(input: {scope: ASSET, scopeId: "AS-...", userEmail: "ada@example.com", level: VIEW}) { id } }
```
