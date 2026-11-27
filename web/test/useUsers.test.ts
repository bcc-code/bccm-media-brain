import { describe, expect, it } from 'vitest'
import { useUsers } from '../app/composables/useUsers'
import { apiError, fakeFetch, json, page, person } from './helpers'

const me = { me: { id: 'US-9' } }

describe('useUsers', () => {
  it('lists people, with the search, and knows the caller', async () => {
    const { fetcher, sent } = fakeFetch(page('users', [person()], 'US-1', me), page('users', [person({ id: 'US-2' })], null, me))
    const s = useUsers(fetcher)
    await s.reload()
    expect(sent[0]?.variables).toEqual({ filter: { kind: 'PERSON', search: null }, first: 50, after: null })
    expect(s.meId.value).toBe('US-9')
    s.search.value = '  ann '
    await s.more()
    expect(sent[1]?.variables).toEqual({ filter: { kind: 'PERSON', search: 'ann' }, first: 50, after: 'US-1' })
    expect(s.users.value.map((u) => u.id)).toEqual(['US-1', 'US-2'])
  })

  it('switches a person off and makes one an administrator', async () => {
    const { fetcher, sent } = fakeFetch(
      page('users', [person(), person({ id: 'US-2' })], null, me),
      json({ data: { setUserActive: person({ active: false }) } }),
      json({ data: { setUserAdmin: person({ id: 'US-2', isAdmin: true }) } }),
    )
    const s = useUsers(fetcher)
    await s.reload()
    const pending = s.setActive('US-1', false)
    expect(s.saving.value.has('US-1')).toBe(true)
    await pending
    expect(sent[1]?.query).toContain('setUserActive')
    expect(sent[1]?.variables).toEqual({ user: 'US-1', active: false })
    await s.setAdmin('US-2', true)
    expect(sent[2]?.variables).toEqual({ user: 'US-2', admin: true })
    expect(s.users.value.map((u) => [u.active, u.isAdmin])).toEqual([
      [false, false],
      [true, true],
    ])
    expect(s.saving.value.size).toBe(0)
  })

  it('keeps the user as it was when a change is refused', async () => {
    const { fetcher } = fakeFetch(page('users', [person()], null, me), apiError('not yourself', { code: 'INVALID_INPUT', field: 'user' }))
    const s = useUsers(fetcher)
    await s.reload()
    await s.setActive('US-1', false)
    expect(s.changeError.value?.message).toBe('not yourself')
    expect(s.users.value[0]?.active).toBe(true)
    expect(s.saving.value.size).toBe(0)
  })
})
