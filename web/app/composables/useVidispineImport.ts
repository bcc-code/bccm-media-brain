import { ref } from 'vue'
import { ApiError, gql, type Fetcher } from '../utils/graphql'

export interface Option {
  id: string
  name: string
}

export interface ImportedAsset {
  id: string
  title: string
}

/** What the item id was checked against: an asset made from it, or none yet. */
export type Lookup = { itemId: string; asset: ImportedAsset | null }

const ITEM_ID = /^VX-\d+$/

/** Normalises what was typed ("vx-12 ") to an item id, or null when it is not one. */
export function itemIdOf(text: string): string | null {
  const id = text.trim().toUpperCase()
  return ITEM_ID.test(id) ? id : null
}

/**
 * Importing one Vidispine item, or re-importing the asset made from it: the API's
 * importFromVidispine and refreshFromVidispine. Administrators only, as the API decides; the
 * page asks `me` so it shows the form only to them.
 */
export function useVidispineImport(fetcher?: Fetcher) {
  const isAdmin = ref(false)
  const types = ref<{ id: number; name: string }[]>([])
  const projects = ref<Option[]>([])
  const lookup = ref<Lookup | null>(null)
  const busy = ref(false)
  const error = ref<ApiError | null>(null)
  const done = ref<{ verb: 'Imported' | 'Re-imported'; asset: ImportedAsset } | null>(null)

  async function run<T>(work: () => Promise<T>): Promise<T | undefined> {
    busy.value = true
    error.value = null
    try {
      return await work()
    } catch (e) {
      error.value = e instanceof ApiError ? e : new ApiError('Something went wrong.', 'INTERNAL')
      return undefined
    } finally {
      busy.value = false
    }
  }

  /** Loads who the caller is and, for an administrator, the choices of the form. */
  async function prepare() {
    const me = await run(() => gql<{ me: { isAdmin: boolean } }>(`query Me { me { isAdmin } }`, {}, fetcher))
    isAdmin.value = me?.me.isAdmin ?? false
    if (!isAdmin.value) return
    const data = await run(() =>
      gql<{ assetTypes: { id: number; name: string }[]; projects: { nodes: { id: string; code: string; name: string }[] } }>(
        `query ImportChoices { assetTypes { id name } projects(first: 500) { nodes { id code name } } }`,
        {},
        fetcher,
      ),
    )
    if (!data) return
    types.value = data.assetTypes
    projects.value = data.projects.nodes.map((p) => ({ id: p.id, name: `${p.code} ${p.name}` }))
  }

  /** Finds out whether the item was imported already. */
  async function check(text: string) {
    done.value = null
    lookup.value = null
    const itemId = itemIdOf(text)
    if (!itemId) {
      error.value = new ApiError('An item id looks like VX-123.', 'INVALID_INPUT', 'itemId')
      return
    }
    const data = await run(() =>
      gql<{ assets: { nodes: ImportedAsset[] } }>(
        `query ByItem($id: String!) { assets(filter: { legacyId: $id }, first: 1) { nodes { id title } } }`,
        { id: itemId },
        fetcher,
      ),
    )
    if (data) lookup.value = { itemId, asset: data.assets.nodes[0] ?? null }
  }

  async function importItem(typeId: number, projectId: string | null) {
    const item = lookup.value
    if (!item || item.asset) return
    const data = await run(() =>
      gql<{ importFromVidispine: ImportedAsset }>(
        `mutation Import($in: VidispineImportInput!) { importFromVidispine(input: $in) { id title } }`,
        { in: { itemId: item.itemId, typeId, projectId } },
        fetcher,
      ),
    )
    if (data) finish('Imported', data.importFromVidispine)
  }

  async function reimport() {
    const asset = lookup.value?.asset
    if (!asset) return
    const data = await run(() =>
      gql<{ refreshFromVidispine: ImportedAsset }>(
        `mutation Reimport($id: ID!) { refreshFromVidispine(assetId: $id) { id title } }`,
        { id: asset.id },
        fetcher,
      ),
    )
    if (data) finish('Re-imported', data.refreshFromVidispine)
  }

  function finish(verb: 'Imported' | 'Re-imported', asset: ImportedAsset) {
    done.value = { verb, asset }
    lookup.value = null
  }

  return { isAdmin, types, projects, lookup, busy, error, done, prepare, check, importItem, reimport }
}
