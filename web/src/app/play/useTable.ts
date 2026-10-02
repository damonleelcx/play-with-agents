import { useCallback, useEffect, useRef, useState } from 'react'
import { ApiError } from '../../lib/api'
import { playApi, postMove, type ChatLine, type Move, type TableView } from '../../lib/playApi'
import { createMock, type MockGame } from './fixtures'

export type Conn = 'connecting' | 'live' | 'reconnecting' | 'offline'
export type Toast = { id: number; kind: 'error' | 'info'; text: string }

export type TableClient = {
  table: TableView | null
  loading: boolean
  notFound: boolean
  conn: Conn
  busy: boolean
  mock: boolean
  toasts: Toast[]
  dismiss: (id: number) => void
  notify: (kind: Toast['kind'], text: string) => void
  move: (m: Move) => Promise<void>
  chat: (text: string) => Promise<void>
  setSeat: (seat: number, kind: 'agent' | 'open', agentId?: string) => Promise<void>
  start: () => Promise<void>
  leave: () => Promise<boolean>
  rematch: () => Promise<string | null>
  // live chat lines (SSE, or new mock lines), never history
  onLiveChat: (fn: (line: ChatLine) => void) => () => void
}

let toastSeq = 0

// useTable keeps one table in sync: GET on mount, an SSE stream that tells us
// when to refetch (deduped by version, bursts coalesced into one fetch),
// reconnect with backoff, a refetch whenever the tab becomes visible again,
// and moves with optimistic disabling. With `mock` it runs a local fixture.
export function useTable(id: string, mockKind: string | null, staleText = 'The table moved on.'): TableClient {
  const [table, setTable] = useState<TableView | null>(null)
  const [loading, setLoading] = useState(true)
  const [notFound, setNotFound] = useState(false)
  const [conn, setConn] = useState<Conn>('connecting')
  const [busy, setBusy] = useState(false)
  const [toasts, setToasts] = useState<Toast[]>([])
  const versionRef = useRef(-1)
  const mockRef = useRef<MockGame | null>(null)
  const isMock = !!mockKind
  const chatSeen = useRef(new Set<string>())
  const liveSubs = useRef(new Set<(l: ChatLine) => void>())
  const onLiveChat = useCallback((fn: (l: ChatLine) => void) => {
    liveSubs.current.add(fn)
    return () => {
      liveSubs.current.delete(fn)
    }
  }, [])
  const markSeen = (t: TableView) => t.chat.forEach((c) => chatSeen.current.add(c.id))

  const notify = useCallback((kind: Toast['kind'], text: string) => {
    const tid = ++toastSeq
    setToasts((ts) => [...ts.slice(-2), { id: tid, kind, text }])
    setTimeout(() => setToasts((ts) => ts.filter((x) => x.id !== tid)), kind === 'error' ? 5200 : 3200)
  }, [])
  const dismiss = useCallback((tid: number) => setToasts((ts) => ts.filter((x) => x.id !== tid)), [])

  const adopt = useCallback((t: TableView | null | undefined) => {
    if (!t) return
    if (t.version >= versionRef.current) {
      versionRef.current = t.version
      markSeen(t)
      setTable(t)
    }
  }, [])

  // ── real mode: fetch with coalescing ──
  const fetching = useRef(false)
  const again = useRef(false)
  const refetch = useCallback(async () => {
    if (isMock) return
    if (fetching.current) {
      again.current = true
      return
    }
    fetching.current = true
    try {
      do {
        again.current = false
        const t = await playApi.table(id)
        adopt(t)
        setNotFound(false)
      } while (again.current)
    } catch (e) {
      if (e instanceof ApiError && (e.status === 404 || e.status === 403)) setNotFound(true)
    } finally {
      fetching.current = false
      setLoading(false)
    }
  }, [id, isMock, adopt])

  useEffect(() => {
    if (isMock) return
    versionRef.current = -1
    setTable(null)
    setLoading(true)
    refetch()
  }, [id, isMock, refetch])

  // ── real mode: SSE ──
  useEffect(() => {
    if (isMock) return
    let es: EventSource | null = null
    let attempt = 0
    let timer: number | undefined
    let closed = false
    const connect = () => {
      if (closed) return
      setConn(attempt === 0 ? 'connecting' : 'reconnecting')
      try {
        es = new EventSource(playApi.streamURL(id), { withCredentials: true })
      } catch {
        schedule()
        return
      }
      es.onopen = () => {
        if (attempt > 0) refetch()
        attempt = 0
        setConn('live')
      }
      es.addEventListener('table', (ev) => {
        try {
          const v = JSON.parse((ev as MessageEvent).data)?.version
          if (typeof v !== 'number' || v > versionRef.current) refetch()
        } catch {
          refetch()
        }
      })
      es.addEventListener('chat', (ev) => {
        try {
          const line = JSON.parse((ev as MessageEvent).data) as ChatLine
          if (!line?.id || chatSeen.current.has(line.id)) return
          chatSeen.current.add(line.id)
          liveSubs.current.forEach((fn) => fn(line))
          setTable((t) => (t && !t.chat.some((c) => c.id === line.id) ? { ...t, chat: [...t.chat, line].slice(-80) } : t))
        } catch {}
      })
      es.onerror = () => {
        es?.close()
        es = null
        schedule()
      }
    }
    const schedule = () => {
      if (closed) return
      attempt++
      setConn(navigator.onLine === false ? 'offline' : 'reconnecting')
      const delay = Math.min(15000, 800 * 2 ** Math.min(attempt - 1, 5)) * (0.75 + Math.random() * 0.5)
      timer = window.setTimeout(connect, delay)
    }
    const onVis = () => {
      if (document.visibilityState !== 'visible') return
      refetch()
      if (!es) {
        clearTimeout(timer)
        connect()
      }
    }
    const onOnline = () => onVis()
    connect()
    document.addEventListener('visibilitychange', onVis)
    window.addEventListener('online', onOnline)
    return () => {
      closed = true
      clearTimeout(timer)
      es?.close()
      document.removeEventListener('visibilitychange', onVis)
      window.removeEventListener('online', onOnline)
    }
  }, [id, isMock, refetch])

  // ── mock mode ──
  const [mockTick, setMockTick] = useState(0)
  const syncMock = useCallback(() => {
    const m = mockRef.current
    if (!m) return
    const v = m.view()
    versionRef.current = v.version
    const first = chatSeen.current.size === 0
    for (const c of v.chat)
      if (!chatSeen.current.has(c.id)) {
        chatSeen.current.add(c.id)
        if (!first) liveSubs.current.forEach((fn) => fn(c))
      }
    setTable(v)
  }, [])
  useEffect(() => {
    if (!isMock) return
    mockRef.current = createMock(mockKind)
    if (!mockRef.current) {
      setNotFound(true)
      setLoading(false)
      return
    }
    syncMock()
    setLoading(false)
    setConn('live')
  }, [isMock, mockKind, syncMock])
  useEffect(() => {
    if (!isMock || !mockRef.current) return
    const ms = mockRef.current.pending()
    if (ms == null) return
    const tm = window.setTimeout(() => {
      try {
        mockRef.current?.step()
      } catch (e) {
        console.warn('mock step', e)
      }
      syncMock()
      setMockTick((x) => x + 1)
    }, ms)
    return () => clearTimeout(tm)
  }, [isMock, table?.version, mockTick, syncMock])

  // A new version always re-enables the action bar.
  useEffect(() => setBusy(false), [table?.version])

  const move = useCallback(
    async (m: Move) => {
      if (!table || busy) return
      setBusy(true)
      if (isMock) {
        await new Promise((r) => setTimeout(r, 140))
        try {
          mockRef.current!.apply(table.my_seat, m)
          syncMock()
        } catch (e: any) {
          notify('error', e?.message || 'Illegal move')
          setBusy(false)
        }
        return
      }
      const r = await postMove(table.id, table.version, m)
      if (r.ok) {
        adopt(r.table)
        setBusy(false)
        return
      }
      if (r.status === 409) {
        if (r.table) {
          versionRef.current = -1
          adopt(r.table)
        } else refetch()
        notify('info', staleText)
      } else {
        notify('error', r.error)
      }
      setBusy(false)
    },
    [table, busy, isMock, syncMock, notify, adopt, refetch, staleText],
  )

  const chat = useCallback(
    async (text: string) => {
      if (!table) return
      if (isMock) {
        mockRef.current!.chat(text)
        syncMock()
        setTimeout(syncMock, 700)
        return
      }
      try {
        await playApi.chat(table.id, text)
      } catch (e: any) {
        notify('error', e?.message || 'Could not send')
      }
    },
    [table, isMock, syncMock, notify],
  )

  const setSeat = useCallback(
    async (seat: number, kind: 'agent' | 'open', agentId?: string) => {
      if (!table) return
      if (isMock) {
        mockRef.current?.setSeat?.(seat, kind, agentId)
        syncMock()
        return
      }
      try {
        const t = await playApi.setSeat(table.id, seat, kind === 'open' ? { kind: 'open' } : { kind: 'agent', agent_id: agentId || 'aoi' })
        adopt(t)
      } catch (e: any) {
        notify('error', e?.message || 'Could not change the seat')
      }
    },
    [table, isMock, syncMock, adopt, notify],
  )

  const start = useCallback(async () => {
    if (!table) return
    if (isMock) {
      mockRef.current?.start?.()
      syncMock()
      return
    }
    try {
      adopt(await playApi.start(table.id))
    } catch (e: any) {
      notify('error', e?.message || 'Could not start')
    }
  }, [table, isMock, syncMock, adopt, notify])

  const leave = useCallback(async () => {
    if (!table || isMock) return true
    try {
      await playApi.leave(table.id)
      return true
    } catch (e: any) {
      notify('error', e?.message || 'Could not leave')
      return false
    }
  }, [table, isMock, notify])

  const rematch = useCallback(async () => {
    if (!table) return null
    if (isMock) {
      mockRef.current = createMock(mockKind === 'holdem-showdown' || mockKind === 'finished' ? 'holdem' : mockKind)
      syncMock()
      return null
    }
    try {
      const t = await playApi.rematch(table.id)
      return t.id
    } catch (e: any) {
      notify('error', e?.message || 'Could not start a rematch')
      return null
    }
  }, [table, isMock, mockKind, syncMock, notify])

  return { table, loading, notFound, conn, busy, mock: isMock, toasts, dismiss, notify, move, chat, setSeat, start, leave, rematch, onLiveChat }
}
