import { useEffect, useMemo, useRef, useState, type CSSProperties, type KeyboardEvent as ReactKeyboardEvent, type MouseEvent as ReactMouseEvent, type PointerEvent as ReactPointerEvent } from 'react'
import { isMapBoard, resolveColor, type BoardCard, type BoardCell, type BoardData, type BoardPiece, type Move, type MoveSpec, type TableView } from '../../lib/playApi'
import { SayBubble, type Bubbles } from './Bubbles'
import MapBoard, { stackSlot } from './MapBoard'
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
const piecesOf = (cell: BoardCell): BoardPiece[] => (cell ? cell.pieces || (cell.piece ? [cell.piece] : []) : [])
// More hint-less moves than this collapse into a compact list.
const MAX_BUTTONS = 12

type Peek = { card: BoardCard; rect: DOMRect; k: string }
type Anim = { kind: 'drop' | 'slide' | 'pop' | 'glide'; dx?: number; dy?: number }

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
  const { s, f } = usePlayT()
  const d = (table.view?.data || {}) as BoardData
  const myTurn = table.legal.length > 0
  const reduced = prefs.motion === 'reduced'
  const b = d.board
  const map = isMapBoard(b) ? b : null
  const grid = b && !isMapBoard(b) ? b : null

  // ── places: every square of a grid ("r,c") or space of a map (its id) ──
  const places = useMemo(() => {
    const out = new Map<string, { cell: BoardCell; r?: number; c?: number; x?: number; y?: number }>()
    if (grid) grid.cells?.forEach((row, r) => row?.forEach((cell, c) => out.set(key(r, c), { cell, r, c })))
    if (map) for (const sp of map.spaces) out.set(sp.id, { cell: sp, x: sp.x, y: sp.y })
    if (grid) for (let r = 0; r < grid.rows; r++) for (let c = 0; c < grid.cols; c++) if (!out.has(key(r, c))) out.set(key(r, c), { cell: null, r, c })
    return out
  }, [b]) // eslint-disable-line react-hooks/exhaustive-deps
  // The place a hint points at, or null when the view shows no such place.
  const placeOf = (x: unknown): string | null => {
    if (grid && isCell(x)) return x[0] >= 0 && x[1] >= 0 && x[0] < grid.rows && x[1] < grid.cols ? key(x[0], x[1]) : null
    if (map && typeof x === 'string') return places.has(x) ? x : null
    return null
  }

  // ── classify legal moves by their UI hints ──
  // A hint that points at nothing the view shows falls back to a button, so
  // every legal move stays reachable.
  const { cellMoves, fromMoves, zoneMoves, cardCells, cardTargets, buttons, ranges } = useMemo(() => {
    const cellMoves = new Map<string, MoveSpec>()
    const fromMoves = new Map<string, { to: string; m: MoveSpec }[]>()
    const zoneMoves = new Map<string, MoveSpec>()
    const cardCells = new Map<string, Map<string, MoveSpec>>()
    const cardTargets = new Map<string, MoveSpec[]>()
    const buttons: MoveSpec[] = []
    const ranges: MoveSpec[] = []
    const hasCard = (zone: string, i: number) => !!d.zones?.some((z) => z.id === zone && i >= 0 && i < z.cards.length)
    for (const m of table.legal) {
      const ui = m.ui || {}
      const at = ui.cell ?? ui.space
      if (m.range) ranges.push(m)
      else if (typeof ui.zone === 'string' && typeof ui.index === 'number') {
        const ck = `${ui.zone}:${ui.index}`
        if (!hasCard(ui.zone, ui.index)) buttons.push(m)
        else if (at !== undefined) {
          const k = placeOf(at)
          if (!k) buttons.push(m)
          else {
            if (!cardCells.has(ck)) cardCells.set(ck, new Map())
            const cells = cardCells.get(ck)!
            if (!cells.has(k)) cells.set(k, m)
          }
        } else if (typeof ui.target === 'string') {
          if (!cardTargets.has(ck)) cardTargets.set(ck, [])
          cardTargets.get(ck)!.push(m)
        } else if (!zoneMoves.has(ck)) zoneMoves.set(ck, m)
        else buttons.push(m)
      } else if (ui.from !== undefined && ui.to !== undefined) {
        const from = placeOf(ui.from)
        const to = placeOf(ui.to)
        if (!from || !to) buttons.push(m)
        else {
          if (!fromMoves.has(from)) fromMoves.set(from, [])
          fromMoves.get(from)!.push({ to, m })
        }
      } else if (at !== undefined) {
        const k = placeOf(at)
        if (!k) buttons.push(m)
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
  }, [table.legal, d.zones, places]) // eslint-disable-line react-hooks/exhaustive-deps

  const [sel, setSel] = useState<string | null>(null) // a piece (from → to)
  const [selCard, setSelCard] = useState<string | null>(null) // "zone:index" (card → place / target)
  const [hoverCard, setHoverCard] = useState<string | null>(null)
  const [peek, setPeek] = useState<Peek | null>(null)
  const [allMoves, setAllMoves] = useState(false)
  const [mapSize, setMapSize] = useState({ w: 0, h: 0 })
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
    if (sel) for (const x of fromMoves.get(sel) || []) if (!t.has(x.to)) t.set(x.to, x.m)
    return t
  }, [sel, fromMoves])
  const cardDest = (selCard && cardCells.get(selCard)) || null
  const preview = (!selCard && hoverCard && cardCells.get(hoverCard)) || null

  // ── piece animations: diff against the previous board ──
  // A piece that left one place and arrived at another slides (a grid) or
  // glides (a map) between them; anything new drops in or pops up.
  const prevPlaces = useRef<typeof places | null>(null)
  const { anims, fresh } = useMemo(() => {
    const out = new Map<string, Map<number, Anim>>()
    const fresh = new Set<string>()
    const prev = prevPlaces.current
    if (!prev || reduced) return { anims: out, fresh }
    const vacated: { k: string; sig: string }[] = []
    const arrived: { k: string; i: number; sig: string }[] = []
    for (const [k, now] of places) {
      const was = prev.get(k)
      const before = piecesOf(was?.cell ?? null).map(sig)
      const after = piecesOf(now.cell).map(sig)
      const left = [...before]
      after.forEach((p, i) => {
        const j = left.indexOf(p)
        if (j >= 0) left.splice(j, 1)
        else arrived.push({ k, i, sig: p })
      })
      for (const p of left) vacated.push({ k, sig: p })
      if (now.cell?.card && cardSig(now.cell.card) !== cardSig(was?.cell?.card)) fresh.add(k)
    }
    for (const a of arrived) {
      let j = vacated.findIndex((v) => v.sig === a.sig)
      if (j < 0) j = vacated.findIndex((v) => v.sig.split('|')[1] === a.sig.split('|')[1])
      let anim: Anim
      if (j >= 0) {
        const from = places.get(vacated.splice(j, 1)[0].k)!
        const to = places.get(a.k)!
        if (map) anim = { kind: 'glide', dx: (((from.x ?? 0) - (to.x ?? 0)) / 100) * mapSize.w, dy: (((from.y ?? 0) - (to.y ?? 0)) / 100) * mapSize.h }
        else anim = { kind: 'slide', dx: (from.c ?? 0) - (to.c ?? 0), dy: (from.r ?? 0) - (to.r ?? 0) }
      } else anim = { kind: grid?.style === 'grid' ? 'drop' : 'pop', dy: (places.get(a.k)?.r ?? 0) + 1 }
      if (!out.has(a.k)) out.set(a.k, new Map())
      out.get(a.k)!.set(a.i, anim)
    }
    return { anims: out, fresh }
  }, [places, reduced]) // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (prevPlaces.current && (anims.size || fresh.size)) blip('chips', prefs.sound)
    prevPlaces.current = places
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

  const clickPlace = (k: string, cell: BoardCell, e: ReactMouseEvent<HTMLButtonElement>) => {
    if (busy) return
    if (cardDest?.has(k)) return play(cardDest.get(k)!)
    if (sel && targets.has(k)) return play(targets.get(k)!)
    if (fromMoves.has(k)) {
      setSelCard(null)
      setSel(sel === k ? null : k)
      return
    }
    if (cellMoves.has(k) && !selCard) return play(cellMoves.get(k)!)
    // nothing to play here: a card on the place opens (touch) or toggles its peek
    if (cell?.card && !cell.card.hidden) {
      setPeek(peek?.k === k ? null : { card: cell.card, rect: e.currentTarget.getBoundingClientRect(), k })
      return
    }
    setSel(null)
  }

  // Keyboard on a grid: arrows move between the focusable cells. (A map's
  // spaces are in tab order.)
  const onBoardKey = (e: ReactKeyboardEvent<HTMLDivElement>) => {
    const dir = { ArrowUp: [-1, 0], ArrowDown: [1, 0], ArrowLeft: [0, -1], ArrowRight: [0, 1] }[e.key]
    const el = e.target as HTMLElement
    if (!dir || !grid || !el.dataset.r) return
    let r = Number(el.dataset.r)
    let c = Number(el.dataset.c)
    for (;;) {
      r += dir[0]
      c += dir[1]
      if (r < 0 || c < 0 || r >= grid.rows || c >= grid.cols) return
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
    // keyboard users jump to the first place the card can go
    if (next && viaKeyboard && cardCells.get(next)?.size) {
      const [first] = cardCells.get(next)!.keys()
      requestAnimationFrame(() => boardRef.current?.querySelector<HTMLButtonElement>(`[data-k="${CSS.escape(first)}"]`)?.focus())
    }
  }

  const players = d.players || []
  const seatColor = (seat: number) => resolveColor(players.find((p) => p.seat === seat)?.color || `p${seat % 8}`)
  const seatName = (seat: number) => table.seats[seat]?.name || `Seat ${seat + 1}`
  const anyRich = !!d.zones?.some((z) => z.cards.some(isRich)) || [...places.values()].some((p) => isRich(p.cell?.card))
  const storyMode = !!d.story || anyRich
  // An empty story panel only makes sense where cards are played onto the board.
  const cardsOnBoard = cardCells.size > 0 || [...places.values()].some((p) => !!p.cell?.card)
  const selCardData = selCard ? cardAt(d, selCard) : null
  const area = (z: NonNullable<BoardData['zones']>[number]) => z.area || (z.owner !== undefined && z.owner === table.my_seat ? 'bottom' : 'top')
  const myZones = d.zones?.filter((z) => z.owner !== undefined && z.owner === table.my_seat && area(z) === 'bottom') || []
  const otherZones = d.zones?.filter((z) => !myZones.includes(z)) || []
  const zonesAt = (a: string) => otherZones.filter((z) => area(z) === a)
  const chooser = selCard ? cardTargets.get(selCard) : undefined
  const dims = grid ? { rows: grid.rows, cols: grid.cols } : map ? { rows: 1, cols: map.aspect || 1.4 } : null

  let message = d.message
  if (sel) message = `${s.board.pickTarget} · ${s.board.cancel}`
  else if (selCard && cardDest) message = `${f(s.board.pickSquare, { title: selCardData?.title || selCardData?.face || '' })} · ${s.board.cancel}`
  else if (selCard && chooser) message = `${s.board.chooseTarget} · ${s.board.cancel}`

  const zoneProps = { zoneMoves, cardCells, cardTargets, selCard, busy, prefs, seatColor, storyMode, onPlay: play, onSelect: selectCard, onHover: setHoverCard }

  // One place's button: the same for a grid square and a map space; only the
  // container lays them out differently.
  const renderPlace = (k: string, cell: BoardCell, base: string, style: CSSProperties | undefined, label: string, extra?: Record<string, string | number>) => {
    const dest = !!cardDest?.has(k)
    const clickable = myTurn && (dest || (!selCard && cellMoves.has(k)) || fromMoves.has(k) || targets.has(k))
    const peekable = !!cell?.card && !cell.card.hidden
    const pieces = piecesOf(cell)
    const placeAnims = anims.get(k)
    const onEnter = (e: ReactPointerEvent<HTMLButtonElement>) => {
      if (peekable && e.pointerType === 'mouse') setPeek({ card: cell!.card!, rect: e.currentTarget.getBoundingClientRect(), k })
    }
    return (
      <button
        key={k}
        type="button"
        data-k={k}
        {...extra}
        className={[
          base,
          clickable ? 'is-click' : '',
          !selCard && cellMoves.has(k) && myTurn ? 'is-target' : '',
          fromMoves.has(k) && myTurn ? 'is-movable' : '',
          sel === k ? 'is-sel' : '',
          targets.has(k) ? 'is-dest' : '',
          dest ? 'is-card-dest' : '',
          preview?.has(k) ? 'is-preview' : '',
          cell?.blocked ? 'is-blocked' : '',
          cell?.card ? 'has-card' : '',
          pieces.length > 1 ? 'has-stack' : '',
          peekable && !clickable ? 'is-peek' : '',
        ].join(' ')}
        disabled={busy || (!clickable && !peekable)}
        aria-disabled={!clickable || undefined}
        onClick={(e) => clickPlace(k, cell, e)}
        onPointerEnter={onEnter}
        onPointerLeave={(e) => e.pointerType === 'mouse' && setPeek(null)}
        onFocus={(e) => peekable && e.currentTarget.matches(':focus-visible') && setPeek({ card: cell!.card!, rect: e.currentTarget.getBoundingClientRect(), k })}
        onBlur={() => setPeek(null)}
        aria-label={[label, cell?.blocked ? s.board.sealed : '', cell?.card?.title || '', pieces.length > 1 ? `×${pieces.length}` : '', dest ? s.board.playHere : ''].filter(Boolean).join(' · ')}
        title={dest ? s.board.playHere : undefined}
        style={cell?.mark ? ({ ...style, '--mark': resolveColor(cell.mark) } as CSSProperties) : style}
      >
        {cell?.blocked && <span className="pw-cell-sealed" aria-hidden />}
        {cell?.mark && <span className="pw-cell-mark" />}
        {cell?.text && <span className="pw-cell-text">{cell.text}</span>}
        {pieces.map((p, i) => (
          <Piece key={`${k}|${i}|${sig(p)}`} piece={p} anim={placeAnims?.get(i)} slot={stackSlot(i, pieces.length)} />
        ))}
        {cell?.card && <MiniCard key={`${k}|${cardSig(cell.card)}`} card={cell.card} seatColor={cell.card.seat !== undefined ? seatColor(cell.card.seat) : undefined} fresh={fresh.has(k)} />}
        {!selCard && cellMoves.has(k) && myTurn && !pieces.length && <span className="pw-cell-hint" style={{ '--pc': seatColor(table.my_seat) } as CSSProperties} />}
        {targets.has(k) && <span className="pw-cell-hint is-dest" />}
        {dest && <span className="pw-cell-slot" style={{ '--pc': seatColor(table.my_seat) } as CSSProperties} />}
        {preview?.has(k) && <span className="pw-cell-hint is-preview" />}
      </button>
    )
  }

  const boardEl = map ? (
    <MapBoard
      b={map}
      boardRef={boardRef}
      size={mapSize}
      onSize={setMapSize}
      choosing={!!cardDest}
      renderSpace={(sp, style, className) => (
        <div key={sp.id} className="pw-space-wrap" style={style}>
          {renderPlace(sp.id, sp, `pw-cell ${className}`, undefined, sp.label || sp.id)}
          {sp.label && <span className="pw-space-label">{sp.label}</span>}
        </div>
      )}
    />
  ) : (
    grid && (
      <div className="pw-bg-board-wrap">
        <div
          ref={boardRef}
          className={`pw-bg-board style-${grid.style || 'grid'} ${grid.theme ? `theme-${grid.theme}` : ''} ${cardDest ? 'is-choosing' : ''}`}
          style={{ '--rows': grid.rows, '--cols': grid.cols, aspectRatio: grid.style === 'hex' ? undefined : `${grid.cols} / ${grid.rows}` } as CSSProperties}
          onKeyDown={onBoardKey}
          onPointerLeave={() => setPeek((p) => (p ? null : p))}
        >
          {Array.from({ length: grid.rows }, (_, r) =>
            Array.from({ length: grid.cols }, (_, c) => {
              const cell = grid.cells?.[r]?.[c] || null
              const cls = ['pw-cell', (r + c) % 2 === 1 ? 'is-dark' : 'is-light', r === 0 ? 'r-first' : '', r === grid.rows - 1 ? 'r-last' : '', c === 0 ? 'c-first' : '', c === grid.cols - 1 ? 'c-last' : '', r % 2 ? 'r-odd' : ''].join(' ')
              const style = grid.style === 'hex' ? ({ gridRow: r + 1, gridColumn: `${c * 2 + (r % 2) + 1} / span 2` } as CSSProperties) : undefined
              return renderPlace(key(r, c), cell, cls, style, `r${r + 1} c${c + 1}`, { 'data-r': r, 'data-c': c })
            }),
          )}
        </div>
      </div>
    )
  )
  const sideZones = (a: 'left' | 'right') => zonesAt(a).length > 0 && <Zones zones={zonesAt(a)} {...zoneProps} side />
  const centerZones = zonesAt('center').length > 0 && <Zones zones={zonesAt('center')} {...zoneProps} center />

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

      {message && (
        <div className={`pw-bg-message ${myTurn ? 'is-turn' : ''}`} role="status">
          {myTurn && <span className="pw-wait-dot is-me" />}
          {message}
        </div>
      )}

      {storyMode ? (
        <div className="pw-bg-main is-story">
          <div className={`pw-bg-stage ${b ? '' : 'no-board'} ${otherZones.length === 0 && !(d.story && (d.story.length > 0 || cardsOnBoard)) ? 'no-side' : ''}`} style={dims ? ({ '--rows': dims.rows, '--cols': dims.cols } as CSSProperties) : undefined}>
            <div className="pw-bg-center">{boardEl}</div>
            <aside className="pw-bg-side">
              {otherZones.length > 0 && <Zones zones={otherZones} {...zoneProps} compact />}
              {d.story && (d.story.length > 0 || cardsOnBoard) && <StoryPanel story={d.story} seatColor={seatColor} seatName={seatName} reduced={reduced} />}
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
          {/* the table: other players' zones above, shared ones beside or in
              the middle, the board, then my own */}
          {zonesAt('top').length > 0 && <Zones zones={zonesAt('top')} {...zoneProps} />}
          <div className={`pw-table-row ${zonesAt('left').length ? 'has-left' : ''} ${zonesAt('right').length ? 'has-right' : ''}`}>
            {sideZones('left')}
            <div className="pw-table-mid">
              {centerZones}
              {boardEl}
            </div>
            {sideZones('right')}
          </div>
          {zonesAt('bottom').length > 0 && <Zones zones={zonesAt('bottom')} {...zoneProps} />}
          {myTurn && d.prompt && myZones.length > 0 && <p className={`pw-hand-prompt ${selCard ? 'is-sel' : ''}`}>{d.prompt}</p>}
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

// Shapes drawn as figures rather than discs: a silhouette in the player's
// colour, lit from the top left.
const FIGURES: Record<string, string> = {
  pawn: 'M50 8a15 15 0 0 1 9 27c9 5 14 14 15 25H26c1-11 6-20 15-25a15 15 0 0 1 9-27zM18 74h64l6 18H12z',
  meeple: 'M50 6c9 0 15 7 15 15 0 6-3 10-6 12 14 2 33 7 33 17 0 6-8 7-16 6l10 30c1 4-2 6-6 6H66L50 70 34 92H20c-4 0-7-2-6-6l10-30c-8 1-16 0-16-6 0-10 19-15 33-17-3-2-6-6-6-12 0-8 6-15 15-15z',
  cube: 'M50 6l40 20v48L50 94 10 74V26zM50 50L10 28M50 50l40-22M50 50v44',
  ship: 'M50 4c10 12 16 28 16 46l14 18-4 12-12-6-6 14H42l-6-14-12 6-4-12 14-18c0-18 6-34 16-46z',
  star: 'M50 4l13 30 32 3-24 22 7 32-28-17-28 17 7-32L5 37l32-3z',
  hex: 'M27 8h46l23 42-23 42H27L4 50z',
}

function Piece({ piece, anim, slot }: { piece: BoardPiece; anim?: Anim; slot?: CSSProperties }) {
  const shape = piece.shape || 'disc'
  const col = resolveColor(piece.color, '#e6e6e6')
  const fig = FIGURES[shape]
  const style = {
    ...slot,
    '--pc': col,
    '--dx': anim?.kind === 'glide' ? `${anim.dx ?? 0}px` : (anim?.dx ?? 0),
    '--dy': anim?.kind === 'glide' ? `${anim.dy ?? 0}px` : (anim?.dy ?? 0),
  } as CSSProperties
  return (
    <span className={`pw-piece shape-${shape} ${fig ? 'is-figure' : ''} ${anim ? `anim-${anim.kind}` : ''}`} style={style}>
      {fig && (
        <svg viewBox="0 0 100 100" aria-hidden>
          <path d={fig} />
        </svg>
      )}
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
  side = false,
  center = false,
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
  side?: boolean
  center?: boolean
}) {
  const { s } = usePlayT()
  if (!zones.length) return null
  return (
    <div className={`pw-zones ${mine ? 'is-mine' : ''} ${compact ? 'is-compact' : ''} ${tray ? 'is-tray' : ''} ${side ? 'is-side' : ''} ${center ? 'is-center' : ''}`}>
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
                const size = mine ? 'lg' : compact || side ? 'sm' : z.owner === undefined ? 'md' : 'sm'
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
