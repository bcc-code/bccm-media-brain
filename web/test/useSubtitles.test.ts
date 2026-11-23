import { describe, expect, it } from 'vitest'
import { timecode, trackLabel, useSubtitles } from '../app/composables/useSubtitles'
import { apiError, cue, cuePage, fakeFetch, json, revision } from './helpers'

const asset = { id: 'AS-1', title: 'Evening news', legacyIds: ['VX-123'] }
const history = [
  revision({ id: 'ST-1', revision: 1 }),
  revision({ id: 'ST-3', revision: 3, origin: 'MANUAL' }),
  revision({ id: 'ST-2', revision: 2, origin: 'ASR', model: 'nb-whisper' }),
  revision({ id: 'ST-9', renditionId: 'RE-en', language: 'en' }),
]
const tracksAnswer = () => json({ data: { subtitleTracks: history } })

describe('timecode', () => {
  it('writes milliseconds as hours, minutes, seconds', () => {
    expect(timecode(0)).toBe('00:00:00.000')
    expect(timecode(3_723_045)).toBe('01:02:03.045')
  })
})

describe('trackLabel', () => {
  it('names the language and the kind', () => {
    expect(trackLabel({ language: 'nb', kind: 'CC' })).toBe('nb · CC')
    expect(trackLabel({ language: null, kind: 'TRANSCRIPT' })).toBe('No language · TRANSCRIPT')
  })
})

describe('useSubtitles', () => {
  it('opens an asset by id and shows the newest revision of its first track', async () => {
    const { fetcher, sent } = fakeFetch(json({ data: { asset } }), tracksAnswer(), cuePage([cue(1, 'Hello')]))
    const s = useSubtitles(fetcher)
    const pending = s.open(' as-1 ')
    expect(s.loading.value).toBe(true)
    await pending
    expect(s.loading.value).toBe(false)
    expect(sent[0]?.variables).toEqual({ id: 'AS-1' })
    expect(sent[1]?.variables).toEqual({ assetId: 'AS-1' })
    expect(s.asset.value).toEqual(asset)
    expect(s.tracks.value.map((t) => [t.language, t.revisions.map((r) => r.revision)])).toEqual([
      ['en', [1]],
      ['nb', [3, 2, 1]],
    ])
    expect(s.track.value?.language).toBe('en')
    expect(s.shown.value?.id).toBe('ST-9')
    expect(sent[2]?.variables).toEqual({ id: 'ST-9', first: 5000, after: null })
    expect(s.shownCues.value).toEqual([cue(1, 'Hello')])
    expect(s.comparison.value).toBeNull()
  })

  it('opens an asset by its legacy id', async () => {
    const { fetcher, sent } = fakeFetch(
      json({ data: { assets: { nodes: [asset] } } }),
      json({ data: { subtitleTracks: [] } }),
    )
    const s = useSubtitles(fetcher)
    await s.open('VX-123')
    expect(sent[0]?.variables).toEqual({ legacyId: 'VX-123' })
    expect(s.asset.value).toEqual(asset)
    expect(s.tracks.value).toEqual([])
    expect(s.track.value).toBeNull()
  })

  it('lets the user pick when several assets have the legacy id', async () => {
    const other = { id: 'AS-2', title: 'Copy', legacyIds: ['VX-123'] }
    const { fetcher, sent } = fakeFetch(
      json({ data: { assets: { nodes: [asset, other] } } }),
      json({ data: { subtitleTracks: [] } }),
    )
    const s = useSubtitles(fetcher)
    await s.open('VX-123')
    expect(s.asset.value).toBeNull()
    expect(s.candidates.value).toEqual([asset, other])
    await s.choose(other)
    expect(sent[1]?.variables).toEqual({ assetId: 'AS-2' })
    expect(s.asset.value).toEqual(other)
  })

  it('says when nothing matches, by id or by legacy id', async () => {
    const { fetcher } = fakeFetch(json({ data: { asset: null } }), json({ data: { assets: { nodes: [] } } }))
    const s = useSubtitles(fetcher)
    await s.open('AS-404')
    expect(s.notFound.value).toBe(true)
    await s.open('VX-404')
    expect(s.notFound.value).toBe(true)
    expect(s.asset.value).toBeNull()
  })

  it('does nothing for an empty id', async () => {
    const { fetcher, sent } = fakeFetch()
    await useSubtitles(fetcher).open('   ')
    expect(sent).toHaveLength(0)
  })

  it('keeps the error of a failed search and of failed tracks', async () => {
    const { fetcher } = fakeFetch(
      apiError('invalid id', { code: 'INVALID_INPUT' }),
      json({ data: { asset } }),
      apiError('boom', { code: 'INTERNAL' }),
      json({ data: { asset: null } }),
    )
    const s = useSubtitles(fetcher)
    await s.open('AS-x')
    expect(s.error.value?.code).toBe('INVALID_INPUT')
    await s.open('AS-1')
    expect(s.error.value?.code).toBe('INTERNAL')
    expect(s.asset.value).toBeNull()
    expect(s.loading.value).toBe(false)
    await s.open('AS-2')
    expect(s.error.value).toBeNull()
  })

  async function opened(...answers: Response[]) {
    const fake = fakeFetch(json({ data: { asset } }), tracksAnswer(), cuePage([]), ...answers)
    const s = useSubtitles(fake.fetcher)
    await s.open('AS-1')
    return { s, sent: fake.sent }
  }

  it('reads every page of a revision and reads it only once', async () => {
    const { s, sent } = await opened(cuePage([cue(1, 'a')], 'c1'), cuePage([cue(2, 'b')]))
    await s.selectTrack('RE-nb')
    expect(s.shown.value?.id).toBe('ST-3')
    expect(sent[4]?.variables).toEqual({ id: 'ST-3', first: 5000, after: 'c1' })
    expect(s.shownCues.value.map((c) => c.text)).toEqual(['a', 'b'])
    await s.selectTrack('RE-en')
    await s.selectTrack('RE-nb')
    expect(sent).toHaveLength(5)
    expect(s.shownCues.value).toHaveLength(2)
  })

  it('compares two revisions from the older to the newer, whichever is shown', async () => {
    const older = [cue(1, 'Hello'), cue(2, 'wrold'), cue(3, 'Bye')]
    const newer = [cue(1, 'Hello'), cue(2, 'world'), cue(3, 'Bye'), cue(4, 'Thanks')]
    const { s } = await opened(cuePage(newer), cuePage(older))
    await s.selectTrack('RE-nb')
    await s.compare('ST-1')
    expect(s.base.value?.id).toBe('ST-1')
    const want = ['same Hello', 'removed wrold', 'added world', 'same Bye', 'added Thanks']
    const rows = () => s.comparison.value?.rows.map((r) => `${r.kind} ${r.item.text}`)
    expect(rows()).toEqual(want)
    expect(s.comparison.value).toMatchObject({ from: { revision: 1 }, to: { revision: 3 }, added: 2, removed: 1 })
    expect(s.comparison.value?.pairs.map((p) => `${p.kind} ${p.older?.cue.text ?? '-'} ${p.newer?.cue.text ?? '-'}`)).toEqual([
      'same Hello Hello',
      'change wrold world',
      'same Bye Bye',
      'change - Thanks',
    ])

    // The other way round is the same comparison.
    await s.show('ST-1')
    expect(s.base.value).toBeNull()
    await s.compare('ST-3')
    expect(rows()).toEqual(want)
    expect(s.comparison.value).toMatchObject({ from: { revision: 1 }, to: { revision: 3 } })

    await s.compare(null)
    expect(s.comparison.value).toBeNull()
  })

  it('can leave timing out of a comparison', async () => {
    const { s } = await opened(
      cuePage([cue(1, 'Hello', { startMs: 1040 }), cue(2, 'Bye', { speaker: 'Anna' })]),
      cuePage([cue(1, 'Hello'), cue(2, 'Bye')]),
    )
    await s.selectTrack('RE-nb')
    await s.compare('ST-1')
    expect(s.comparison.value).toMatchObject({ added: 2, removed: 2 })
    s.ignoreTiming.value = true
    expect(s.comparison.value).toMatchObject({ added: 1, removed: 1 })
    // An unchanged row carries each revision's own cue, with its own timing.
    expect(s.comparison.value?.rows[0]).toEqual({ kind: 'same', item: cue(1, 'Hello', { startMs: 1040 }), older: cue(1, 'Hello') })
    const side = (x: { cue: { text: string }; text: { text: string; changed: boolean }[] } | null) =>
      x && x.text.map((t) => (t.changed ? `[${t.text}]` : t.text)).join('')
    expect(s.comparison.value?.pairs.map((p) => [p.kind, side(p.older), side(p.newer)])).toEqual([
      ['same', 'Hello', 'Hello'],
      ['change', 'Bye', '[Anna:] Bye'],
    ])
  })

  it('ignores a track, a revision or a comparison that is not there', async () => {
    const { s, sent } = await opened(cuePage([]))
    await s.selectTrack('RE-nb')
    const before = sent.length
    await s.selectTrack('RE-none')
    await s.show('ST-none')
    expect(s.shown.value?.id).toBe('ST-3')
    await s.compare('ST-3')
    await s.compare('ST-9')
    expect(s.base.value).toBeNull()
    expect(sent).toHaveLength(before)
  })

  it('keeps the error when cues cannot be read', async () => {
    const { s } = await opened(
      json({ data: { subtitleTrack: null } }),
      apiError('boom', { code: 'INTERNAL' }),
    )
    await s.selectTrack('RE-nb')
    expect(s.error.value?.code).toBe('NOT_FOUND')
    expect(s.cuesLoading.value).toBe(false)
    await s.show('ST-2')
    expect(s.error.value?.code).toBe('INTERNAL')
  })

  it('lets the last selection win over a slower earlier one', async () => {
    const { s } = await opened(cuePage([cue(1, 'three')]), cuePage([cue(1, 'two')]))
    const first = s.selectTrack('RE-nb')
    const second = s.show('ST-2')
    await Promise.all([first, second])
    expect(s.shown.value?.id).toBe('ST-2')
    expect(s.shownCues.value.map((c) => c.text)).toEqual(['two'])
    expect(s.cuesLoading.value).toBe(false)
  })

  it('drops the error of a selection that was replaced', async () => {
    const { s } = await opened(apiError('boom', { code: 'INTERNAL' }), cuePage([cue(1, 'two')]))
    const first = s.selectTrack('RE-nb')
    const second = s.show('ST-2')
    await Promise.all([first, second])
    expect(s.error.value).toBeNull()
    expect(s.shownCues.value.map((c) => c.text)).toEqual(['two'])
  })
})
