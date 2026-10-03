import { useEffect, useRef, useState, type CSSProperties } from 'react'
import Img from './Img'
import type { LandingStrings } from './strings'

// A looping Hold'em hand, drawn with CSS: hole cards fly from the dealer,
// bets slide in, the board turns over, the pot slides to the winner.
// Pauses offscreen; prefers-reduced-motion shows one still frame.

type V = LandingStrings['poker']['vignette']

const SEATS = [
  { id: 'you', x: 50, y: 87, color: 'var(--p0)' },
  { id: 'aoi', x: 11, y: 64, color: 'var(--p6)' },
  { id: 'ren', x: 15, y: 24, color: 'var(--p3)' },
  { id: 'mika', x: 50, y: 12, color: 'var(--p1)' },
  { id: 'bram', x: 85, y: 24, color: 'var(--p2)' },
  { id: 'lin', x: 89, y: 64, color: 'var(--p4)' },
]
const NAMES: Record<string, string> = { aoi: 'Aoi', ren: 'Ren', mika: 'Mika', bram: 'Bram', lin: 'Lin' }
const CENTER = { x: 50, y: 47 }
const DEALER = { x: 50, y: 30 }
// action shown at each seat once the betting starts
const ACTIONS: Record<string, keyof V['actions']> = { aoi: 'call', ren: 'fold', mika: 'raise', bram: 'call', lin: 'fold', you: 'call' }
const BOARD = [['Q', '♦'], ['7', '♣'], ['2', '♠'], ['K', '♥'], ['4', '♦']]
const YOURS = [['Q', '♠'], ['Q', '♥']]
// phase durations (ms): reset, deal, bets, flop+pot, turn, river, win
const DUR = [700, 1900, 1500, 1700, 1700, 1400, 2600]
const STILL = 5

const lerp = (a: number, b: number, t: number) => a + (b - a) * t

function Card({ x, y, delay, up, rank, suit, out, dim, z }: {
  x: number; y: number; delay: number; up: boolean; rank?: string; suit?: string; out: boolean; dim?: boolean; z?: number
}) {
  const red = suit === '♥' || suit === '♦'
  const style = {
    '--cx': out ? DEALER.x : x,
    '--cy': out ? DEALER.y : y,
    '--d': `${delay}ms`,
    zIndex: z,
  } as CSSProperties
  return (
    <div className={`pk-card ${out ? 'is-out' : ''} ${up ? 'is-up' : ''} ${dim ? 'is-dim' : ''}`} style={style} aria-hidden="true">
      <div className="pk-card-in">
        <div className="pk-back" />
        <div className={`pk-front ${red ? 'is-red' : ''}`}>
          <span className="pk-rank">{rank}</span>
          <span className="pk-suit">{suit}</span>
        </div>
      </div>
    </div>
  )
}

function Chips({ x, y, n, delay = 0 }: { x: number; y: number; n: number; delay?: number }) {
  const style = { '--cx': x, '--cy': y, '--d': `${delay}ms` } as CSSProperties
  return (
    <div className="pk-chips" style={style} aria-hidden="true">
      {Array.from({ length: n }, (_, i) => (
        <i key={i} style={{ '--i': i } as CSSProperties} />
      ))}
    </div>
  )
}

export default function PokerTable({ v }: { v: V }) {
  const ref = useRef<HTMLDivElement>(null)
  const [phase, setPhase] = useState(0)
  const [visible, setVisible] = useState(false)
  const [still, setStill] = useState(false)

  useEffect(() => {
    const reduce = window.matchMedia?.('(prefers-reduced-motion: reduce)').matches ?? false
    if (reduce) {
      setStill(true)
      setPhase(STILL)
      return
    }
    const el = ref.current
    if (!el) return
    const io = new IntersectionObserver(([e]) => setVisible(e.isIntersecting), { threshold: 0.15 })
    io.observe(el)
    return () => io.disconnect()
  }, [])

  useEffect(() => {
    if (still || !visible) return
    const id = window.setTimeout(() => setPhase((p) => (p + 1) % DUR.length), DUR[phase])
    return () => window.clearTimeout(id)
  }, [phase, visible, still])

  const dealt = phase >= 1
  const betting = phase >= 2
  const potIn = phase >= 3
  const won = phase >= 6
  const pot = { x: 50, y: 63 }

  const holes: JSX.Element[] = []
  SEATS.forEach((s, si) => {
    const hx = lerp(s.x, CENTER.x, 0.3)
    const hy = lerp(s.y, CENTER.y, 0.36)
    const folded = betting && ACTIONS[s.id] === 'fold'
    ;[0, 1].forEach((k) => {
      const mine = s.id === 'you'
      holes.push(
        <Card
          key={`${s.id}-${k}`}
          x={hx + (k ? 2.6 : -2.6)}
          y={hy + (k ? 0.6 : -0.6)}
          delay={(k * SEATS.length + si) * 85}
          out={!dealt}
          up={mine && dealt && phase >= 1}
          rank={mine ? YOURS[k][0] : undefined}
          suit={mine ? YOURS[k][1] : undefined}
          dim={folded}
          z={10 + k}
        />,
      )
    })
  })

  const boardShown = [potIn, potIn, potIn, phase >= 4, phase >= 5]

  return (
    <div ref={ref} className={`pk ${won ? 'is-won' : ''}`} role="img" aria-label={v.label}>
      <div className="pk-bg">
        <Img srcs={['/play/scene-table.webp']} alt="" className="pk-bg-img" />
      </div>
      <div className="pk-felt" aria-hidden="true">
        <div className="pk-felt-in" />
        <span className="pk-brand">PLAY WITH AGENTS</span>
      </div>

      {/* seats */}
      {SEATS.map((s) => {
        const act = ACTIONS[s.id]
        const mine = s.id === 'you'
        return (
          <div
            key={s.id}
            className={`pk-seat ${mine ? 'is-you' : ''} ${betting && act === 'fold' ? 'is-folded' : ''} ${won && mine ? 'is-winner' : ''}`}
            style={{ '--cx': s.x, '--cy': s.y, '--pc': s.color } as CSSProperties}
            aria-hidden="true"
          >
            <span className="pk-ava">
              {mine ? (
                <span className="pk-ava-you">{v.you}</span>
              ) : (
                <Img
                  srcs={[`/play/agents/${s.id}.webp`]}
                  alt=""
                  fallback={<span className="pk-ava-init">{NAMES[s.id][0]}</span>}
                />
              )}
            </span>
            <span className="pk-name">{mine ? v.you : NAMES[s.id]}</span>
            <span className={`pk-act ${betting && !won ? 'is-on' : ''} is-${act}`}>{v.actions[act]}</span>
          </div>
        )
      })}

      {holes}

      {/* bets: from the seats into the pot, then to the winner */}
      {SEATS.filter((s) => ACTIONS[s.id] !== 'fold').map((s, i) => {
        const bx = lerp(s.x, CENTER.x, 0.5)
        const by = lerp(s.y, CENTER.y, 0.55)
        const at = won ? { x: SEATS[0].x + 8, y: SEATS[0].y - 6 } : potIn ? pot : betting ? { x: bx, y: by } : { x: s.x, y: s.y }
        return (
          <div key={s.id} className={`pk-bet ${betting ? 'is-on' : ''}`}>
            <Chips x={at.x + (potIn ? (i - 1.5) * 2.2 : 0)} y={at.y} n={s.id === 'mika' ? 5 : 3} delay={i * 90} />
          </div>
        )
      })}

      {/* the board */}
      {BOARD.map(([r, su], i) => (
        <Card
          key={`b${i}`}
          x={50 + (i - 2) * 7.4}
          y={CENTER.y}
          delay={i < 3 ? i * 140 : 0}
          out={!boardShown[i]}
          up={boardShown[i]}
          rank={r}
          suit={su}
          z={20}
        />
      ))}

      <div className={`pk-pot ${potIn && !won ? 'is-on' : ''}`} aria-hidden="true">
        <span>{v.pot}</span>
        <b>{phase >= 4 ? '1,920' : '960'}</b>
      </div>

      <div className={`pk-tip ${phase >= 4 && phase <= 5 ? 'is-on' : ''}`} aria-hidden="true">
        <Img srcs={['/play/agents/aoi.webp']} alt="" className="pk-tip-ava" />
        <span>{v.tip}</span>
      </div>
      <div className={`pk-win ${won ? 'is-on' : ''}`} aria-hidden="true">{v.win}</div>
    </div>
  )
}
