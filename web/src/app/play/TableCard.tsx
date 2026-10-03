import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import { playApi, type HoldemData, type TableView } from '../../lib/playApi'
import { createMock } from './fixtures'
import { Avatar, Spinner } from './parts'
import { fmtChips } from './poker'
import { usePlayT } from './strings'

// A compact live card for chat embeds (Aoi's meta.cards: {kind:"table"}).
// It listens to the table stream and refetches when the version moves.
export default function TableCard({ tableId, mock }: { tableId: string; mock?: string }) {
  const { s } = usePlayT()
  const [t, setT] = useState<TableView | null>(null)
  const [missing, setMissing] = useState(false)
  const ver = useRef(-1)

  useEffect(() => {
    if (mock) {
      const m = createMock(mock)
      if (m) setT(m.view())
      return
    }
    let live = true
    let es: EventSource | null = null
    let timer: number | undefined
    let attempt = 0
    const fetchIt = () =>
      playApi
        .table(tableId)
        .then((x) => {
          if (!live || x.version < ver.current) return
          ver.current = x.version
          setT(x)
        })
        .catch(() => live && setMissing(true))
    const connect = () => {
      if (!live) return
      es = new EventSource(playApi.streamURL(tableId), { withCredentials: true })
      es.onopen = () => (attempt = 0)
      es.addEventListener('table', (ev) => {
        try {
          const v = JSON.parse((ev as MessageEvent).data)?.version
          if (typeof v !== 'number' || v > ver.current) fetchIt()
        } catch {
          fetchIt()
        }
      })
      es.onerror = () => {
        es?.close()
        es = null
        attempt++
        timer = window.setTimeout(connect, Math.min(30000, 1000 * 2 ** Math.min(attempt, 5)))
      }
    }
    fetchIt()
    connect()
    return () => {
      live = false
      clearTimeout(timer)
      es?.close()
    }
  }, [tableId, mock])

  if (!t)
    return (
      <div className="pw-tcard is-loading">
        {missing ? <span className="pw-muted">{s.card.missing}</span> : <><Spinner /> <span className="pw-muted">{s.card.loading}</span></>}
      </div>
    )
  const hd = t.view?.kind === 'holdem' ? (t.view.data as HoldemData) : null
  const href = mock ? `/app/table/demo?mock=${mock}` : `/app/table/${t.id}`
  return (
    <div className={`pw-tcard st-${t.status}`}>
      <div className="pw-tcard-head">
        <span className="pw-tcard-game">{t.game.name}</span>
        <span className={`pw-pill st-${t.status}`}>
          {t.status === 'playing' && <span className="pw-live-dot" />}
          {s.card.status[t.status]}
        </span>
      </div>
      <b className="pw-tcard-name">{t.name}</b>
      <div className="pw-tcard-seats">
        {t.seats.map((st) => (
          <span key={st.seat} className={`pw-tcard-seat ${t.to_move.includes(st.seat) ? 'is-active' : ''}`} title={st.name || s.room.openSeat}>
            {st.kind === 'open' ? (
              <span className="pw-av pw-av-empty" style={{ width: 28, height: 28 }} />
            ) : (
              <Avatar name={st.name} src={st.avatar} seat={st.seat} size={28} agent={st.kind === 'agent'} />
            )}
          </span>
        ))}
      </div>
      <div className="pw-tcard-foot">
        <small className="pw-muted">
          {hd ? `${s.holdem.pot} ${fmtChips(hd.pot_total)} · ${s.holdem.streets[hd.street] || ''}` : t.view?.status || t.outcome?.summary || `${t.seats.filter((x) => x.kind !== 'open').length}/${t.seats.length}`}
        </small>
        <Link to={href} className="pw-btn pw-btn-primary pw-btn-sm">
          {s.card.open}
        </Link>
      </div>
    </div>
  )
}
