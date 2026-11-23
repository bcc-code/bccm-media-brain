import { describe, expect, it } from 'vitest'
import { diff } from '../app/utils/diff'

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
    expect(rows).toEqual([{ kind: 'same', item: { k: 'a', v: 2 } }])
  })

  it('gives a middle too large to compare as removed, then added', () => {
    expect(show('axbyc', 'apbqc', 8)).toBe('a -x -b -y +p +b +q c')
  })
})
