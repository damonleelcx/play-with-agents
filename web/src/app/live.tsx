import { createContext, useContext, useEffect, useState, type ReactNode } from 'react'

// One server-sent event stream per tab. The server says only "something
// changed for you"; views refetch what they show. Simple, and never stale for
// long, because a reconnect also counts as a change.
type Live = { tick: number; online: boolean }
const LiveCtx = createContext<Live>({ tick: 0, online: true })

export function LiveProvider({ children }: { children: ReactNode }) {
  const [tick, setTick] = useState(0)
  const [online, setOnline] = useState(true)
  useEffect(() => {
    let es: EventSource | null = null
    let retry = 0
    let timer = 0
    const open = () => {
      es = new EventSource('/api/stream', { withCredentials: true })
      es.addEventListener('hello', () => {
        setOnline(true)
        retry = 0
        setTick((t) => t + 1)
      })
      es.addEventListener('refresh', () => setTick((t) => t + 1))
      es.onerror = () => {
        setOnline(false)
        es?.close()
        timer = window.setTimeout(open, Math.min(30000, 1000 * 2 ** retry++))
      }
    }
    open()
    return () => {
      es?.close()
      clearTimeout(timer)
    }
  }, [])
  return <LiveCtx.Provider value={{ tick, online }}>{children}</LiveCtx.Provider>
}

export const useLive = () => useContext(LiveCtx)
