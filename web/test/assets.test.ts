import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import Page from '../app/pages/assets.vue'
import { apiError, assetRow, fakeFetch, json, page } from './helpers'

afterEach(() => vi.unstubAllGlobals())

// The import panel mounts first and asks who the caller is; here not an administrator.
async function mountPage(...answers: (Response | Error)[]) {
  const { fetcher, sent } = fakeFetch(json({ data: { me: { isAdmin: false } } }), ...answers)
  vi.stubGlobal('fetch', fetcher)
  const wrapper = mount(Page, { attachTo: document.body })
  await flushPromises()
  return { page: wrapper, sent }
}

describe('assets page', () => {
  it('reloads the list after an import', async () => {
    const { fetcher, sent } = fakeFetch(
      json({ data: { me: { isAdmin: true } } }),
      page('assets', []),
      json({ data: { assetTypes: [{ id: 1, name: 'Clip' }], projects: { nodes: [] } } }),
      json({ data: { assets: { nodes: [] } } }),
      json({ data: { importFromVidispine: { id: 'AS-9', title: 'New' } } }),
      page('assets', [assetRow({ id: 'AS-9', title: 'New' })]),
    )
    vi.stubGlobal('fetch', fetcher)
    const p = mount(Page, { attachTo: document.body })
    await flushPromises()
    await p.get('input[aria-label="Vidispine item id"]').setValue('VX-9')
    await p.get('form').trigger('submit')
    await flushPromises()
    await p.get('[data-test=new]').trigger('submit')
    await flushPromises()
    expect(sent).toHaveLength(6)
    expect(p.get('[data-test=asset]').text()).toContain('New')
    p.unmount()
  })

  it('lists assets and loads more', async () => {
    const { page: p, sent } = await mountPage(
      page('assets', [assetRow(), assetRow({ id: 'AS-2', title: 'Interview', legacyIds: [] })], 'AS-2'),
      page('assets', [assetRow({ id: 'AS-3', legacyIds: ['VX-3', 'VX-4'] })]),
    )
    expect(p.find('[aria-label="Import from Vidispine"]').exists()).toBe(false)
    let rows = p.findAll('[data-test=asset]')
    expect(rows[0]!.text()).toContain('Opening')
    expect(rows[0]!.text()).toContain('AS-1')
    expect(rows[0]!.text()).toContain('Clip')
    expect(rows[0]!.text()).toContain('VX-1')
    expect(rows[0]!.text()).toContain('2026-11-12')
    expect(rows[1]!.text()).toContain('—')
    await p.get('[data-test=more]').trigger('click')
    await flushPromises()
    expect(sent[2]?.variables).toEqual({ first: 50, after: 'AS-2' })
    rows = p.findAll('[data-test=asset]')
    expect(rows).toHaveLength(3)
    expect(rows[2]!.text()).toContain('VX-3, VX-4')
    expect(p.find('[data-test=more]').exists()).toBe(false)
    p.unmount()
  })

  it('says so when there are none', async () => {
    const { page: p } = await mountPage(page('assets', []))
    expect(p.text()).toContain('No assets you can see.')
    p.unmount()
  })

  it('tries the first page again after a failed load, and the next page after a failed more', async () => {
    const { page: p, sent } = await mountPage(
      apiError('down', { code: 'INTERNAL' }),
      page('assets', [assetRow()], 'AS-1'),
      apiError('down again', { code: 'INTERNAL' }),
      page('assets', [assetRow({ id: 'AS-0' })]),
    )
    expect(p.text()).not.toContain('No assets')
    await p.get('[role=alert] button').trigger('click')
    await flushPromises()
    expect(sent[2]?.variables).toEqual({ first: 50, after: null })
    await p.get('[data-test=more]').trigger('click')
    await flushPromises()
    expect(p.get('[role=alert]').text()).toContain('down again')
    await p.get('[role=alert] button').trigger('click')
    await flushPromises()
    expect(sent[4]?.variables).toEqual({ first: 50, after: 'AS-1' })
    expect(p.findAll('[data-test=asset]')).toHaveLength(2)
    p.unmount()
  })
})
