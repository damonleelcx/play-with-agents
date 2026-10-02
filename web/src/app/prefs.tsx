import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { IconAlert, IconCheck } from '../components/Icons'
import { api, type SettingsData } from '../lib/api'
import { useI18n, type Lang } from '../lib/i18n'
import { setVoiceVolume } from '../lib/voice'
import { useSession } from '../lib/session'

// ── Toasts ────────────────────────────────────────────────────────────────
type Toast = { id: number; text: string; kind: 'ok' | 'error' | 'info' }
type ToastCtx = (text: string, kind?: Toast['kind']) => void
const ToastC = createContext<ToastCtx>(() => {})
export const useToast = () => useContext(ToastC)

export function ToastProvider({ children }: { children: ReactNode }) {
  const [items, setItems] = useState<Toast[]>([])
  const n = useRef(0)
  const push = useCallback<ToastCtx>((text, kind = 'ok') => {
    const id = ++n.current
    // A burst of instant saves shows one toast, not a stack.
    setItems((xs) => [...xs.filter((x) => x.text !== text), { id, text, kind }].slice(-3))
    window.setTimeout(() => setItems((xs) => xs.filter((x) => x.id !== id)), kind === 'error' ? 4800 : 2200)
  }, [])
  return (
    <ToastC.Provider value={push}>
      {children}
      <div className="toasts" role="status" aria-live="polite">
        {items.map((x) => (
          <div key={x.id} className={`toast toast-${x.kind}`}>
            {x.kind === 'error' ? <IconAlert size={16} /> : <IconCheck size={16} />}
            <span>{x.text}</span>
          </div>
        ))}
      </div>
    </ToastC.Provider>
  )
}

// ── Preferences ───────────────────────────────────────────────────────────
// Preferences live on the server (GET/PUT /api/settings). The theme is also
// cached locally so a reload paints in the right colours before the API answers.
type Prefs = Record<string, any>
type PrefsCtx = {
  data: SettingsData | null
  prefs: Prefs
  error: string
  reload: () => void
  save: (patch: { name?: string; preferences?: Prefs }, quiet?: boolean) => Promise<boolean>
}
const PrefsC = createContext<PrefsCtx>(null as unknown as PrefsCtx)
export const usePrefs = () => useContext(PrefsC)

export function resolveTheme(pref: string | undefined): 'dark' | 'light' {
  if (pref === 'light') return 'light'
  if (pref === 'system') return window.matchMedia?.('(prefers-color-scheme: light)').matches ? 'light' : 'dark'
  return 'dark' // the app is dark unless asked otherwise
}

export function applyLook(p: Prefs) {
  const d = document.documentElement
  d.dataset.theme = resolveTheme(p.theme)
  d.dataset.fs = p.font_size || 'medium'
  if (p.motion === 'reduced') d.dataset.motion = 'reduce'
  else delete d.dataset.motion
}

function cached(): Prefs {
  try {
    return JSON.parse(localStorage.getItem('play.look') || '{}')
  } catch {
    return {}
  }
}

export function PrefsProvider({ children }: { children: ReactNode }) {
  const { t, f, setLang } = useI18n()
  const { user, setUser } = useSession()
  const toast = useToast()
  const [data, setData] = useState<SettingsData | null>(null)
  const [prefs, setPrefs] = useState<Prefs>(cached)
  const [error, setError] = useState('')

  const accept = useCallback((d: SettingsData) => {
    setData(d)
    const p = d.preferences || {}
    setPrefs(p)
    try {
      localStorage.setItem('play.look', JSON.stringify({ theme: p.theme, font_size: p.font_size, motion: p.motion }))
    } catch {}
  }, [])

  const reload = useCallback(() => {
    api.get<SettingsData>('/api/settings').then((d) => { accept(d); setError('') }).catch((e) => setError(e.message))
  }, [accept])
  useEffect(reload, [reload])

  useEffect(() => {
    applyLook(prefs)
    document.body.classList.add('themed-body')
    if (prefs.theme !== 'system') return
    const mq = window.matchMedia?.('(prefers-color-scheme: light)')
    const on = () => applyLook(prefs)
    mq?.addEventListener?.('change', on)
    return () => mq?.removeEventListener?.('change', on)
  }, [prefs])
  useEffect(() => () => document.body.classList.remove('themed-body'), [])
  useEffect(() => setVoiceVolume(prefs.voice_volume ?? 80), [prefs.voice_volume])

  const save = useCallback<PrefsCtx['save']>(async (patch, quiet) => {
    // Optimistic: the control flips now; a failure flips it back.
    const before = prefs
    if (patch.preferences) setPrefs((p) => ({ ...p, ...patch.preferences }))
    try {
      const d = await api.put<SettingsData>('/api/settings', patch)
      if (d && d.preferences) accept(d)
      if (patch.preferences?.language) setLang(patch.preferences.language as Lang)
      if (patch.name !== undefined && user) setUser({ ...user, name: d?.user?.name ?? patch.name })
      if (!quiet) toast(t.settings.saved)
      return true
    } catch (e: any) {
      setPrefs(before)
      toast(f(t.settings.saveFailed, { e: e.message === 'offline' ? t.common.offline : e.message }), 'error')
      return false
    }
  }, [prefs, accept, setLang, user, setUser, toast, t, f])

  return <PrefsC.Provider value={{ data, prefs, error, reload, save }}>{children}</PrefsC.Provider>
}
