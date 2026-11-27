import { computed, reactive, ref } from 'vue'
import { wordDiff, type Segment } from '../utils/diff'
import { ApiError, gql, type Fetcher } from '../utils/graphql'
import { REVISION, cueText, grouped, readCues, type Cue, type Revision, type Track } from './useSubtitles'

export interface AssetFile {
  role: string
  file: { id: string; mimeType: string | null; durationMs: number | null; width: number | null; height: number | null }
}

export interface Rendition {
  id: string
  kind: string
  language: string | null
  purpose: string | null
  files: AssetFile[]
}

export interface Version {
  id: string
  number: number
  label: string | null
  status: string
  renditions: Rendition[]
}

export interface AssetDetail {
  id: string
  revision: number
  title: string
  description: string | null
  capturedAt: string | null
  camera: string | null
  reel: string | null
  metadata: Record<string, unknown>
  legacyIds: string[]
  originalLanguage: string | null
  createdAt: string
  updatedAt: string
  myLevel: string
  type: { name: string }
  projects: { role: string; linkOrigin: string; project: { id: string; code: string; name: string } | null }[]
  /** Oldest first. */
  versions: { nodes: Version[] }
}

/** How many subtitle revisions can be shown on the preview at once. */
export const MAX_LAYERS = 3

/**
 * The colour of each layer's text on the preview. They differ in lightness as well as hue, and
 * each line carries its tag too, so the layers can be told apart without colour.
 */
export const LAYER_COLORS = ['#ffffff', '#fde047', '#7dd3fc'] as const

/** What a layer's line on the preview is labelled with: language, revision and model or source. */
export function layerTag(r: Revision): string {
  return [`${r.language ?? 'No language'} r${r.revision}`, r.source].filter(Boolean).join(' · ')
}

/** The versions an asset page reads; an asset has a handful. */
const VERSIONS = 50

const ASSET = `id revision title description capturedAt camera reel metadata legacyIds originalLanguage createdAt updatedAt myLevel
  type { name }
  projects { role linkOrigin project { id code name } }
  versions(first: ${VERSIONS}) { nodes { id number label status renditions {
    id kind language purpose files { role file { id mimeType durationMs width height } }
  } } }`

/** The cues shown at a moment, of overlapping cues all of them. */
export function cuesAt(cues: Cue[], ms: number): Cue[] {
  return cues.filter((c) => c.startMs <= ms && ms < c.endMs)
}

/** One subtitle revision on the preview. */
export interface Layer {
  revision: Revision
  /** 0 is the lowest line, and the one the others are compared with. */
  slot: number
  /** What it says at the current time, with the words that differ from layer 0 marked. */
  text: Segment[]
}

export function useAsset(fetcher?: Fetcher) {
  const asset = ref<AssetDetail | null>(null)
  const notFound = ref(false)
  const tracks = ref<Track[]>([])
  const loading = ref(false)
  const error = ref<ApiError | null>(null)

  /** Revision ids in layer order. */
  const selected = ref<string[]>([])
  const cues = reactive(new Map<string, Cue[]>())
  const cuesLoading = ref(false)
  /** Where the preview is, in milliseconds. */
  const time = ref(0)
  const highlight = ref(true)

  async function load(id: string) {
    asset.value = null
    notFound.value = false
    tracks.value = []
    selected.value = []
    cues.clear()
    error.value = null
    loading.value = true
    try {
      const data = await gql<{ asset: AssetDetail | null; subtitleTracks: Revision[] }>(
        `query AssetPage($id: ID!) { asset(id: $id) { ${ASSET} } subtitleTracks(assetId: $id, history: true) { ${REVISION} } }`,
        { id },
        fetcher,
      )
      if (!data.asset) {
        notFound.value = true
        return
      }
      asset.value = data.asset
      tracks.value = grouped(data.subtitleTracks)
      const first = tracks.value.find((t) => t.language === data.asset!.originalLanguage) ?? tracks.value[0]
      if (first) await toggle(first.revisions[0]!.id)
    } catch (e) {
      const err = e as ApiError
      // A malformed id is no asset either.
      if (err.code === 'NOT_FOUND' || err.code === 'INVALID_INPUT') notFound.value = true
      else error.value = err
    } finally {
      loading.value = false
    }
  }

  const revisions = computed(() => new Map(tracks.value.flatMap((t) => t.revisions.map((r) => [r.id, r] as const))))

  /** Shows a revision as the next layer, or takes it off. A fourth is not added. */
  async function toggle(id: string) {
    if (selected.value.includes(id)) {
      selected.value = selected.value.filter((x) => x !== id)
      return
    }
    if (selected.value.length >= MAX_LAYERS || !revisions.value.has(id)) return
    selected.value = [...selected.value, id]
    if (cues.has(id)) return
    error.value = null
    cuesLoading.value = true
    try {
      cues.set(id, await readCues(id, fetcher))
    } catch (e) {
      selected.value = selected.value.filter((x) => x !== id)
      error.value = e as ApiError
    } finally {
      cuesLoading.value = false
    }
  }

  const textAt = (id: string) => cuesAt(cues.get(id) ?? [], time.value).map(cueText).join('\n')

  const layers = computed<Layer[]>(() => {
    const texts = selected.value.map(textAt)
    const base = texts[0] ?? ''
    return selected.value.flatMap((id, slot) => {
      const revision = revisions.value.get(id)
      const text = texts[slot]!
      if (!revision || !cues.has(id)) return []
      const plain = [{ text, changed: false }]
      const marked = highlight.value && slot > 0 && base && text ? wordDiff([base], [text]).newer[0]! : plain
      return [{ revision, slot, text: text ? marked : [] }]
    })
  })

  /** The newest preview of the asset, if it has one: an encode rendition for previews with a video file. */
  const preview = computed(() => {
    const versions = asset.value?.versions.nodes ?? []
    for (const v of [...versions].reverse()) {
      const r = v.renditions.find((x) => x.kind === 'ENCODE' && x.purpose === 'preview' && x.files.some((f) => f.role === 'VIDEO'))
      if (r) return { renditionId: r.id, url: `/media/renditions/${encodeURIComponent(r.id)}` }
    }
    return null
  })

  /** How long the subtitles run, for a timeline when there is no preview to play. */
  const duration = computed(() => {
    let end = 0
    for (const id of selected.value) for (const c of cues.get(id) ?? []) end = Math.max(end, c.endMs)
    return end
  })

  return { asset, notFound, tracks, loading, error, selected, cuesLoading, time, highlight, layers, preview, duration, load, toggle }
}
