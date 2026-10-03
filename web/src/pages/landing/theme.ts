import { useCallback, useEffect, useLayoutEffect, useState } from 'react'
import { api } from '../../lib/api'

// The landing page's light/dark switch. It shares the app's `play.look`
// cache (key `theme`: dark | light | system) so the two stay in sync, but
// with one difference: with no saved choice the landing follows the OS,
// while the app stays dark. public/look.js applies the same rule before the
// first paint; keep the two in step.

export type Mode = 'light' | 'dark'

const LOOK = 'play.look'
const META_DARK = '#0a1020'
const META_LIGHT = '#f4f7fc'

function readLook(): Record<string, any> {
  try {
    return JSON.parse(localStorage.getItem(LOOK) || '{}') || {}
  } catch {
    return {}
  }
}

const osLight = () => !!window.matchMedia?.('(prefers-color-scheme: light)').matches

/** The landing's theme for a stored preference (undefined/system → the OS). */
export function landingMode(pref: unknown = readLook().theme): Mode {
  if (pref === 'light' || pref === 'dark') return pref
  return osLight() ? 'light' : 'dark'
}

/** The app's rule (src/app/prefs.tsx resolveTheme): dark unless asked otherwise. */
function appMode(pref: unknown): Mode {
  if (pref === 'light') return 'light'
  if (pref === 'system') return osLight() ? 'light' : 'dark'
  return 'dark'
}

function setMeta(color: string) {
  document.querySelector('meta[name="theme-color"]')?.setAttribute('content', color)
}

export function useLandingTheme(signedIn: boolean) {
  const [pref, setPref] = useState<unknown>(() => readLook().theme)
  const [os, setOs] = useState(osLight)
  const mode: Mode = pref === 'light' || pref === 'dark' ? pref : os ? 'light' : 'dark'

  // follow the OS live while there is no explicit choice
  useEffect(() => {
    const mq = window.matchMedia?.('(prefers-color-scheme: light)')
    if (!mq) return
    const on = () => setOs(mq.matches)
    mq.addEventListener?.('change', on)
    return () => mq.removeEventListener?.('change', on)
  }, [])

  // another tab (or the app) changed the cached look
  useEffect(() => {
    const on = (e: StorageEvent) => {
      if (e.key === LOOK) setPref(readLook().theme)
    }
    window.addEventListener('storage', on)
    return () => window.removeEventListener('storage', on)
  }, [])

  useLayoutEffect(() => {
    document.documentElement.dataset.theme = mode
    setMeta(mode === 'light' ? META_LIGHT : META_DARK)
  }, [mode])

  // leaving the landing: hand the page back to the app's rule
  useLayoutEffect(() => {
    document.body.classList.add('lp-body')
    return () => {
      document.body.classList.remove('lp-body')
      document.documentElement.dataset.theme = appMode(readLook().theme)
      setMeta(META_DARK)
    }
  }, [])

  const toggle = useCallback(() => {
    const next: Mode = mode === 'light' ? 'dark' : 'light'
    try {
      localStorage.setItem(LOOK, JSON.stringify({ ...readLook(), theme: next }))
    } catch {}
    setPref(next)
    if (signedIn) api.put('/api/settings', { preferences: { theme: next } }).catch(() => {})
  }, [mode, signedIn])

  return { mode, toggle }
}
