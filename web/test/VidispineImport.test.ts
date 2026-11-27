import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import VidispineImport from '../app/components/VidispineImport.vue'
import { fakeFetch, json } from './helpers'

afterEach(() => vi.unstubAllGlobals())

const admin = () => json({ data: { me: { isAdmin: true } } })
const choices = () =>
  json({
  data: {
    assetTypes: [
      { id: 1, name: 'Clip' },
      { id: 2, name: 'Programme' },
    ],
    projects: { nodes: [{ id: 'PJ-1', code: 'ABCD', name: 'Summer' }] },
  },
})

async function mountPanel(...answers: Response[]) {
  const { fetcher, sent } = fakeFetch(...answers)
  vi.stubGlobal('fetch', fetcher)
  const panel = mount(VidispineImport, { attachTo: document.body })
  await flushPromises()
  return { panel, sent }
}

describe('Vidispine import panel', () => {
  it('is not shown to someone who is not an administrator', async () => {
    const { panel } = await mountPanel(json({ data: { me: { isAdmin: false } } }))
    expect(panel.find('section').exists()).toBe(false)
    panel.unmount()
  })

  it('imports a new item with the chosen type and project', async () => {
    const { panel, sent } = await mountPanel(
      admin(),
      choices(),
      json({ data: { assets: { nodes: [] } } }),
      json({ data: { importFromVidispine: { id: 'AS-1', title: 'Talk' } } }),
    )
    expect(panel.get('button[type=submit]').attributes('disabled')).toBeDefined()
    await panel.get('input').setValue('vx-7')
    await panel.get('form').trigger('submit')
    await flushPromises()
    expect(panel.get('[data-test=import]').text()).toBe('Import VX-7')
    await panel.get('[data-test=type]').setValue('2')
    await panel.get('[data-test=project]').setValue('PJ-1')
    await panel.get('[data-test=new]').trigger('submit')
    await flushPromises()
    expect(sent[3]?.variables).toEqual({ in: { itemId: 'VX-7', typeId: 2, projectId: 'PJ-1' } })
    expect(panel.get('[data-test=done]').text()).toContain('Imported Talk')
    expect((panel.get('input').element as HTMLInputElement).value).toBe('')
    expect(panel.emitted('changed')).toHaveLength(1)
    panel.unmount()
  })

  it('imports without a project by default', async () => {
    const { panel, sent } = await mountPanel(
      admin(),
      choices(),
      json({ data: { assets: { nodes: [] } } }),
      json({ data: { importFromVidispine: { id: 'AS-1', title: 'Talk' } } }),
    )
    await panel.get('input').setValue('VX-7')
    await panel.get('form').trigger('submit')
    await flushPromises()
    await panel.get('[data-test=new]').trigger('submit')
    await flushPromises()
    expect(sent[3]?.variables).toEqual({ in: { itemId: 'VX-7', typeId: 1, projectId: null } })
    panel.unmount()
  })

  it('re-imports an item that was imported before', async () => {
    const { panel, sent } = await mountPanel(
      admin(),
      choices(),
      json({ data: { assets: { nodes: [{ id: 'AS-1', title: 'Talk' }] } } }),
      json({ data: { refreshFromVidispine: { id: 'AS-1', title: 'Talk' } } }),
    )
    await panel.get('input').setValue('VX-7')
    await panel.get('form').trigger('submit')
    await flushPromises()
    expect(panel.get('[data-test=existing]').text()).toContain('VX-7 is Talk')
    await panel.get('[data-test=reimport]').trigger('click')
    await flushPromises()
    expect(sent[3]?.variables).toEqual({ id: 'AS-1' })
    expect(panel.get('[data-test=done]').text()).toContain('Re-imported Talk')
    panel.unmount()
  })

  it('says what is wrong with the id', async () => {
    const { panel } = await mountPanel(admin(), choices())
    await panel.get('input').setValue('12')
    await panel.get('form').trigger('submit')
    await flushPromises()
    expect(panel.get('[role=alert]').text()).toContain('VX-123')
    panel.unmount()
  })
})
