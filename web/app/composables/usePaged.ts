import { computed, ref, type Ref } from 'vue'
import type { ApiError } from '../utils/graphql'

export interface Page<T> {
  nodes: T[]
  pageInfo: { hasNextPage: boolean; endCursor: string | null }
}

/**
 * A list read one page at a time ("Load more"). `fetchPage` gets the cursor of the last item
 * so far, or null for the first page.
 */
export function usePaged<T>(fetchPage: (after: string | null) => Promise<Page<T>>) {
  const items = ref([]) as Ref<T[]>
  const loading = ref(false)
  const loaded = ref(false)
  const error = ref<ApiError | null>(null)
  const cursor = ref<string | null>(null)
  const hasMore = ref(false)
  // A reload while a page is on its way wins: the older answer is dropped.
  let generation = 0

  async function fetchInto(after: string | null, replace: boolean) {
    const mine = ++generation
    loading.value = true
    error.value = null
    try {
      const page = await fetchPage(after)
      if (mine !== generation) return
      items.value = replace ? page.nodes : [...items.value, ...page.nodes]
      cursor.value = page.pageInfo.endCursor
      hasMore.value = page.pageInfo.hasNextPage
      loaded.value = true
    } catch (e) {
      if (mine === generation) error.value = e as ApiError
    } finally {
      if (mine === generation) loading.value = false
    }
  }

  return {
    items,
    loading,
    loaded,
    error,
    hasMore: computed(() => hasMore.value),
    reload: () => fetchInto(null, true),
    more: () => fetchInto(cursor.value, false),
  }
}
