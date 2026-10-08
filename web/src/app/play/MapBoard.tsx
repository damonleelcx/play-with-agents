import { useId, useLayoutEffect, useMemo, type CSSProperties, type ReactNode, type RefObject } from 'react'
import { resolveColor, type MapBoard as MapData, type MapLink, type MapSpace } from '../../lib/playApi'

// A board that is not a grid: spaces anywhere (x, y in percent of the board),
// the paths between them and the regions under them. Tracks, routes, islands,
// star maps, networks of rooms — whatever the game's design draws.
//
// BoardView owns the game logic (which space is clickable, what a click
// plays); this component only lays the board out and asks renderSpace for
// each space's button.

export type SpaceRender = (sp: MapSpace, style: CSSProperties, className: string) => ReactNode

// The base space diameter: a share of the board's width, clamped so a small
// board stays clickable and a big one does not look inflated.
export function spaceUnit(width: number) {
  return Math.max(26, Math.min(64, width * 0.058))
}

export default function MapBoard({
  b,
  boardRef,
  size,
  onSize,
  choosing,
  renderSpace,
}: {
  b: MapData
  boardRef: RefObject<HTMLDivElement>
  size: { w: number; h: number }
  onSize: (s: { w: number; h: number }) => void
  choosing: boolean
  renderSpace: SpaceRender
}) {
  const aspect = b.aspect || 1.4
  useLayoutEffect(() => {
    const el = boardRef.current
    if (!el) return
    const measure = () => onSize({ w: el.clientWidth, h: el.clientHeight })
    measure()
    const ro = new ResizeObserver(measure)
    ro.observe(el)
    return () => ro.disconnect()
  }, [boardRef]) // eslint-disable-line react-hooks/exhaustive-deps

  const byId = useMemo(() => new Map(b.spaces.map((s) => [s.id, s])), [b.spaces])
  const unit = spaceUnit(size.w)
  const theme = b.theme || 'night'

  return (
    <div className="pw-bg-board-wrap">
      <div
        ref={boardRef}
        className={`pw-map theme-${theme} ${choosing ? 'is-choosing' : ''} ${b.grid ? 'has-guides' : ''}`}
        style={{ '--aspect': aspect, aspectRatio: String(aspect), '--u': `${unit}px` } as CSSProperties}
      >
        {(b.regions || []).map((r, i) => (
          <div
            key={`r${i}`}
            className={`pw-map-region shape-${r.shape || 'rect'}`}
            style={{ left: `${r.x}%`, top: `${r.y}%`, width: `${r.w}%`, height: `${r.h}%`, '--rc': resolveColor(r.color, '#8fb6ff') } as CSSProperties}
          >
            {r.label && <span className="pw-map-region-label">{r.label}</span>}
          </div>
        ))}
        {size.w > 0 && <Links links={b.links || []} byId={byId} w={size.w} h={size.h} unit={unit} />}
        {b.spaces.map((sp) => {
          const z = sp.size || 1
          const style = {
            left: `${sp.x}%`,
            top: `${sp.y}%`,
            '--sz': z,
            '--sc': sp.color ? resolveColor(sp.color) : undefined,
          } as CSSProperties
          return renderSpace(sp, style, `pw-space shape-${sp.shape || 'circle'}`)
        })}
      </div>
    </div>
  )
}

// The paths, drawn in pixels so strokes and arrowheads keep their shape
// whatever the board's proportions. Each path stops at the edge of its
// spaces instead of running under them.
function Links({ links, byId, w, h, unit }: { links: MapLink[]; byId: Map<string, MapSpace>; w: number; h: number; unit: number }) {
  const id = `m${useId().replace(/[^a-zA-Z0-9]/g, '')}`
  return (
    <svg className="pw-map-links" viewBox={`0 0 ${w} ${h}`} width={w} height={h} aria-hidden>
      <defs>
        <marker id={`${id}-arrow`} viewBox="0 0 10 10" refX="8" refY="5" markerWidth="7" markerHeight="7" orient="auto-start-reverse" markerUnits="userSpaceOnUse">
          <path d="M0,0 L10,5 L0,10 z" fill="context-stroke" />
        </marker>
      </defs>
      {links.map((l, i) => {
        const a = byId.get(l.from)
        const b = byId.get(l.to)
        if (!a || !b) return null
        const ax = (a.x / 100) * w, ay = (a.y / 100) * h
        const bx = (b.x / 100) * w, by = (b.y / 100) * h
        const dx = bx - ax, dy = by - ay
        const len = Math.hypot(dx, dy) || 1
        const ra = ((a.size || 1) * unit) / 2 + 2
        const rb = ((b.size || 1) * unit) / 2 + (l.style === 'arrow' ? 4 : 2)
        if (len <= ra + rb) return null
        const x1 = ax + (dx / len) * ra, y1 = ay + (dy / len) * ra
        const x2 = bx - (dx / len) * rb, y2 = by - (dy / len) * rb
        const style = l.style || 'line'
        const col = l.color ? resolveColor(l.color) : undefined
        return (
          <g key={i} className={`pw-link style-${style}`} style={col ? ({ '--lc': col } as CSSProperties) : undefined}>
            {(style === 'road' || style === 'river' || style === 'bridge' || style === 'rail') && <line className="pw-link-base" x1={x1} y1={y1} x2={x2} y2={y2} />}
            <line className="pw-link-line" x1={x1} y1={y1} x2={x2} y2={y2} markerEnd={style === 'arrow' ? `url(#${id}-arrow)` : undefined} />
            {l.label && (
              <text className="pw-link-label" x={(x1 + x2) / 2} y={(y1 + y2) / 2 - 6} textAnchor="middle">
                {l.label}
              </text>
            )}
          </g>
        )
      })}
    </svg>
  )
}

// Where piece i of n sits on a place: one piece fills it; several share it
// in a ring (2–6) or a tight cluster (more), each a little smaller.
export function stackSlot(i: number, n: number): CSSProperties | undefined {
  if (n <= 1) return undefined
  const size = n <= 2 ? 62 : n <= 4 ? 54 : n <= 6 ? 46 : 40
  const r = n <= 2 ? 20 : n <= 6 ? 24 : 28
  const a = -Math.PI / 2 + (2 * Math.PI * i) / n + (n === 2 ? Math.PI / 2 : 0)
  return {
    inset: 'auto',
    width: `${size}%`,
    height: `${size}%`,
    left: `${50 + r * Math.cos(a) - size / 2}%`,
    top: `${50 + r * Math.sin(a) - size / 2}%`,
  }
}
