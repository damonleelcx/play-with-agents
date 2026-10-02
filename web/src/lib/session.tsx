import { createContext, useCallback, useContext, useEffect, useState, type ReactNode } from 'react'
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
  const { setLang } = useI18n()

  const setUser = useCallback(
    (u: User | null) => {
      setUserState(u)
      if (u?.language) setLang(u.language)
    },
    // eslint-disable-next-line react-hooks/exhaustive-deps
    [],
  )

  const refresh = useCallback(async () => {
    try {
      const u = await api.get<User>('/api/auth/me')
      setUser(u)
      return u
    } catch {
      setUser(null)
      return null
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
