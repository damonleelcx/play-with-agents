import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { resolveColor, type BoardCell, type BoardData, type BoardPiece, type Move, type MoveSpec, type TableView } from '../../lib/playApi'
import { SayBubble, type Bubbles } from './Bubbles'
import { Avatar, TimerRing, VoiceWave, ZoneCard } from './parts'
import { blip } from './sound'
import { usePlayT } from './strings'
import type { PlayPrefs } from './usePrefs'

const key = (r: number, c: number) => `${r},${c}`
const toMove = (m: MoveSpec): Move => ({ type: m.type, args: m.args })
const sig = (p?: BoardPiece | null) => (p ? `${p.shape || 'disc'}|${p.color || ''}|${p.glyph || ''}|${p.label || ''}` : '')

export default function BoardView({
  table,
  busy,
  onMove,
  prefs,
  speakingSeat = -1,
  bubbles,
}: {
  table: TableView
  busy: boolean
  onMove: (m: Move) => void
  prefs: PlayPrefs
  speakingSeat?: number
  bubbles?: Bubbles
}) {
  const { s } = usePlayT()
  const d = (table.view?.data || {}) as BoardData
  const myTurn = table.legal.length > 0
  const reduced = prefs.motion === 'reduced'

  // ── classify legal moves by their UI hints ──
  const { cellMoves, fromMoves, zoneMoves, buttons, ranges } = useMemo(() => {
    const cellMoves = new Map<string, MoveSpec>()
    const fromMoves = new Map<string, { to: [number, number]; m: MoveSpec }[]>()
    const zoneMoves = new Map<string, MoveSpec>()
    const buttons: MoveSpec[] = []
    const ranges: MoveSpec[] = []
    for (const m of table.legal) {
      const ui = m.ui || {}
      if (m.range) ranges.push(m)
      else if (Array.isArray(ui.from) && Array.isArray(ui.to)) {
        const k = key(ui.from[0], ui.from[1])
        if (!fromMoves.has(k)) fromMoves.set(k, [])
        fromMoves.get(k)!.push({ to: [ui.to[0], ui.to[1]], m })
      } else if (Array.isArray(ui.cell)) {
        const k = key(ui.cell[0], ui.cell[1])
        if (!cellMoves.has(k)) cellMoves.set(k, m)
      } else if (typeof ui.zone === 'string' && typeof ui.index === 'number') zoneMoves.set(`${ui.zone}:${ui.index}`, m)
      else buttons.push(m)
    }
    return { cellMoves, fromMoves, zoneMoves, buttons, ranges }
  }, [table.legal])

  const [sel, setSel] = useState<string | null>(null)
  useEffect(() => setSel(null), [table.version])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && setSel(null)
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  const targets = useMemo(() => {
    const t = new Map<string, MoveSpec>()
    if (sel) for (const x of fromMoves.get(sel) || []) if (!t.has(key(...x.to))) t.set(key(...x.to), x.m)
    return t
  }, [sel, fromMoves])

  // ── piece animations: diff against the previous board ──
  const prevCells = useRef<BoardCell[][] | null>(null)
  const anims = useMemo(() => {
    const out = new Map<string, { kind: 'drop' | 'slide' | 'pop'; dx?: number; dy?: number }>()
    const prev = prevCells.current
    const cur = d.board?.cells
    if (!prev || !cur || reduced) return out
    const vacated: [number, number, string][] = []
    cur.forEach((row, r) =>
      row.forEach((cell, c) => {
        const was = prev[r]?.[c]
        if (sig(was?.piece) && sig(was?.piece) !== sig(cell?.piece)) vacated.push([r, c, sig(was?.piece)])
      }),
    )
    cur.forEach((row, r) =>
      row.forEach((cell, c) => {
        const now = sig(cell?.piece)
        if (!now || now === sig(prev[r]?.[c]?.piece)) return
        const i = vacated.findIndex((v) => v[2] === now || v[2].split('|')[1] === now.split('|')[1])
        if (i >= 0) {
          const [vr, vc] = vacated.splice(i, 1)[0]
          out.set(key(r, c), { kind: 'slide', dx: vc - c, dy: vr - r })
        } else out.set(key(r, c), { kind: d.board?.style === 'grid' ? 'drop' : 'pop', dy: r + 1 })
      }),
    )
    return out
  }, [d.board?.cells, reduced]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (prevCells.current && anims.size) blip('chips', prefs.sound)
    prevCells.current = d.board?.cells || null
  }, [table.version]) // eslint-disable-line react-hooks/exhaustive-deps
  const wasTurn = useRef(false)
  useEffect(() => {
    if (myTurn && !wasTurn.current) blip('turn', prefs.sound)
    wasTurn.current = myTurn
  }, [myTurn, prefs.sound])

  const clickCell = (r: number, c: number) => {
    if (busy) return
    const k = key(r, c)
    if (sel && targets.has(k)) {
      onMove(toMove(targets.get(k)!))
      setSel(null)
      return
    }
    if (fromMoves.has(k)) {
      setSel(sel === k ? null : k)
      return
    }
    if (cellMoves.has(k)) onMove(toMove(cellMoves.get(k)!))
    else setSel(null)
  }

  const b = d.board
  const players = d.players || []
  const seatColor = (seat: number) => resolveColor(players.find((p) => p.seat === seat)?.color || `p${seat % 8}`)

  return (
    <div className={`pw-boardgame ${reduced ? 'is-reduced' : ''}`}>
      {/* players + counters */}
      <div className="pw-bg-top">
        <div className="pw-bg-players">
          {(players.length ? players : table.seats.map((x) => ({ seat: x.seat }))).map((p: any) => {
            const info = table.seats[p.seat]
            const active = table.to_move.includes(p.seat)
            const col = resolveColor(p.color || `p${p.seat % 8}`)
            return (
              <div key={p.seat} className={`pw-bg-player ${active ? 'is-active' : ''} ${p.seat === table.my_seat ? 'is-me' : ''} ${info?.away ? 'is-away' : ''}`} style={{ '--pc': col } as CSSProperties}>
                <span className="pw-bg-av">
                  <Avatar name={info?.name || ''} src={info?.avatar} seat={p.seat} size={36} agent={info?.kind === 'agent'} />
                  {active && <TimerRing deadline={table.deadline} total={table.turn_seconds || 30} size={46} />}
                  {speakingSeat === p.seat && <VoiceWave />}
                  <SayBubble b={bubbles?.[p.seat]} className="is-board" />
                </span>
                <span className="pw-bg-pname">
                  <b>
                    {info?.name || `Seat ${p.seat + 1}`}
                    {info?.away && <em className="pw-away-tag">{s.room.away}</em>}
                  </b>
                  <small>
                    <i className="pw-swatch" />
                    {p.info || ''}
                  </small>
                </span>
                {p.score !== undefined && <span className="pw-bg-score">{p.score}</span>}
              </div>
            )
          })}
        </div>
        {!!d.counters?.length && (
          <div className="pw-bg-counters">
            {d.counters.map((c, i) => (
              <span key={i} className="pw-counter">
                <small>{c.label}</small>
                <b>{c.value}</b>
              </span>
            ))}
          </div>
        )}
      </div>

      {d.message && (
        <div className={`pw-bg-message ${myTurn ? 'is-turn' : ''}`}>
          {myTurn && <span className="pw-wait-dot is-me" />}
          {sel ? `${s.board.pickTarget} · ${s.board.cancel}` : d.message}
        </div>
      )}

      <div className={`pw-bg-main ${b ? '' : `no-board felt-${prefs.felt}`}`}>
        {/* zones above the board: those not owned by me */}
        {d.zones && <Zones zones={d.zones.filter((z) => z.owner === undefined || z.owner !== table.my_seat)} zoneMoves={zoneMoves} busy={busy} onMove={onMove} prefs={prefs} seatColor={seatColor} />}

        {b && (
          <div className="pw-bg-board-wrap">
            <div
              className={`pw-bg-board style-${b.style || 'grid'}`}
              style={{ '--rows': b.rows, '--cols': b.cols, aspectRatio: `${b.cols} / ${b.rows}` } as CSSProperties}
            >
              {Array.from({ length: b.rows }, (_, r) =>
                Array.from({ length: b.cols }, (_, c) => {
                  const cell = b.cells?.[r]?.[c] || null
                  const k = key(r, c)
                  const clickable = myTurn && (cellMoves.has(k) || fromMoves.has(k) || targets.has(k))
                  const dark = (r + c) % 2 === 1
                  const anim = anims.get(k)
                  return (
                    <button
                      key={k}
                      type="button"
                      className={[
                        'pw-cell',
                        dark ? 'is-dark' : 'is-light',
                        clickable ? 'is-click' : '',
                        cellMoves.has(k) && myTurn ? 'is-target' : '',
                        fromMoves.has(k) && myTurn ? 'is-movable' : '',
                        sel === k ? 'is-sel' : '',
                        targets.has(k) ? 'is-dest' : '',
                        r === 0 ? 'r-first' : '',
                        r === b.rows - 1 ? 'r-last' : '',
                        c === 0 ? 'c-first' : '',
                        c === b.cols - 1 ? 'c-last' : '',
                      ].join(' ')}
                      disabled={!clickable || busy}
                      onClick={() => clickCell(r, c)}
                      aria-label={`r${r + 1} c${c + 1}`}
                      style={cell?.mark ? ({ '--mark': resolveColor(cell.mark) } as CSSProperties) : undefined}
                    >
                      {cell?.mark && <span className="pw-cell-mark" />}
                      {cell?.text && <span className="pw-cell-text">{cell.text}</span>}
                      {cell?.piece && (
                        <Piece
                          key={`${k}|${sig(cell.piece)}`}
                          piece={cell.piece}
                          anim={anim}
                        />
                      )}
                      {cellMoves.has(k) && myTurn && !cell?.piece && <span className="pw-cell-hint" style={{ '--pc': seatColor(table.my_seat) } as CSSProperties} />}
                      {targets.has(k) && <span className="pw-cell-hint is-dest" />}
                    </button>
                  )
                }),
              )}
            </div>
          </div>
        )}

        {d.zones && (
          <Zones zones={d.zones.filter((z) => z.owner !== undefined && z.owner === table.my_seat)} zoneMoves={zoneMoves} busy={busy} onMove={onMove} prefs={prefs} seatColor={seatColor} mine />
        )}
      </div>

      {(buttons.length > 0 || ranges.length > 0) && (
        <div className="pw-bg-actions">
          {buttons.map((m, i) => (
            <button key={i} type="button" className="pw-btn pw-btn-soft" disabled={busy} onClick={() => onMove(toMove(m))}>
              {m.label || m.type}
            </button>
          ))}
          {ranges.map((m, i) => (
            <RangeMove key={`${table.version}-${i}`} m={m} busy={busy} onMove={onMove} />
          ))}
        </div>
      )}
    </div>
  )
}

function Piece({ piece, anim }: { piece: BoardPiece; anim?: { kind: string; dx?: number; dy?: number } }) {
  const shape = piece.shape || 'disc'
  const col = resolveColor(piece.color, '#e6e6e6')
  const style = {
    '--pc': col,
    '--dx': anim?.dx ?? 0,
    '--dy': anim?.dy ?? 0,
  } as CSSProperties
  return (
    <span className={`pw-piece shape-${shape} ${anim ? `anim-${anim.kind}` : ''}`} style={style}>
      {(piece.glyph || piece.label) && <span className="pw-piece-glyph">{piece.glyph || piece.label}</span>}
    </span>
  )
}

function Zones({
  zones,
  zoneMoves,
  busy,
  onMove,
  prefs,
  seatColor,
  mine = false,
}: {
  zones: NonNullable<BoardData['zones']>
  zoneMoves: Map<string, MoveSpec>
  busy: boolean
  onMove: (m: Move) => void
  prefs: PlayPrefs
  seatColor: (s: number) => string
  mine?: boolean
}) {
  if (!zones.length) return null
  return (
    <div className={`pw-zones ${mine ? 'is-mine' : ''}`}>
      {zones.map((z) => (
        <div key={z.id} className={`pw-zone layout-${z.layout || 'row'}`} style={z.owner !== undefined ? ({ '--pc': seatColor(z.owner) } as CSSProperties) : undefined}>
          {z.label && (
            <div className="pw-zone-label">
              {z.owner !== undefined && <i className="pw-swatch" />}
              {z.label}
              <small>{z.cards.length}</small>
            </div>
          )}
          <div className="pw-zone-cards" style={{ '--n': z.cards.length } as CSSProperties}>
            {z.cards.length === 0 && <div className="pw-card-slot pw-card--sm" />}
            {z.cards.map((c, i) => {
              const m = zoneMoves.get(`${z.id}:${i}`)
              const mid = (z.cards.length - 1) / 2
              const style = {
                '--i': i,
                '--rot': z.layout === 'fan' ? `${(i - mid) * Math.min(8, 40 / Math.max(1, z.cards.length))}deg` : '0deg',
                '--lift': z.layout === 'fan' ? `${Math.abs(i - mid) * 3}px` : '0px',
              } as CSSProperties
              return (
                <button
                  key={`${i}-${c.face || 'x'}`}
                  type="button"
                  className={`pw-zone-card ${m ? 'is-play' : ''}`}
                  style={style}
                  disabled={!m || busy}
                  onClick={() => m && onMove(toMove(m))}
                  aria-label={c.hidden ? 'hidden card' : c.face}
                >
                  <ZoneCard face={c.face} color={c.color} hidden={c.hidden} back={prefs.card_back} size={mine ? 'lg' : z.owner === undefined ? 'md' : 'sm'} />
                </button>
              )
            })}
          </div>
        </div>
      ))}
    </div>
  )
}

function RangeMove({ m, busy, onMove }: { m: MoveSpec; busy: boolean; onMove: (m: Move) => void }) {
  const r = m.range!
  const step = r.step || 1
  const [v, setV] = useState(r.min)
  const [text, setText] = useState(String(r.min))
  const clamp = (x: number) => Math.max(r.min, Math.min(r.max, Math.round(x / step) * step))
  const go = () => onMove({ type: m.type, args: { ...(m.args || {}), [r.arg]: clamp(Number(text) || v) } })
  const pct = r.max > r.min ? ((v - r.min) / (r.max - r.min)) * 100 : 100
  return (
    <div className="pw-range">
      <input
        type="range"
        className="pw-slider"
        min={r.min}
        max={r.max}
        step={step}
        value={v}
        disabled={busy}
        style={{ '--pct': `${pct}%` } as CSSProperties}
        onChange={(e) => {
          setV(Number(e.target.value))
          setText(e.target.value)
        }}
      />
      <input
        className="pw-raise-input"
        inputMode="numeric"
        value={text}
        disabled={busy}
        onChange={(e) => {
          const t = e.target.value.replace(/[^\d-]/g, '')
          setText(t)
          if (t && !isNaN(Number(t))) setV(clamp(Number(t)))
        }}
        onBlur={() => {
          const c = clamp(Number(text) || r.min)
          setV(c)
          setText(String(c))
        }}
        onKeyDown={(e) => e.key === 'Enter' && go()}
      />
      <button type="button" className="pw-btn pw-btn-primary" disabled={busy} onClick={go}>
        {m.label || m.type} <b>{clamp(Number(text) || v)}</b>
      </button>
    </div>
  )
}
