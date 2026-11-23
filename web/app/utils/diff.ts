export interface DiffRow<T> {
  kind: 'same' | 'removed' | 'added'
  /** Of an unchanged row, the item of the newer list. */
  item: T
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
  for (let j = 0; j < start; j++) push('same', newer[j]!)

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
        push('same', newer[start + j]!)
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

  for (let j = endB; j < newer.length; j++) push('same', newer[j]!)
  return rows
}
