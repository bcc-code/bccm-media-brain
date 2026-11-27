import { describe, expect, it } from 'vitest'
import { cuesAt, layerTag, useAsset, type Layer } from '../app/composables/useAsset'
import { apiError, assetDetail, cue, cuePage, fakeFetch, history, json, loaded, revision, said } from './helpers'

/** A layer as "<tag>: <text>", with changed words in brackets. */
const shown = (layers: Layer[]) =>
  layers.map((l) => `${layerTag(l.revision)}: ${l.text.map((s) => (s.changed ? `[${s.text}]` : s.text)).join('')}`)

describe('useAsset', () => {
  it('opens an asset with the newest revision of its original language on the preview', async () => {
    const { fetcher, sent } = fakeFetch(loaded(), cuePage(said['ST-3']!))
    const a = useAsset(fetcher)
    await a.load('AS-1')
    expect(sent[0]!.variables).toEqual({ id: 'AS-1' })
    expect(sent[0]!.query).toContain('subtitleTracks(assetId: $id, history: true)')
    expect(a.asset.value?.title).toBe('Autumn conference – opening')
    expect(a.tracks.value.map((t) => t.renditionId)).toEqual(['RE-en', 'RE-nb'])
    expect(a.selected.value).toEqual(['ST-3'])
    expect(sent[1]!.variables).toEqual({ id: 'ST-3', first: 5000, after: null })
    expect(a.preview.value).toEqual({ renditionId: 'RE-p', url: '/media/renditions/RE-p' })
    expect(a.duration.value).toBe(3900)
    expect(a.loading.value).toBe(false)
  })

  it('starts with the first track when none is in the original language, and with none without subtitles', async () => {
    let { fetcher } = fakeFetch(loaded({ originalLanguage: 'de' }), cuePage(said['ST-5']!))
    let a = useAsset(fetcher)
    await a.load('AS-1')
    expect(a.selected.value).toEqual(['ST-5'])

    fetcher = fakeFetch(json({ data: { asset: assetDetail(), subtitleTracks: [] } })).fetcher
    a = useAsset(fetcher)
    await a.load('AS-1')
    expect(a.selected.value).toEqual([])
    expect(a.layers.value).toEqual([])
    expect(a.duration.value).toBe(0)
  })

  it('layers up to three revisions and marks the words that differ from the first', async () => {
    const { fetcher, sent } = fakeFetch(loaded(), cuePage(said['ST-3']!), cuePage(said['ST-2']!), cuePage(said['ST-1']!))
    const a = useAsset(fetcher)
    await a.load('AS-1')
    await a.toggle('ST-2')
    await a.toggle('ST-1')
    await a.toggle('ST-5') // a fourth is not added
    expect(a.selected.value).toEqual(['ST-3', 'ST-2', 'ST-1'])
    expect(sent).toHaveLength(4)

    a.time.value = 1200
    expect(shown(a.layers.value)).toEqual([
      'nb r3: Velkommen alle sammen',
      'nb r2 · whisper-large-v3: Velkommen alle sammen',
      'nb r1 · nb-whisper-large: Velkommen alle',
    ])
    // Overlapping cues are shown together. Words layer 1 lacks are marked, words it has more are not.
    a.time.value = 1600
    expect(shown(a.layers.value)[2]).toBe('nb r1 · nb-whisper-large: Velkommen alle\n[og hei]')
    a.time.value = 3100
    expect(shown(a.layers.value)).toEqual([
      'nb r3: Korøvelsen begynner nå',
      'nb r2 · whisper-large-v3: [Kor øvelsen] begynner nå',
      'nb r1 · nb-whisper-large: ',
    ])
    a.time.value = 2950
    expect(shown(a.layers.value)[1]).toBe('nb r2 · whisper-large-v3: ')

    a.highlight.value = false
    a.time.value = 3100
    expect(shown(a.layers.value)[1]).toBe('nb r2 · whisper-large-v3: Kor øvelsen begynner nå')

    // Taking a layer off moves the others down; its cues are kept for later.
    await a.toggle('ST-3')
    expect(a.layers.value.map((l) => [l.revision.id, l.slot])).toEqual([
      ['ST-2', 0],
      ['ST-1', 1],
    ])
    await a.toggle('ST-3')
    expect(sent).toHaveLength(4)
    await a.toggle('ST-404')
    expect(a.selected.value).toEqual(['ST-2', 'ST-1', 'ST-3'])
  })

  it('drops a revision whose cues cannot be read', async () => {
    const { fetcher } = fakeFetch(loaded(), cuePage(said['ST-3']!), json({ data: { subtitleTrack: null } }))
    const a = useAsset(fetcher)
    await a.load('AS-1')
    await a.toggle('ST-5')
    expect(a.selected.value).toEqual(['ST-3'])
    expect(a.error.value?.code).toBe('NOT_FOUND')
    expect(a.cuesLoading.value).toBe(false)
  })

  it('finds the newest preview with a video file', async () => {
    const versions = assetDetail().versions.nodes
    const noVideo = { ...versions[1]!, renditions: [{ ...versions[1]!.renditions[1]!, files: [] }] }
    const { fetcher } = fakeFetch(json({ data: { asset: assetDetail({ versions: { nodes: [versions[0]!, noVideo] } }), subtitleTracks: [] } }))
    const a = useAsset(fetcher)
    await a.load('AS-1')
    expect(a.preview.value).toBeNull()
  })

  it('tells a missing asset from a failure', async () => {
    for (const answer of [
      json({ data: { asset: null, subtitleTracks: [] } }),
      apiError('not found', { code: 'NOT_FOUND' }),
      apiError('bad id', { code: 'INVALID_INPUT' }),
    ]) {
      const a = useAsset(fakeFetch(answer).fetcher)
      await a.load('AS-404')
      expect(a.notFound.value).toBe(true)
      expect(a.asset.value).toBeNull()
      expect(a.error.value).toBeNull()
    }
    const a = useAsset(fakeFetch(apiError('boom', { code: 'INTERNAL' })).fetcher)
    await a.load('AS-1')
    expect(a.notFound.value).toBe(false)
    expect(a.error.value?.message).toBe('boom')
  })
})

describe('cuesAt and layerTag', () => {
  it('finds the cues at a time, end excluded', () => {
    const cues = [cue(1, 'a'), cue(2, 'b')]
    expect(cuesAt(cues, 999)).toEqual([])
    expect(cuesAt(cues, 1000).map((c) => c.text)).toEqual(['a'])
    expect(cuesAt(cues, 1900)).toEqual([])
  })

  it('names a revision by language, number and source', () => {
    expect(layerTag(revision({ language: null, revision: 2 }))).toBe('No language r2')
  })
})
