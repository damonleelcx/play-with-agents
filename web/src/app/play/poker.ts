// Card parsing and a compact hand evaluator. The server is authoritative for
// real tables; the evaluator only powers mock mode.

export const RANKS = '23456789TJQKA'
export const SUITS = 'shdc'
export const SUIT_GLYPH: Record<string, string> = { s: '♠', h: '♥', d: '♦', c: '♣' }
export const RANK_LABEL: Record<string, string> = { T: '10', J: 'J', Q: 'Q', K: 'K', A: 'A' }

export function parseCard(c: string) {
  const r = c[0]
  const s = c[1]
  return { rank: r, suit: s, label: RANK_LABEL[r] || r, glyph: SUIT_GLYPH[s] || '?', value: RANKS.indexOf(r) + 2 }
}

const NAMES: Record<number, string> = {
  2: 'Twos', 3: 'Threes', 4: 'Fours', 5: 'Fives', 6: 'Sixes', 7: 'Sevens', 8: 'Eights', 9: 'Nines',
  10: 'Tens', 11: 'Jacks', 12: 'Queens', 13: 'Kings', 14: 'Aces',
}
const ONE: Record<number, string> = {
  2: 'Two', 3: 'Three', 4: 'Four', 5: 'Five', 6: 'Six', 7: 'Seven', 8: 'Eight', 9: 'Nine',
  10: 'Ten', 11: 'Jack', 12: 'Queen', 13: 'King', 14: 'Ace',
}

function straightHigh(vals: number[]): number {
  const set = new Set(vals)
  if (set.has(14)) set.add(1)
  for (let hi = 14; hi >= 5; hi--) {
    let ok = true
    for (let k = 0; k < 5; k++) if (!set.has(hi - k)) { ok = false; break }
    if (ok) return hi
  }
  return 0
}

// evaluate returns a comparable score (category first) and an English name.
export function evaluate(cards: string[]): { score: number[]; name: string } {
  const cs = cards.map(parseCard)
  const vals = cs.map((c) => c.value).sort((a, b) => b - a)
  const bySuit: Record<string, number[]> = {}
  for (const c of cs) (bySuit[c.suit] ||= []).push(c.value)
  const counts = new Map<number, number>()
  for (const v of vals) counts.set(v, (counts.get(v) || 0) + 1)
  const groups = [...counts.entries()].sort((a, b) => b[1] - a[1] || b[0] - a[0])
  const flushSuit = Object.keys(bySuit).find((s) => bySuit[s].length >= 5)

  if (flushSuit) {
    const sf = straightHigh(bySuit[flushSuit])
    if (sf) return { score: [8, sf], name: sf === 14 ? 'Royal Flush' : `Straight Flush, ${ONE[sf]} high` }
  }
  if (groups[0]?.[1] === 4) {
    const k = vals.filter((v) => v !== groups[0][0])[0] || 0
    return { score: [7, groups[0][0], k], name: `Four of a Kind, ${NAMES[groups[0][0]]}` }
  }
  if (groups[0]?.[1] === 3 && groups.slice(1).some((g) => g[1] >= 2)) {
    const pair = groups.slice(1).find((g) => g[1] >= 2)![0]
    return { score: [6, groups[0][0], pair], name: `Full House, ${NAMES[groups[0][0]]} over ${NAMES[pair]}` }
  }
  if (flushSuit) {
    const f = bySuit[flushSuit].sort((a, b) => b - a).slice(0, 5)
    return { score: [5, ...f], name: `Flush, ${ONE[f[0]]} high` }
  }
  const st = straightHigh(vals)
  if (st) return { score: [4, st], name: `Straight, ${ONE[st]} high` }
  if (groups[0]?.[1] === 3) {
    const ks = vals.filter((v) => v !== groups[0][0]).slice(0, 2)
    return { score: [3, groups[0][0], ...ks], name: `Three of a Kind, ${NAMES[groups[0][0]]}` }
  }
  if (groups[0]?.[1] === 2 && groups[1]?.[1] === 2) {
    const [a, b] = [groups[0][0], groups[1][0]]
    const k = vals.filter((v) => v !== a && v !== b)[0] || 0
    return { score: [2, a, b, k], name: `Two Pair, ${NAMES[a]} and ${NAMES[b]}` }
  }
  if (groups[0]?.[1] === 2) {
    const ks = vals.filter((v) => v !== groups[0][0]).slice(0, 3)
    return { score: [1, groups[0][0], ...ks], name: `Pair of ${NAMES[groups[0][0]]}` }
  }
  return { score: [0, ...vals.slice(0, 5)], name: `${ONE[vals[0]] || ''} high` }
}

export function compareScore(a: number[], b: number[]) {
  for (let i = 0; i < Math.max(a.length, b.length); i++) {
    const d = (a[i] || 0) - (b[i] || 0)
    if (d) return d
  }
  return 0
}

export function freshDeck(): string[] {
  const d: string[] = []
  for (const r of RANKS) for (const s of SUITS) d.push(r + s)
  for (let i = d.length - 1; i > 0; i--) {
    const j = Math.floor(Math.random() * (i + 1))
    ;[d[i], d[j]] = [d[j], d[i]]
  }
  return d
}

export function fmtChips(n: number): string {
  if (n >= 1_000_000) return (n / 1_000_000).toFixed(n % 1_000_000 ? 1 : 0) + 'M'
  if (n >= 10_000) return (n / 1000).toFixed(n % 1000 ? 1 : 0) + 'k'
  return n.toLocaleString('en-US')
}
