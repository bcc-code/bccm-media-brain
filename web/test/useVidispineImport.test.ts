import { describe, expect, it } from 'vitest'
import { itemIdOf, useVidispineImport } from '../app/composables/useVidispineImport'
import { apiError, fakeFetch, json } from './helpers'

const choices = () =>
  json({
  data: { assetTypes: [{ id: 1, name: 'Clip' }], projects: { nodes: [{ id: 'PJ-1', code: 'ABCD', name: 'Summer' }] } },
})

describe('itemIdOf', () => {
  it('accepts VX ids in any case and with spaces', () => {
    expect(itemIdOf(' vx-12 ')).toBe('VX-12')
    expect(itemIdOf('VX-')).toBeNull()
    expect(itemIdOf('AS-1')).toBeNull()
  })
})

describe('useVidispineImport', () => {
  it('loads the choices for an administrator', async () => {
    const { fetcher } = fakeFetch(json({ data: { me: { isAdmin: true } } }), choices())
    const s = useVidispineImport(fetcher)
    await s.prepare()
    expect(s.isAdmin.value).toBe(true)
    expect(s.types.value).toEqual([{ id: 1, name: 'Clip' }])
    expect(s.projects.value).toEqual([{ id: 'PJ-1', name: 'ABCD Summer' }])
  })

  it('asks nothing more for anyone else, and stays quiet when the choices fail', async () => {
    const { fetcher, sent } = fakeFetch(json({ data: { me: { isAdmin: false } } }))
    const s = useVidispineImport(fetcher)
    await s.prepare()
    expect(s.isAdmin.value).toBe(false)
    expect(sent).toHaveLength(1)

    const failing = fakeFetch(json({ data: { me: { isAdmin: true } } }), apiError('down', { code: 'INTERNAL' }))
    const t = useVidispineImport(failing.fetcher)
    await t.prepare()
    expect(t.types.value).toEqual([])
    expect(t.error.value?.message).toBe('down')
  })

  it('imports an item that was not imported', async () => {
    const { fetcher, sent } = fakeFetch(
      json({ data: { assets: { nodes: [] } } }),
      json({ data: { importFromVidispine: { id: 'AS-1', title: 'Talk' } } }),
    )
    const s = useVidispineImport(fetcher)
    await s.check('vx-7')
    expect(sent[0]?.variables).toEqual({ id: 'VX-7' })
    expect(s.lookup.value).toEqual({ itemId: 'VX-7', asset: null })
    await s.reimport() // nothing to re-import
    await s.importItem(1, 'PJ-1')
    expect(sent[1]?.variables).toEqual({ in: { itemId: 'VX-7', typeId: 1, projectId: 'PJ-1' } })
    expect(s.done.value).toEqual({ verb: 'Imported', asset: { id: 'AS-1', title: 'Talk' } })
    expect(s.lookup.value).toBeNull()
  })

  it('re-imports an item that was imported', async () => {
    const { fetcher, sent } = fakeFetch(
      json({ data: { assets: { nodes: [{ id: 'AS-1', title: 'Talk' }] } } }),
      json({ data: { refreshFromVidispine: { id: 'AS-1', title: 'Talk' } } }),
    )
    const s = useVidispineImport(fetcher)
    await s.check('VX-7')
    await s.importItem(1, null) // already imported
    expect(sent).toHaveLength(1)
    await s.reimport()
    expect(sent[1]?.variables).toEqual({ id: 'AS-1' })
    expect(s.done.value?.verb).toBe('Re-imported')
  })

  it('refuses what is not an item id, and keeps the server error', async () => {
    const { fetcher, sent } = fakeFetch(
      json({ data: { assets: { nodes: [] } } }),
      apiError('Vidispine did not answer', { code: 'UNAVAILABLE' }),
    )
    const s = useVidispineImport(fetcher)
    await s.check('12')
    expect(s.error.value?.field).toBe('itemId')
    expect(sent).toHaveLength(0)
    await s.check('VX-1')
    await s.importItem(1, null)
    expect(s.error.value?.code).toBe('UNAVAILABLE')
    expect(s.lookup.value).not.toBeNull()
  })

  it('turns a failure that is not an API error into a plain message', async () => {
    const s = useVidispineImport((async () => {
      throw new TypeError('boom')
    }) as unknown as typeof fetch)
    await s.check('VX-1')
    expect(s.error.value?.code).toBe('NETWORK')
  })
})
