import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import Page from '../app/pages/users.vue'
import { apiError, fakeFetch, json, page, person } from './helpers'

afterEach(() => {
  vi.unstubAllGlobals()
  vi.useRealTimers()
})

const me = { me: { id: 'US-9' } }

async function mountPage(...answers: (Response | Error)[]) {
  const { fetcher, sent } = fakeFetch(...answers)
  vi.stubGlobal('fetch', fetcher)
  const wrapper = mount(Page, { attachTo: document.body })
  await flushPromises()
  return { page: wrapper, sent }
}

const checked = (el: { element: Element }) => (el.element as HTMLInputElement).checked

describe('users page', () => {
  it('lists people with their groups, role and state', async () => {
    const { page: p } = await mountPage(
      page(
        'users',
        [
          person({ groups: [{ id: 'UG-1', name: 'editors' }, { id: 'UG-2', name: 'crew' }], isAdmin: true }),
          person({ id: 'US-2', name: null, email: 'bob@example.test', active: false }),
          person({ id: 'US-9', name: null, email: null }),
        ],
        null,
        me,
      ),
    )
    const rows = p.findAll('[data-test=user]')
    expect(rows).toHaveLength(3)
    expect(rows[0]!.text()).toContain('Ann')
    expect(rows[0]!.text()).toContain('ann@example.test')
    expect(rows[0]!.findAll('[data-test=group]').map((g) => g.text())).toEqual(['editors', 'crew'])
    expect(rows[0]!.text()).toContain('Administrator')
    expect(rows[0]!.text()).toContain('2026-11-11')
    expect(rows[1]!.text()).toContain('bob@example.test')
    expect(rows[1]!.text()).toContain('Grants only')
    expect(rows[1]!.text()).toContain('—')
    expect(checked(rows[1]!.get('[data-test=active] input'))).toBe(false)
    // The caller's own row is shown by id and cannot be changed.
    expect(rows[2]!.text()).toContain('US-9')
    expect(rows[2]!.text()).toContain('(you)')
    expect(rows[2]!.get('[data-test=active] input').attributes('disabled')).toBeDefined()
    expect(rows[2]!.get('[data-test=admin] input').attributes('disabled')).toBeDefined()
    expect(rows[0]!.get('[data-test=active] input').attributes('disabled')).toBeUndefined()
    expect(p.find('[data-test=more]').exists()).toBe(false)
    p.unmount()
  })

  it('searches after a pause and says when nobody matches', async () => {
    vi.useFakeTimers()
    const { page: p, sent } = await mountPage(page('users', [], null, me), page('users', [], null, me))
    expect(p.text()).toContain('Nobody has signed in yet.')
    await p.get('input[type=search]').setValue('zed')
    await p.get('input[type=search]').setValue('zelda')
    expect(sent).toHaveLength(1)
    await vi.advanceTimersByTimeAsync(300)
    await flushPromises()
    expect(sent).toHaveLength(2)
    expect(sent[1]?.variables).toMatchObject({ filter: { kind: 'PERSON', search: 'zelda' }, after: null })
    expect(p.text()).toContain('Nobody matches.')
    p.unmount()
  })

  it('loads more', async () => {
    const { page: p, sent } = await mountPage(page('users', [person()], 'US-1', me), page('users', [person({ id: 'US-2' })], null, me))
    await p.get('[data-test=more]').trigger('click')
    await flushPromises()
    expect(sent[1]?.variables).toMatchObject({ after: 'US-1' })
    expect(p.findAll('[data-test=user]')).toHaveLength(2)
    expect(p.find('[data-test=more]').exists()).toBe(false)
    p.unmount()
  })

  it('switches a person off and makes another an administrator', async () => {
    const { page: p, sent } = await mountPage(
      page('users', [person(), person({ id: 'US-2' })], null, me),
      json({ data: { setUserActive: person({ active: false }) } }),
      json({ data: { setUserAdmin: person({ id: 'US-2', isAdmin: true }) } }),
    )
    const rows = p.findAll('[data-test=user]')
    await rows[0]!.get('[data-test=active] input').trigger('click')
    await flushPromises()
    expect(sent[1]?.variables).toEqual({ user: 'US-1', active: false })
    expect(checked(rows[0]!.get('[data-test=active] input'))).toBe(false)
    await rows[1]!.get('[data-test=admin] input').trigger('click')
    await flushPromises()
    expect(sent[2]?.variables).toEqual({ user: 'US-2', admin: true })
    expect(rows[1]!.text()).toContain('Administrator')
    p.unmount()
  })

  it('shows a refused change and a failed load, and reloads', async () => {
    const { page: p } = await mountPage(
      apiError('administrators only', { code: 'FORBIDDEN' }),
      page('users', [person()], null, me),
      apiError('not allowed', { code: 'FORBIDDEN' }),
    )
    expect(p.get('[role=alert]').text()).toContain('administrators only')
    expect(p.text()).not.toContain('Nobody')
    await p.get('[role=alert] button').trigger('click')
    await flushPromises()
    expect(p.find('[role=alert]').exists()).toBe(false)
    await p.get('[data-test=admin] input').trigger('click')
    await flushPromises()
    expect(p.get('[role=alert]').text()).toContain('not allowed')
    expect(p.get('[data-test=user]').text()).toContain('Grants only')
    p.unmount()
  })
})
