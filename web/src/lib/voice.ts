import { useEffect, useState } from 'react'

// Aoi's own voice (Fish Audio, via POST /api/speech). There is deliberately
// no speechSynthesis fallback: if her voice isn't available, she is silent
// and every voice control hides itself.
//
//   voiceEnabled()       → Promise<boolean> (GET /api/speech, asked once)
//   speak(text, key?)    → plays it on one shared <audio>, stopping whatever
//                          was playing; resolves 'played' | 'blocked' | 'off' | 'error'
//   stop()               → stops the current utterance
//   setVoiceVolume(0..100)
//   useVoice()           → { enabled, speaking, key, loading, blocked, unlock }

type State = { enabled: boolean | null; speaking: boolean; loading: boolean; key: string | null; blocked: boolean }
let state: State = { enabled: null, speaking: false, loading: false, key: null, blocked: false }
const listeners = new Set<(s: State) => void>()
function set(p: Partial<State>) {
  state = { ...state, ...p }
  listeners.forEach((l) => l(state))
}

let probe: Promise<boolean> | null = null
export function voiceEnabled(): Promise<boolean> {
  if (!probe) {
    probe = fetch('/api/speech', { credentials: 'same-origin' })
      .then((r) => (r.ok ? r.json() : { enabled: false }))
      .then((d) => !!d?.enabled)
      .catch(() => false)
      .then((on) => {
        set({ enabled: on })
        return on
      })
  }
  return probe
}

// What she says, not how it's formatted: markdown, links, code and emoji go.
export function speechText(md: string) {
  return md
    .replace(/```[\s\S]*?```/g, ' ')
    .replace(/`([^`]*)`/g, '$1')
    .replace(/!\[[^\]]*\]\([^)]*\)/g, ' ')
    .replace(/\[([^\]]+)\]\([^)]*\)/g, '$1')
    .replace(/^\s{0,3}(#{1,6}|>|[-*+]|\d+[.)])\s+/gm, '')
    .replace(/(\*\*|__|\*|_|~~)(.+?)\1/g, '$2')
    .replace(/\|/g, ' ')
    .replace(/[\p{Extended_Pictographic}\u{FE0F}\u{200D}]/gu, '')
    .replace(/\s+/g, ' ')
    .trim()
    .slice(0, 600)
}

let audio: HTMLAudioElement | null = null
let volume = 0.8
let token = 0
let pending: { url: string; key: string | null } | null = null
const cache = new Map<string, string>() // text → object URL, oldest first

function el() {
  if (!audio) {
    audio = new Audio()
    audio.preload = 'auto'
    audio.addEventListener('ended', () => set({ speaking: false, key: null }))
    audio.addEventListener('pause', () => set({ speaking: false }))
    audio.addEventListener('playing', () => set({ speaking: true, loading: false }))
  }
  audio.volume = volume
  return audio
}

export function setVoiceVolume(v: number) {
  volume = Math.max(0, Math.min(1, (Number.isFinite(v) ? v : 80) / 100))
  if (audio) audio.volume = volume
}

export function stop() {
  token++
  pending = null
  if (audio) {
    audio.pause()
    audio.currentTime = 0
  }
  set({ speaking: false, loading: false, key: null })
}

async function urlFor(text: string): Promise<string | null> {
  const hit = cache.get(text)
  if (hit) {
    cache.delete(text) // refresh its place in the LRU
    cache.set(text, hit)
    return hit
  }
  const res = await fetch('/api/speech', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'X-Play': '1' },
    body: JSON.stringify({ text }),
  })
  if (!res.ok) {
    if (res.status === 503) set({ enabled: false })
    return null
  }
  const url = URL.createObjectURL(await res.blob())
  cache.set(text, url)
  if (cache.size > 40) {
    const [oldText, oldUrl] = cache.entries().next().value as [string, string]
    cache.delete(oldText)
    URL.revokeObjectURL(oldUrl)
  }
  return url
}

async function play(url: string, key: string | null): Promise<'played' | 'blocked' | 'error'> {
  const a = el()
  a.src = url
  try {
    await a.play()
    set({ speaking: true, loading: false, key, blocked: false })
    return 'played'
  } catch (e: any) {
    if (e?.name === 'NotAllowedError') {
      // The browser wants a tap first. Keep this one ready for it.
      pending = { url, key }
      set({ blocked: true, loading: false, speaking: false, key: null })
      return 'blocked'
    }
    set({ loading: false, speaking: false, key: null })
    return 'error'
  }
}

export async function speak(text: string, key?: string): Promise<'played' | 'blocked' | 'off' | 'error'> {
  const clean = speechText(text)
  if (!clean) return 'off'
  if (!(await voiceEnabled())) return 'off'
  stop()
  const mine = ++token
  set({ loading: true, key: key ?? clean })
  try {
    const url = await urlFor(clean)
    if (mine !== token) return 'off' // superseded while fetching
    if (!url) {
      set({ loading: false, key: null })
      return 'error'
    }
    return await play(url, key ?? clean)
  } catch {
    if (mine === token) set({ loading: false, key: null })
    return 'error'
  }
}

// Called from a user gesture (the "tap to enable" chip): plays what was held back.
export async function unlock() {
  const p = pending
  pending = null
  set({ blocked: false })
  if (p) await play(p.url, p.key)
}

export function useVoice() {
  const [s, setS] = useState(state)
  useEffect(() => {
    listeners.add(setS)
    setS(state)
    voiceEnabled()
    return () => {
      listeners.delete(setS)
    }
  }, [])
  return { ...s, enabled: s.enabled === true, speak, stop, unlock }
}
