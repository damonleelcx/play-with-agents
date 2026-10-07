import { useEffect, useLayoutEffect, useRef, useState, type CSSProperties } from 'react'
import { resolveColor, type BoardCard, type StoryLine } from '../../lib/playApi'
import { usePlayT } from './strings'

// Rich story cards (title, story line, effect, kind, cost/value), their
// mini form on board cells, and the "Story so far" panel. See the board view
// schema in docs/00-architecture.md.

export const isRich = (c?: BoardCard | null) => !!c && !c.hidden && !!(c.title || c.text || c.effect || c.kind)

// Preset kind colours and emblems; any other kind gets a stable hue.
const KINDS: Record<string, { color: string; glyph: string }> = {
  character: { color: '#e7b75f', glyph: '♛' },
  place: { color: '#4fbfa5', glyph: '⌂' },
  event: { color: '#7aa2ff', glyph: '✦' },
  twist: { color: '#c58cff', glyph: '↻' },
  item: { color: '#ff8f6b', glyph: '⚷' },
}

export function kindStyle(kind?: string, accent?: string): { color: string; glyph: string } {
  const k = (kind || '').toLowerCase().trim()
  const preset = KINDS[k]
  let color = preset?.color
  if (!color) {
    let h = 0
    for (const ch of k) h = (h * 31 + ch.charCodeAt(0)) % 360
    color = k ? `hsl(${h} 62% 66%)` : '#d9c7a0'
  }
  return { color: accent ? resolveColor(accent) : color, glyph: preset?.glyph || (k ? k[0].toUpperCase() : '❖') }
}

type Size = 'sm' | 'md' | 'lg' | 'xl'

export function StoryCard({ card, size = 'md', seatColor, className = '' }: { card: BoardCard; size?: Size; seatColor?: string; className?: string }) {
  const { s } = usePlayT()
  if (card.hidden) return <CardBack size={size} className={className} label={s.board.hiddenCard} />
  const k = kindStyle(card.kind, card.accent)
  const style = { '--kc': k.color, ...(seatColor ? { '--sc': seatColor } : {}) } as CSSProperties
  return (
    <div className={`pw-sc pw-sc--${size} ${seatColor ? 'has-seat' : ''} ${className}`} style={style}>
      <div className="pw-sc-frame">
        <div className="pw-sc-head">
          {card.cost !== undefined && card.cost !== null && <span className="pw-sc-cost">{card.cost}</span>}
          <span className="pw-sc-title">{card.title || card.face || ''}</span>
          {card.value !== undefined && card.value !== null && <span className="pw-sc-value">{card.value}</span>}
        </div>
        <div className="pw-sc-emblem">
          <span aria-hidden>{k.glyph}</span>
          {card.kind && <em className="pw-sc-kind">{card.kind}</em>}
        </div>
        <div className="pw-sc-body">
          {card.text && <p className="pw-sc-text">{card.text}</p>}
          {card.effect && <div className="pw-sc-effect">{card.effect}</div>}
        </div>
      </div>
    </div>
  )
}

export function CardBack({ size = 'md', className = '', label }: { size?: Size; className?: string; label?: string }) {
  return (
    <div className={`pw-sc pw-sc--${size} is-back ${className}`} role="img" aria-label={label}>
      <div className="pw-sc-back">
        <span className="pw-sc-back-medal">✦</span>
      </div>
    </div>
  )
}

// The compact form of a card on a board cell: title on the kind colour,
// framed in the colour of the player who played it.
export function MiniCard({ card, seatColor, fresh }: { card: BoardCard; seatColor?: string; fresh?: boolean }) {
  if (card.hidden) return <span className="pw-mini is-back" aria-hidden />
  const k = kindStyle(card.kind, card.accent)
  return (
    <span className={`pw-mini ${fresh ? 'is-fresh' : ''}`} style={{ '--kc': k.color, '--sc': seatColor || 'rgba(255,255,255,.25)' } as CSSProperties}>
      <span className="pw-mini-band">
        <i>{k.glyph}</i>
        {card.value !== undefined && card.value !== null && <b>{card.value}</b>}
      </span>
      <span className="pw-mini-title">{card.title || card.face || ''}</span>
    </span>
  )
}

// A full card floating over the page (board hover / tap), clamped to the
// viewport.
export function CardPeek({ card, rect, seatColor, caption }: { card: BoardCard; rect: DOMRect; seatColor?: string; caption?: string }) {
  const ref = useRef<HTMLDivElement>(null)
  const [pos, setPos] = useState<{ left: number; top: number } | null>(null)
  useLayoutEffect(() => {
    const el = ref.current
    if (!el) return
    const w = el.offsetWidth
    const h = el.offsetHeight
    const vw = window.innerWidth
    const vh = window.innerHeight
    let left = rect.left + rect.width / 2 - w / 2
    left = Math.max(8, Math.min(vw - w - 8, left))
    let top = rect.top - h - 10
    if (top < 8) top = Math.min(vh - h - 8, rect.bottom + 10)
    setPos({ left, top })
  }, [rect])
  return (
    <div ref={ref} className="pw-peek" role="tooltip" style={pos ? { left: pos.left, top: pos.top } : { left: -9999, top: 0 }}>
      <StoryCard card={card} size="lg" seatColor={seatColor} />
      {caption && <small className="pw-peek-cap" style={{ '--sc': seatColor } as CSSProperties}>{caption}</small>}
    </div>
  )
}

// ── Story so far ──────────────────────────────────────────────────────────

export function StoryPanel({
  story,
  seatColor,
  seatName,
  reduced,
}: {
  story: StoryLine[]
  seatColor: (s: number) => string
  seatName: (s: number) => string
  reduced: boolean
}) {
  const { s, f } = usePlayT()
  const narrow = useNarrow()
  const [open, setOpen] = useState(!narrow)
  useEffect(() => setOpen(!narrow), [narrow])
  const list = useRef<HTMLOListElement>(null)

  // The newest line types itself in once; older ones are static.
  const seen = useRef(story.length)
  const [typing, setTyping] = useState<{ i: number; n: number } | null>(null)
  useEffect(() => {
    const last = story.length - 1
    if (story.length > seen.current && last >= 0 && !reduced) {
      const full = story[last].text.length
      let n = 0
      setTyping({ i: last, n: 0 })
      const step = Math.max(1, Math.ceil(full / 60))
      const id = window.setInterval(() => {
        n += step
        if (n >= full) {
          window.clearInterval(id)
          setTyping(null)
        } else setTyping({ i: last, n })
      }, 22)
      seen.current = story.length
      return () => window.clearInterval(id)
    }
    seen.current = story.length
  }, [story, reduced])

  useEffect(() => {
    const el = list.current
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: reduced ? 'auto' : 'smooth' })
  }, [story.length, open, typing?.n, reduced])

  const last = story.length - 1
  const shown = open ? story : story.slice(-1)
  const offset = open ? 0 : Math.max(0, last)
  return (
    <section className={`pw-story ${open ? 'is-open' : 'is-closed'}`} aria-label={s.board.storySoFar}>
      <button type="button" className="pw-story-head" onClick={() => setOpen((o) => !o)} aria-expanded={open}>
        <span className="pw-story-mark" aria-hidden>❦</span>
        <b>{s.board.storySoFar}</b>
        <small>{f(s.board.storyLines, { n: story.length })}</small>
        <span className="pw-story-toggle">{open ? s.board.hideStory : s.board.showStory}</span>
      </button>
      {story.length === 0 ? (
        <p className="pw-story-empty">{s.board.storyEmpty}</p>
      ) : (
        <ol ref={list} className="pw-story-lines" aria-live="polite">
          {shown.map((line, j) => {
            const i = j + offset
            const col = line.seat !== undefined ? seatColor(line.seat) : 'var(--pw-gold)'
            const t = typing && typing.i === i ? line.text.slice(0, typing.n) : line.text
            return (
              <li key={i} className={`pw-story-line ${i === last ? 'is-new' : ''}`} style={{ '--sc': col } as CSSProperties}>
                <span className="pw-story-num">{i + 1}</span>
                <div>
                  {(line.title || line.seat !== undefined) && (
                    <small className="pw-story-meta">
                      {line.title && <em>{line.title}</em>}
                      {line.seat !== undefined && <span>{seatName(line.seat)}</span>}
                    </small>
                  )}
                  <p>
                    {t}
                    {typing && typing.i === i && <span className="pw-story-caret" aria-hidden />}
                  </p>
                </div>
              </li>
            )
          })}
        </ol>
      )}
    </section>
  )
}

export function useNarrow(q = '(max-width: 900px)') {
  const [m, setM] = useState(() => typeof window !== 'undefined' && window.matchMedia(q).matches)
  useEffect(() => {
    const mq = window.matchMedia(q)
    const on = () => setM(mq.matches)
    on()
    mq.addEventListener('change', on)
    return () => mq.removeEventListener('change', on)
  }, [q])
  return m
}
