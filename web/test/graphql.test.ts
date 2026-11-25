import { describe, expect, it, vi } from 'vitest'
import { ApiError, gql, reloadForSignIn } from '../app/utils/graphql'
import { apiError, fakeFetch, json } from './helpers'

describe('reloadForSignIn', () => {
  const memory = () => {
    const m = new Map<string, string>()
    return { getItem: (k: string) => m.get(k) ?? null, setItem: (k: string, v: string) => void m.set(k, v) }
  }

  it('reloads again once the pause has passed', () => {
    const storage = memory()
    const reload = vi.fn()
    expect(reloadForSignIn({ storage, reload, now: () => 1_000_000 })).toBe(true)
    expect(reloadForSignIn({ storage, reload, now: () => 1_029_999 })).toBe(false)
    expect(reloadForSignIn({ storage, reload, now: () => 1_030_000 })).toBe(true)
    expect(reload).toHaveBeenCalledTimes(2)
  })

  it('does not reload when session storage is unavailable', () => {
    const reload = vi.fn()
    const storage = {
      getItem: () => {
        throw new Error('blocked')
      },
      setItem: () => {},
    }
    expect(reloadForSignIn({ storage, reload })).toBe(false)
    expect(reload).not.toHaveBeenCalled()
  })
})

describe('gql', () => {
  it('posts the operation as JSON and returns data', async () => {
    const { fetcher, sent } = fakeFetch(json({ data: { me: { id: 'US-1' } } }))
    const data = await gql<{ me: { id: string } }>('query { me { id } }', { a: 1 }, fetcher)
    expect(data.me.id).toBe('US-1')
    expect(sent[0]).toEqual({ query: 'query { me { id } }', variables: { a: 1 } })
  })

  it('turns a GraphQL error into an ApiError with code, field and reason', async () => {
    const { fetcher } = fakeFetch(apiError('name is required', { code: 'INVALID_INPUT', field: 'name', reason: 'X' }))
    const err = await gql('query { me { id } }', {}, fetcher).catch((e) => e)
    expect(err).toBeInstanceOf(ApiError)
    expect(err).toMatchObject({ message: 'name is required', code: 'INVALID_INPUT', field: 'name', reason: 'X' })
  })

  it('answers INTERNAL for an error without a code', async () => {
    const { fetcher } = fakeFetch(json({ errors: [{ message: 'boom' }] }))
    await expect(gql('q', {}, fetcher)).rejects.toMatchObject({ code: 'INTERNAL' })
  })

  it('explains a refused sign-in, which is not JSON', async () => {
    const { fetcher } = fakeFetch(new Response('forbidden', { status: 403 }))
    await expect(gql('q', {}, fetcher)).rejects.toMatchObject({
      code: 'HTTP_403',
      message: 'Not signed in, or not allowed to use MediaBrain.',
    })
  })

  it('reloads the page on a 401, so the proxy can sign in again, and still reports it', async () => {
    const reload = vi.fn()
    vi.stubGlobal('location', { reload })
    sessionStorage.clear()
    try {
      const { fetcher } = fakeFetch(new Response('no token', { status: 401 }), new Response('no token', { status: 401 }))
      await expect(gql('q', {}, fetcher)).rejects.toMatchObject({ code: 'HTTP_401' })
      expect(reload).toHaveBeenCalledTimes(1)
      // A second 401 right after the reload is not cured by another one.
      await expect(gql('q', {}, fetcher)).rejects.toMatchObject({ code: 'HTTP_401' })
      expect(reload).toHaveBeenCalledTimes(1)
    } finally {
      vi.unstubAllGlobals()
      sessionStorage.clear()
    }
  })

  it('does not reload on a 403: signing in again would not change it', async () => {
    const reload = vi.fn()
    vi.stubGlobal('location', { reload })
    try {
      const { fetcher } = fakeFetch(new Response('forbidden', { status: 403 }))
      await expect(gql('q', {}, fetcher)).rejects.toMatchObject({ code: 'HTTP_403' })
      expect(reload).not.toHaveBeenCalled()
    } finally {
      vi.unstubAllGlobals()
    }
  })

  it('reports other HTTP failures and answers without data', async () => {
    const down = fakeFetch(new Response('bad gateway', { status: 502 }))
    await expect(gql('q', {}, down.fetcher)).rejects.toMatchObject({ code: 'HTTP_502', message: 'The server answered 502.' })
    const empty = fakeFetch(json({}))
    await expect(gql('q', {}, empty.fetcher)).rejects.toMatchObject({ code: 'HTTP_200' })
  })

  it('reports a server that cannot be reached', async () => {
    const { fetcher } = fakeFetch(new TypeError('failed to fetch'))
    await expect(gql('q', undefined, fetcher)).rejects.toMatchObject({ code: 'NETWORK' })
  })
})
