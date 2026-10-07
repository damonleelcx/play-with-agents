import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent } from 'react'
import { resolveColor, type BoardCard, type BoardCell, type BoardData, type BoardPiece, type Move, type MoveSpec, type TableView } from '../../lib/playApi'
import { Avatar, TimerRing, VoiceWave, ZoneCard } from './parts'
import { blip } from './sound'
import { CardBack, CardPeek, isRich, MiniCard, StoryCard, StoryPanel } from './StoryCard'
import { usePlayT } from './strings'
import type { PlayPrefs } from './usePrefs'

const key = (r: number, c: number) => `${r},${c}`
const toMove = (m: MoveSpec): Move => ({ type: m.type, args: m.args })
const sig = (p?: BoardPiece | null) => (p ? `${p.shape || 'disc'}|${p.color || ''}|${p.glyph || ''}|${p.label || ''}` : '')
const cardSig = (c?: BoardCard | null) => (c ? `${c.title || c.face || ''}|${c.seat ?? ''}|${c.hidden ? 1 : 0}` : '')
const isCell = (x: unknown): x is [number, number] => Array.isArray(x) && x.length === 2 && x.every((n) => typeof n === 'number')
// More hint-less moves than this collapse into a compact list.
const MAX_BUTTONS = 12

type Peek = { card: BoardCard; rect: DOMRect; k: string }

export default function BoardView({
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
  const d = (table.view?.data || {}) as BoardData
  const myTurn = table.legal.length > 0
  const reduced = prefs.motion === 'reduced'
  const b = d.board

  // ── classify legal moves by their UI hints ──
  // A hint that points at nothing the view shows falls back to a button, so
  // every legal move stays reachable.
  const { cellMoves, fromMoves, zoneMoves, cardCells, cardTargets, buttons, ranges } = useMemo(() => {
    const cellMoves = new Map<string, MoveSpec>()
    const fromMoves = new Map<string, { to: [number, number]; m: MoveSpec }[]>()
    const zoneMoves = new Map<string, MoveSpec>()
    const cardCells = new Map<string, Map<string, MoveSpec>>()
    const cardTargets = new Map<string, MoveSpec[]>()
    const buttons: MoveSpec[] = []
    const ranges: MoveSpec[] = []
    const onBoard = (x: [number, number]) => !!b && x[0] >= 0 && x[1] >= 0 && x[0] < b.rows && x[1] < b.cols
    const hasCard = (zone: string, i: number) => !!d.zones?.some((z) => z.id === zone && i >= 0 && i < z.cards.length)
    for (const m of table.legal) {
      const ui = m.ui || {}
      if (m.range) ranges.push(m)
      else if (typeof ui.zone === 'string' && typeof ui.index === 'number') {
        const ck = `${ui.zone}:${ui.index}`
        if (!hasCard(ui.zone, ui.index)) buttons.push(m)
        else if (isCell(ui.cell)) {
          if (!onBoard(ui.cell)) buttons.push(m)
          else {
            if (!cardCells.has(ck)) cardCells.set(ck, new Map())
            const cells = cardCells.get(ck)!
            if (!cells.has(key(ui.cell[0], ui.cell[1]))) cells.set(key(ui.cell[0], ui.cell[1]), m)
          }
        } else if (typeof ui.target === 'string') {
          if (!cardTargets.has(ck)) cardTargets.set(ck, [])
          cardTargets.get(ck)!.push(m)
        } else if (!zoneMoves.has(ck)) zoneMoves.set(ck, m)
        else buttons.push(m)
      } else if (isCell(ui.from) && isCell(ui.to)) {
        if (!onBoard(ui.from) || !onBoard(ui.to)) buttons.push(m)
        else {
          const k = key(ui.from[0], ui.from[1])
          if (!fromMoves.has(k)) fromMoves.set(k, [])
          fromMoves.get(k)!.push({ to: [ui.to[0], ui.to[1]], m })
        }
      } else if (isCell(ui.cell)) {
        const k = key(ui.cell[0], ui.cell[1])
        if (!onBoard(ui.cell)) buttons.push(m)
        else if (!cellMoves.has(k)) cellMoves.set(k, m)
      } else buttons.push(m)
    }
    // A card that is both selectable and immediately playable: the immediate
    // move joins its chooser.
    for (const [ck, m] of zoneMoves) {
      if (cardCells.has(ck) || cardTargets.has(ck)) {
        cardTargets.set(ck, [m, ...(cardTargets.get(ck) || [])])
        zoneMoves.delete(ck)
      }
    }
    return { cellMoves, fromMoves, zoneMoves, cardCells, cardTargets, buttons, ranges }
  }, [table.legal, d.zones, b]) // eslint-disable-line react-hooks/exhaustive-deps

  const [sel, setSel] = useState<string | null>(null) // a piece (from → to)
  const [selCard, setSelCard] = useState<string | null>(null) // "zone:index" (card → cell / target)
  const [hoverCard, setHoverCard] = useState<string | null>(null)
  const [peek, setPeek] = useState<Peek | null>(null)
  const [allMoves, setAllMoves] = useState(false)
  const boardRef = useRef<HTMLDivElement>(null)
  useEffect(() => {
    setSel(null)
    setSelCard(null)
    setHoverCard(null)
    setPeek(null)
  }, [table.version])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key !== 'Escape') return
      setSel(null)
      setSelCard(null)
      setPeek(null)
    }
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [])
  const targets = useMemo(() => {
    const t = new Map<string, MoveSpec>()
    if (sel) for (const x of fromMoves.get(sel) || []) if (!t.has(key(...x.to))) t.set(key(...x.to), x.m)
    return t
  }, [sel, fromMoves])
  const cardDest = (selCard && cardCells.get(selCard)) || null
  const preview = (!selCard && hoverCard && cardCells.get(hoverCard)) || null

  // ── piece animations: diff against the previous board ──
  const prevCells = useRef<BoardCell[][] | null>(null)
  const { anims, fresh } = useMemo(() => {
    const out = new Map<string, { kind: 'drop' | 'slide' | 'pop'; dx?: number; dy?: number }>()
    const fresh = new Set<string>()
    const prev = prevCells.current
    const cur = d.board?.cells
    if (!prev || !cur || reduced) return { anims: out, fresh }
    const vacated: [number, number, string][] = []
    cur.forEach((row, r) =>
      row.forEach((cell, c) => {
        const was = prev[r]?.[c]
        if (sig(was?.piece) && sig(was?.piece) !== sig(cell?.piece)) vacated.push([r, c, sig(was?.piece)])
        if (cell?.card && cardSig(cell.card) !== cardSig(was?.card)) fresh.add(key(r, c))
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
    return { anims: out, fresh }
  }, [d.board?.cells, reduced]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (prevCells.current && (anims.size || fresh.size)) blip('chips', prefs.sound)
    prevCells.current = d.board?.cells || null
  }, [table.version]) // eslint-disable-line react-hooks/exhaustive-deps
  const wasTurn = useRef(false)
  useEffect(() => {
    if (myTurn && !wasTurn.current) blip('turn', prefs.sound)
    wasTurn.current = myTurn
  }, [myTurn, prefs.sound])

  const play = (m: MoveSpec) => {
    if (busy) return
    onMove(toMove(m))
    setSel(null)
    setSelCard(null)
    setPeek(null)
  }

  const clickCell = (r: number, c: number, cell: BoardCell, e: ReactMouseEvent<HTMLButtonElement>) => {
    if (busy) return
    const k = key(r, c)
    if (cardDest?.has(k)) return play(cardDest.get(k)!)
    if (sel && targets.has(k)) return play(targets.get(k)!)
    if (fromMoves.has(k)) {
      setSelCard(null)
      setSel(sel === k ? null : k)
      return
    }
    if (cellMoves.has(k) && !selCard) return play(cellMoves.get(k)!)
    // nothing to play here: a card on the cell opens (touch) or toggles its peek
    if (cell?.card && !cell.card.hidden) {
      setPeek(peek?.k === k ? null : { card: cell.card, rect: e.currentTarget.getBoundingClientRect(), k })
      return
    }
    setSel(null)
  }

  // Keyboard: arrows move between the board's focusable cells.
  const onBoardKey = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    const dir = { ArrowUp: [-1, 0], ArrowDown: [1, 0], ArrowLeft: [0, -1], ArrowRight: [0, 1] }[e.key]
    const el = e.target as HTMLElement
    if (!dir || !b || !el.dataset.r) return
    let r = Number(el.dataset.r)
    let c = Number(el.dataset.c)
    for (;;) {
      r += dir[0]
      c += dir[1]
      if (r < 0 || c < 0 || r >= b.rows || c >= b.cols) return
      const next = boardRef.current?.querySelector<HTMLButtonElement>(`[data-r="${r}"][data-c="${c}"]`)
      if (next && !next.disabled) {
        e.preventDefault()
        next.focus()
        return
      }
    }
  }

  const selectCard = (ck: string, viaKeyboard: boolean) => {
    if (busy) return
    setSel(null)
    setPeek(null)
    const next = selCard === ck ? null : ck
    setSelCard(next)
    // keyboard users jump to the first square the card can go
    if (next && viaKeyboard && cardCells.get(next)?.size) {
      const [first] = cardCells.get(next)!.keys()
      const [r, c] = first.split(',')
      requestAnimationFrame(() => boardRef.current?.querySelector<HTMLButtonElement>(`[data-r="${r}"][data-c="${c}"]`)?.focus())
    }
  }

  const players = d.players || []
  const seatColor = (seat: number) => resolveColor(players.find((p) => p.seat === seat)?.color || `p${seat % 8}`)
  const seatName = (seat: number) => table.seats[seat]?.name || `Seat ${seat + 1}`
  const anyRich = !!d.zones?.some((z) => z.cards.some(isRich)) || !!b?.cells?.some((row) => row?.some((cell) => isRich(cell?.card)))
  const storyMode = !!d.story || anyRich
  const selCardData = selCard ? cardAt(d, selCard) : null
  const otherZones = d.zones?.filter((z) => z.owner === undefined || z.owner !== table.my_seat) || []
  const myZones = d.zones?.filter((z) => z.owner !== undefined && z.owner === table.my_seat) || []
  const chooser = selCard ? cardTargets.get(selCard) : undefined

  let message = d.message
  if (sel) message = `${s.board.pickTarget} · ${s.board.cancel}`
  else if (selCard && cardDest) message = `${f(s.board.pickSquare, { title: selCardData?.title || selCardData?.face || '' })} · ${s.board.cancel}`
  else if (selCard && chooser) message = `${s.board.chooseTarget} · ${s.board.cancel}`

  const zoneProps = { zoneMoves, cardCells, cardTargets, selCard, busy, prefs, seatColor, storyMode, onPlay: play, onSelect: selectCard, onHover: setHoverCard }

  const boardEl = b && (
    <div className="pw-bg-board-wrap">
      <div
        ref={boardRef}
        className={`pw-bg-board style-${b.style || 'grid'} ${cardDest ? 'is-choosing' : ''}`}
        style={{ '--rows': b.rows, '--cols': b.cols, aspectRatio: `${b.cols} / ${b.rows}` } as CSSProperties}
        onKeyDown={onBoardKey}
        onPointerLeave={() => setPeek((p) => (p ? null : p))}
      >
        {Array.from({ length: b.rows }, (_, r) =>
          Array.from({ length: b.cols }, (_, c) => {
            const cell = b.cells?.[r]?.[c] || null
            const k = key(r, c)
            const dest = !!cardDest?.has(k)
            const clickable = myTurn && (dest || (!selCard && cellMoves.has(k)) || fromMoves.has(k) || targets.has(k))
            const peekable = !!cell?.card && !cell.card.hidden
            const dark = (r + c) % 2 === 1
            const anim = anims.get(k)
            const onEnter = (e: ReactPointerEvent<HTMLButtonElement>) => {
              if (peekable && e.pointerType === 'mouse') setPeek({ card: cell!.card!, rect: e.currentTarget.getBoundingClientRect(), k })
            }
            return (
              <button
                key={k}
                type="button"
                data-r={r}
                data-c={c}
                className={[
                  'pw-cell',
                  dark ? 'is-dark' : 'is-light',
                  clickable ? 'is-click' : '',
                  !selCard && cellMoves.has(k) && myTurn ? 'is-target' : '',
                  fromMoves.has(k) && myTurn ? 'is-movable' : '',
                  sel === k ? 'is-sel' : '',
                  targets.has(k) ? 'is-dest' : '',
                  dest ? 'is-card-dest' : '',
                  preview?.has(k) ? 'is-preview' : '',
                  cell?.blocked ? 'is-blocked' : '',
                  cell?.card ? 'has-card' : '',
                  peekable && !clickable ? 'is-peek' : '',
                  r === 0 ? 'r-first' : '',
                  r === b.rows - 1 ? 'r-last' : '',
                  c === 0 ? 'c-first' : '',
                  c === b.cols - 1 ? 'c-last' : '',
                ].join(' ')}
                disabled={busy || (!clickable && !peekable)}
                aria-disabled={!clickable || undefined}
                onClick={(e) => clickCell(r, c, cell, e)}
                onPointerEnter={onEnter}
                onPointerLeave={(e) => e.pointerType === 'mouse' && setPeek(null)}
                onFocus={(e) => peekable && e.currentTarget.matches(':focus-visible') && setPeek({ card: cell!.card!, rect: e.currentTarget.getBoundingClientRect(), k })}
                onBlur={() => setPeek(null)}
                aria-label={[
                  `r${r + 1} c${c + 1}`,
                  cell?.blocked ? s.board.sealed : '',
                  cell?.card?.title || '',
                  dest ? s.board.playHere : '',
                ].filter(Boolean).join(' · ')}
                title={dest ? s.board.playHere : undefined}
                style={cell?.mark ? ({ '--mark': resolveColor(cell.mark) } as CSSProperties) : undefined}
              >
                {cell?.blocked && <span className="pw-cell-sealed" aria-hidden />}
                {cell?.mark && <span className="pw-cell-mark" />}
                {cell?.text && <span className="pw-cell-text">{cell.text}</span>}
                {cell?.piece && <Piece key={`${k}|${sig(cell.piece)}`} piece={cell.piece} anim={anim} />}
                {cell?.card && (
                  <MiniCard key={`${k}|${cardSig(cell.card)}`} card={cell.card} seatColor={cell.card.seat !== undefined ? seatColor(cell.card.seat) : undefined} fresh={fresh.has(k)} />
                )}
                {!selCard && cellMoves.has(k) && myTurn && !cell?.piece && <span className="pw-cell-hint" style={{ '--pc': seatColor(table.my_seat) } as CSSProperties} />}
                {targets.has(k) && <span className="pw-cell-hint is-dest" />}
                {dest && <span className="pw-cell-slot" style={{ '--pc': seatColor(table.my_seat) } as CSSProperties} />}
                {preview?.has(k) && <span className="pw-cell-hint is-preview" />}
              </button>
            )
          }),
        )}
      </div>
    </div>
  )

  return (
    <div className={`pw-boardgame ${reduced ? 'is-reduced' : ''} ${storyMode ? 'is-story' : ''}`}>
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

      {message && (
        <div className={`pw-bg-message ${myTurn ? 'is-turn' : ''}`} role="status">
          {myTurn && <span className="pw-wait-dot is-me" />}
          {message}
        </div>
      )}

      {storyMode ? (
        <div className="pw-bg-main is-story">
          <div className={`pw-bg-stage ${b ? '' : 'no-board'}`} style={b ? ({ '--rows': b.rows, '--cols': b.cols } as CSSProperties) : undefined}>
            <div className="pw-bg-center">{boardEl}</div>
            <aside className="pw-bg-side">
              {otherZones.length > 0 && <Zones zones={otherZones} {...zoneProps} compact />}
              {d.story && <StoryPanel story={d.story} seatColor={seatColor} seatName={seatName} reduced={reduced} />}
            </aside>
          </div>
          {myZones.length > 0 && (
            <div className="pw-hand-area">
              <div className={`pw-hand-caption ${selCard ? 'is-sel' : ''}`}>
                {myTurn && (d.prompt || cardCells.size > 0 || zoneMoves.size > 0) ? (
                  <p className={`pw-hand-prompt ${selCard ? 'is-sel' : ''}`}>
                    {selCard && selCardData ? f(s.board.pickSquare, { title: selCardData.title || selCardData.face || '' }) : d.prompt || s.board.chooseCard}
                  </p>
                ) : (
                  <p className="pw-hand-prompt is-idle">{myZones.map((z) => z.label).filter(Boolean).join(' · ')}</p>
                )}
                {selCardData?.effect && <span className="pw-hand-effect">{selCardData.effect}</span>}
              </div>
              <Zones zones={myZones} {...zoneProps} mine tray />
            </div>
          )}
        </div>
      ) : (
        <div className={`pw-bg-main ${b ? '' : `no-board felt-${prefs.felt}`}`}>
          {/* zones above the board: those not owned by me */}
          {otherZones.length > 0 && <Zones zones={otherZones} {...zoneProps} />}
          {boardEl}
          {myZones.length > 0 && <Zones zones={myZones} {...zoneProps} mine />}
        </div>
      )}

      {chooser && (
        <div className="pw-chooser" role="group" aria-label={s.board.chooseTarget}>
          <small>{s.board.chooseTarget}</small>
          {chooser.map((m, i) => (
            <button key={i} type="button" className="pw-btn pw-btn-primary" disabled={busy} onClick={() => play(m)}>
              {m.ui?.target || m.label || m.type}
            </button>
          ))}
          <button type="button" className="pw-btn pw-btn-soft" onClick={() => setSelCard(null)}>
            {s.board.cancel}
          </button>
        </div>
      )}

      {(buttons.length > 0 || ranges.length > 0) && (
        <div className={`pw-bg-actions ${buttons.length > MAX_BUTTONS ? 'is-many' : ''}`}>
          {buttons.length > MAX_BUTTONS ? (
            <>
              <button type="button" className="pw-btn pw-btn-soft pw-moves-toggle" aria-expanded={allMoves} onClick={() => setAllMoves((x) => !x)}>
                {allMoves ? s.board.fewerMoves : f(s.board.allMoves, { n: buttons.length })}
              </button>
              {allMoves && (
                <div className="pw-move-list" role="list">
                  {buttons.map((m, i) => (
                    <button key={i} type="button" role="listitem" className="pw-move-chip" disabled={busy} onClick={() => play(m)}>
                      {m.label || m.type}
                    </button>
                  ))}
                </div>
              )}
            </>
          ) : (
            buttons.map((m, i) => (
              <button key={i} type="button" className="pw-btn pw-btn-soft" disabled={busy} onClick={() => play(m)}>
                {m.label || m.type}
              </button>
            ))
          )}
          {ranges.map((m, i) => (
            <RangeMove key={`${table.version}-${i}`} m={m} busy={busy} onMove={onMove} />
          ))}
        </div>
      )}

      {peek && (
        <CardPeek
          card={peek.card}
          rect={peek.rect}
          seatColor={peek.card.seat !== undefined ? seatColor(peek.card.seat) : undefined}
          caption={peek.card.seat !== undefined ? f(s.board.playedBy, { name: seatName(peek.card.seat) }) : undefined}
        />
      )}
    </div>
  )
}

function cardAt(d: BoardData, ck: string): BoardCard | null {
  const i = ck.lastIndexOf(':')
  const z = d.zones?.find((x) => x.id === ck.slice(0, i))
  return z?.cards[Number(ck.slice(i + 1))] || null
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
  cardCells,
  cardTargets,
  selCard,
  busy,
  onPlay,
  onSelect,
  onHover,
  prefs,
  seatColor,
  storyMode,
  mine = false,
  compact = false,
  tray = false,
}: {
  zones: NonNullable<BoardData['zones']>
  zoneMoves: Map<string, MoveSpec>
  cardCells: Map<string, Map<string, MoveSpec>>
  cardTargets: Map<string, MoveSpec[]>
  selCard: string | null
  busy: boolean
  onPlay: (m: MoveSpec) => void
  onSelect: (ck: string, viaKeyboard: boolean) => void
  onHover: (ck: string | null) => void
  prefs: PlayPrefs
  seatColor: (s: number) => string
  storyMode: boolean
  mine?: boolean
  compact?: boolean
  tray?: boolean
}) {
  const { s } = usePlayT()
  if (!zones.length) return null
  return (
    <div className={`pw-zones ${mine ? 'is-mine' : ''} ${compact ? 'is-compact' : ''} ${tray ? 'is-tray' : ''}`}>
      {zones.map((z) => {
        const rich = storyMode && (z.cards.some(isRich) || z.cards.every((c) => c.hidden))
        return (
          <div key={z.id} className={`pw-zone layout-${z.layout || 'row'} ${rich ? 'is-rich' : ''}`} style={{ '--pc': z.owner !== undefined ? seatColor(z.owner) : undefined, '--n': z.cards.length } as CSSProperties}>
            {z.label && (
              <div className="pw-zone-label">
                {z.owner !== undefined && <i className="pw-swatch" />}
                {z.label}
                <small>{z.cards.length}</small>
              </div>
            )}
            <div className="pw-zone-cards" style={{ '--n': z.cards.length } as CSSProperties}>
              {z.cards.length === 0 && (rich ? <span className="pw-sc-slot" /> : <div className="pw-card-slot pw-card--sm" />)}
              {z.cards.map((c, i) => {
                const ck = `${z.id}:${i}`
                const m = zoneMoves.get(ck)
                const selectable = cardCells.has(ck) || cardTargets.has(ck)
                const playable = !!m || selectable
                const selected = selCard === ck
                const mid = (z.cards.length - 1) / 2
                const fan = z.layout === 'fan'
                const style = {
                  '--i': i,
                  '--rot': fan ? `${(i - mid) * Math.min(rich ? 4 : 8, 40 / Math.max(1, z.cards.length))}deg` : '0deg',
                  '--lift': fan ? `${Math.abs(i - mid) * (rich ? 5 : 3)}px` : '0px',
                } as CSSProperties
                const size = mine ? 'lg' : compact ? 'sm' : z.owner === undefined ? 'md' : 'sm'
                return (
                  <button
                    key={`${i}-${c.title || c.face || 'x'}`}
                    type="button"
                    className={`pw-zone-card ${rich ? 'is-rich' : ''} ${playable ? 'is-play' : ''} ${selected ? 'is-sel' : ''} ${selCard && !selected && mine ? 'is-dim' : ''}`}
                    style={style}
                    disabled={!playable || busy}
                    aria-pressed={selectable ? selected : undefined}
                    onClick={(e) => (selectable ? onSelect(ck, e.detail === 0) : m && onPlay(m))}
                    onPointerEnter={(e) => e.pointerType === 'mouse' && selectable && onHover(ck)}
                    onPointerLeave={() => onHover(null)}
                    onFocus={() => selectable && onHover(ck)}
                    onBlur={() => onHover(null)}
                    aria-label={c.hidden ? s.board.hiddenCard : [c.title || c.face, c.kind, c.effect].filter(Boolean).join(' · ')}
                  >
                    {rich ? (
                      c.hidden ? (
                        <CardBack size={size === 'lg' ? 'lg' : 'sm'} />
                      ) : (
                        <StoryCard card={c} size={size === 'lg' ? 'lg' : size} />
                      )
                    ) : (
                      <ZoneCard face={c.face || c.title} color={c.color} hidden={c.hidden} back={prefs.card_back} size={size === 'lg' ? 'lg' : size} />
                    )}
                  </button>
                )
              })}
            </div>
          </div>
        )
      })}
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
