import { useEffect, useState, type CSSProperties } from 'react'

// Images that may not exist yet (art lands separately from code): each <Img>
// walks a list of sources and, when all fail, renders a branded fallback, so
// a missing file never shows a broken-image icon.
export function Img({ srcs, alt = '', className, style, fallback, width, height }: {
  srcs: string[]; alt?: string; className?: string; style?: CSSProperties; fallback?: React.ReactNode; width?: number; height?: number
}) {
  const [i, setI] = useState(0)
  const key = srcs.join('|')
  useEffect(() => setI(0), [key])
  if (i >= srcs.length) return <>{fallback ?? null}</>
  return <img src={srcs[i]} alt={alt} className={className} style={style} width={width} height={height} onError={() => setI((n) => n + 1)} draggable={false} />
}

export type Mood = 'neutral' | 'smile' | 'wink' | 'surprised' | 'angry' | 'sad'
export const MOODS: Mood[] = ['neutral', 'smile', 'wink', 'surprised', 'angry', 'sad']

// A monogram tile for when the art is missing: 葵 on the brand gradient.
function Mono({ text, size }: { text: string; size: number }) {
  return <span className="mono-tile" style={{ fontSize: Math.round(size * 0.46) }} aria-hidden="true">{text}</span>
}

export function AoiFace({ mood = 'neutral', size = 36, pulse, ring, speaking }: { mood?: Mood; size?: number; pulse?: boolean; ring?: boolean; speaking?: boolean }) {
  return (
    <span className={`aoi-face ${pulse ? 'pulse' : ''} ${ring ? 'ringed' : ''} ${speaking ? 'speaking' : ''}`} style={{ width: size, height: size }}>
      <Img key={mood} srcs={[`/play/aoi/aoi-face-${mood}.webp`, '/play/agents/aoi.webp', '/play/aoi/aoi-face-neutral.webp']} alt="Aoi" width={size} height={size}
        fallback={<Mono text="葵" size={size} />} />
    </span>
  )
}

export function AgentAvatar({ id, name, src, size = 36 }: { id: string; name: string; src?: string; size?: number }) {
  return (
    <span className={`agent-av av-${id}`} style={{ width: size, height: size }}>
      <Img srcs={[src, `/play/agents/${id}.webp`].filter(Boolean) as string[]} alt={name} width={size} height={size}
        fallback={<Mono text={[...name][0] || '?'} size={size} />} />
    </span>
  )
}

// A rough read of a reply's mood, for messages that don't say: the face
// follows what she says, not a random pick.
export function guessMood(text: string): Mood {
  const s = text.toLowerCase()
  if (/(😉|;\)|just kidding|bluff|wink|开玩笑|诈唬|嘿嘿)/.test(s)) return 'wink'
  if (/(sorry|unfortunately|bad beat|ouch|lost|抱歉|可惜|遗憾|输了)/.test(s)) return 'sad'
  if (/(hmph|not fair|how dare|grr|哼|不服|可恶)/.test(s)) return 'angry'
  if (/(whoa|wow|no way|what\?!|!\?|really\?|哇|真的吗|竟然|天哪)/.test(s)) return 'surprised'
  if (/(!|great|nice|yay|welcome|let's|ready|好耶|太好了|欢迎|来吧|～)/.test(s)) return 'smile'
  return 'neutral'
}
