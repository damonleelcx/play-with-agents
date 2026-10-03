import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import type { HoldemData, HoldemPlayer, Move, MoveSpec, SeatInfo, TableView } from '../../lib/playApi'
import { Avatar, PlayingCard, TimerRing, VoiceWave } from './parts'
import { fmtChips } from './poker'
import { blip } from './sound'
import { usePlayT } from './strings'
import type { PlayPrefs } from './usePrefs'

type LastHand = NonNullable<HoldemData['last_hand']>

export function useMediaQuery(q: string) {
  const get = () => (typeof window !== 'undefined' && window.matchMedia ? window.matchMedia(q).matches : false)
  const [m, setM] = useState(get)
  useEffect(() => {
    const mq = window.matchMedia(q)
    const on = () => setM(mq.matches)
    on()
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [q])
  return m
}

// Seat geometry: an ellipse around the felt, the viewer at bottom centre.
function geom(rel: number, n: number, compact: boolean) {
  const a = ((90 + (rel * 360) / n) * Math.PI) / 180
  const rx = compact ? 39 : 44.5
  const ry = compact ? 43 : 41
  const cos = Math.cos(a)
  const sin = Math.sin(a)
  const bf = sin < -0.5 ? 0.7 : 0.6
  const x = 50 + rx * cos
  const y = 50 + ry * sin
  return {
    x,
    y,
    // my own bet sits beside my hole cards rather than under them
    bx: rel === 0 ? x + (compact ? 25 : 12) : 50 + rx * bf * cos,
    by: rel === 0 ? y - (compact ? 12 : 17) : 50 + ry * (bf - 0.04) * sin,
    // dealer button sits just clockwise of the bet
    dx: 50 + rx * 0.72 * Math.cos(a + 0.32),
    dy: 50 + ry * 0.66 * Math.sin(a + 0.32),
    upper: sin < -0.2,
    side: cos > 0.35 ? 'right' : cos < -0.35 ? 'left' : 'mid',
  }
}

type Flight = { id: number; fx: number; fy: number; tx: number; ty: number; amount: number; win?: boolean }
let flightSeq = 0

const POT = { x: 50, y: 35 }

export default function HoldemTable({
  table,
  busy,
  onMove,
  prefs,
  speakingSeat = -1,
}: {
  table: TableView
  busy: boolean
  onMove: (m: Move) => void
  prefs: PlayPrefs
  speakingSeat?: number
}) {
  const { s, f } = usePlayT()
  const compact = useMediaQuery('(max-width: 720px)')
  const d = table.view!.data as HoldemData
  const me = table.my_seat
  const viewer = me >= 0 ? me : 0
  const n = Math.max(d.players.length, table.seats.length, 2)
  const seatInfo = (i: number): SeatInfo => table.seats[i] || { seat: i, kind: 'open', name: `Seat ${i + 1}`, avatar: '' }
  const reduced = prefs.motion === 'reduced'
  const sound = prefs.sound

  const wrapRef = useRef<HTMLDivElement>(null)
  const [size, setSize] = useState({ w: 1000, h: 560 })
  useLayoutEffect(() => {
    const el = wrapRef.current
    if (!el) return
    const ro = new ResizeObserver(() => setSize({ w: el.clientWidth, h: el.clientHeight }))
    ro.observe(el)
    setSize({ w: el.clientWidth, h: el.clientHeight })
    return () => ro.disconnect()
  }, [])

  const G = useMemo(() => {
    const out: ReturnType<typeof geom>[] = []
    for (let i = 0; i < n; i++) out.push(geom((i - viewer + n) % n, n, compact))
    return out
  }, [n, viewer, compact])

  // ── showdown / result window ──
  const [result, setResult] = useState<LastHand | null>(null)
  const seen = useRef<number | null>(null)
  const resultTimer = useRef<number>()
  const [flights, setFlights] = useState<Flight[]>([])
  const fly = useCallback(
    (list: Omit<Flight, 'id'>[]) => {
      if (reduced || !list.length) return
      const withIds = list.map((x) => ({ ...x, id: ++flightSeq }))
      setFlights((fl) => [...fl, ...withIds])
      window.setTimeout(() => setFlights((fl) => fl.filter((x) => !withIds.some((w) => w.id === x.id))), 900)
    },
    [reduced],
  )
  useEffect(() => {
    const lh = d.last_hand
    if (!lh) return
    const first = seen.current === null
    if (!first && lh.hand_no === seen.current) return
    seen.current = lh.hand_no
    if (first && d.street !== 'showdown' && d.street !== 'over') return
    setResult(lh)
    window.clearTimeout(resultTimer.current)
    resultTimer.current = window.setTimeout(() => setResult(null), 5600)
    window.setTimeout(
      () => fly(lh.winners.map((w) => ({ fx: POT.x, fy: POT.y, tx: G[w.seat]?.x ?? 50, ty: G[w.seat]?.y ?? 50, amount: w.amount, win: true }))),
      first ? 300 : 650,
    )
    if (lh.winners.some((w) => w.seat === me)) blip('win', sound)
    else blip('chips', sound)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [d.last_hand?.hand_no])
  useEffect(() => () => window.clearTimeout(resultTimer.current), [])
  const showdownStreet = d.street === 'showdown' || d.street === 'over'
  const res = result || (showdownStreet ? d.last_hand : null)
  const resultOn = !!res && (showdownStreet || !!result)

  const revealed = useMemo(() => {
    const m = new Map<number, { cards: string[] | null; hand: string; won: number }>()
    if (!resultOn || !res) return m
    for (const sh of res.shown) m.set(sh.seat, { cards: sh.cards, hand: sh.hand_name, won: 0 })
    for (const w of res.winners) m.set(w.seat, { cards: w.cards || m.get(w.seat)?.cards || null, hand: w.hand_name, won: w.amount })
    return m
  }, [resultOn, res])

  // ── chips into the pot when a street ends; sounds ──
  const prev = useRef<{ hand: number; street: string; bets: number[]; board: number; myTurn: boolean } | null>(null)
  const myTurn = table.legal.length > 0 && table.to_move.includes(me)
  useEffect(() => {
    const p = prev.current
    const bets = d.players.map((x) => x.bet)
    if (p) {
      const newStreet = p.street !== d.street || p.hand !== d.hand_no
      if (newStreet && p.bets.some((b) => b > 0) && p.hand === d.hand_no) {
        fly(p.bets.map((b, i) => (b > 0 && G[i] ? { fx: G[i].bx, fy: G[i].by, tx: POT.x, ty: POT.y, amount: b } : null)).filter(Boolean) as Omit<Flight, 'id'>[])
        blip('chips', sound)
      } else if (bets.reduce((a, b) => a + b, 0) > p.bets.reduce((a, b) => a + b, 0)) blip('chips', sound)
      if (p.hand !== d.hand_no || d.board.length > p.board) blip('deal', sound)
      if (myTurn && !p.myTurn) blip('turn', sound)
    }
    prev.current = { hand: d.hand_no, street: d.street, bets, board: d.board.length, myTurn }
  }, [table.version]) // eslint-disable-line react-hooks/exhaustive-deps

  const board = resultOn && res && res.board.length >= d.board.length ? res.board : d.board
  const pots = d.pots.filter((p) => p.amount > 0)
  const mainPot = pots[0]?.amount || 0
  const betsOut = d.players.reduce((a, p) => a + p.bet, 0)
  const meP = me >= 0 ? d.players[me] : undefined
  const myCards = resultOn ? revealed.get(me)?.cards || meP?.cards || null : meP?.cards || null
  const toAct = d.to_act
  const fromCenter = (x: number, y: number) => ({ x: `${((50 - x) / 100) * size.w}px`, y: `${((50 - y) / 100) * size.h}px` })
  // everything on the felt scales with the table (designed at 534px tall, or 370px wide on phones)
  const k = Math.max(0.6, Math.min(1.25, compact ? size.w / 370 : size.h / 534))
  const winnerSeats = new Set(resultOn && res ? res.winners.map((w) => w.seat) : [])

  return (
    <div className={`pw-holdem felt-${prefs.felt} ${compact ? 'is-compact' : ''} ${reduced ? 'is-reduced' : ''}`}>
      <div className="pw-holdem-stage">
        {/* hand info and status live in the band above the felt, never over a seat */}
        <div className="pw-felt-meta">
          <div className="pw-hand-info">
            <span>{f(s.holdem.hand, { n: d.hand_no })}</span>
            <i />
            <span>{s.holdem.streets[d.street] || d.street}</span>
            <i />
            <span>{f(s.holdem.blinds, { sb: d.small_blind, bb: d.big_blind })}</span>
            {d.hands_left != null && (
              <>
                <i />
                <span>{f(s.holdem.handsLeft, { n: d.hands_left })}</span>
              </>
            )}
          </div>
          {table.view?.status && <div className="pw-table-status">{table.view.status}</div>}
        </div>
        <div className="pw-table-wrap" ref={wrapRef} style={{ '--k': k } as CSSProperties}>
          <div className="pw-rail">
            <div className="pw-felt">
              <div className="pw-felt-light" />
              <div className="pw-felt-line" />
              <div className="pw-felt-logo" aria-hidden>
                <span>PLAY WITH AGENTS</span>
              </div>
            </div>
          </div>

          {/* pot */}
          <div className="pw-pot" style={{ left: `${POT.x}%`, top: `${POT.y}%` }}>
            {(mainPot > 0 || betsOut > 0) && !resultOn && (
              <>
                {mainPot > 0 && <div className="pw-pot-main">
                  <ChipPile amount={mainPot || betsOut} />
                  <div className="pw-pot-label">
                    <small>{s.holdem.pot}</small>
                    <b>{fmtChips(mainPot)}</b>
                  </div>
                </div>}
                {pots.length > 1 && (
                  <div className="pw-sidepots">
                    {pots.slice(1).map((p, i) => (
                      <span key={i} className="pw-sidepot">
                        {s.holdem.side} {fmtChips(p.amount)}
                      </span>
                    ))}
                  </div>
                )}
                {d.pot_total > mainPot && betsOut > 0 && <div className="pw-pot-total">{mainPot > 0 ? 'Σ' : s.holdem.pot} {fmtChips(d.pot_total)}</div>}
              </>
            )}
          </div>

          {/* board */}
          <div className="pw-board" style={{ left: '50%', top: '50%' }}>
            {[0, 1, 2, 3, 4].map((i) =>
              board[i] ? (
                <PlayingCard
                  key={`${d.hand_no}-${i}-${board[i]}`}
                  card={board[i]}
                  size={compact ? 'sm' : 'md'}
                  fourColor={prefs.four_color_deck}
                  back={prefs.card_back}
                  reveal={!reduced}
                  delay={board.length === 3 || board.length === 5 && i < 3 ? i * 160 : 0}
                  glow={!!res && winnerSeats.size > 0 && resultOn}
                />
              ) : (
                <div key={`slot-${i}`} className={`pw-card-slot pw-card--${compact ? 'sm' : 'md'}`} />
              ),
            )}
          </div>

          {/* bets */}
          {d.players.map((p) =>
            p.bet > 0 && G[p.seat] ? (
              <div key={`bet-${p.seat}`} className="pw-bet" style={{ left: `${G[p.seat].bx}%`, top: `${G[p.seat].by}%` }}>
                <ChipPile amount={p.bet} />
                <span>{fmtChips(p.bet)}</span>
              </div>
            ) : null,
          )}

          {/* dealer button + blinds */}
          {G[d.button] && (
            <div className="pw-dealer" style={{ left: `${G[d.button].dx}%`, top: `${G[d.button].dy}%` }} title="Dealer">
              D
            </div>
          )}

          {/* flying chips */}
          {flights.map((fl) => (
            <div
              key={fl.id}
              className={`pw-flight ${fl.win ? 'is-win' : ''}`}
              style={{ '--fx': `${fl.fx}%`, '--fy': `${fl.fy}%`, '--tx': `${fl.tx}%`, '--ty': `${fl.ty}%` } as CSSProperties}
            >
              <ChipPile amount={fl.amount} />
            </div>
          ))}

          {/* seats */}
          {Array.from({ length: n }, (_, i) => {
            const info = seatInfo(i)
            const p = d.players[i]
            const g = G[i]
            if (!g) return null
            return (
              <Seat
                key={i}
                g={g}
                info={info}
                p={p}
                isMe={i === me}
                active={toAct === i && !resultOn}
                deadline={toAct === i ? table.deadline : null}
                turnSeconds={table.turn_seconds}
                sb={d.sb_seat === i}
                bb={d.bb_seat === i}
                handNo={d.hand_no}
                reveal={revealed.get(i)}
                winner={winnerSeats.has(i)}
                prefs={prefs}
                compact={compact}
                from={fromCenter(g.x, g.y)}
                speaking={speakingSeat === i}
              />
            )
          })}

          {/* my hole cards */}
          {me >= 0 && myCards && G[me] && (
            <div className={`pw-mycards ${meP?.status === 'folded' ? 'is-folded' : ''}`} style={{ left: `${G[me].x}%`, top: `${G[me].y}%` }}>
              {myCards.map((c, i) => (
                <PlayingCard
                  key={`${d.hand_no}-${c}`}
                  card={c}
                  size={compact ? 'md' : 'lg'}
                  back={prefs.card_back}
                  fourColor={prefs.four_color_deck}
                  deal={!reduced}
                  reveal={!reduced}
                  delay={i * 120}
                  from={fromCenter(G[me].x, G[me].y - 12)}
                  glow={winnerSeats.has(me)}
                />
              ))}
              {prefs.show_hand_strength && !resultOn && meP?.hand_name && meP?.status !== 'folded' && (
                <div className="pw-strength">{meP?.hand_name}</div>
              )}
            </div>
          )}

          {/* result toast */}
          {resultOn && res && res.winners.length > 0 && (
            <ResultToast res={res} seats={table.seats} me={me} key={res.hand_no} />
          )}
        </div>
      </div>

      <div className={`pw-dock ${me < 0 ? 'is-spectator' : ''}`}>
        {myTurn && me >= 0 && meP ? (
          <ActionBar key={table.version} legal={table.legal} d={d} me={meP} busy={busy} onMove={onMove} compact={compact} />
        ) : me >= 0 && toAct >= 0 && !resultOn && table.status === 'playing' ? (
          <div className="pw-waitbar">
            <span className="pw-wait-dot" />
            {f(s.holdem.waiting, { name: seatInfo(toAct).name })}
          </div>
        ) : null}
      </div>
    </div>
  )
}

// ── chips ──────────────────────────────────────────────────────────────────
const DENOMS: [number, string][] = [
  [1000, 'gold'],
  [500, 'violet'],
  [100, 'navy'],
  [25, 'green'],
  [5, 'red'],
  [1, 'white'],
]
export function ChipPile({ amount }: { amount: number }) {
  const chips: string[] = []
  let left = amount
  for (const [v, c] of DENOMS) {
    let k = Math.floor(left / v)
    left -= k * v
    while (k-- > 0 && chips.length < 7) chips.push(c)
  }
  if (!chips.length) chips.push('white')
  const cols = chips.length > 4 ? [chips.slice(0, Math.ceil(chips.length / 2)), chips.slice(Math.ceil(chips.length / 2))] : [chips]
  return (
    <span className="pw-chips" aria-hidden>
      {cols.map((col, ci) => (
        <span className="pw-chip-col" key={ci}>
          {col.map((c, i) => (
            <span key={i} className={`pw-chip chip-${c}`} style={{ '--i': i } as CSSProperties} />
          ))}
        </span>
      ))}
    </span>
  )
}

// ── a seat ────────────────────────────────────────────────────────────────
function Seat({
  g,
  info,
  p,
  isMe,
  active,
  deadline,
  turnSeconds,
  sb,
  bb,
  handNo,
  reveal,
  winner,
  prefs,
  compact,
  from,
  speaking,
}: {
  g: ReturnType<typeof geom>
  info: SeatInfo
  p?: HoldemPlayer
  isMe: boolean
  active: boolean
  deadline: string | null
  turnSeconds: number
  sb: boolean
  bb: boolean
  handNo: number
  reveal?: { cards: string[] | null; hand: string; won: number }
  winner: boolean
  prefs: PlayPrefs
  compact: boolean
  from: { x: string; y: string }
  speaking: boolean
}) {
  const { s } = usePlayT()
  if (info.kind === 'open' && !p) {
    return (
      <div className="pw-seat is-empty" style={{ left: `${g.x}%`, top: `${g.y}%` }}>
        <div className="pw-seat-av">
          <span className="pw-av pw-av-empty" />
        </div>
      </div>
    )
  }
  const status = p?.status || 'active'
  const avSize = compact ? (isMe ? 48 : 42) : isMe ? 68 : 60
  const showBacks = !isMe && p && status !== 'folded' && status !== 'out' && !reveal?.cards && handNo > 0
  const faceCards = !isMe && reveal?.cards
  const act = p?.last_action
  // The amount is what makes a call or a raise readable at a glance; the
  // street's bet is still on the table while the bubble shows.
  const actAmount = act && ['call', 'bet', 'raise', 'allin'].includes(act) && p && p.bet > 0 ? ` ${fmtChips(p.bet)}` : ''
  const showAct = !!act && status !== 'out' && status !== 'allin' && !winner
  const actBubble = showAct ? (
    <span className={`pw-act act-${act} ${isMe ? 'pw-act-me' : ''}`} key={`${handNo}-${act}-${p?.bet ?? 0}`}>
      {s.holdem.actions[act!] || act}
      {actAmount}
    </span>
  ) : null
  const cls = [
    'pw-seat',
    isMe ? 'is-me' : '',
    info.away ? 'is-away' : '',
    `st-${status}`,
    active ? 'is-active' : '',
    winner ? 'is-winner' : '',
    g.upper ? 'is-upper' : '',
    `side-${g.side}`,
  ].join(' ')
  return (
    <div className={cls} style={{ left: `${g.x}%`, top: `${g.y}%`, '--av': `${avSize}px` } as CSSProperties}>
      {(showBacks || faceCards) && (
        <div className={`pw-seat-cards ${faceCards ? 'is-face' : ''}`}>
          {faceCards
            ? reveal!.cards!.map((c, i) => (
                <PlayingCard key={`${handNo}-${c}`} card={c} size={compact ? 'xs' : 'sm'} fourColor={prefs.four_color_deck} back={prefs.card_back} reveal={prefs.motion !== 'reduced'} delay={i * 120} glow={winner} />
              ))
            : [0, 1].map((i) => (
                <PlayingCard key={`${handNo}-b${i}`} hidden size="xs" back={prefs.card_back} deal={prefs.motion !== 'reduced'} delay={i * 90 + info.seat * 40} from={from} />
              ))}
        </div>
      )}
      <div className="pw-seat-av" style={{ width: avSize, height: avSize }}>
        <Avatar name={info.name} src={info.avatar} seat={info.seat} size={avSize} agent={info.kind === 'agent'} />
        {active && <TimerRing deadline={deadline} total={turnSeconds || 30} size={avSize + 12} />}
        {!isMe && actBubble}
        {(sb || bb) && status !== 'out' && <span className={`pw-blind ${bb ? 'bb' : 'sb'}`}>{bb ? 'BB' : 'SB'}</span>}
        {speaking && <VoiceWave />}
        {info.away && <span className="pw-away-chip">{s.room.away}</span>}
      </div>
      <div className="pw-seat-plate">
        <b>
          <span className="pw-seat-name">{info.name}</span>
          {info.kind === 'agent' && <em className="pw-ai">AI</em>}
        </b>
        <span>{status === 'out' ? s.holdem.out : fmtChips(p?.stack ?? 0)}</span>
        {/* My own cards cover my avatar, so my last action sits by my plate. */}
        {isMe && actBubble}
      </div>
      {status === 'allin' && !winner && <span className="pw-badge-allin">{s.holdem.allin}</span>}
      {reveal?.hand && !isMe && <span className={`pw-handname ${winner ? 'is-win' : ''}`}>{reveal.hand}</span>}
      {winner && reveal && reveal.won > 0 && <span className="pw-won">+{fmtChips(reveal.won)}</span>}
    </div>
  )
}

// ── result toast ──────────────────────────────────────────────────────────
function ResultToast({ res, seats, me }: { res: LastHand; seats: SeatInfo[]; me: number }) {
  const { s, f } = usePlayT()
  const w = res.winners
  const main = w[0]
  const info = seats[main.seat]
  return (
    <div className={`pw-result ${w.some((x) => x.seat === me) ? 'is-me' : ''}`} role="status">
      <div className="pw-result-avs">
        {w.slice(0, 3).map((x) => (
          <Avatar key={x.seat} name={seats[x.seat]?.name || ''} src={seats[x.seat]?.avatar} seat={x.seat} size={44} agent={seats[x.seat]?.kind === 'agent'} />
        ))}
      </div>
      <div className="pw-result-text">
        <b>
          {w.length > 1
            ? s.holdem.split
            : main.seat === me
              ? f(s.holdem.youWin, { amount: fmtChips(main.amount) })
              : f(s.holdem.wins, { name: info?.name || '', amount: fmtChips(main.amount) })}
        </b>
        <span>{w.length > 1 ? w.map((x) => `${seats[x.seat]?.name} +${fmtChips(x.amount)}`).join(' · ') : main.hand_name || ''}</span>
      </div>
    </div>
  )
}

// ── action bar ────────────────────────────────────────────────────────────
function ActionBar({
  legal,
  d,
  me,
  busy,
  onMove,
  compact,
}: {
  legal: MoveSpec[]
  d: HoldemData
  me: HoldemPlayer
  busy: boolean
  onMove: (m: Move) => void
  compact: boolean
}) {
  const { s } = usePlayT()
  const by = (t: string) => legal.find((m) => m.type === t)
  const fold = by('fold')
  const check = by('check')
  const call = by('call')
  const raise = by('raise')
  const allin = by('allin')
  const r = raise?.range
  const step = r?.step || 1
  const toCall = Math.max(0, d.current_bet - me.bet)
  const potNow = d.pots.reduce((a, p) => a + p.amount, 0) + d.players.reduce((a, p) => a + p.bet, 0)
  const clamp = (v: number) => (r ? Math.max(r.min, Math.min(r.max, Math.round(v / step) * step)) : v)
  const [amt, setAmt] = useState<number>(r ? r.min : 0)
  const [text, setText] = useState<string>(r ? String(r.min) : '')
  const [raiseOpen, setRaiseOpen] = useState(!compact)
  const inputRef = useRef<HTMLInputElement>(null)
  const setBoth = (v: number) => {
    const c = clamp(v)
    setAmt(c)
    setText(String(c))
  }
  const preset = (frac: number) => (d.current_bet === 0 ? frac * potNow : d.current_bet + frac * (potNow + toCall))
  const presets: { k: string; v: number }[] = r
    ? [
        { k: s.holdem.min, v: r.min },
        { k: s.holdem.half, v: clamp(preset(0.5)) },
        { k: s.holdem.threeq, v: clamp(preset(0.75)) },
        { k: s.holdem.potBtn, v: clamp(preset(1)) },
        { k: s.holdem.max, v: r.max },
      ]
    : []
  const isBet = d.current_bet === 0
  const callAmt = call ? Math.min(toCall, me.stack) : 0
  const allinAmt = me.bet + me.stack

  const doRaise = useCallback(() => {
    if (!r || busy) return
    const v = clamp(Number(text) || amt)
    if (v >= r.max && allin) onMove({ type: 'allin' })
    else onMove({ type: 'raise', args: { [r.arg || 'to']: v } })
  }, [r, busy, text, amt, allin, onMove]) // eslint-disable-line react-hooks/exhaustive-deps

  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (busy || e.metaKey || e.ctrlKey || e.altKey) return
      const t = e.target as HTMLElement
      const inRaise = t === inputRef.current
      const typing = !inRaise && (t.tagName === 'INPUT' || t.tagName === 'TEXTAREA' || t.isContentEditable)
      if (typing) return
      const k = e.key.toLowerCase()
      if (inRaise) {
        if (e.key === 'Enter') {
          e.preventDefault()
          doRaise()
        } else if (e.key === 'Escape') inputRef.current?.blur()
        return
      }
      if (k === 'f' && fold) onMove({ type: 'fold' })
      else if (k === 'c' && (check || call)) onMove({ type: check ? 'check' : 'call' })
      else if (k === 'r' && raise) {
        e.preventDefault()
        const focus = () => {
          inputRef.current?.focus()
          inputRef.current?.select()
        }
        if (inputRef.current) focus()
        else {
          setRaiseOpen(true)
          setTimeout(focus, 0)
        }
      } else if (k === 'a' && allin) onMove({ type: 'allin' })
      else if (e.key === 'Enter' && raiseOpen && raise) doRaise()
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [busy, fold, check, call, raise, allin, onMove, doRaise, raiseOpen])

  const pct = r && r.max > r.min ? ((amt - r.min) / (r.max - r.min)) * 100 : 100
  return (
    <div className={`pw-actionbar ${busy ? 'is-busy' : ''}`} role="toolbar" aria-label={s.holdem.yourTurn}>
      <div className="pw-ab-head">
        <span className="pw-ab-turn">
          <span className="pw-wait-dot is-me" />
          {s.holdem.yourTurn}
        </span>
        {!compact && <span className="pw-ab-keys">{s.holdem.keys}</span>}
      </div>
      {r && raiseOpen && (
        <div className="pw-raise">
          <div className="pw-presets">
            {presets.map((p) => (
              <button key={p.k} type="button" className={`pw-preset ${p.v === amt ? 'on' : ''}`} disabled={busy} onClick={() => setBoth(p.v)}>
                {p.k}
              </button>
            ))}
          </div>
          <div className="pw-raise-row">
            <input
              type="range"
              className="pw-slider"
              min={r.min}
              max={r.max}
              step={step}
              value={amt}
              disabled={busy}
              style={{ '--pct': `${pct}%` } as CSSProperties}
              onChange={(e) => setBoth(Number(e.target.value))}
              aria-label={isBet ? s.holdem.bet : s.holdem.raiseTo}
            />
            <input
              ref={inputRef}
              className="pw-raise-input"
              inputMode="numeric"
              value={text}
              disabled={busy}
              onChange={(e) => {
                const v = e.target.value.replace(/[^\d]/g, '')
                setText(v)
                if (v) setAmt(Math.max(r.min, Math.min(r.max, Number(v))))
              }}
              onBlur={() => setBoth(Number(text) || r.min)}
              aria-label={isBet ? s.holdem.bet : s.holdem.raiseTo}
            />
          </div>
        </div>
      )}
      <div className="pw-ab-buttons">
        {fold && (
          <button type="button" className="pw-act-btn is-fold" disabled={busy} onClick={() => onMove({ type: 'fold' })}>
            <span>{s.holdem.fold}</span>
            <kbd>F</kbd>
          </button>
        )}
        {check && (
          <button type="button" className="pw-act-btn is-check" disabled={busy} onClick={() => onMove({ type: 'check' })}>
            <span>{s.holdem.check}</span>
            <kbd>C</kbd>
          </button>
        )}
        {call && (
          <button type="button" className="pw-act-btn is-call" disabled={busy} onClick={() => onMove({ type: 'call' })}>
            <span>
              {s.holdem.call} <b>{fmtChips(callAmt)}</b>
            </span>
            <kbd>C</kbd>
          </button>
        )}
        {raise && (
          <button
            type="button"
            className="pw-act-btn is-raise"
            disabled={busy}
            onClick={() => (raiseOpen ? doRaise() : setRaiseOpen(true))}
          >
            <span>
              {isBet ? s.holdem.bet : s.holdem.raiseTo} {raiseOpen && <b>{fmtChips(clamp(Number(text) || amt))}</b>}
            </span>
            <kbd>R</kbd>
          </button>
        )}
        {allin && (
          <button type="button" className="pw-act-btn is-allin" disabled={busy} onClick={() => onMove({ type: 'allin' })}>
            <span>
              {s.holdem.allin} <b>{fmtChips(allinAmt)}</b>
            </span>
            <kbd>A</kbd>
          </button>
        )}
      </div>
    </div>
  )
}
