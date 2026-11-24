import { flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import Page from '../app/pages/subtitles.vue'
import { apiError, cue, cuePage, fakeFetch, json, revision } from './helpers'

afterEach(() => vi.unstubAllGlobals())

const asset = { id: 'AS-1', title: 'Evening news', legacyIds: ['VX-123'] }
const history = [
  revision({ id: 'ST-2', revision: 2, origin: 'MANUAL', createdBy: 'US-7' }),
  revision({ id: 'ST-1', revision: 1, origin: 'ASR', model: 'nb-whisper' }),
  revision({ id: 'ST-9', renditionId: 'RE-zz', language: null, kind: 'TRANSCRIPT' }),
]
const newer = [cue(1, 'Hello'), cue(2, 'world', { speaker: 'Anna' })]
const older = [cue(1, 'Hello'), cue(2, 'wrold')]

async function openPage(id: string, ...answers: (Response | Error)[]) {
  const { fetcher, sent } = fakeFetch(...answers)
  vi.stubGlobal('fetch', fetcher)
  const page = mount(Page, { attachTo: document.body })
  await page.get('input[placeholder="AS-… or VX-123"]').setValue(id)
  await page.get('form').trigger('submit')
  await flushPromises()
  return { page, sent }
}

type Page = Awaited<ReturnType<typeof openPage>>['page']

/** A revision by itself: "same <text>"; a comparison: "<kind> <older> | <newer>", changed words in brackets. */
const kinds = (page: Page) =>
  page.findAll('[data-test=cue]').map((r) => {
    if (!r.find('[data-test=old]').exists()) return `${r.attributes('data-kind')} ${r.findAll('td')[1]!.text()}`
    const side = (name: string) => {
      const cell = r.get(`[data-test=${name}]`)
      let html = cell.html()
      for (const m of cell.findAll('[data-test=changed]')) html = html.replace(m.html(), `[${m.text()}]`)
      return html.replace(/<[^>]+>/g, '').trim()
    }
    return `${r.attributes('data-kind')} ${side('old')} | ${side('new')}`
  })

describe('subtitles page', () => {
  it('does not search without an id', async () => {
    const { page, sent } = await openPage('  ')
    expect(page.get('button[type=submit]').attributes('disabled')).toBeDefined()
    expect(sent).toHaveLength(0)
    page.unmount()
  })

  it('shows the tracks of an asset and the cues of the newest revision', async () => {
    const { page } = await openPage(
      'AS-1',
      json({ data: { asset } }),
      json({ data: { subtitleTracks: history } }),
      cuePage(newer),
      cuePage([]),
    )
    expect(page.get('[data-test=asset]').text()).toContain('Evening news')
    expect(page.get('[data-test=asset]').text()).toContain('AS-1 · VX-123')
    expect(page.findAll('[data-test=vault]').map((a) => [a.text(), a.attributes('href'), a.attributes('target')])).toEqual([
      ['VX-123', 'https://vault.bcc.media/item/VX-123', '_blank'],
    ])
    const tracks = page.findAll('[data-test=track]')
    expect(tracks.map((t) => t.text())).toEqual(['nb · SUBTITLE2 revisions', 'No language · TRANSCRIPT1 revision'])
    expect(tracks[0]!.attributes('aria-current')).toBe('true')
    expect((page.get('[data-test=shown]').element as HTMLSelectElement).value).toBe('ST-2')
    expect(page.get('[data-test=shown]').text()).toContain('Revision 2 · MANUAL · 2026-11-20')
    expect(page.get('[data-test=details]').text()).toBe('ST-2 · by US-7 · 2026-11-20T08:00:00Z · 2 cues')
    expect(kinds(page)).toEqual(['same Hello', 'same Anna: world'])
    expect(page.get('[data-test=cue]').text()).toContain('00:00:01.000 → 00:00:01.900')
    expect(page.find('[data-test=ignore-timing]').exists()).toBe(false)

    await tracks[1]!.trigger('click')
    await flushPromises()
    expect(tracks[1]!.attributes('aria-current')).toBe('true')
    expect(page.text()).toContain('No cues.')
    expect(page.get('[data-test=base]').attributes('disabled')).toBeDefined()
    page.unmount()
  })

  it('shows the difference between two revisions', async () => {
    const { page } = await openPage(
      'VX-123',
      json({ data: { assets: { nodes: [asset] } } }),
      json({ data: { subtitleTracks: history } }),
      cuePage(newer),
      cuePage(older),
    )
    const base = page.get('[data-test=base]')
    expect(base.findAll('option').map((o) => o.attributes('value'))).toEqual(['', 'ST-1'])
    expect(base.findAll('option').map((o) => o.text())).toEqual(['Nothing', 'Revision 1 · ASR · nb-whisper · 2026-11-20'])
    await base.setValue('ST-1')
    await flushPromises()
    expect(kinds(page)).toEqual(['same Hello | Hello', 'change [wrold] | [Anna: world]'])
    expect(page.get('[data-test=details]').text()).toBe('Revision 1 → 2: +1 −1 cues')
    expect(page.findAll('[data-test=comparison] th').map((h) => h.text())).toEqual(['Revision 1 · nb-whisper', 'Revision 2'])

    await page.get('[data-test=changes-only] input[type=checkbox]').trigger('click')
    expect(kinds(page)).toEqual(['change [wrold] | [Anna: world]'])

    // Showing the older revision ends the comparison with itself.
    await page.get('[data-test=shown]').setValue('ST-1')
    await flushPromises()
    expect(kinds(page)).toEqual(['same Hello', 'same wrold'])
    expect(page.get('[data-test=details]').text()).toContain('nb-whisper')
    page.unmount()
  })

  it('downloads the shown revision as SRT', async () => {
    const { page, sent } = await openPage(
      'AS-1',
      json({ data: { asset } }),
      json({ data: { subtitleTracks: history } }),
      cuePage(newer),
      json({ data: { subtitleTrack: { export: 'SRT TEXT' } } }),
      json({ data: { subtitleTrack: null } }),
    )
    const blobs: Blob[] = []
    vi.stubGlobal('URL', Object.assign(URL, { createObjectURL: vi.fn((b: Blob) => (blobs.push(b), 'blob:x')), revokeObjectURL: vi.fn() }))
    const click = vi.spyOn(HTMLAnchorElement.prototype, 'click').mockImplementation(function (this: HTMLAnchorElement) {
      expect([this.download, this.href]).toEqual(['AS-1-nb-subtitle-r2.srt', 'blob:x'])
    })
    await page.get('[data-test=download]').trigger('click')
    await flushPromises()
    expect(sent[3]?.variables).toEqual({ id: 'ST-2' })
    expect(click).toHaveBeenCalledOnce()
    expect(await blobs[0]!.text()).toBe('SRT TEXT')
    expect(URL.revokeObjectURL).toHaveBeenCalledWith('blob:x')

    // A failed export downloads nothing and says why.
    await page.get('[data-test=download]').trigger('click')
    await flushPromises()
    expect(click).toHaveBeenCalledOnce()
    expect(page.get('[role=alert]').text()).toContain('no longer exists')
    click.mockRestore()
    page.unmount()
  })

  it('says when two revisions do not differ once timing is left out', async () => {
    const { page } = await openPage(
      'AS-1',
      json({ data: { asset } }),
      json({ data: { subtitleTracks: history } }),
      cuePage([cue(1, 'Hello', { startMs: 1040 })]),
      cuePage([cue(1, 'Hello')]),
    )
    await page.get('[data-test=base]').setValue('ST-1')
    await flushPromises()
    await page.get('[data-test=changes-only] input[type=checkbox]').trigger('click')
    // Text unchanged, timing moved: the timecodes are marked, the words are not.
    expect(kinds(page)).toEqual(['change Hello | Hello'])
    expect(page.findAll('[data-test=retimed]')).toHaveLength(2)
    await page.get('[data-test=ignore-timing] input[type=checkbox]').trigger('click')
    expect(kinds(page)).toHaveLength(0)
    expect(page.text()).toContain('No differences.')
    await page.get('[data-test=changes-only] input[type=checkbox]').trigger('click')
    expect(kinds(page)).toEqual(['same Hello | Hello'])
    expect(page.findAll('[data-test=retimed]')).toHaveLength(2)
    page.unmount()
  })

  it('lets the user pick among assets with the same legacy id', async () => {
    const { page, sent } = await openPage(
      'VX-123',
      json({ data: { assets: { nodes: [asset, { id: 'AS-2', title: 'Copy', legacyIds: ['VX-123'] }] } } }),
      json({ data: { subtitleTracks: [] } }),
    )
    const choices = page.findAll('[data-test=candidate]')
    expect(choices.map((c) => c.text())).toEqual(['Evening newsAS-1', 'CopyAS-2'])
    await choices[1]!.trigger('click')
    await flushPromises()
    expect(sent[1]?.variables).toEqual({ assetId: 'AS-2' })
    expect(page.get('[data-test=asset]').text()).toContain('Copy')
    expect(page.get('[data-test=no-tracks]').text()).toBe('This asset has no subtitles.')
    page.unmount()
  })

  it('says when no asset matches and shows a refusal', async () => {
    const { page } = await openPage('VX-404', json({ data: { assets: { nodes: [] } } }), apiError('boom', { code: 'INTERNAL' }))
    expect(page.find('[data-test=not-found]').exists()).toBe(true)
    await page.get('form').trigger('submit')
    await flushPromises()
    expect(page.find('[data-test=not-found]').exists()).toBe(false)
    expect(page.get('[role=alert]').text()).toBe('boom')
    page.unmount()
  })

  it('deletes the shown revision after asking, and never a track\'s only one', async () => {
    const { page, sent } = await openPage(
      'AS-1',
      json({ data: { asset: { ...asset, myLevel: 'DELETE' } } }),
      json({ data: { subtitleTracks: history } }),
      cuePage(newer),
      json({ data: { deleteSubtitleTrack: true } }),
      cuePage(older),
    )
    await page.get('[data-test=delete]').trigger('click')
    expect(page.get('[data-test=confirm]').text()).toContain('Delete revision 2? This cannot be undone.')
    await page.get('[data-test=cancel-delete]').trigger('click')
    expect(page.find('[data-test=confirm]').exists()).toBe(false)
    expect(sent).toHaveLength(3)

    await page.get('[data-test=delete]').trigger('click')
    await page.get('[data-test=confirm-delete]').trigger('click')
    expect(page.get('[data-test=confirm-delete]').text()).toBe('Deleting…')
    await flushPromises()
    expect(sent[3]?.variables).toEqual({ id: 'ST-2' })
    expect(page.find('[data-test=confirm]').exists()).toBe(false)
    expect(page.findAll('[data-test=shown] option').map((o) => o.attributes('value'))).toEqual(['ST-1'])
    expect(page.findAll('[data-test=track]').map((t) => t.text())).toEqual(['nb · SUBTITLE1 revision', 'No language · TRANSCRIPT1 revision'])
    expect(kinds(page)).toEqual(['same Hello', 'same wrold'])
    const only = page.get('[data-test=delete]')
    expect(only.attributes('disabled')).toBeDefined()
    expect(only.attributes('title')).toBe('The only revision of this track cannot be deleted')
    page.unmount()
  })

  it('shows why a delete was refused and forgets the question on another revision', async () => {
    const { page } = await openPage(
      'AS-1',
      json({ data: { asset: { ...asset, myLevel: 'ADMIN' } } }),
      json({ data: { subtitleTracks: history } }),
      cuePage(newer),
      apiError('not allowed', { code: 'FORBIDDEN' }),
      cuePage(older),
    )
    await page.get('[data-test=delete]').trigger('click')
    await page.get('[data-test=confirm-delete]').trigger('click')
    await flushPromises()
    expect(page.get('[role=alert]').text()).toBe('not allowed')
    expect(page.find('[data-test=confirm]').exists()).toBe(true)
    await page.get('[data-test=shown]').setValue('ST-1')
    await flushPromises()
    expect(page.find('[data-test=confirm]').exists()).toBe(false)
    expect(page.get('[data-test=delete]').attributes('title')).toBe('Delete revision 1')
    page.unmount()
  })

  it('offers no delete without DELETE on the asset', async () => {
    const { page } = await openPage(
      'AS-1',
      json({ data: { asset: { ...asset, myLevel: 'EDIT' } } }),
      json({ data: { subtitleTracks: history } }),
      cuePage(newer),
    )
    expect(page.find('[data-test=download]').exists()).toBe(true)
    expect(page.find('[data-test=delete]').exists()).toBe(false)
    page.unmount()
  })

  it('says Loading while the first cues are on their way', async () => {
    let answer!: (r: Response) => void
    const slow = new Promise<Response>((resolve) => (answer = resolve))
    const { fetcher } = fakeFetch(json({ data: { asset } }), json({ data: { subtitleTracks: history } }))
    const fetchAll = vi.fn((url: RequestInfo | URL, init?: RequestInit) =>
      String(init?.body).includes('SubtitleCues') ? slow : fetcher(url, init),
    )
    vi.stubGlobal('fetch', fetchAll)
    const page = mount(Page, { attachTo: document.body })
    await page.get('input').setValue('AS-1')
    await page.get('form').trigger('submit')
    await flushPromises()
    // The asset is not shown before its tracks, and those select a revision whose cues are slow.
    expect(page.get('button[type=submit]').text()).toBe('Opening…')
    answer(cuePage(newer))
    await flushPromises()
    expect(page.get('button[type=submit]').text()).toBe('Open')
    expect(kinds(page)).toHaveLength(2)
    page.unmount()
  })
})
