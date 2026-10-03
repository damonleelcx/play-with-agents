import { createContext, useCallback, useContext, useEffect, useRef, useState, type ReactNode } from 'react'
import { api, type User } from './api'
import { useI18n } from './i18n'

type Ctx = {
  user: User | null
  loading: boolean
  refresh: () => Promise<User | null>
  setUser: (u: User | null) => void
  signOut: () => Promise<void>
}

const Session = createContext<Ctx>(null as unknown as Ctx)

export function SessionProvider({ children }: { children: ReactNode }) {
  const [user, setUserState] = useState<User | null>(null)
  const [loading, setLoading] = useState(true)
  const userRef = useRef<User | null>(null)
  userRef.current = user
  const { adoptLang } = useI18n()

  const setUser = useCallback(
    (u: User | null) => {
      setUserState(u)
      if (u?.language) adoptLang(u.language)
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  )

  const refresh = useCallback(async () => {
    try {
      const u = await api.get<User>('/api/auth/me')
      setUser(u)
      return u
    } catch (e: any) {
      // Only a real "not signed in" signs the user out; a network blip or a
      // restarting server keeps whoever was here.
      if (e?.status === 401 || e?.status === 403 || e?.status === 404) {
        setUser(null)
        return null
      }
      return userRef.current
    } finally {
      setLoading(false)
    }
  }, [setUser])

  useEffect(() => {
    refresh()
  }, [refresh])

  const signOut = async () => {
    await api.post('/api/auth/signout').catch(() => {})
    setUserState(null)
  }

  return <Session.Provider value={{ user, loading, refresh, setUser, signOut }}>{children}</Session.Provider>
}

export const useSession = () => useContext(Session)

// Where to go once signed in and verified. Kept across sign-up and email
// verification so an invite link survives the whole trip. Same-site paths only:
// an open redirect would be a phishing aid.
export function safePath(p: string | null | undefined) {
  return p && p.startsWith('/') && !p.startsWith('//') && !p.startsWith('/\\') ? p : ''
}
export function rememberNext(path: string) {
  try { if (safePath(path)) sessionStorage.setItem('play.next', path) } catch {}
}
export function takeNext(fallback = '/app') {
  try {
    const p = safePath(sessionStorage.getItem('play.next'))
    sessionStorage.removeItem('play.next')
    return p || fallback
  } catch {
    return fallback
  }
}
