import { useEffect, useState, type CSSProperties } from 'react'
import { PLAYER_COLORS } from '../../lib/playApi'
import '../../styles/play.css'
import { parseCard } from './poker'

// ── Avatar: agent art, or initials on a seat colour for humans ────────────
export function initials(name: string) {
  const parts = (name || '?').trim().split(/\s+/)
  const s = parts.length > 1 ? parts[0][0] + parts[parts.length - 1][0] : parts[0].slice(0, 2)
  return s.toUpperCase()
}

export function Avatar({
  name,
  src,
  seat = 0,
  size = 44,
  agent = false,
  className = '',
}: {
  name: string
  src?: string
  seat?: number
  size?: number
  agent?: boolean
  className?: string
}) {
  const [broken, setBroken] = useState(false)
  useEffect(() => setBroken(false), [src])
  const color = PLAYER_COLORS[((seat % 8) + 8) % 8]
  const style = { width: size, height: size, '--av': color, fontSize: Math.max(11, size * 0.36) } as CSSProperties
  if (src && !broken)
    return (
      <span className={`pw-av ${agent ? 'is-agent' : ''} ${className}`} style={style}>
        <img src={src} alt="" loading="lazy" draggable={false} onError={() => setBroken(true)} />
      </span>
    )
  return (
    <span className={`pw-av pw-av-initials ${agent ? 'is-agent' : ''} ${className}`} style={style} aria-hidden>
      {initials(name)}
    </span>
  )
}

export function agentAvatar(id?: string) {
  return id ? `/play/agents/${id}.webp` : ''
}

// ── Playing cards, drawn in CSS ────────────────────────────────────────────
export type CardBackStyle = 'aoi' | 'classic' | 'midnight'

export function PlayingCard({
  card,
  hidden = false,
  back = 'aoi',
  fourColor = false,
  size = 'md',
  reveal = false,
  deal = false,
  delay = 0,
  from,
  className = '',
  dim = false,
  glow = false,
}: {
  card?: string | null
  hidden?: boolean
  back?: CardBackStyle
  fourColor?: boolean
  size?: 'xs' | 'sm' | 'md' | 'lg'
  reveal?: boolean // flip from back to face on mount
  deal?: boolean // slide in from the dealer on mount
  delay?: number
  from?: { x: string; y: string }
  className?: string
  dim?: boolean
  glow?: boolean
}) {
  const faceDown = hidden || !card
  const c = card ? parseCard(card) : null
  const style = {
    '--d': `${delay}ms`,
    ...(from ? { '--from-x': from.x, '--from-y': from.y } : {}),
  } as CSSProperties
  const cls = [
    'pw-card',
    `pw-card--${size}`,
    faceDown ? 'is-down' : '',
    reveal && !faceDown ? 'is-reveal' : '',
    deal ? 'is-deal' : '',
    dim ? 'is-dim' : '',
    glow ? 'is-glow' : '',
    className,
  ].join(' ')
  return (
    <div className={cls} style={style} aria-label={faceDown ? 'card face down' : `${c!.label}${c!.glyph}`} role="img">
      <div className="pw-card-inner">
        <div className={`pw-card-face pw-card-front suit-${c?.suit || 's'} ${fourColor ? 'four' : 'two'}`}>
          {c && (
            <>
              <span className="pw-card-corner">
                <b>{c.label}</b>
                <i>{c.glyph}</i>
              </span>
              {'JQK'.includes(c.rank) ? (
                <span className="pw-card-court">
                  <b>{c.rank}</b>
                  <i>{c.glyph}</i>
                </span>
              ) : (
                <span className="pw-card-pip">{c.glyph}</span>
              )}
              <span className="pw-card-corner br">
                <b>{c.label}</b>
                <i>{c.glyph}</i>
              </span>
            </>
          )}
        </div>
        <div className={`pw-card-face pw-card-back back-${back}`}>
          {back === 'aoi' && <span className="pw-back-emblem" style={{ backgroundImage: 'url(/play/agents/aoi.webp)' }} />}
          {back === 'classic' && <span className="pw-back-diamond" />}
          {back === 'midnight' && <span className="pw-back-moon" />}
        </div>
      </div>
    </div>
  )
}

// A generic face card for board-game zones ("7♥", "Q", "+2").
export function ZoneCard({ face, color, hidden, back = 'aoi', size = 'md' }: { face?: string; color?: string; hidden?: boolean; back?: CardBackStyle; size?: 'sm' | 'md' | 'lg' }) {
  if (hidden)
    return (
      <div className={`pw-card pw-card--${size} is-down`}>
        <div className="pw-card-inner">
          <div className="pw-card-face pw-card-front" />
          <div className={`pw-card-face pw-card-back back-${back}`}>
            {back === 'aoi' && <span className="pw-back-emblem" style={{ backgroundImage: 'url(/play/agents/aoi.webp)' }} />}
            {back === 'classic' && <span className="pw-back-diamond" />}
            {back === 'midnight' && <span className="pw-back-moon" />}
          </div>
        </div>
      </div>
    )
  const m = /^(.*?)([♠♥♦♣])$/.exec(face || '')
  return (
    <div className={`pw-card pw-card--${size}`}>
      <div className="pw-card-inner">
        <div className="pw-card-face pw-card-front pw-zone-face" style={{ color: color || '#111827' }}>
          {m ? (
            <>
              <span className="pw-card-corner">
                <b>{m[1]}</b>
                <i>{m[2]}</i>
              </span>
              <span className="pw-card-pip">{m[2]}</span>
              <span className="pw-card-corner br">
                <b>{m[1]}</b>
                <i>{m[2]}</i>
              </span>
            </>
          ) : (
            <span className="pw-zone-text">{face}</span>
          )}
        </div>
        <div className="pw-card-face pw-card-back" />
      </div>
    </div>
  )
}

// ── Turn timer ring, driven by the table deadline ─────────────────────────
export function TimerRing({ deadline, total, size }: { deadline: string | null; total: number; size: number }) {
  if (!deadline || !total) return null
  const end = Date.parse(deadline)
  const remaining = Math.max(0, (end - Date.now()) / 1000)
  const elapsed = Math.max(0, total - remaining)
  const r = size / 2 - 2
  const circ = 2 * Math.PI * r
  return (
    <svg className="pw-timer" width={size} height={size} viewBox={`0 0 ${size} ${size}`} key={deadline} aria-hidden>
      <circle cx={size / 2} cy={size / 2} r={r} className="pw-timer-track" />
      <circle
        cx={size / 2}
        cy={size / 2}
        r={r}
        className="pw-timer-bar"
        style={{ strokeDasharray: circ, '--circ': circ, animationDuration: `${total}s`, animationDelay: `-${elapsed}s` } as CSSProperties}
      />
    </svg>
  )
}

export function Spinner() {
  return <span className="pw-spin" aria-hidden />
}

// Sound-wave indicator shown on Aoi's seat while her line is read aloud.
export function VoiceWave({ inline = false }: { inline?: boolean }) {
  return (
    <span className={`pw-voice ${inline ? 'is-inline' : ''}`} role="img" aria-label="speaking">
      {[0, 1, 2, 3, 4].map((i) => (
        <i key={i} style={{ '--i': i } as CSSProperties} />
      ))}
    </span>
  )
}
