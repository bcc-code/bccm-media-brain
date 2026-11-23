import { describe, expect, it } from 'vitest'
import { diff, hunks, tokens, wordDiff } from '../app/utils/diff'

/** "a -b +c": unchanged a, removed b, added c. */
function show(older: string, newer: string, maxCells?: number): string {
  const sign = { same: '', removed: '-', added: '+' }
  return diff([...older], [...newer], (c) => c, maxCells)
    .map((r) => sign[r.kind] + r.item)
    .join(' ')
}

describe('diff', () => {
  it.each([
    ['', '', ''],
    ['abc', 'abc', 'a b c'],
    ['', 'ab', '+a +b'],
    ['ab', '', '-a -b'],
    ['abc', 'axc', 'a -b +x c'],
    ['abcd', 'ad', 'a -b -c d'],
    ['ad', 'abcd', 'a +b +c d'],
    ['abc', 'bcd', '-a b c +d'],
    ['axbyc', 'apbqc', 'a -x +p b -y +q c'],
    ['abcabba', 'cbabac', '-a -b c -a b +a b a +c'],
    ['aa', 'a', 'a -a'],
  ])('turns %j into %j', (older, newer, want) => {
    expect(show(older, newer)).toBe(want)
  })

  it('takes an unchanged item from the newer list', () => {
    const rows = diff([{ k: 'a', v: 1 }], [{ k: 'a', v: 2 }], (x) => x.k)
    expect(rows).toEqual([{ kind: 'same', item: { k: 'a', v: 2 }, older: { k: 'a', v: 1 } }])
  })

  it('gives a middle too large to compare as removed, then added', () => {
    expect(show('axbyc', 'apbqc', 8)).toBe('a -x -b -y +p +b +q c')
  })

  it('pairs each unchanged item with the older one at its place', () => {
    const older = ['a', 'x', 'b', 'c'].map((k, v) => ({ k, v }))
    const newer = ['a', 'b', 'y', 'c'].map((k, v) => ({ k, v: v + 10 }))
    const same = diff(older, newer, (x) => x.k).filter((r) => r.kind === 'same')
    expect(same.map((r) => [r.older?.v, r.item.v])).toEqual([[0, 10], [2, 11], [3, 13]])
  })
})

/** Hunks as text: "a", "-x+y", "-x", "+y". */
function showHunks(older: string, newer: string): string[] {
  return hunks(diff([...older], [...newer], (c) => c)).map((h) =>
    h.kind === 'same' ? h.newer : h.removed.map((x) => '-' + x).join('') + h.added.map((x) => '+' + x).join(''),
  )
}

describe('hunks', () => {
  it.each([
    ['', '', []],
    ['abc', 'abc', ['a', 'b', 'c']],
    ['abc', 'axc', ['a', '-b+x', 'c']],
    ['xbc', 'ybc', ['-x+y', 'b', 'c']],
    ['abx', 'aby', ['a', 'b', '-x+y']],
    ['abcd', 'ad', ['a', '-b-c', 'd']],
    ['ad', 'abcd', ['a', '+b+c', 'd']],
    ['axbyc', 'apbqc', ['a', '-x+p', 'b', '-y+q', 'c']],
  ])('groups %j → %j', (older, newer, want) => {
    expect(showHunks(older, newer)).toEqual(want)
  })
})

describe('tokens', () => {
  it('splits words, punctuation and whitespace', () => {
    expect(tokens('Og da kan vi si: Se, alt\ner nytt.')).toEqual([
      'Og', ' ', 'da', ' ', 'kan', ' ', 'vi', ' ', 'si', ':', ' ', 'Se', ',', ' ', 'alt', '\n', 'er', ' ', 'nytt', '.',
    ])
    expect(tokens('får lov 2026')).toEqual(['får', ' ', 'lov', ' ', '2026'])
    expect(tokens('')).toEqual([])
  })
})

/** Each text with its changed segments in brackets. */
function marked(older: string[], newer: string[]) {
  const show = (segs: { text: string; changed: boolean }[][]) => segs.map((s) => s.map((x) => (x.changed ? `[${x.text}]` : x.text)).join(''))
  const d = wordDiff(older, newer)
  return { older: show(d.older), newer: show(d.newer) }
}

describe('wordDiff', () => {
  it('marks a changed word', () => {
    expect(marked(['Johan Oskar Smith'], ['Johan Oscar Smith'])).toEqual({ older: ['Johan [Oskar] Smith'], newer: ['Johan [Oscar] Smith'] })
  })

  it('marks two changed words next to each other as one', () => {
    expect(marked(['Alt det at jeg'], ['Alt dette jeg'])).toEqual({ older: ['Alt [det at] jeg'], newer: ['Alt [dette] jeg'] })
  })

  it('marks punctuation by itself', () => {
    expect(marked(['vi si alt'], ['vi si: alt'])).toEqual({ older: ['vi si alt'], newer: ['vi si[:] alt'] })
  })

  it('does not mark a cue split in two, or a line wrapped differently', () => {
    expect(marked(['nye liv\nfram.'], ['nye', 'liv fram.'])).toEqual({ older: ['nye liv\nfram.'], newer: ['nye', 'liv fram.'] })
  })

  it('marks the words of a merged cue that changed', () => {
    expect(marked(['Og da kan vi si alt.', 'Det gamle.'], ['Og da kan vi si: Se, alt.\ndet gamle.'])).toEqual({
      older: ['Og da kan vi si alt.', '[Det] gamle.'],
      newer: ['Og da kan vi si[: Se,] alt.\n[det] gamle.'],
    })
  })

  it('marks everything when one side is empty', () => {
    expect(marked([], ['ny tekst'])).toEqual({ older: [], newer: ['[ny tekst]'] })
    expect(marked(['', 'til å'], [])).toEqual({ older: ['', '[til å]'], newer: [] })
  })
})
