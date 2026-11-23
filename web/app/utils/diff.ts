export interface DiffRow<T> {
  kind: 'same' | 'removed' | 'added'
  /** Of an unchanged row, the item of the newer list. */
  item: T
  /** Of an unchanged row, the item of the older list, which `key` may find equal but not identical. */
  older?: T
}

/** The table of the changed middle holds two bytes per cell: this is 32 MB. */
const MAX_CELLS = 16_000_000

/**
 * The rows that turn `older` into `newer`, in order. Two items are the same when `key` gives the
 * same string. A middle too large to compare (see `maxCells`) comes back as all removed, then
 * all added, which is still a correct diff.
 */
export function diff<T>(older: T[], newer: T[], key: (item: T) => string, maxCells = MAX_CELLS): DiffRow<T>[] {
  const a = older.map(key)
  const b = newer.map(key)

  // Revisions mostly share their beginning and end, so only the middle needs the table.
  let start = 0
  while (start < a.length && start < b.length && a[start] === b[start]) start++
  let endA = a.length
  let endB = b.length
  while (endA > start && endB > start && a[endA - 1] === b[endB - 1]) {
    endA--
    endB--
  }

  const rows: DiffRow<T>[] = []
  const push = (kind: DiffRow<T>['kind'], item: T) => rows.push({ kind, item })
  const same = (i: number, j: number) => rows.push({ kind: 'same', item: newer[j]!, older: older[i]! })
  for (let j = 0; j < start; j++) same(j, j)

  const n = endA - start
  const m = endB - start
  if (n * m > maxCells) {
    for (let i = start; i < endA; i++) push('removed', older[i]!)
    for (let j = start; j < endB; j++) push('added', newer[j]!)
  } else {
    // lcs[i][j]: how many items the rests a[i..] and b[j..] have in common, in order.
    const width = m + 1
    const lcs = new Uint16Array((n + 1) * width)
    for (let i = n - 1; i >= 0; i--) {
      for (let j = m - 1; j >= 0; j--) {
        lcs[i * width + j] =
          a[start + i] === b[start + j]
            ? lcs[(i + 1) * width + j + 1]! + 1
            : Math.max(lcs[(i + 1) * width + j]!, lcs[i * width + j + 1]!)
      }
    }
    let i = 0
    let j = 0
    while (i < n && j < m) {
      if (a[start + i] === b[start + j]) {
        same(start + i, start + j)
        i++
        j++
      } else if (lcs[(i + 1) * width + j]! >= lcs[i * width + j + 1]!) {
        push('removed', older[start + i++]!)
      } else {
        push('added', newer[start + j++]!)
      }
    }
    while (i < n) push('removed', older[start + i++]!)
    while (j < m) push('added', newer[start + j++]!)
  }

  for (let j = endB; j < newer.length; j++) same(j - endB + endA, j)
  return rows
}

export type Hunk<T> = { kind: 'same'; older: T; newer: T } | { kind: 'change'; removed: T[]; added: T[] }

/** The rows of a diff with each run of changed rows gathered into one hunk. */
export function hunks<T>(rows: DiffRow<T>[]): Hunk<T>[] {
  const out: Hunk<T>[] = []
  for (const r of rows) {
    if (r.kind === 'same') {
      out.push({ kind: 'same', older: r.older!, newer: r.item })
      continue
    }
    let last = out[out.length - 1]
    if (last?.kind !== 'change') out.push((last = { kind: 'change', removed: [], added: [] }))
    last[r.kind].push(r.item)
  }
  return out
}

/** Words, single punctuation marks and runs of whitespace, so that "si" and "si:" differ by the colon alone. */
export function tokens(text: string): string[] {
  return text.match(/\s+|[\p{L}\p{N}]+|[^\s\p{L}\p{N}]/gu) ?? []
}

export interface Segment {
  text: string
  changed: boolean
}

const isSpace = (s: string) => /^\s/.test(s)

/**
 * The word-level difference between two runs of texts (the cues of one hunk), as segments per
 * text. The words of a run are compared as one sequence, so a word that moved into the next cue
 * is not a change. Whitespace is compared as one space and never marked by itself: re-wrapping a
 * line is not a change of wording.
 */
export function wordDiff(older: string[], newer: string[]): { older: Segment[][]; newer: Segment[][] } {
  const flat = (texts: string[]) => texts.flatMap((t, at) => tokens(t).map((text) => ({ text, at })))
  const raw = { older: older.map((): Segment[] => []), newer: newer.map((): Segment[] => []) }
  for (const r of diff(flat(older), flat(newer), (t) => (isSpace(t.text) ? ' ' : t.text))) {
    if (r.kind === 'same') {
      raw.older[r.older!.at]!.push({ text: r.older!.text, changed: false })
      raw.newer[r.item.at]!.push({ text: r.item.text, changed: false })
    } else {
      const t = r.item
      raw[r.kind === 'removed' ? 'older' : 'newer'][t.at]!.push({ text: t.text, changed: !isSpace(t.text) })
    }
  }
  return { older: raw.older.map(merged), newer: raw.newer.map(merged) }
}

/** Adjacent segments of the same kind joined; whitespace between two changes joins them into one. */
function merged(segs: Segment[]): Segment[] {
  const out: Segment[] = []
  segs.forEach((s, i) => {
    const changed = s.changed || (isSpace(s.text) && !!segs[i - 1]?.changed && !!segs[i + 1]?.changed)
    const last = out[out.length - 1]
    if (last?.changed === changed) last.text += s.text
    else out.push({ text: s.text, changed })
  })
  return out
}
