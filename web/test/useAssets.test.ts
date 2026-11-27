import { describe, expect, it } from 'vitest'
import { useAssets } from '../app/composables/useAssets'
import { assetRow, fakeFetch, page } from './helpers'

describe('useAssets', () => {
  it('pages through the assets', async () => {
    const { fetcher, sent } = fakeFetch(page('assets', [assetRow()], 'AS-1'), page('assets', [assetRow({ id: 'AS-0' })]))
    const s = useAssets(fetcher)
    await s.reload()
    await s.more()
    expect(sent.map((r) => r.variables)).toEqual([
      { first: 50, after: null },
      { first: 50, after: 'AS-1' },
    ])
    expect(s.assets.value.map((a) => a.id)).toEqual(['AS-1', 'AS-0'])
    expect(s.hasMore.value).toBe(false)
  })
})
