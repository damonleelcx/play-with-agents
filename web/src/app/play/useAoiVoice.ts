import { useEffect, useRef, useState } from 'react'
import type { ChatLine, TableView } from '../../lib/playApi'
import { speakTableLine, stop, unlock, useVoice } from '../../lib/voice'

// Aoi's table talk read aloud (Settings → Aoi → table_voice). Only lines that
// arrive live (SSE) are spoken, never history; other agents stay text.
// Playback, caching and the autoplay unlock live in lib/voice.ts.

export function isAoiLine(line: ChatLine, table: TableView | null) {
  if (!line.agent) return false
  const seat = table?.seats.find((s) => s.seat === line.seat)
  return seat?.agent_id === 'aoi' || (!!line.avatar && /\/aoi\.webp$/.test(line.avatar))
}

const KEY_PREFIX = 'table-line:'

// Returns the seat Aoi is speaking from (for the sound-wave indicator, -1 when
// silent), and whether the browser is holding audio back until a tap.
export function useAoiVoice(
  enabled: boolean,
  subscribe: (fn: (line: ChatLine) => void) => () => void,
  tableRef: { current: TableView | null },
  simulate = false, // mock mode: show the indicator even without a voice endpoint
): { seat: number; blocked: boolean; unlock: () => void } {
  const v = useVoice()
  const seats = useRef(new Map<string, number>())
  const [simSeat, setSimSeat] = useState(-1)
  const [held, setHeld] = useState(false) // a line of ours is waiting for a tap
  const keyRef = useRef<string | null>(null)
  keyRef.current = v.key ?? null

  useEffect(() => {
    if (!enabled) return
    let timer: number | undefined
    const off = subscribe((line) => {
      if (!isAoiLine(line, tableRef.current)) return
      const key = KEY_PREFIX + line.id
      seats.current.set(key, line.seat)
      if (seats.current.size > 50) seats.current.delete(seats.current.keys().next().value as string)
      const id = tableRef.current?.id ?? ''
      speakTableLine(id, Number(line.id), key).then((r) => {
        setHeld(r === 'blocked')
        if (r === 'played' || r === 'blocked' || !simulate) return
        setSimSeat(line.seat)
        clearTimeout(timer)
        timer = window.setTimeout(() => setSimSeat(-1), Math.min(12000, Math.max(1500, line.text.length * 70)))
      })
    })
    return () => {
      off()
      clearTimeout(timer)
      setSimSeat(-1)
      if (keyRef.current?.startsWith(KEY_PREFIX)) stop()
    }
  }, [enabled, subscribe, tableRef, simulate])

  const ours = !!v.key && seats.current.has(v.key)
  const live = (v.speaking || v.loading) && ours ? seats.current.get(v.key!)! : -1
  return {
    seat: live >= 0 ? live : simSeat,
    blocked: enabled && held && v.blocked,
    unlock: () => {
      setHeld(false)
      unlock()
    },
  }
}
