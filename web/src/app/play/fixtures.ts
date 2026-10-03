// Mock mode: realistic tables that run entirely in the browser, so the table
// UI can be exercised without the backend (/app/table/demo?mock=holdem).
// Agents in mock mode play a simple persona-weighted strategy.
import type {
  Agent,
  BoardCell,
  BoardData,
  ChatLine,
  HoldemData,
  HoldemPlayer,
  LogEntry,
  Move,
  MoveSpec,
  Outcome,
  SeatInfo,
  TableStatus,
  TableView,
} from '../../lib/playApi'
import { compareScore, evaluate, freshDeck } from './poker'

export const ROSTER: Agent[] = [
  { id: 'aoi', name: 'Aoi', name_zh: '葵', title: 'The host', title_zh: '主持人', bio: 'Balanced and adaptive. Warm, teasing, very competitive.', bio_zh: '均衡、善变。温暖、爱逗人，好胜心极强。', avatar: '/play/agents/aoi.webp', style: { tightness: 0.55, aggression: 0.6, bluff: 0.45, talk: 0.7 } },
  { id: 'ren', name: 'Ren', name_zh: '蓮', title: 'The strategist', title_zh: '策略家', bio: 'Tight-aggressive. Calm, few words, dry humour.', bio_zh: '紧凶型。冷静、话少、冷幽默。', avatar: '/play/agents/ren.webp', style: { tightness: 0.8, aggression: 0.75, bluff: 0.25, talk: 0.25 } },
  { id: 'mika', name: 'Mika', name_zh: '美香', title: 'The showoff', title_zh: '表演家', bio: 'Loose-aggressive and loves an all-in. Loud and fearless.', bio_zh: '松凶型，最爱全下。大胆又张扬。', avatar: '/play/agents/mika.webp', style: { tightness: 0.25, aggression: 0.9, bluff: 0.8, talk: 0.9 } },
  { id: 'bram', name: 'Captain Bram', name_zh: '布拉姆船长', title: 'The old sailor', title_zh: '老水手', bio: 'Loose-passive, calls a lot. Never folds a good yarn.', bio_zh: '松弱型，爱跟注。故事永远讲不完。', avatar: '/play/agents/bram.webp', style: { tightness: 0.2, aggression: 0.2, bluff: 0.2, talk: 0.85 } },
  { id: 'nova', name: 'Nova', name_zh: '诺瓦', title: 'The calculator', title_zh: '计算机', bio: 'Balanced and math-driven. Quotes odds, tells terrible jokes.', bio_zh: '均衡、靠数学。爱报概率，爱讲冷笑话。', avatar: '/play/agents/nova.webp', style: { tightness: 0.55, aggression: 0.5, bluff: 0.35, talk: 0.6 } },
  { id: 'lin', name: 'Lin', name_zh: '琳', title: 'The prodigy', title_zh: '天才少女', bio: 'Tight-passive, rarely bluffs. Shy, polite, quietly deadly.', bio_zh: '紧弱型，很少诈唬。害羞有礼，却出手致命。', avatar: '/play/agents/lin.webp', style: { tightness: 0.85, aggression: 0.3, bluff: 0.1, talk: 0.3 } },
]
const agentById = (id: string) => ROSTER.find((a) => a.id === id)!

const BANTER: Record<string, string[]> = {
  aoi: ['Ooh, brave. I like it.', 'You know I can read you, right?', 'Fine, fine. Show me what you’ve got.', 'Hehe. Nice hand!'],
  ren: ['Hm.', 'Predictable.', 'Fair.', 'Again.'],
  mika: ['ALL the chips. All of them!', 'Scared yet?', 'Woo! That’s how it’s done!', 'Boring! Somebody raise!'],
  bram: ['Reminds me of a storm off Cape Horn…', 'Aye, I’ll see that.', 'A sailor never folds on a full moon.', 'Har! Well played, lad.'],
  nova: ['Pot odds say 23.4%. I say yes.', 'Why did the robot fold? Bad connection.', 'Calculating… calling.', 'Expected value: positive. Probably.'],
  lin: ['Oh — sorry.', 'Um. Call, please.', 'Good game.', '…'],
}

function seatOf(kind: 'me' | string, seat: number): SeatInfo {
  if (kind === 'me') return { seat, kind: 'human', name: 'Damon', avatar: '', is_me: true }
  if (kind === 'friend') return { seat, kind: 'human', name: 'Yuki', avatar: '' }
  const a = agentById(kind)
  return { seat, kind: 'agent', name: a.name, avatar: a.avatar, agent_id: a.id }
}

export interface MockGame {
  view(): TableView
  apply(seat: number, move: Move): void // throws Error(message) when illegal
  pending(): number | null // ms until step() should run, or null
  step(): void
  chat(text: string): void
  setSeat?(seat: number, kind: 'agent' | 'open', agentId?: string): void
  start?(): void
  rematch?(): void
  back?(): void
}

const iso = (ms = 0) => new Date(Date.now() + ms).toISOString()

abstract class MockBase implements MockGame {
  version = 1
  seq = 0
  log: LogEntry[] = []
  chatLines: ChatLine[] = []
  status: TableStatus = 'playing'
  outcome: Outcome | null = null
  deadline: string | null = null
  turnSeconds = 30
  me = 0
  paused = false
  code = 'AOI7K2'
  abstract seats: SeatInfo[]
  abstract name: string
  abstract game: { id: string; name: string; kind: string }
  abstract viewKind: 'holdem' | 'board'
  abstract data(): any
  abstract statusText(): string
  abstract toMove(): number[]
  abstract legalFor(seat: number): MoveSpec[]
  abstract applyMove(seat: number, move: Move): void
  abstract pending(): number | null
  abstract step(): void

  name_(seat: number) {
    return this.seats[seat]?.name || `Seat ${seat + 1}`
  }
  addLog(seat: number, text: string, type = 'move') {
    this.log.push({ seq: ++this.seq, type, seat, text, at: iso() })
    if (this.log.length > 60) this.log.shift()
  }
  addChat(seat: number, text: string) {
    const s = this.seats[seat]
    this.chatLines.push({ id: 'c' + Math.random().toString(36).slice(2), seat, name: s.name, avatar: s.avatar, text, at: iso(), agent: s.kind === 'agent' })
    if (this.chatLines.length > 60) this.chatLines.shift()
  }
  maybeBanter(seat: number, p = 0.25) {
    const s = this.seats[seat]
    if (s?.kind !== 'agent' || Math.random() > p) return
    const lines = BANTER[s.agent_id || ''] || []
    if (lines.length) this.addChat(seat, lines[Math.floor(Math.random() * lines.length)])
  }
  touch() {
    this.version++
    const tm = this.toMove()
    const awayTurn = tm.length > 0 && tm.every((s) => this.seats[s]?.away)
    this.deadline = this.status === 'playing' && tm.length && this.turnSeconds ? iso(awayTurn ? 1500 : this.turnSeconds * 1000) : null
  }
  meAway() {
    return !!this.seats[this.me]?.away
  }
  back() {
    const s = this.seats[this.me]
    if (s) delete s.away
    this.paused = false
    this.touch()
  }
  apply(seat: number, move: Move) {
    if (this.status !== 'playing') throw new Error('The game is not running.')
    if (!this.toMove().includes(seat)) throw new Error('It is not your turn.')
    this.applyMove(seat, move)
    if (seat === this.me && this.seats[seat]) delete this.seats[seat].away
    this.paused = false
    this.touch()
  }
  chat(text: string) {
    this.addChat(this.me, text)
    const m = /@(\w+)/.exec(text)
    const target = m ? this.seats.find((s) => s.agent_id === m[1].toLowerCase()) : this.seats.find((s) => s.kind === 'agent')
    if (target) setTimeout(() => { this.maybeBanter(target.seat, 1); this.version++ }, 600)
    this.version++
  }
  view(): TableView {
    const tm = this.status === 'playing' ? this.toMove() : []
    return {
      id: 'demo',
      name: this.name,
      code: this.code,
      game: this.game,
      status: this.status,
      host_id: 'me',
      is_host: true,
      my_seat: this.me,
      version: this.version,
      seats: this.seats.map((s) => ({ ...s })),
      to_move: tm,
      deadline: this.paused ? null : this.deadline,
      turn_seconds: this.turnSeconds,
      legal: this.status === 'playing' && tm.includes(this.me) ? this.legalFor(this.me) : [],
      view: { kind: this.viewKind, data: this.data(), status: this.statusText() },
      log: this.log.slice(),
      chat: this.chatLines.slice(),
      outcome: this.outcome,
      paused: this.paused,
    }
  }
}

// ── Texas Hold'em ──────────────────────────────────────────────────────────

class HoldemMock extends MockBase {
  name = 'Friday night hold’em'
  game = { id: 'holdem', name: "Texas Hold'em", kind: 'builtin' }
  viewKind = 'holdem' as const
  seats: SeatInfo[]
  d: HoldemData
  hole: Record<number, string[]> = {}
  deck: string[] = []
  acted = new Set<number>()
  holdShowdownMs = 5200
  constructor(seats: SeatInfo[], d: HoldemData) {
    super()
    this.seats = seats
    this.d = d
  }
  p(seat: number) {
    return this.d.players[seat]
  }
  toMove() {
    return this.d.to_act >= 0 ? [this.d.to_act] : []
  }
  statusText() {
    const d = this.d
    if (d.street === 'showdown' || d.street === 'over') return 'Hand over'
    if (d.to_act >= 0) return `${this.name_(d.to_act)} to act`
    return ''
  }
  data(): HoldemData {
    const d = this.d
    const showdown = d.street === 'showdown'
    return {
      ...d,
      board: d.board.slice(),
      pots: d.pots.map((p) => ({ ...p, eligible: p.eligible.slice() })),
      players: d.players.map((p) => {
        const mine = p.seat === this.me
        const visible = (mine && p.status !== 'out') || (showdown && p.status !== 'folded' && p.status !== 'out' && !!p.cards)
        const cards = visible ? this.hole[p.seat] || null : null
        let hand_name = ''
        if (cards && p.status !== 'folded' && (mine || showdown)) hand_name = evaluate([...cards, ...d.board]).name
        return { ...p, cards, hand_name }
      }),
      last_hand: d.last_hand && JSON.parse(JSON.stringify(d.last_hand)),
    }
  }
  legalFor(seat: number): MoveSpec[] {
    const d = this.d
    const p = this.p(seat)
    if (!p || p.status !== 'active' || d.to_act !== seat) return []
    const toCall = d.current_bet - p.bet
    const maxTo = p.bet + p.stack
    const out: MoveSpec[] = []
    if (toCall > 0) out.push({ type: 'fold', label: 'Fold' })
    if (toCall <= 0) out.push({ type: 'check', label: 'Check' })
    else if (p.stack > toCall) out.push({ type: 'call', label: `Call ${toCall}` })
    const minTo = Math.min(d.current_bet === 0 ? d.big_blind : d.min_raise_to, maxTo)
    if (maxTo > d.current_bet && maxTo > minTo)
      out.push({ type: 'raise', label: d.current_bet === 0 ? 'Bet' : 'Raise to', range: { arg: 'to', min: minTo, max: maxTo, step: 1 } })
    out.push({ type: 'allin', label: `All-in ${maxTo}` })
    return out
  }
  applyMove(seat: number, move: Move) {
    const d = this.d
    const p = this.p(seat)
    const legal = this.legalFor(seat)
    if (!legal.some((l) => l.type === move.type)) throw new Error(`${move.type} is not legal now`)
    const who = this.name_(seat)
    const put = (amt: number) => {
      amt = Math.min(amt, p.stack)
      p.stack -= amt
      p.bet += amt
      p.total_bet += amt
      if (p.stack === 0) p.status = 'allin'
    }
    const raiseTo = (to: number) => {
      const prev = d.current_bet
      put(to - p.bet)
      const inc = p.bet - prev
      if (p.bet > prev) {
        if (inc >= d.min_raise_to - prev || prev === 0) d.min_raise_to = p.bet + Math.max(inc, d.big_blind)
        d.current_bet = p.bet
        this.acted = new Set([seat])
      }
    }
    switch (move.type) {
      case 'fold':
        p.status = 'folded'
        p.last_action = 'fold'
        this.addLog(seat, `${who} folds`)
        break
      case 'check':
        p.last_action = 'check'
        this.addLog(seat, `${who} checks`)
        break
      case 'call':
        put(d.current_bet - p.bet)
        p.last_action = p.status === 'allin' ? 'allin' : 'call'
        this.addLog(seat, `${who} calls ${p.bet}`)
        break
      case 'raise': {
        const r = legal.find((l) => l.type === 'raise')!.range!
        const to = Math.round(Number(move.args?.to))
        if (!Number.isFinite(to) || to < r.min || to > r.max) throw new Error(`Raise must be between ${r.min} and ${r.max}`)
        const bet = d.current_bet === 0
        raiseTo(to)
        p.last_action = p.status === 'allin' ? 'allin' : bet ? 'bet' : 'raise'
        this.addLog(seat, bet ? `${who} bets ${to}` : `${who} raises to ${to}`)
        break
      }
      case 'allin':
        raiseTo(p.bet + p.stack)
        p.last_action = 'allin'
        this.addLog(seat, `${who} is all-in for ${p.bet}`)
        break
    }
    this.acted.add(seat)
    this.advance(seat)
  }
  nextActive(from: number, pred: (p: HoldemPlayer) => boolean) {
    const n = this.d.players.length
    for (let k = 1; k <= n; k++) {
      const s = (from + k) % n
      if (pred(this.d.players[s])) return s
    }
    return -1
  }
  advance(from: number): void {
    const d = this.d
    const live = d.players.filter((p) => p.status === 'active' || p.status === 'allin')
    if (live.length === 1) return this.awardUncontested(live[0].seat)
    const nxt = this.nextActive(from, (p) => p.status === 'active' && (!this.acted.has(p.seat) || p.bet < d.current_bet))
    if (nxt >= 0) {
      d.to_act = nxt
      return
    }
    this.endStreet()
  }
  collect() {
    const d = this.d
    for (const p of d.players) p.bet = 0
    // pots from contributions, split at every all-in level
    const contrib = d.players.map((p) => p.total_bet)
    const live = d.players.filter((p) => p.status === 'active' || p.status === 'allin')
    const levels = [...new Set(live.map((p) => p.total_bet))].sort((a, b) => a - b)
    const pots: HoldemData['pots'] = []
    let prev = 0
    for (const L of levels) {
      const amount = contrib.reduce((s, c) => s + Math.max(0, Math.min(c, L) - prev), 0)
      const eligible = live.filter((p) => p.total_bet >= L).map((p) => p.seat)
      if (amount > 0) {
        const last = pots[pots.length - 1]
        if (last && last.eligible.length === eligible.length) last.amount += amount
        else pots.push({ amount, eligible })
      }
      prev = L
    }
    d.pots = pots
    d.pot_total = contrib.reduce((a, b) => a + b, 0)
  }
  endStreet(): void {
    const d = this.d
    this.collect()
    d.current_bet = 0
    d.min_raise_to = d.big_blind
    this.acted.clear()
    for (const p of d.players) if (p.status === 'active') p.last_action = ''
    const order: HoldemData['street'][] = ['preflop', 'flop', 'turn', 'river', 'showdown']
    const nextStreet = order[order.indexOf(d.street) + 1]
    if (nextStreet === 'showdown') return this.showdown()
    d.street = nextStreet
    const n = nextStreet === 'flop' ? 3 : 1
    for (let i = 0; i < n; i++) d.board.push(this.deck.shift()!)
    this.addLog(-1, `${nextStreet[0].toUpperCase() + nextStreet.slice(1)}: ${d.board.join(' ')}`, 'deal')
    const canAct = d.players.filter((p) => p.status === 'active')
    if (canAct.length <= 1) {
      d.to_act = -1
      return this.endStreet()
    }
    d.to_act = this.nextActive(d.button, (p) => p.status === 'active')
  }
  awardUncontested(seat: number): void {
    const d = this.d
    this.collect()
    const amount = d.pot_total
    d.players[seat].stack += amount
    d.last_hand = { hand_no: d.hand_no, board: d.board.slice(), winners: [{ seat, amount, hand_name: '', cards: null }], shown: [] }
    this.addLog(seat, `${this.name_(seat)} wins ${amount}`, 'win')
    this.maybeBanter(seat, 0.5)
    d.street = 'over'
    d.to_act = -1
    d.pots = []
    d.pot_total = 0
  }
  showdown(): void {
    const d = this.d
    d.street = 'showdown'
    d.to_act = -1
    const scores: Record<number, { score: number[]; name: string }> = {}
    for (const p of d.players) if (p.status === 'active' || p.status === 'allin') {
      scores[p.seat] = evaluate([...this.hole[p.seat], ...d.board])
      p.cards = this.hole[p.seat]
    }
    const won: Record<number, number> = {}
    for (const pot of d.pots) {
      let best: number[] = []
      for (const s of pot.eligible) if (!best.length || compareScore(scores[s].score, scores[best[0]].score) > 0) best = [s]
      else if (compareScore(scores[s].score, scores[best[0]].score) === 0) best.push(s)
      const share = Math.floor(pot.amount / best.length)
      best.forEach((s, i) => (won[s] = (won[s] || 0) + share + (i === 0 ? pot.amount - share * best.length : 0)))
    }
    const winners = Object.entries(won).map(([s, amount]) => ({ seat: +s, amount, hand_name: scores[+s].name, cards: this.hole[+s] }))
    for (const w of winners) {
      d.players[w.seat].stack += w.amount
      this.addLog(w.seat, `${this.name_(w.seat)} wins ${w.amount} with ${w.hand_name}`, 'win')
    }
    const shown = Object.keys(scores).map(Number).filter((s) => !won[s]).map((s) => ({ seat: s, cards: this.hole[s], hand_name: scores[s].name }))
    d.last_hand = { hand_no: d.hand_no, board: d.board.slice(), winners, shown }
    winners.forEach((w) => this.maybeBanter(w.seat, 0.6))
  }
  nextHand() {
    const d = this.d
    for (const p of d.players) {
      if (p.stack === 0) p.status = 'out'
      p.bet = 0
      p.total_bet = 0
      p.cards = null
      p.last_action = ''
      if (p.status !== 'out') p.status = 'active'
    }
    const alive = d.players.filter((p) => p.status !== 'out')
    if (alive.length <= 1 || d.players[this.me].status === 'out') {
      this.status = 'finished'
      const order = d.players.slice().sort((a, b) => b.stack - a.stack)
      const rank = d.players.map((p) => order.findIndex((o) => o.stack === p.stack) + 1)
      this.outcome = { rank, score: d.players.map((p) => p.stack), summary: `${this.name_(order[0].seat)} takes every chip.` }
      d.to_act = -1
      return
    }
    d.hand_no++
    d.button = this.nextActive(d.button, (p) => p.status !== 'out')
    d.sb_seat = alive.length === 2 ? d.button : this.nextActive(d.button, (p) => p.status !== 'out')
    d.bb_seat = this.nextActive(d.sb_seat, (p) => p.status !== 'out')
    this.deck = freshDeck()
    this.hole = {}
    for (const p of alive) this.hole[p.seat] = [this.deck.shift()!, this.deck.shift()!]
    d.board = []
    d.street = 'preflop'
    d.pots = []
    const post = (s: number, amt: number, a: string) => {
      const p = d.players[s]
      const x = Math.min(amt, p.stack)
      p.stack -= x
      p.bet = x
      p.total_bet = x
      p.last_action = a
      if (p.stack === 0) p.status = 'allin'
    }
    post(d.sb_seat, d.small_blind, 'sb')
    post(d.bb_seat, d.big_blind, 'bb')
    d.current_bet = d.big_blind
    d.min_raise_to = d.big_blind * 2
    d.pot_total = d.small_blind + d.big_blind
    this.acted.clear()
    d.to_act = this.nextActive(d.bb_seat, (p) => p.status === 'active')
    if (d.hands_left != null) d.hands_left = Math.max(0, d.hands_left - 1)
    this.addLog(-1, `Hand #${d.hand_no}`, 'deal')
  }
  pending() {
    if (this.status !== 'playing' || this.paused) return null
    const d = this.d
    if (d.street === 'showdown' || d.street === 'over') return d.street === 'showdown' ? this.holdShowdownMs : 2600
    if (d.to_act >= 0 && d.to_act !== this.me) return 900 + Math.random() * 900
    if (d.to_act === this.me && this.meAway()) return 1500 // away: the default move after a short grace
    return null
  }
  step() {
    const d = this.d
    if (d.street === 'showdown' || d.street === 'over') {
      this.holdShowdownMs = 5200
      this.nextHand()
      this.touch()
      return
    }
    if (d.to_act === this.me && this.meAway()) {
      const legal = this.legalFor(this.me)
      this.applyMove(this.me, { type: legal.some((l) => l.type === 'check') ? 'check' : 'fold' })
      this.touch()
      return
    }
    if (d.to_act < 0 || d.to_act === this.me) return
    const seat = d.to_act
    this.applyMove(seat, this.agentMove(seat))
    this.touch()
  }
  agentMove(seat: number): Move {
    const d = this.d
    const p = this.p(seat)
    const st = agentById(this.seats[seat].agent_id || 'aoi')?.style || ROSTER[0].style
    const legal = this.legalFor(seat)
    const has = (t: string) => legal.find((l) => l.type === t)
    const strength = evaluate([...(this.hole[seat] || []), ...d.board]).score[0] + Math.random() * 1.6
    const r = Math.random()
    const raise = has('raise')
    const potish = Math.max(d.big_blind * 2, Math.round((d.pot_total * (0.5 + Math.random() * 0.4)) / 10) * 10)
    if (raise && r < st.aggression * 0.22 + (strength > 1.8 ? 0.25 : 0) + st.bluff * 0.08) {
      const to = Math.max(raise.range!.min, Math.min(raise.range!.max, d.current_bet + potish))
      this.maybeBanter(seat, 0.3)
      return { type: 'raise', args: { to } }
    }
    if (has('check')) return { type: 'check' }
    const toCall = d.current_bet - p.bet
    if (strength < 0.9 && toCall > d.big_blind * 2 && r > 1 - st.tightness * 0.7) return { type: 'fold' }
    if (has('call')) return { type: 'call' }
    return strength > 1.5 || r < 0.3 ? { type: 'allin' } : { type: 'fold' }
  }
}

function holdemSeats(): SeatInfo[] {
  return [seatOf('me', 0), seatOf('aoi', 1), seatOf('ren', 2), seatOf('mika', 3), seatOf('bram', 4), seatOf('nova', 5)]
}

function midFlop(): HoldemMock {
  const pl = (seat: number, stack: number, bet: number, total: number, status: HoldemPlayer['status'], last: string): HoldemPlayer => ({
    seat, stack, bet, total_bet: total, status, cards: null, last_action: last,
  })
  const d: HoldemData = {
    hand_no: 7, hands_left: null, street: 'flop',
    board: ['Kh', '9h', '4c'],
    pots: [{ amount: 435, eligible: [0, 1, 2, 3, 5] }], pot_total: 555,
    button: 3, sb_seat: 4, bb_seat: 5, small_blind: 10, big_blind: 20,
    current_bet: 120, min_raise_to: 240, to_act: 0,
    players: [
      pl(0, 915, 0, 85, 'active', ''),
      pl(1, 1155, 0, 85, 'active', ''),
      pl(2, 0, 0, 85, 'allin', 'allin'),
      pl(3, 2125, 0, 85, 'active', ''),
      pl(4, 640, 0, 10, 'folded', 'fold'),
      pl(5, 675, 120, 205, 'active', 'bet'),
    ],
    last_hand: {
      hand_no: 6, board: ['Qc', '8c', '3d', 'Jc', '2c'],
      winners: [{ seat: 3, amount: 480, hand_name: 'Flush, Queen high', cards: ['Ac', '5c'] }],
      shown: [{ seat: 1, cards: ['Qd', 'Qs'], hand_name: 'Three of a Kind, Queens' }],
    },
  }
  const m = new HoldemMock(holdemSeats(), d)
  m.hole = { 0: ['Ah', 'Kd'], 1: ['Jc', 'Tc'], 2: ['Qd', 'Qc'], 3: ['9s', '9d'], 4: ['7d', '3s'], 5: ['8s', '8c'] }
  m.hole[0] = ['Ah', 'Kh']
  const used = new Set([...Object.values(m.hole).flat(), ...d.board, '2h', 'Qs'])
  m.deck = ['2h', 'Qs', ...freshDeck().filter((c) => !used.has(c))]
  m.acted = new Set([5])
  m.log = [
    { seq: 1, type: 'deal', seat: -1, text: 'Hand #7', at: iso(-60000) },
    { seq: 2, type: 'move', seat: 4, text: 'Captain Bram posts 10', at: iso(-59000) },
    { seq: 3, type: 'move', seat: 5, text: 'Nova posts 20', at: iso(-58000) },
    { seq: 4, type: 'move', seat: 1, text: 'Aoi raises to 60', at: iso(-50000) },
    { seq: 5, type: 'move', seat: 2, text: 'Ren is all-in for 85', at: iso(-46000) },
    { seq: 6, type: 'move', seat: 3, text: 'Mika calls 85', at: iso(-42000) },
    { seq: 7, type: 'move', seat: 4, text: 'Captain Bram folds', at: iso(-40000) },
    { seq: 8, type: 'move', seat: 5, text: 'Nova calls 85', at: iso(-38000) },
    { seq: 9, type: 'move', seat: 0, text: 'Damon calls 85', at: iso(-30000) },
    { seq: 10, type: 'deal', seat: -1, text: 'Flop: Kh 9h 4c', at: iso(-20000) },
    { seq: 11, type: 'move', seat: 5, text: 'Nova bets 120', at: iso(-6000) },
  ]
  m.seq = 11
  m.chatLines = [
    { id: 'c1', seat: 1, name: 'Aoi', avatar: '/play/agents/aoi.webp', text: 'Welcome to the table, Damon! Don’t let Mika scare you.', at: iso(-70000), agent: true },
    { id: 'c2', seat: 3, name: 'Mika', avatar: '/play/agents/mika.webp', text: 'Too late. I’m already scary. 😈', at: iso(-65000), agent: true },
    { id: 'c3', seat: 4, name: 'Captain Bram', avatar: '/play/agents/bram.webp', text: 'Folding this one. Saving my chips for a proper storm.', at: iso(-39000), agent: true },
    { id: 'c4', seat: 5, name: 'Nova', avatar: '/play/agents/nova.webp', text: 'Two hearts on board. Flush probability for anyone holding two: 34.97%. You’re welcome.', at: iso(-5000), agent: true },
  ]
  m.version = 42
  m.touch()
  return m
}

function showdownFixture(): HoldemMock {
  const m = midFlop()
  m.holdShowdownMs = 15000
  m.apply(0, { type: 'call' })
  m.apply(1, { type: 'fold' })
  m.apply(3, { type: 'call' })
  // turn 2h
  m.apply(5, { type: 'check' })
  m.apply(0, { type: 'raise', args: { to: 300 } })
  m.apply(3, { type: 'call' })
  m.apply(5, { type: 'fold' })
  // river Qs
  m.apply(0, { type: 'raise', args: { to: 400 } })
  m.apply(3, { type: 'call' })
  m.addChat(3, 'NOOO. A flush?! Rematch. Right now.')
  m.addChat(1, 'Hehe — told you to watch out for Damon.')
  m.touch()
  return m
}

// ── board games ────────────────────────────────────────────────────────────

class ConnectFourMock extends MockBase {
  name = 'Connect Four with Ren'
  game = { id: 'c4', name: 'Connect Four', kind: 'script' }
  viewKind = 'board' as const
  seats = [seatOf('me', 0), seatOf('ren', 1)]
  rows = 6
  cols = 7
  g: (number | null)[][] = Array.from({ length: 6 }, () => Array(7).fill(null))
  turn = 0
  winLine: [number, number][] = []
  lastDrop: [number, number] | null = null
  constructor() {
    super()
    const moves = [3, 3, 4, 2, 2, 4, 5, 3, 4, 1]
    for (const c of moves) this.drop(c)
    this.turnSeconds = 30
    this.version = 18
    this.touch()
    this.addChat(1, 'Your move.')
  }
  landing(c: number) {
    for (let r = this.rows - 1; r >= 0; r--) if (this.g[r][c] === null) return r
    return -1
  }
  drop(c: number) {
    const r = this.landing(c)
    this.g[r][c] = this.turn
    this.lastDrop = [r, c]
    this.addLog(this.turn, `${this.name_(this.turn)} drops in column ${c + 1}`)
    const line = this.findLine(r, c)
    if (line) {
      this.winLine = line
      this.status = 'finished'
      this.outcome = { rank: this.seats.map((s) => (s.seat === this.turn ? 1 : 2)), score: this.seats.map((s) => (s.seat === this.turn ? 1 : 0)), summary: `${this.name_(this.turn)} connects four.` }
      return
    }
    if (this.g.every((row) => row.every((x) => x !== null))) {
      this.status = 'finished'
      this.outcome = { rank: [1, 1], score: [0.5, 0.5], summary: 'A draw.' }
      return
    }
    this.turn = 1 - this.turn
  }
  findLine(r: number, c: number): [number, number][] | null {
    const who = this.g[r][c]
    for (const [dr, dc] of [[0, 1], [1, 0], [1, 1], [1, -1]]) {
      const line: [number, number][] = [[r, c]]
      for (const s of [1, -1]) {
        let rr = r + dr * s
        let cc = c + dc * s
        while (rr >= 0 && rr < this.rows && cc >= 0 && cc < this.cols && this.g[rr][cc] === who) {
          line.push([rr, cc])
          rr += dr * s
          cc += dc * s
        }
      }
      if (line.length >= 4) return line
    }
    return null
  }
  toMove() {
    return this.status === 'playing' ? [this.turn] : []
  }
  statusText() {
    return this.status === 'playing' ? `{s:${this.turn}} to move`.replace(`{s:${this.turn}}`, this.name_(this.turn)) : this.outcome?.summary || ''
  }
  legalFor(seat: number): MoveSpec[] {
    if (seat !== this.turn) return []
    const out: MoveSpec[] = []
    for (let c = 0; c < this.cols; c++) {
      const r = this.landing(c)
      if (r >= 0) out.push({ type: 'drop', label: `Column ${c + 1}`, args: { col: c }, ui: { cell: [r, c] } })
    }
    return out
  }
  applyMove(seat: number, move: Move) {
    const c = Number(move.args?.col)
    if (!this.legalFor(seat).some((m) => m.args?.col === c)) throw new Error('That column is full.')
    this.drop(c)
  }
  pending() {
    return this.status === 'playing' && this.turn !== this.me ? 1000 : null
  }
  step() {
    const legal = this.legalFor(this.turn)
    // win if possible, else block, else prefer the centre
    const tryCol = (who: number) =>
      legal.find((m) => {
        const [r, c] = m.ui!.cell
        this.g[r][c] = who
        const ok = !!this.findLine(r, c)
        this.g[r][c] = null
        return ok
      })
    const pick = tryCol(this.turn) || tryCol(1 - this.turn) || legal.slice().sort((a, b) => Math.abs(a.args!.col - 3) - Math.abs(b.args!.col - 3) + (Math.random() - 0.5) * 2)[0]
    this.drop(pick.args!.col)
    this.touch()
  }
  data(): BoardData {
    const win = new Set(this.winLine.map(([r, c]) => `${r},${c}`))
    const cells: BoardCell[][] = this.g.map((row, r) =>
      row.map((v, c) => {
        if (v === null) return null
        const cell: BoardCell = { piece: { shape: 'disc', color: v === 0 ? 'p1' : 'p2' } }
        if (win.has(`${r},${c}`)) cell!.mark = '#ffffff'
        return cell
      }),
    )
    return {
      title: 'Connect Four',
      board: { rows: this.rows, cols: this.cols, style: 'grid', cells },
      players: [
        { seat: 0, score: 0, info: 'Red', color: 'p1' },
        { seat: 1, score: 0, info: 'Gold', color: 'p2' },
      ],
      counters: [{ label: 'Moves', value: this.g.flat().filter((x) => x !== null).length }],
      message: this.status === 'playing' ? (this.turn === this.me ? 'Drop a disc: line up four.' : 'Ren is thinking…') : this.outcome?.summary,
    }
  }
}

class CheckersMock extends MockBase {
  name = 'Checkers with Lin'
  game = { id: 'checkers', name: 'Checkers', kind: 'script' }
  viewKind = 'board' as const
  seats = [seatOf('me', 0), seatOf('lin', 1)]
  g: ({ who: number; king: boolean } | null)[][] = Array.from({ length: 8 }, () => Array(8).fill(null))
  turn = 0
  captured = [0, 0]
  constructor() {
    super()
    for (let r = 0; r < 8; r++)
      for (let c = 0; c < 8; c++) if ((r + c) % 2 === 1) {
        if (r < 3) this.g[r][c] = { who: 1, king: false }
        if (r > 4) this.g[r][c] = { who: 0, king: false }
      }
    this.version = 3
    this.touch()
  }
  dirs(p: { who: number; king: boolean }) {
    const f = p.who === 0 ? -1 : 1
    return p.king ? [[1, 1], [1, -1], [-1, 1], [-1, -1]] : [[f, 1], [f, -1]]
  }
  movesFor(seat: number) {
    const caps: MoveSpec[] = []
    const steps: MoveSpec[] = []
    const inb = (r: number, c: number) => r >= 0 && r < 8 && c >= 0 && c < 8
    for (let r = 0; r < 8; r++)
      for (let c = 0; c < 8; c++) {
        const p = this.g[r][c]
        if (!p || p.who !== seat) continue
        for (const [dr, dc] of this.dirs(p)) {
          const r1 = r + dr, c1 = c + dc, r2 = r + 2 * dr, c2 = c + 2 * dc
          if (inb(r1, c1) && !this.g[r1][c1]) steps.push({ type: 'move', label: `${r},${c}→${r1},${c1}`, args: { from: [r, c], to: [r1, c1] }, ui: { from: [r, c], to: [r1, c1] } })
          const mid = inb(r1, c1) ? this.g[r1][c1] : null
          if (inb(r2, c2) && mid && mid.who !== seat && !this.g[r2][c2]) caps.push({ type: 'move', label: `${r},${c}×${r2},${c2}`, args: { from: [r, c], to: [r2, c2] }, ui: { from: [r, c], to: [r2, c2] } })
        }
      }
    return caps.length ? caps : steps
  }
  toMove() {
    return this.status === 'playing' ? [this.turn] : []
  }
  statusText() {
    return this.status === 'playing' ? `${this.name_(this.turn)} to move` : this.outcome?.summary || ''
  }
  legalFor(seat: number) {
    if (seat !== this.turn) return []
    const ms = this.movesFor(seat)
    return [...ms, { type: 'resign', label: 'Resign' }]
  }
  doMove(m: MoveSpec | Move) {
    if (m.type === 'resign') {
      this.status = 'finished'
      this.outcome = { rank: this.turn === 0 ? [2, 1] : [1, 2], score: [0, 0], summary: `${this.name_(this.turn)} resigns.` }
      return
    }
    const [fr, fc] = m.args!.from
    const [tr, tc] = m.args!.to
    const p = this.g[fr][fc]!
    this.g[tr][tc] = p
    this.g[fr][fc] = null
    if (Math.abs(tr - fr) === 2) {
      this.g[(tr + fr) / 2][(tc + fc) / 2] = null
      this.captured[this.turn]++
      this.addLog(this.turn, `${this.name_(this.turn)} captures`)
    } else this.addLog(this.turn, `${this.name_(this.turn)} moves`)
    if ((p.who === 0 && tr === 0) || (p.who === 1 && tr === 7)) p.king = true
    this.turn = 1 - this.turn
    if (!this.movesFor(this.turn).length) {
      this.status = 'finished'
      const w = 1 - this.turn
      this.outcome = { rank: w === 0 ? [1, 2] : [2, 1], score: this.captured.slice(), summary: `${this.name_(w)} wins.` }
    }
  }
  applyMove(seat: number, move: Move) {
    const ok = this.legalFor(seat).find((m) => m.type === move.type && JSON.stringify(m.args) === JSON.stringify(move.args))
    if (!ok) throw new Error('That move is not legal.')
    this.doMove(ok)
  }
  pending() {
    return this.status === 'playing' && this.turn !== this.me ? 1100 : null
  }
  step() {
    const ms = this.movesFor(this.turn)
    this.doMove(ms[Math.floor(Math.random() * ms.length)])
    this.touch()
  }
  data(): BoardData {
    return {
      title: 'Checkers',
      board: {
        rows: 8, cols: 8, style: 'checker',
        cells: this.g.map((row) => row.map((p) => (p ? { piece: { shape: p.king ? 'king' : 'disc', color: p.who === 0 ? 'p1' : '#e9e4d8', glyph: p.king ? '♛' : undefined } } : null))),
      },
      players: [
        { seat: 0, score: this.captured[0], info: 'Red', color: 'p1' },
        { seat: 1, score: this.captured[1], info: 'Ivory', color: '#e9e4d8' },
      ],
      counters: [{ label: 'Captured', value: `${this.captured[0]} – ${this.captured[1]}` }],
      message: this.turn === this.me ? 'Pick up a piece, then drop it on a highlighted square.' : 'Lin is thinking…',
    }
  }
}

// A small shedding game: match the top card's suit or rank; draw otherwise.
class CardGameMock extends MockBase {
  name = 'Sevens with Mika and Bram'
  game = { id: 'sevens', name: 'Lucky Sevens', kind: 'script' }
  viewKind = 'board' as const
  seats = [seatOf('me', 0), seatOf('mika', 1), seatOf('bram', 2)]
  hands: string[][] = [[], [], []]
  deck: string[] = []
  pile: string[] = []
  turn = 0
  round = 2
  bid = 0
  score = [4, 6, 3]
  constructor() {
    super()
    const suits = ['♠', '♥', '♦', '♣']
    const ranks = ['A', '2', '3', '4', '5', '6', '7', '8', '9', '10', 'J', 'Q', 'K']
    const all: string[] = []
    for (const s of suits) for (const r of ranks) all.push(r + s)
    for (let i = all.length - 1; i > 0; i--) {
      const j = Math.floor(Math.random() * (i + 1))
      ;[all[i], all[j]] = [all[j], all[i]]
    }
    this.deck = all
    for (let k = 0; k < 6; k++) for (let s = 0; s < 3; s++) this.hands[s].push(this.deck.shift()!)
    this.pile = [this.deck.shift()!]
    this.version = 9
    this.touch()
  }
  rank(c: string) {
    return c.slice(0, -1)
  }
  suit(c: string) {
    return c.slice(-1)
  }
  playable(c: string) {
    const top = this.pile[this.pile.length - 1]
    return this.rank(c) === '7' || this.rank(c) === this.rank(top) || this.suit(c) === this.suit(top)
  }
  toMove() {
    return this.status === 'playing' ? [this.turn] : []
  }
  statusText() {
    return this.status === 'playing' ? `${this.name_(this.turn)} to play` : this.outcome?.summary || ''
  }
  legalFor(seat: number): MoveSpec[] {
    if (seat !== this.turn) return []
    const out: MoveSpec[] = []
    this.hands[seat].forEach((c, i) => {
      if (this.playable(c)) out.push({ type: 'play', label: `Play ${c}`, args: { index: i }, ui: { zone: `hand-${seat}`, index: i } })
    })
    out.push({ type: 'draw', label: 'Draw a card' })
    out.push({ type: 'wager', label: 'Wager points', range: { arg: 'points', min: 1, max: Math.max(1, this.score[seat]), step: 1 } })
    return out
  }
  applyMove(seat: number, move: Move) {
    const who = this.name_(seat)
    if (move.type === 'play') {
      const i = Number(move.args?.index)
      const c = this.hands[seat][i]
      if (!c || !this.playable(c)) throw new Error('That card does not match the pile.')
      this.hands[seat].splice(i, 1)
      this.pile.push(c)
      this.addLog(seat, `${who} plays ${c}`)
      if (!this.hands[seat].length) {
        this.score[seat] += 5
        this.status = 'finished'
        const order = [0, 1, 2].sort((a, b) => this.score[b] - this.score[a])
        this.outcome = { rank: [0, 1, 2].map((s) => order.indexOf(s) + 1), score: this.score.slice(), summary: `${who} empties their hand.` }
        return
      }
    } else if (move.type === 'draw') {
      if (this.deck.length) this.hands[seat].push(this.deck.shift()!)
      this.addLog(seat, `${who} draws`)
    } else if (move.type === 'wager') {
      const n = Math.round(Number(move.args?.points))
      if (!(n >= 1 && n <= this.score[seat])) throw new Error('Wager out of range.')
      this.bid = n
      this.addLog(seat, `${who} wagers ${n}`)
      return
    } else throw new Error('Unknown move')
    this.turn = (this.turn + 1) % 3
  }
  pending() {
    return this.status === 'playing' && this.turn !== this.me ? 1000 : null
  }
  step() {
    const seat = this.turn
    const legal = this.legalFor(seat).filter((m) => m.type !== 'wager')
    const play = legal.find((m) => m.type === 'play')
    this.applyMove(seat, play || legal[legal.length - 1])
    this.maybeBanter(seat, 0.2)
    this.touch()
  }
  data(): BoardData {
    const cardOf = (c: string) => ({ face: c, color: '♥♦'.includes(this.suit(c)) ? '#d33a4a' : '#111827' })
    return {
      title: 'Lucky Sevens',
      zones: [
        { id: 'hand-1', label: 'Mika', owner: 1, layout: 'fan', cards: this.hands[1].map(() => ({ hidden: true })) },
        { id: 'hand-2', label: 'Captain Bram', owner: 2, layout: 'fan', cards: this.hands[2].map(() => ({ hidden: true })) },
        { id: 'deck', label: `Deck · ${this.deck.length}`, layout: 'stack', cards: this.deck.slice(0, 6).map(() => ({ hidden: true })) },
        { id: 'pile', label: 'Pile', layout: 'stack', cards: this.pile.slice(-4).map(cardOf) },
        { id: 'hand-0', label: 'Your hand', owner: 0, layout: 'row', cards: this.hands[0].map(cardOf) },
      ],
      players: [
        { seat: 0, score: this.score[0], info: `${this.hands[0].length} cards`, color: 'p0' },
        { seat: 1, score: this.score[1], info: `${this.hands[1].length} cards`, color: 'p4' },
        { seat: 2, score: this.score[2], info: `${this.hands[2].length} cards`, color: 'p5' },
      ],
      counters: [
        { label: 'Round', value: `${this.round} / 5` },
        { label: 'Wager', value: this.bid },
      ],
      message: this.turn === this.me ? 'Match the suit or rank of the pile. Sevens are wild.' : `${this.name_(this.turn)} is playing…`,
    }
  }
}

// A table in its lobby, to exercise seat swapping and start.
class LobbyMock implements MockGame {
  version = 3
  seats: SeatInfo[] = [seatOf('me', 0), seatOf('aoi', 1), { seat: 2, kind: 'open', name: '', avatar: '' }, seatOf('friend', 3), seatOf('mika', 4), { seat: 5, kind: 'open', name: '', avatar: '' }]
  started: HoldemMock | null = null
  chatLines: ChatLine[] = [
    { id: 'l1', seat: 1, name: 'Aoi', avatar: '/play/agents/aoi.webp', text: 'Two seats open. Send the link to a friend, or let me call Ren over.', at: iso(-20000), agent: true },
  ]
  view(): TableView {
    if (this.started) return this.started.view()
    return {
      id: 'demo', name: 'Friday night hold’em', code: 'AOI7K2', game: { id: 'holdem', name: "Texas Hold'em", kind: 'builtin' },
      status: 'lobby', host_id: 'me', is_host: true, my_seat: 0, version: this.version,
      seats: this.seats.map((s) => ({ ...s })), to_move: [], deadline: null, turn_seconds: 30, legal: [], view: null,
      log: [], chat: this.chatLines.slice(), outcome: null,
    }
  }
  apply(seat: number, move: Move) {
    if (!this.started) throw new Error('The game has not started.')
    this.started.apply(seat, move)
  }
  pending() {
    return this.started ? this.started.pending() : null
  }
  step() {
    this.started?.step()
  }
  chat(text: string) {
    if (this.started) return this.started.chat(text)
    this.chatLines.push({ id: 'm' + this.version, seat: 0, name: 'Damon', avatar: '', text, at: iso(), agent: false })
    this.version++
  }
  setSeat(seat: number, kind: 'agent' | 'open', agentId?: string) {
    this.seats[seat] = kind === 'open' ? { seat, kind: 'open', name: '', avatar: '' } : seatOf(agentId || 'ren', seat)
    this.version++
  }
  start() {
    const free = ROSTER.filter((a) => !this.seats.some((s) => s.agent_id === a.id))
    const seats = this.seats.map((s, i) => (s.kind === 'open' ? seatOf((free.shift() || ROSTER[i % ROSTER.length]).id, i) : s))
    const m = new HoldemMock(seats, {
      hand_no: 0, hands_left: null, street: 'over', board: [], pots: [], pot_total: 0, button: seats.length - 1, sb_seat: 0, bb_seat: 1,
      small_blind: 10, big_blind: 20, current_bet: 0, min_raise_to: 40, to_act: -1,
      players: seats.map((s) => ({ seat: s.seat, stack: 1000, bet: 0, total_bet: 0, status: 'active', cards: null, last_action: '' })),
      last_hand: null,
    })
    m.chatLines = this.chatLines.slice()
    m.nextHand()
    m.version = this.version + 1
    m.touch()
    this.started = m
  }
}

function finishedFixture(): HoldemMock {
  const m = midFlop()
  m.status = 'finished'
  m.d.to_act = -1
  m.d.street = 'over'
  const stacks = [3120, 1480, 0, 1200, 0, 0]
  m.d.players.forEach((p, i) => {
    p.stack = stacks[i]
    p.bet = 0
    p.status = stacks[i] ? 'active' : 'out'
  })
  m.outcome = { rank: [1, 2, 5, 3, 6, 4], score: stacks, summary: 'Damon wins the table after 42 hands.' }
  m.touch()
  return m
}

// A table of n seats (2..9) at hand #63 with big blinds, for layout checks:
// /app/table/demo?mock=holdem-9
function seatCountFixture(n: number): HoldemMock {
  const others: SeatInfo[] = [
    seatOf('aoi', 1), seatOf('ren', 2), seatOf('mika', 3), seatOf('bram', 4), seatOf('nova', 5), seatOf('lin', 6),
    seatOf('friend', 7), { seat: 8, kind: 'human', name: 'Alexandria Montgomery', avatar: '' },
  ]
  const seats = [seatOf('me', 0), ...others.slice(0, n - 1).map((s, i) => ({ ...s, seat: i + 1 }))]
  const d: HoldemData = {
    hand_no: 62, hands_left: null, street: 'over', board: [], pots: [], pot_total: 0,
    button: n - 1, sb_seat: 0, bb_seat: 1, small_blind: 640, big_blind: 1280,
    current_bet: 0, min_raise_to: 2560, to_act: -1,
    players: seats.map((s) => ({ seat: s.seat, stack: 18000 + s.seat * 3170, bet: 0, total_bet: 0, status: 'active', cards: null, last_action: '' })),
    last_hand: null,
  }
  const m = new HoldemMock(seats, d)
  m.nextHand()
  m.touch()
  return m
}

export function createMock(kind: string | null): MockGame | null {
  const many = /^holdem-([2-9])$/.exec(kind || '')
  if (many) return seatCountFixture(Number(many[1]))
  switch (kind) {
    case 'holdem-away': {
      // my seat is away (two missed clocks): dimmed seat, "I'm back" banner
      const m = midFlop()
      m.seats[0].away = true
      m.touch()
      return m
    }
    case 'holdem-paused': {
      const m = midFlop()
      m.seats[0].away = true
      m.paused = true
      m.touch()
      return m
    }
    case 'holdem':
      return midFlop()
    case 'holdem-showdown':
      return showdownFixture()
    case 'board':
    case 'connect4':
      return new ConnectFourMock()
    case 'cards':
      return new CardGameMock()
    case 'checkers':
      return new CheckersMock()
    case 'lobby':
      return new LobbyMock()
    case 'finished':
      return finishedFixture()
    default:
      return null
  }
}

// Mock lobby data for /app/games when the API is unavailable in mock mode.
export const MOCK_GAMES = {
  builtin: [{ id: 'holdem', kind: 'builtin', name: "Texas Hold'em", summary: 'No-limit hold’em for 2 to 9 players.', min_seats: 2, max_seats: 9, hidden_info: true, status: 'published', visibility: 'public', version: 1, plays: 1284 }],
  mine: [{ id: 'sevens', kind: 'script', name: 'Lucky Sevens', summary: 'Shed your hand by matching suit or rank. Sevens are wild.', min_seats: 2, max_seats: 5, hidden_info: true, status: 'draft', visibility: 'private', version: 3, plays: 12 }],
  community: [
    { id: 'c4', kind: 'script', name: 'Connect Four', summary: 'Drop discs, line up four.', min_seats: 2, max_seats: 2, hidden_info: false, status: 'published', visibility: 'public', owner_name: 'Yuki', version: 2, plays: 341 },
    { id: 'checkers', kind: 'script', name: 'Checkers', summary: 'The classic, with forced captures.', min_seats: 2, max_seats: 2, hidden_info: false, status: 'published', visibility: 'public', owner_name: 'Nova', version: 5, plays: 97 },
  ],
} as const
