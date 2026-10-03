import { useEffect, useState } from 'react'
import { api } from '../../lib/api'

// Table preferences (Settings → Table / Agents), with sensible defaults while
// loading or when the settings endpoint is unavailable.
export type PlayPrefs = {
  felt: 'navy' | 'emerald' | 'crimson'
  card_back: 'aoi' | 'classic' | 'midnight'
  four_color_deck: boolean
  show_hand_strength: boolean
  sound: boolean
  motion: 'full' | 'reduced'
  turn_seconds: number
  agent_difficulty: 'casual' | 'regular' | 'shark'
  favorite_agents: string[]
  table_voice: boolean
}

export const DEFAULT_PREFS: PlayPrefs = {
  felt: 'navy',
  card_back: 'aoi',
  four_color_deck: false,
  show_hand_strength: true,
  sound: true,
  motion: 'full',
  turn_seconds: 30,
  agent_difficulty: 'regular',
  favorite_agents: [],
  table_voice: false,
}

let cache: PlayPrefs | null = null
let inflight: Promise<PlayPrefs> | null = null

function bool(v: any, d: boolean) {
  if (typeof v === 'boolean') return v
  if (v === 'true') return true
  if (v === 'false') return false
  return d
}

function normalise(p: Record<string, any>): PlayPrefs {
  const d = DEFAULT_PREFS
  const oneOf = <T extends string>(v: any, opts: readonly T[], def: T): T => (opts.includes(v) ? v : def)
  let fav = p.favorite_agents
  if (typeof fav === 'string') fav = fav.split(',').map((x: string) => x.trim()).filter(Boolean)
  const ts = Number(p.turn_seconds)
  return {
    felt: oneOf(p.felt, ['navy', 'emerald', 'crimson'] as const, d.felt),
    card_back: oneOf(p.card_back, ['aoi', 'classic', 'midnight'] as const, d.card_back),
    four_color_deck: bool(p.four_color_deck, d.four_color_deck),
    show_hand_strength: bool(p.show_hand_strength, d.show_hand_strength),
    sound: bool(p.sound, d.sound),
    motion: oneOf(p.motion, ['full', 'reduced'] as const, d.motion),
    turn_seconds: [0, 15, 30, 60].includes(ts) ? ts : d.turn_seconds,
    agent_difficulty: oneOf(p.agent_difficulty, ['casual', 'regular', 'shark'] as const, d.agent_difficulty),
    favorite_agents: Array.isArray(fav) ? fav : d.favorite_agents,
    table_voice: bool(p.table_voice, d.table_voice),
  }
}

function load(): Promise<PlayPrefs> {
  if (!inflight) {
    inflight = api
      .get<{ preferences?: Record<string, any> }>('/api/settings')
      .then((r) => (cache = normalise(r?.preferences || {})))
      .catch(() => (cache = { ...DEFAULT_PREFS }))
  }
  return inflight
}

export function usePlayPrefs(overrides?: Partial<PlayPrefs>): PlayPrefs {
  const [prefs, setPrefs] = useState<PlayPrefs>(cache || DEFAULT_PREFS)
  useEffect(() => {
    let live = true
    load().then((p) => live && setPrefs(p))
    return () => {
      live = false
    }
  }, [])
  // The OS-level reduced-motion setting is honoured too.
  const osReduce = typeof window !== 'undefined' && window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
  const merged = { ...prefs, ...(overrides || {}) }
  if (osReduce) merged.motion = 'reduced'
  return merged
}
