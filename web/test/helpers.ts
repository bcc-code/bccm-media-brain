import { vi } from 'vitest'
import type { ServiceAccount } from '../app/composables/useServiceAccounts'
import type { Cue, Revision } from '../app/composables/useSubtitles'
import type { AssetRow } from '../app/composables/useAssets'
import type { AssetDetail } from '../app/composables/useAsset'
import type { Person } from '../app/composables/useUsers'

export function account(over: Partial<ServiceAccount> = {}): ServiceAccount {
  return {
    id: 'US-1',
    name: 'ingest',
    externalId: 'service|ingest',
    active: true,
    isAdmin: false,
    createdAt: '2026-11-11T09:30:00Z',
    ...over,
  }
}

export function json(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), { status, headers: { 'Content-Type': 'application/json' } })
}

export function apiError(message: string, extensions: Record<string, string>): Response {
  return json({ errors: [{ message, extensions }], data: null })
}

/** A fetch that answers the given responses in order and records the request bodies. */
export function fakeFetch(...answers: (Response | Error)[]) {
  const sent: { query: string; variables: Record<string, unknown> }[] = []
  const fetcher = vi.fn(async (_url: unknown, init?: RequestInit) => {
    sent.push(JSON.parse(String(init?.body)))
    const next = answers.shift()
    if (!next) throw new Error('unexpected request')
    if (next instanceof Error) throw next
    return next
  })
  return { fetcher: fetcher as unknown as typeof fetch, sent }
}

export function revision(over: Partial<Revision> = {}): Revision {
  return {
    id: 'ST-1',
    renditionId: 'RE-nb',
    language: 'nb',
    kind: 'SUBTITLE',
    origin: 'IMPORT',
    source: null,
    revision: 1,
    createdBy: null,
    createdAt: '2026-11-20T08:00:00Z',
    ...over,
  }
}

export function cue(seq: number, text: string, over: Partial<Cue> = {}): Cue {
  return { seq, startMs: seq * 1000, endMs: seq * 1000 + 900, text, speaker: null, ...over }
}

/** The answer to one page of a revision's cues. */
export function cuePage(cues: Cue[], endCursor: string | null = null): Response {
  return json({ data: { subtitleTrack: { cues: { nodes: cues, pageInfo: { hasNextPage: endCursor !== null, endCursor } } } } })
}

export function person(over: Partial<Person> = {}): Person {
  return {
    id: 'US-1',
    email: 'ann@example.test',
    name: 'Ann',
    active: true,
    isAdmin: false,
    createdAt: '2026-11-11T09:30:00Z',
    groups: [],
    ...over,
  }
}

export function assetRow(over: Partial<AssetRow> = {}): AssetRow {
  return {
    id: 'AS-1',
    title: 'Opening',
    legacyIds: ['VX-1'],
    createdAt: '2026-11-12T10:00:00Z',
    type: { name: 'Clip' },
    ...over,
  }
}

/** One page of a connection, under `field`. `extra` goes beside it in data. */
export function page<T>(field: string, nodes: T[], endCursor: string | null = null, extra: Record<string, unknown> = {}): Response {
  return json({ data: { ...extra, [field]: { nodes, pageInfo: { hasNextPage: endCursor !== null, endCursor } } } })
}

export function assetDetail(over: Partial<AssetDetail> = {}): AssetDetail {
  return {
    id: 'AS-1',
    revision: 4,
    title: 'Autumn conference – opening',
    description: 'Opening session, main hall.',
    capturedAt: '2026-10-04T11:02:00Z',
    camera: null,
    reel: null,
    metadata: { season: 3, vidispine: { itemId: 'VX-1' } },
    legacyIds: ['VX-1'],
    originalLanguage: 'nb',
    createdAt: '2026-10-04T13:40:00Z',
    updatedAt: '2026-11-24T14:08:00Z',
    myLevel: 'EDIT',
    type: { name: 'Programme' },
    projects: [
      { role: 'SOURCE', linkOrigin: 'FOLDER', project: { id: 'PJ-1', code: 'AC26', name: 'Autumn conference' } },
      { role: 'DELIVERABLE', linkOrigin: 'MANUAL', project: null },
    ],
    versions: {
      nodes: [
        { id: 'AV-1', number: 1, label: 'Rough cut', status: 'DRAFT', renditions: [] },
        {
          id: 'AV-2',
          number: 2,
          label: 'Vidispine original',
          status: 'FINAL',
          renditions: [
            { id: 'RE-m', kind: 'MASTER', language: null, purpose: null, files: [] },
            {
              id: 'RE-p',
              kind: 'ENCODE',
              language: null,
              purpose: 'preview',
              files: [{ role: 'VIDEO', file: { id: 'FI-1', mimeType: 'video/mp4', durationMs: 60000, width: 640, height: 360 } }],
            },
          ],
        },
      ],
    },
    ...over,
  }
}

/** An asset page's subtitles: three revisions of a Norwegian track and one English. */
export const history = [
  revision({ id: 'ST-3', renditionId: 'RE-nb', revision: 3, origin: 'MANUAL' }),
  revision({ id: 'ST-2', renditionId: 'RE-nb', revision: 2, origin: 'ASR', source: 'whisper-large-v3' }),
  revision({ id: 'ST-1', renditionId: 'RE-nb', revision: 1, origin: 'ASR', source: 'nb-whisper-large' }),
  revision({ id: 'ST-5', renditionId: 'RE-en', language: 'en', revision: 1 }),
]

/** The cues of each revision: the same speech, heard differently. */
export const said: Record<string, ReturnType<typeof cue>[]> = {
  'ST-3': [cue(1, 'Velkommen alle sammen'), cue(3, 'Korøvelsen begynner nå')],
  'ST-2': [cue(1, 'Velkommen alle sammen'), cue(3, 'Kor øvelsen begynner nå')],
  'ST-1': [cue(1, 'Velkommen alle'), cue(2, 'og hei', { startMs: 1500, endMs: 2500 })],
  'ST-5': [cue(1, 'Welcome everyone', { speaker: 'Host' })],
}

export const loaded = (over: Partial<AssetDetail> = {}) => json({ data: { asset: assetDetail(over), subtitleTracks: history } })

