import { describe, expect, it } from 'vitest'
import { ApiError, gql } from '../app/utils/graphql'
import { apiError, fakeFetch, json } from './helpers'

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
