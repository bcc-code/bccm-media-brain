import { describe, expect, it } from 'vitest'
import { awaitsFirstSignIn, useServiceAccounts } from '../app/composables/useServiceAccounts'
import { account, apiError, fakeFetch, json } from './helpers'

describe('awaitsFirstSignIn', () => {
  it('is true until the account is stored under a token subject', () => {
    expect(awaitsFirstSignIn(account({ externalId: 'service|ingest' }))).toBe(true)
    expect(awaitsFirstSignIn(account({ externalId: 'c0ffee' }))).toBe(false)
  })
})

describe('useServiceAccounts', () => {
  it('loads the accounts', async () => {
    const { fetcher } = fakeFetch(json({ data: { serviceAccounts: [account()] } }))
    const s = useServiceAccounts(fetcher)
    const pending = s.load()
    expect(s.loading.value).toBe(true)
    await pending
    expect(s.loading.value).toBe(false)
    expect(s.loaded.value).toBe(true)
    expect(s.accounts.value).toEqual([account()])
  })

  it('keeps the error of a failed load and clears it on the next one', async () => {
    const { fetcher } = fakeFetch(
      apiError('forbidden', { code: 'FORBIDDEN' }),
      json({ data: { serviceAccounts: [] } }),
    )
    const s = useServiceAccounts(fetcher)
    await s.load()
    expect(s.error.value?.code).toBe('FORBIDDEN')
    expect(s.loaded.value).toBe(false)
    await s.load()
    expect(s.error.value).toBeNull()
  })

  it('creates an account and adds it to the list', async () => {
    const made = account({ id: 'US-2', name: 'transcoder', isAdmin: true })
    const { fetcher, sent } = fakeFetch(json({ data: { createServiceAccount: made } }))
    const s = useServiceAccounts(fetcher)
    s.accounts.value = [account()]
    await expect(s.create({ name: 'transcoder', admin: true })).resolves.toEqual(made)
    expect(sent[0]?.variables).toEqual({ input: { name: 'transcoder', admin: true, user: null } })
    expect(s.accounts.value.map((a) => a.id)).toEqual(['US-1', 'US-2'])
  })

  it('sends the user to convert and does not list the account twice', async () => {
    const made = account({ id: 'US-1', name: 'renamed' })
    const { fetcher, sent } = fakeFetch(json({ data: { createServiceAccount: made } }))
    const s = useServiceAccounts(fetcher)
    s.accounts.value = [account()]
    await s.create({ name: 'renamed', admin: false, user: 'US-1' })
    expect(sent[0]?.variables).toEqual({ input: { name: 'renamed', admin: false, user: 'US-1' } })
    expect(s.accounts.value).toEqual([made])
  })

  it('lets a refused create reach the caller', async () => {
    const { fetcher } = fakeFetch(apiError('taken', { code: 'CONFLICT', reason: 'ALREADY_EXISTS' }))
    const s = useServiceAccounts(fetcher)
    await expect(s.create({ name: 'ingest', admin: false })).rejects.toMatchObject({ reason: 'ALREADY_EXISTS' })
    expect(s.accounts.value).toEqual([])
  })

  it('switches an account off and marks it as saving meanwhile', async () => {
    const off = account({ active: false })
    const { fetcher, sent } = fakeFetch(json({ data: { setServiceAccountActive: off } }))
    const s = useServiceAccounts(fetcher)
    s.accounts.value = [account(), account({ id: 'US-2' })]
    const pending = s.setActive('US-1', false)
    expect(s.saving.value.has('US-1')).toBe(true)
    await pending
    expect(s.saving.value.size).toBe(0)
    expect(sent[0]?.variables).toEqual({ serviceAccount: 'US-1', active: false })
    expect(s.accounts.value.map((a) => a.active)).toEqual([false, true])
  })

  it('makes an account an administrator with the operation for all users', async () => {
    const promoted = account({ isAdmin: true })
    const { fetcher, sent } = fakeFetch(json({ data: { setUserAdmin: promoted } }))
    const s = useServiceAccounts(fetcher)
    s.accounts.value = [account(), account({ id: 'US-2' })]
    const pending = s.setAdmin('US-1', true)
    expect(s.saving.value.has('US-1')).toBe(true)
    await pending
    expect(sent[0]?.query).toContain('setUserAdmin')
    expect(sent[0]?.variables).toEqual({ user: 'US-1', admin: true })
    expect(s.accounts.value.map((a) => a.isAdmin)).toEqual([true, false])
    expect(s.saving.value.size).toBe(0)
  })

  it('leaves the account as it was when the switch is refused', async () => {
    const { fetcher } = fakeFetch(apiError('not found', { code: 'NOT_FOUND' }))
    const s = useServiceAccounts(fetcher)
    s.accounts.value = [account()]
    await s.setActive('US-1', false)
    expect(s.error.value?.code).toBe('NOT_FOUND')
    expect(s.accounts.value[0]?.active).toBe(true)
    expect(s.saving.value.size).toBe(0)
  })
})
