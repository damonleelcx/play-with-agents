import { useEffect, useRef, useState } from 'react'
import type { ChatLine } from '../../lib/playApi'
import { clipText } from './SidePanel'
import type { TypingNow } from './useTable'

// A seat's speech bubble on the felt: the line someone just said (for a
// few seconds, longer for longer lines), or "…" while they type it.
export type SeatBubble = { id: string; text: string } | { typing: true }
export type Bubbles = Record<number, SeatBubble>

export function useSeatBubbles(onLiveChat: (fn: (l: ChatLine) => void) => () => void, typing: TypingNow[]): Bubbles {
  const [said, setSaid] = useState<Record<number, { id: string; text: string }>>({})
  const timers = useRef(new Map<number, number>())
  useEffect(() => {
    const off = onLiveChat((l) => {
      // whispers stay in the chat panel; spectators have no seat to speak from
      if (l.whisper || l.seat < 0) return
      const id = String(l.id)
      setSaid((b) => ({ ...b, [l.seat]: { id, text: l.text } }))
      const ms = Math.min(9000, 3500 + Array.from(l.text).length * 60)
      clearTimeout(timers.current.get(l.seat))
      timers.current.set(
        l.seat,
        window.setTimeout(() => setSaid((b) => (b[l.seat]?.id === id ? omit(b, l.seat) : b)), ms),
      )
    })
    const ts = timers.current
    return () => {
      off()
      ts.forEach((t) => clearTimeout(t))
    }
  }, [onLiveChat])
  const out: Bubbles = {}
  for (const t of typing) if (t.seat >= 0) out[t.seat] = { typing: true }
  for (const [seat, b] of Object.entries(said)) out[+seat] = b
  return out
}

function omit<T>(o: Record<number, T>, k: number) {
  const c = { ...o }
  delete c[k]
  return c
}

export function SayBubble({ b, className = '' }: { b?: SeatBubble; className?: string }) {
  if (!b) return null
  if ('typing' in b)
    return (
      <span className={`pw-say is-typing ${className}`} aria-hidden>
        <span className="pw-typing-dots">
          <i />
          <i />
          <i />
        </span>
      </span>
    )
  return (
    <span key={b.id} className={`pw-say ${className}`} role="status">
      {clipText(b.text, 90)}
    </span>
  )
}
