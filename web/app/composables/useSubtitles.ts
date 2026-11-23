import { computed, ref } from 'vue'
import { diff, hunks, wordDiff, type DiffRow, type Segment } from '../utils/diff'
import { ApiError, gql, type Fetcher } from '../utils/graphql'

export interface SubtitleAsset {
  id: string
  title: string
  legacyIds: string[]
}

/** One `SubtitleTrack` of the API: a revision of a rendition's subtitles. */
export interface Revision {
  id: string
  renditionId: string
  language: string | null
  kind: string
  origin: string
  model: string | null
  revision: number
  createdBy: string | null
  createdAt: string
}

/** The revisions of one subtitle rendition, newest first. */
export interface Track {
  renditionId: string
  language: string | null
  kind: string
  revisions: Revision[]
}

export interface Cue {
  seq: number
  startMs: number
  endMs: number
  text: string
  speaker: string | null
}

const ASSET = 'id title legacyIds'
const REVISION = 'id renditionId language kind origin model revision createdBy createdAt'
/** The API's largest page of cues. */
const CUE_PAGE = 5000

/** One line of a side-by-side comparison; a side without a cue is null. */
export interface Pair {
  kind: 'same' | 'change'
  older: { cue: Cue; text: Segment[] } | null
  newer: { cue: Cue; text: Segment[] } | null
}

export function cueText(c: Cue): string {
  return c.speaker ? `${c.speaker}: ${c.text}` : c.text
}

/**
 * The cues of a hunk of changes in groups that overlap in time, in order: the cues of both sides
 * whose times chain into one stretch. A group with one side empty is a cue the other revision has
 * nothing for at that time.
 */
export function overlapping(removed: Cue[], added: Cue[]): { removed: Cue[]; added: Cue[] }[] {
  const all = [...removed.map((cue) => ({ cue, side: 'removed' as const })), ...added.map((cue) => ({ cue, side: 'added' as const }))]
  // Stable, so cues that start together keep their revision's order.
  all.sort((x, y) => x.cue.startMs - y.cue.startMs)
  const groups: { removed: Cue[]; added: Cue[] }[] = []
  let end = -Infinity
  for (const { cue, side } of all) {
    if (cue.startMs >= end) groups.push({ removed: [], added: [] })
    groups[groups.length - 1]![side].push(cue)
    end = Math.max(end, cue.endMs)
  }
  return groups
}

/**
 * The rows of a diff side by side. Changed cues are lined up by time: those that overlap share
 * rows and have their changed words marked. Words are compared per group, not per hunk, since a
 * hunk can span a whole transcript, too long to compare word by word.
 */
export function sideBySide(rows: DiffRow<Cue>[]): Pair[] {
  const out: Pair[] = []
  const plain = (cue: Cue) => ({ cue, text: [{ text: cueText(cue), changed: false }] })
  for (const h of hunks(rows)) {
    if (h.kind === 'same') {
      out.push({ kind: 'same', older: plain(h.older), newer: plain(h.newer) })
      continue
    }
    for (const g of overlapping(h.removed, h.added)) {
      // Alone at its time, a cue has nothing to compare with: its tint says enough.
      const words =
        g.removed.length && g.added.length
          ? wordDiff(g.removed.map(cueText), g.added.map(cueText))
          : { older: g.removed.map((c) => plain(c).text), newer: g.added.map((c) => plain(c).text) }
      for (let i = 0; i < Math.max(g.removed.length, g.added.length); i++) {
        const side = (cues: Cue[], text: Segment[][]) => (i < cues.length ? { cue: cues[i]!, text: text[i]! } : null)
        out.push({ kind: 'change', older: side(g.removed, words.older), newer: side(g.added, words.newer) })
      }
    }
  }
  return out
}

export function timecode(ms: number): string {
  const p = (n: number, width = 2) => String(n).padStart(width, '0')
  return `${p(Math.floor(ms / 3_600_000))}:${p(Math.floor(ms / 60_000) % 60)}:${p(Math.floor(ms / 1000) % 60)}.${p(ms % 1000, 3)}`
}

export function trackLabel(t: { language: string | null; kind: string }): string {
  return `${t.language ?? 'No language'} · ${t.kind}`
}

function grouped(revisions: Revision[]): Track[] {
  const tracks = new Map<string, Track>()
  for (const r of revisions) {
    let t = tracks.get(r.renditionId)
    if (!t) tracks.set(r.renditionId, (t = { renditionId: r.renditionId, language: r.language, kind: r.kind, revisions: [] }))
    t.revisions.push(r)
  }
  for (const t of tracks.values()) t.revisions.sort((x, y) => y.revision - x.revision)
  return [...tracks.values()].sort((x, y) => trackLabel(x).localeCompare(trackLabel(y)))
}

export function useSubtitles(fetcher?: Fetcher) {
  const asset = ref<SubtitleAsset | null>(null)
  /** Several assets can carry one legacy id; the user picks. */
  const candidates = ref<SubtitleAsset[]>([])
  const notFound = ref(false)
  const tracks = ref<Track[]>([])
  const loading = ref(false)
  const error = ref<ApiError | null>(null)

  const track = ref<Track | null>(null)
  const shown = ref<Revision | null>(null)
  /** The revision `shown` is compared with, if any. */
  const base = ref<Revision | null>(null)
  const shownCues = ref<Cue[]>([])
  const baseCues = ref<Cue[] | null>(null)
  const cuesLoading = ref(false)
  const ignoreTiming = ref(false)

  // A revision never changes, so its cues are read once.
  const cache = new Map<string, Cue[]>()
  let fills = 0

  async function find(input: string): Promise<SubtitleAsset[]> {
    if (/^AS-/i.test(input)) {
      const data = await gql<{ asset: SubtitleAsset | null }>(
        `query SubtitleAsset($id: ID!) { asset(id: $id) { ${ASSET} } }`,
        { id: input.toUpperCase() },
        fetcher,
      )
      return data.asset ? [data.asset] : []
    }
    const data = await gql<{ assets: { nodes: SubtitleAsset[] } }>(
      `query SubtitleAssetsByLegacyId($legacyId: String!) {
        assets(filter: { legacyId: $legacyId }, first: 20) { nodes { ${ASSET} } }
      }`,
      { legacyId: input },
      fetcher,
    )
    return data.assets.nodes
  }

  /** Opens the asset with this id, or the one that has it as a legacy id. */
  async function open(input: string) {
    const wanted = input.trim()
    if (!wanted) return
    asset.value = null
    candidates.value = []
    notFound.value = false
    tracks.value = []
    track.value = null
    error.value = null
    loading.value = true
    try {
      const found = await find(wanted)
      if (found.length === 1) await choose(found[0]!)
      else if (found.length === 0) notFound.value = true
      else candidates.value = found
    } catch (e) {
      error.value = e as ApiError
    } finally {
      loading.value = false
    }
  }

  async function choose(a: SubtitleAsset) {
    error.value = null
    loading.value = true
    try {
      const data = await gql<{ subtitleTracks: Revision[] }>(
        `query SubtitleHistory($assetId: ID!) { subtitleTracks(assetId: $assetId, history: true) { ${REVISION} } }`,
        { assetId: a.id },
        fetcher,
      )
      asset.value = a
      tracks.value = grouped(data.subtitleTracks)
      if (tracks.value[0]) await selectTrack(tracks.value[0].renditionId)
    } catch (e) {
      error.value = e as ApiError
    } finally {
      loading.value = false
    }
  }

  async function cuesOf(id: string): Promise<Cue[]> {
    const cached = cache.get(id)
    if (cached) return cached
    const cues: Cue[] = []
    let after: string | null = null
    do {
      const data: {
        subtitleTrack: { cues: { nodes: Cue[]; pageInfo: { hasNextPage: boolean; endCursor: string | null } } } | null
      } = await gql(
        `query SubtitleCues($id: ID!, $first: Int!, $after: String) {
          subtitleTrack(id: $id) {
            cues(first: $first, after: $after) { nodes { seq startMs endMs text speaker } pageInfo { hasNextPage endCursor } }
          }
        }`,
        { id, first: CUE_PAGE, after },
        fetcher,
      )
      if (!data.subtitleTrack) throw new ApiError('This revision no longer exists.', 'NOT_FOUND')
      cues.push(...data.subtitleTrack.cues.nodes)
      const page = data.subtitleTrack.cues.pageInfo
      after = page.hasNextPage ? page.endCursor : null
    } while (after)
    cache.set(id, cues)
    return cues
  }

  /** Reads the cues of the selection. A selection made meanwhile wins. */
  async function fill() {
    const mine = ++fills
    const s = shown.value
    const b = base.value
    error.value = null
    cuesLoading.value = true
    try {
      const [sc, bc] = await Promise.all([s ? cuesOf(s.id) : [], b ? cuesOf(b.id) : null])
      if (mine !== fills) return
      shownCues.value = sc
      baseCues.value = bc
    } catch (e) {
      if (mine === fills) error.value = e as ApiError
    } finally {
      if (mine === fills) cuesLoading.value = false
    }
  }

  function selectTrack(renditionId: string) {
    const t = tracks.value.find((x) => x.renditionId === renditionId)
    if (!t) return Promise.resolve()
    track.value = t
    shown.value = t.revisions[0]!
    base.value = null
    return fill()
  }

  function show(id: string) {
    const r = track.value?.revisions.find((x) => x.id === id)
    if (!r) return Promise.resolve()
    shown.value = r
    if (base.value?.id === id) base.value = null
    return fill()
  }

  /** Compares the shown revision with another of its track; null or '' ends the comparison. */
  function compare(id: string | null) {
    base.value = track.value?.revisions.find((x) => x.id === id && x.id !== shown.value?.id) ?? null
    return fill()
  }

  /** From the older revision to the newer, whichever of the two is the one shown. */
  const comparison = computed<{ from: Revision; to: Revision; rows: DiffRow<Cue>[]; pairs: Pair[]; added: number; removed: number } | null>(() => {
    const s = shown.value
    const b = base.value
    if (!s || !b || !baseCues.value) return null
    const forward = b.revision < s.revision
    const key = ignoreTiming.value
      ? (c: Cue) => JSON.stringify([c.text, c.speaker])
      : (c: Cue) => JSON.stringify([c.startMs, c.endMs, c.text, c.speaker])
    const rows = forward ? diff(baseCues.value, shownCues.value, key) : diff(shownCues.value, baseCues.value, key)
    return {
      from: forward ? b : s,
      to: forward ? s : b,
      rows,
      pairs: sideBySide(rows),
      added: rows.filter((r) => r.kind === 'added').length,
      removed: rows.filter((r) => r.kind === 'removed').length,
    }
  })

  return {
    asset,
    candidates,
    notFound,
    tracks,
    loading,
    error,
    track,
    shown,
    base,
    shownCues,
    cuesLoading,
    ignoreTiming,
    comparison,
    open,
    choose,
    selectTrack,
    show,
    compare,
  }
}
