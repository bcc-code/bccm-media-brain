# GraphQL API

`POST /query`. The schema in `internal/graph/schema/*.graphqls` is the reference; with
`GRAPHQL_PLAYGROUND=true` it can be explored at `/`. Every request must be authenticated
([security.md](security.md)).

Conventions:

- Lists take `limit` (default 50, max 500) and `offset`.
- Lookups by id return `null` when the object does not exist or is not visible.
- `update...` mutations **replace all editable fields**; a field left out is cleared.
- Errors carry `extensions.code` ([architecture.md](architecture.md#errors)).
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
query    { subtitleTrack(id: "ST-...") { revision cues { seq startMs endMs text } export(format: VTT) } }
```

Read an asset with everything under it (a fixed number of database queries, however many assets):

```graphql
query {
  assets(filter: {projectId: "PJ-..."}) {
    id title myLevel
    versions { number status renditions { kind language
      files { role file { sizeBytes codec copies { path location { name } } } }
      subtitleTracks { language revision } } }
  }
}
```

Share an asset:

```graphql
mutation { grantAccess(input: {scope: ASSET, scopeId: "AS-...", userEmail: "ada@example.com", level: VIEW}) { id } }
```
