import { RouterLinkStub, flushPromises, mount } from '@vue/test-utils'
import { afterEach, describe, expect, it, vi } from 'vitest'
import AssetView from '../app/components/AssetView.vue'
import { apiError, assetDetail, cuePage, fakeFetch, history, json, loaded, said } from './helpers'

afterEach(() => vi.unstubAllGlobals())

async function open(...answers: Response[]) {
  const { fetcher, sent } = fakeFetch(...answers)
  vi.stubGlobal('fetch', fetcher)
  const view = mount(AssetView, { props: { id: 'AS-1' }, attachTo: document.body })
  await flushPromises()
  return { view, sent }
}

type View = Awaited<ReturnType<typeof open>>['view']

/** The lines on the preview, changed words in brackets. */
const lines = (view: View) =>
  view.findAll('[data-test=line]').map((l) => {
    let html = l.html()
    for (const m of l.findAll('[data-test=changed]')) html = html.replace(m.html(), `[${m.text()}]`)
    return html
      .replace(/<[^>]+>/g, ' ')
      .replace(/\s+/g, ' ')
      .trim()
  })

async function seek(view: View, seconds: number, event = 'timeupdate') {
  const video = view.get('video').element as HTMLVideoElement
  Object.defineProperty(video, 'currentTime', { value: seconds, configurable: true })
  await view.get('video').trigger(event)
}

describe('asset view', () => {
  it('shows the asset, its preview and its metadata', async () => {
    const { view } = await open(loaded(), cuePage(said['ST-3']!))
    expect(view.get('[data-test=title]').text()).toBe('Autumn conference – opening')
    expect(view.get('[data-test=level]').text()).toBe('Edit access')
    expect(view.getComponent(RouterLinkStub).props('to')).toBe('/assets')
    expect(view.get('video').attributes('src')).toBe('/media/renditions/RE-p')
    expect(view.find('[data-test=timeline]').exists()).toBe(false)

    const text = view.get('aside').text()
    expect(text).toContain('2026-10-04 11:02')
    expect(text).toContain('Opening session, main hall.')
    const projects = view.findAll('[data-test=project]').map((p) => p.findAll('span').map((s) => s.text()))
    expect(projects[0]).toEqual(['Autumn conference AC26', 'AC26', 'SourceFolder', 'Source', 'Folder'])
    expect(projects[1]).toEqual(['A project', 'DeliverableManual', 'Deliverable', 'Manual'])
    const versions = view.findAll('[data-test=version]')
    expect(versions[0]!.text()).toContain('v2 Vidispine original')
    expect(versions[0]!.text()).toContain('Final')
    expect(versions[0]!.findAll('[data-test=rendition]').map((r) => r.text())).toEqual(['Master', 'Encode · preview'])
    const metadata = view.findAll('[data-test=metadata]')
    expect(metadata[0]!.findAll('span').map((s) => s.text())).toEqual(['season', '3'])
    expect(metadata[1]!.find('details').text()).toContain('"itemId": "VX-1"')
    view.unmount()
  })

  it('follows the video with the subtitles', async () => {
    let tick: FrameRequestCallback = () => {}
    vi.stubGlobal('requestAnimationFrame', (f: FrameRequestCallback) => ((tick = f), 7))
    const cancel = vi.fn()
    vi.stubGlobal('cancelAnimationFrame', cancel)
    const { view } = await open(loaded(), cuePage(said['ST-3']!))
    expect(lines(view)).toEqual(['nb r3']) // a layer keeps its box when it has nothing to say

    await seek(view, 1.2)
    expect(lines(view)).toEqual(['nb r3 Velkommen alle sammen'])
    await seek(view, 3.1, 'play')
    tick(0)
    await flushPromises()
    expect(lines(view)).toEqual(['nb r3 Korøvelsen begynner nå'])
    await seek(view, 4, 'pause')
    expect(cancel).toHaveBeenCalled()
    expect(lines(view)).toEqual(['nb r3'])
    view.unmount()
  })

  it('layers revisions picked from the tracks', async () => {
    const { view, sent } = await open(loaded(), cuePage(said['ST-3']!), cuePage(said['ST-2']!), cuePage(said['ST-1']!))
    const tracks = view.findAll('[data-test=track]')
    expect(tracks[0]!.text()).toContain('en · SUBTITLE')
    expect(tracks[0]!.findAll('[data-test=revision]')).toHaveLength(0)
    expect(tracks[1]!.text()).toContain('3 revisions · 1 shown')
    expect(tracks[1]!.get('[data-test=revision]').text()).toContain('Latest')
    await tracks[0]!.get('[data-test=track-toggle]').trigger('click')
    expect(tracks[0]!.get('[data-test=track-toggle]').attributes('aria-expanded')).toBe('true')
    expect(tracks[0]!.findAll('[data-test=revision]')).toHaveLength(1)

    const nb = () => view.findAll('[data-test=track]')[1]!.findAll('[data-test=revision]')
    await nb()[1]!.get('input[type=checkbox]').trigger('click')
    await flushPromises()
    await nb()[2]!.get('input[type=checkbox]').trigger('click')
    await flushPromises()
    expect(sent.map((s) => s.variables.id)).toEqual(['AS-1', 'ST-3', 'ST-2', 'ST-1'])
    expect(view.get('[data-test=count]').text()).toBe('3 of 3 shown')
    expect(nb()[1]!.get('[data-test=slot]').text()).toBe('Layer 2')
    expect(view.text()).toContain('3 layers shown.')
    const en = view.findAll('[data-test=track]')[0]!.get('[data-test=revision] input[type=checkbox]')
    expect(en.attributes('disabled')).toBeDefined()

    await seek(view, 3.1)
    // In layer order; the lowest line comes first. Differences are marked only when asked for.
    expect(lines(view)).toEqual(['nb r3 Korøvelsen begynner nå', 'nb r2 · whisper-large-v3 Kor øvelsen begynner nå', 'nb r1 · nb-whisper-large'])
    await view.get('[data-test=highlight] input[type=checkbox]').trigger('click')
    expect(lines(view)[1]).toBe('nb r2 · whisper-large-v3 [Kor øvelsen] begynner nå')
    await view.get('[data-test=show-tags] input[type=checkbox]').trigger('click')
    expect(lines(view)).toEqual(['Korøvelsen begynner nå', '[Kor øvelsen] begynner nå', ''])
    expect(view.find('[data-test=tag]').exists()).toBe(false)

    await nb()[0]!.get('input[type=checkbox]').trigger('click')
    await flushPromises()
    expect(view.get('[data-test=count]').text()).toBe('2 of 3 shown')
    view.unmount()
  })

  it('falls back to a timeline when the preview cannot be played', async () => {
    const { view } = await open(loaded(), cuePage(said['ST-3']!))
    await view.get('video').trigger('error')
    expect(view.find('video').exists()).toBe(false)
    expect(view.get('[data-test=no-preview]').text()).toContain('could not be loaded')
    const timeline = view.get('[data-test=timeline]')
    expect(timeline.attributes('max')).toBe('3900')
    await timeline.setValue('1500')
    expect(lines(view)).toEqual(['nb r3 Velkommen alle sammen'])
    view.unmount()
  })

  it('says when there is no preview and no subtitles', async () => {
    const { view } = await open(json({ data: { asset: assetDetail({ versions: { nodes: [] }, metadata: {}, projects: [] }), subtitleTracks: [] } }))
    expect(view.get('[data-test=no-preview]').text()).toContain('No preview')
    expect(view.get('[data-test=no-tracks]').exists()).toBe(true)
    expect(view.get('[data-test=timeline]').attributes('disabled')).toBeDefined()
    expect(view.get('aside').text()).toContain('No versions yet.')
    expect(view.get('aside').text()).toContain('In no project you can see.')
    view.unmount()
  })

  it('reports a missing asset and a failure, and opens another asset', async () => {
    const { view } = await open(
      json({ data: { asset: null, subtitleTracks: [] } }),
      apiError('boom', { code: 'INTERNAL' }),
      json({ data: { asset: assetDetail({ id: 'AS-2', myLevel: 'OWNER?' }), subtitleTracks: history.slice(3) } }),
      cuePage(said['ST-5']!),
    )
    expect(view.get('[data-test=not-found]').exists()).toBe(true)
    await view.setProps({ id: 'AS-3' })
    await flushPromises()
    expect(view.get('[role=alert]').text()).toBe('boom')
    await view.setProps({ id: 'AS-2' })
    await flushPromises()
    expect(view.get('[data-test=level]').text()).toBe('OWNER?')
    await view.get('video').trigger('timeupdate')
    expect(lines(view)).toEqual(['en r1'])
    view.unmount()
  })
})
