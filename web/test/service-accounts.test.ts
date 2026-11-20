import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import CreateServiceAccountForm from '../app/components/CreateServiceAccountForm.vue'
import Page from '../app/pages/service-accounts.vue'
import { ApiError } from '../app/utils/graphql'
import { account, apiError, fakeFetch, json } from './helpers'

afterEach(() => vi.unstubAllGlobals())

async function mountPage(...answers: (Response | Error)[]) {
  const { fetcher, sent } = fakeFetch(...answers)
  vi.stubGlobal('fetch', fetcher)
  const page = mount(Page, { attachTo: document.body })
  await flushPromises()
  return { page, sent }
}

describe('service accounts page', () => {
  it('lists accounts with their sign-in state and role', async () => {
    const { page } = await mountPage(
      json({
        data: {
          serviceAccounts: [
            account({ id: 'US-1', name: 'ingest', externalId: 'abc', isAdmin: true }),
            account({ id: 'US-2', name: 'transcoder' }),
          ],
        },
      }),
    )
    const rows = page.findAll('[data-test=account]')
    expect(rows).toHaveLength(2)
    expect(rows[0]!.text()).toContain('ingest')
    expect(rows[0]!.text()).toContain('US-1')
    expect(rows[0]!.text()).toContain('Signed in')
    expect(rows[0]!.text()).toContain('Administrator')
    expect(rows[0]!.text()).toContain('2026-11-11')
    expect(rows[1]!.text()).toContain('Waiting for first sign-in')
    expect(rows[1]!.text()).toContain('Grants only')
    page.unmount()
  })

  it('says so when there are none', async () => {
    const { page } = await mountPage(json({ data: { serviceAccounts: [] } }))
    expect(page.text()).toContain('No service accounts yet.')
    page.unmount()
  })

  it('shows a failed load and loads again on Reload', async () => {
    const { page } = await mountPage(
      apiError('administrators only', { code: 'FORBIDDEN' }),
      json({ data: { serviceAccounts: [account({ name: null })] } }),
    )
    expect(page.get('[role=alert]').text()).toContain('administrators only')
    await page.get('[role=alert] button').trigger('click')
    await flushPromises()
    expect(page.find('[role=alert]').exists()).toBe(false)
    // An account without a name is shown by what it is stored under.
    expect(page.get('[data-test=account]').text()).toContain('service|ingest')
    page.unmount()
  })

  it('switches an account off', async () => {
    const { page, sent } = await mountPage(
      json({ data: { serviceAccounts: [account()] } }),
      json({ data: { setServiceAccountActive: account({ active: false }) } }),
    )
    const box = page.get('[data-test=active] input[type=checkbox]')
    expect((box.element as HTMLInputElement).checked).toBe(true)
    await box.trigger('click')
    await flushPromises()
    expect(sent[1]?.variables).toEqual({ serviceAccount: 'US-1', active: false })
    expect((box.element as HTMLInputElement).checked).toBe(false)
    page.unmount()
  })

  it('makes an account an administrator and back', async () => {
    const { page, sent } = await mountPage(
      json({ data: { serviceAccounts: [account({ name: null })] } }),
      json({ data: { setUserAdmin: account({ name: null, isAdmin: true }) } }),
      apiError('forbidden', { code: 'FORBIDDEN' }),
    )
    const row = page.get('[data-test=account]')
    const box = page.get('[data-test=admin] input[type=checkbox]')
    expect(row.text()).toContain('Grants only')
    await box.trigger('click')
    await flushPromises()
    expect(sent[1]?.query).toContain('setUserAdmin')
    expect(sent[1]?.variables).toEqual({ user: 'US-1', admin: true })
    expect(row.text()).toContain('Administrator')
    // A refusal leaves the account as the server has it and says why.
    await box.trigger('click')
    await flushPromises()
    expect(sent[2]?.variables).toEqual({ user: 'US-1', admin: false })
    expect(page.get('[role=alert]').text()).toContain('forbidden')
    expect(row.text()).toContain('Administrator')
    page.unmount()
  })

  it('creates an account from the form', async () => {
    const { page, sent } = await mountPage(
      json({ data: { serviceAccounts: [] } }),
      json({ data: { createServiceAccount: account({ id: 'US-9', name: 'subtitle-sync' }) } }),
    )
    await page.get('input[placeholder=ingest]').setValue('  subtitle-sync ')
    await page.get('form').trigger('submit')
    await flushPromises()
    expect(sent[1]?.variables).toEqual({ input: { name: 'subtitle-sync', admin: false, user: null } })
    expect(page.get('[data-test=account]').text()).toContain('subtitle-sync')
    expect((page.get('input[placeholder=ingest]').element as HTMLInputElement).value).toBe('')
    page.unmount()
  })
})

describe('create form', () => {
  function mountForm(create: (input: unknown) => Promise<unknown>) {
    return mount(CreateServiceAccountForm, { props: { create }, attachTo: document.body })
  }

  it('does not submit without a name', async () => {
    const create = vi.fn(async () => {})
    const form = mountForm(create)
    expect(form.get('button[type=submit]').attributes('disabled')).toBeDefined()
    await form.get('form').trigger('submit')
    expect(create).not.toHaveBeenCalled()
    form.unmount()
  })

  it('sends administrator and the user to convert', async () => {
    const create = vi.fn(async () => {})
    const form = mountForm(create)
    await form.get('input[placeholder=ingest]').setValue('ingest')
    await form.get('input[placeholder="US-…"]').setValue(' US-7 ')
    await form.get('input[type=checkbox]').trigger('click')
    await form.get('form').trigger('submit')
    await flushPromises()
    expect(create).toHaveBeenCalledWith({ name: 'ingest', admin: true, user: 'US-7' })
    form.unmount()
  })

  it.each([
    ['a name the server refuses', new ApiError('name must not start with US-', 'INVALID_INPUT', 'name'), 0],
    ['a name that is taken', new ApiError('already exists', 'CONFLICT', undefined, 'ALREADY_EXISTS'), 0],
    ['a user that cannot be converted', new ApiError('user not found', 'INVALID_INPUT', 'user'), 1],
  ])('shows %s beside its field and keeps the input', async (_what, error, field) => {
    const form = mountForm(async () => {
      throw error
    })
    await form.get('input[placeholder=ingest]').setValue('ingest')
    await form.get('form').trigger('submit')
    await flushPromises()
    const fields = form.findAll('[data-part=root][data-scope=field]')
    expect(fields[field]!.text()).toContain(error.message)
    expect(fields[1 - field]!.text()).not.toContain(error.message)
    expect(form.find('[role=alert]').exists()).toBe(false)
    expect((form.get('input[placeholder=ingest]').element as HTMLInputElement).value).toBe('ingest')
    form.unmount()
  })

  it('shows any other failure under the form', async () => {
    const failures: unknown[] = [new ApiError('administrators only', 'FORBIDDEN'), new Error('boom')]
    const form = mountForm(async () => {
      throw failures.shift()
    })
    await form.get('input[placeholder=ingest]').setValue('ingest')
    await form.get('form').trigger('submit')
    await flushPromises()
    expect(form.get('[role=alert]').text()).toBe('administrators only')
    await form.get('form').trigger('submit')
    await flushPromises()
    expect(form.get('[role=alert]').text()).toBe('Something went wrong.')
    form.unmount()
  })
})
