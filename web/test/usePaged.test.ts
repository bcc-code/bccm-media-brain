import { describe, expect, it } from 'vitest'
import { usePaged, type Page } from '../app/composables/usePaged'
import { ApiError } from '../app/utils/graphql'

function deferred<T>() {
  let resolve!: (v: T) => void
  let reject!: (e: unknown) => void
  const promise = new Promise<T>((res, rej) => {
    resolve = res
    reject = rej
  })
  return { promise, resolve, reject }
}

const pageOf = (nodes: number[], endCursor: string | null): Page<number> => ({
  nodes,
  pageInfo: { hasNextPage: endCursor !== null, endCursor },
})

describe('usePaged', () => {
  it('loads a page, then the next one after its cursor', async () => {
    const asked: (string | null)[] = []
    const answers = [pageOf([1, 2], 'c2'), pageOf([3], null)]
    const p = usePaged(async (after) => {
      asked.push(after)
      return answers.shift()!
    })
    await p.reload()
    expect(p.items.value).toEqual([1, 2])
    expect(p.hasMore.value).toBe(true)
    await p.more()
    expect(asked).toEqual([null, 'c2'])
    expect(p.items.value).toEqual([1, 2, 3])
    expect(p.hasMore.value).toBe(false)
    expect(p.loaded.value).toBe(true)
  })

  it('keeps what it has when a page fails', async () => {
    const answers: (Page<number> | Error)[] = [pageOf([1], 'c1'), new ApiError('down', 'NETWORK')]
    const p = usePaged(async () => {
      const a = answers.shift()!
      if (a instanceof Error) throw a
      return a
    })
    await p.reload()
    await p.more()
    expect(p.error.value?.code).toBe('NETWORK')
    expect(p.items.value).toEqual([1])
    expect(p.loading.value).toBe(false)
  })

  it('drops an older answer that arrives after a reload', async () => {
    const slow = deferred<Page<number>>()
    const fast = deferred<Page<number>>()
    const queue = [slow, fast]
    const p = usePaged(() => queue.shift()!.promise)
    const first = p.reload()
    const second = p.reload()
    fast.resolve(pageOf([2], null))
    await second
    slow.resolve(pageOf([1], null))
    await first
    expect(p.items.value).toEqual([2])

    const failing = deferred<Page<number>>()
    const after = deferred<Page<number>>()
    queue.push(failing, after)
    const old = p.reload()
    const fresh = p.reload()
    failing.reject(new ApiError('old', 'NETWORK'))
    await old
    expect(p.error.value).toBeNull()
    expect(p.loading.value).toBe(true)
    after.resolve(pageOf([3], null))
    await fresh
    expect(p.items.value).toEqual([3])
  })
})
