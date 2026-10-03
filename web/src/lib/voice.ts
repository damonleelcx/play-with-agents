import { useEffect, useState } from 'react'

// Aoi's own voice (Fish Audio, via POST /api/speech). There is deliberately
// no speechSynthesis fallback: if her voice isn't available, she is silent
// and every voice control hides itself.
//
// The server only speaks lines it already holds as Aoi's, so callers name a
// line rather than send text:
//
//   voiceEnabled()                    → Promise<boolean> (GET /api/speech, asked once)
//   speakMessage(id, key?)            → one of her chat replies (a saved message id)
//   speakTableLine(tableId, chatId, key?) → one of her table-talk lines
//   speakSample(lang, key?)           → the fixed "Hear Aoi" sample ('en' | 'zh')
//   Each plays on one shared <audio>, stopping whatever was playing, and
//   resolves 'played' | 'blocked' | 'off' | 'error'.
//   stop()                            → stops the current utterance
//   setVoiceVolume(0..100)
//   useVoice()                        → { enabled, speaking, key, loading, blocked, unlock }

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

let audio: HTMLAudioElement | null = null
let volume = 0.8
let token = 0
let pending: { url: string; key: string | null } | null = null
const cache = new Map<string, string>() // line id → object URL, oldest first

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

type SpeechRequest = { message_id: number } | { table_id: string; chat_id: number } | { sample: 'en' | 'zh' }

// lineId names a line for the local cache: the same line always sounds the same.
function lineId(req: SpeechRequest) {
  if ('message_id' in req) return `m:${req.message_id}`
  if ('table_id' in req) return `t:${req.table_id}:${req.chat_id}`
  return `s:${req.sample}`
}

async function urlFor(req: SpeechRequest): Promise<string | null> {
  const id = lineId(req)
  const hit = cache.get(id)
  if (hit) {
    cache.delete(id) // refresh its place in the LRU
    cache.set(id, hit)
    return hit
  }
  const res = await fetch('/api/speech', {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'X-Play': '1' },
    body: JSON.stringify(req),
  })
  if (!res.ok) {
    if (res.status === 503) set({ enabled: false })
    return null
  }
  const url = URL.createObjectURL(await res.blob())
  cache.set(id, url)
  if (cache.size > 40) {
    const [oldId, oldUrl] = cache.entries().next().value as [string, string]
    cache.delete(oldId)
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

export type SpeakResult = 'played' | 'blocked' | 'off' | 'error'

async function say(req: SpeechRequest, key?: string): Promise<SpeakResult> {
  if (!(await voiceEnabled())) return 'off'
  stop()
  const mine = ++token
  const k = key ?? lineId(req)
  set({ loading: true, key: k })
  try {
    const url = await urlFor(req)
    if (mine !== token) return 'off' // superseded while fetching
    if (!url) {
      set({ loading: false, key: null })
      return 'error'
    }
    return await play(url, k)
  } catch {
    if (mine === token) set({ loading: false, key: null })
    return 'error'
  }
}

// One of Aoi's saved chat replies. A reply still streaming has no server id
// yet (a negative placeholder) and cannot be spoken.
export function speakMessage(id: number, key?: string): Promise<SpeakResult> {
  if (!Number.isInteger(id) || id <= 0) return Promise.resolve('off')
  return say({ message_id: id }, key)
}

// One of Aoi's table-talk lines at a table the viewer may watch.
export function speakTableLine(tableId: string, chatId: number, key?: string): Promise<SpeakResult> {
  if (!tableId || !Number.isInteger(chatId) || chatId <= 0) return Promise.resolve('off')
  return say({ table_id: tableId, chat_id: chatId }, key)
}

// The fixed "Hear Aoi" sample of the settings page.
export function speakSample(lang: 'en' | 'zh', key?: string): Promise<SpeakResult> {
  return say({ sample: lang === 'zh' ? 'zh' : 'en' }, key)
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
  return { ...s, enabled: s.enabled === true, speakMessage, speakTableLine, speakSample, stop, unlock }
}
