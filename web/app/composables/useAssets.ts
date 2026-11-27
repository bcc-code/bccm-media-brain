import { gql, type Fetcher } from '../utils/graphql'
import { usePaged, type Page } from './usePaged'

export interface AssetRow {
  id: string
  title: string
  legacyIds: string[]
  createdAt: string
  type: { name: string }
}

export const ASSET_PAGE = 50

/** The assets the caller may see, newest first. */
export function useAssets(fetcher?: Fetcher) {
  const list = usePaged<AssetRow>(async (after) => {
    const data = await gql<{ assets: Page<AssetRow> }>(
      `query Assets($first: Int, $after: String) {
        assets(first: $first, after: $after) {
          nodes { id title legacyIds createdAt type { name } }
          pageInfo { hasNextPage endCursor }
        }
      }`,
      { first: ASSET_PAGE, after },
      fetcher,
    )
    return data.assets
  })
  return { assets: list.items, ...list }
}
