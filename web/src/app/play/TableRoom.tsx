import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { Link, useNavigate, useParams, useSearchParams } from 'react-router-dom'
import { playApi, type Agent, type HoldemData, type TableView } from '../../lib/playApi'
import BoardView from './BoardView'
import { ROSTER } from './fixtures'
import HoldemTable from './HoldemTable'
import { Avatar, Spinner } from './parts'
import { fmtChips } from './poker'
import SidePanel from './SidePanel'
import { usePlayT } from './strings'
import { usePlayPrefs, type PlayPrefs } from './usePrefs'
import { useAoiVoice } from './useAoiVoice'
import { useTable, type TableClient } from './useTable'

export function useAgents() {
  const [agents, setAgents] = useState<Agent[]>(ROSTER)
  useEffect(() => {
    let live = true
    playApi
      .agents()
      .then((a) => live && Array.isArray(a) && a.length && setAgents(a))
      .catch(() => {})
    return () => {
      live = false
    }
  }, [])
  return agents
}

// The side panel (players + chat) needs ~1200px of room next to a felt; below
// that it becomes a drawer. Measured on the room itself, not the viewport,
// because the app sidebar may be expanded or collapsed.
const PANEL_MIN_ROOM = 1200
const NARROW_ROOM = 720

function useWidth(el: HTMLElement | null) {
  const [w, setW] = useState(() => (typeof window !== 'undefined' ? window.innerWidth : 1280))
  useEffect(() => {
    if (!el) return
    const ro = new ResizeObserver(() => setW(el.clientWidth))
    ro.observe(el)
    setW(el.clientWidth)
    return () => ro.disconnect()
  }, [el])
  return w
}

export default function TableRoom({ tableId, mock }: { tableId?: string; mock?: string | null } = {}) {
  const params = useParams()
  const [sp] = useSearchParams()
  const id = tableId || params.id || 'demo'
  const mockKind = mock !== undefined ? mock : sp.get('mock')
  const { s } = usePlayT()
  const client = useTable(id, mockKind, s.room.stale)
  const prefs = usePlayPrefs()
  const nav = useNavigate()
  const [roomEl, setRoomEl] = useState<HTMLDivElement | null>(null)
  const roomW = useWidth(roomEl)
  const compact = roomW < PANEL_MIN_ROOM
  const narrow = roomW < NARROW_ROOM
  const [panelOpen, setPanelOpen] = useState(false)
  useEffect(() => {
    if (!compact) setPanelOpen(false)
  }, [compact])
  const [confirmLeave, setConfirmLeave] = useState(false)
  const { table } = client
  const tableRef = useRef(table)
  tableRef.current = table
  const voiceOn = prefs.table_voice || (client.mock && sp.get('voice') === '1')
  const voice = useAoiVoice(voiceOn, client.onLiveChat, tableRef, client.mock)
  const speakingSeat = voice.seat

  if (client.loading && !table)
    return (
      <div className={`pw-room felt-${prefs.felt}`}>
        <div className="pw-center-note">
          <Spinner />
          {s.room.loading}
        </div>
      </div>
    )
  if (client.notFound || !table)
    return (
      <div className={`pw-room felt-${prefs.felt}`}>
        <div className="pw-center-note">
          <p>{s.room.notFound}</p>
          <Link className="pw-btn pw-btn-soft" to="/app/games">
            {s.room.backToLobby}
          </Link>
        </div>
      </div>
    )

  const spectator = table.my_seat < 0
  const isHoldem = table.view?.kind === 'holdem'
  const stacks: (string | null)[] = table.seats.map((st) => {
    if (isHoldem) {
      const p = (table.view!.data as HoldemData).players?.[st.seat]
      return p ? (p.status === 'out' ? s.holdem.out : fmtChips(p.stack)) : null
    }
    const p = (table.view?.data?.players || []).find((x: any) => x.seat === st.seat)
    return p?.score !== undefined ? String(p.score) : null
  })

  const mine = table.seats.find((st) => st.is_me)
  const meAway = table.status === 'playing' && !!mine?.away
  const paused = table.status === 'playing' && !!table.paused

  const leave = async () => {
    setConfirmLeave(false)
    if (await client.leave()) nav('/app/games')
  }

  const side = <SidePanel table={table} onChat={client.chat} stacks={stacks} speakingSeat={speakingSeat} onClose={compact ? () => setPanelOpen(false) : undefined} />

  return (
    <div ref={setRoomEl} className={`pw-room felt-${prefs.felt} ${compact ? 'is-compact' : ''} ${narrow ? 'is-narrow' : ''}`} data-motion={prefs.motion}>
      <header className="pw-room-bar">
        <Link to="/app/games" className="pw-back" aria-label={s.room.back}>
          <span aria-hidden>‹</span>
          {!narrow && s.room.back}
        </Link>
        <div className="pw-room-title">
          <b>{table.name}</b>
          <small>
            {table.game.name}
            {table.status !== 'playing' && <span className={`pw-pill st-${table.status}`}>{s.card.status[table.status]}</span>}
            {spectator && <span className="pw-pill st-spec">{s.room.spectating}</span>}
          </small>
        </div>
        <ConnBadge conn={client.conn} mock={client.mock} />
        <div className="pw-room-actions">
          {compact && (
            <button
              type="button"
              className={`pw-icon-btn pw-chat-toggle ${narrow ? '' : 'is-wide'}`}
              onClick={() => setPanelOpen(true)}
              aria-label={s.room.playersChat}
              aria-expanded={panelOpen}
            >
              <svg width="18" height="18" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round">
                <path d="M21 15a2 2 0 0 1-2 2H7l-4 4V5a2 2 0 0 1 2-2h14a2 2 0 0 1 2 2z" />
              </svg>
              {!narrow && <span>{s.room.playersChat}</span>}
              {table.chat.length > 0 && <span className="pw-dot" />}
            </button>
          )}
          {!spectator && table.status !== 'finished' && table.status !== 'abandoned' && (
            <button type="button" className="pw-btn pw-btn-ghost pw-btn-sm" onClick={() => setConfirmLeave(true)}>
              {s.room.leave}
            </button>
          )}
        </div>
      </header>

      <div className="pw-room-body">
        <main className="pw-room-main">
          {(meAway || paused) && (
            <div className={`pw-away-banner ${meAway ? 'is-away' : 'is-paused'}`} role="status">
              <span>{meAway ? s.room.awayNote : s.room.pausedNote}</span>
              <button type="button" className="pw-btn pw-btn-primary" onClick={client.back} autoFocus={meAway}>
                {meAway ? s.room.imBack : s.room.resume}
              </button>
            </div>
          )}
          {table.status === 'lobby' && <LobbyStage table={table} client={client} />}
          {table.status === 'playing' && table.view?.kind === 'holdem' && <HoldemTable table={table} busy={client.busy} onMove={client.move} prefs={prefs} speakingSeat={speakingSeat} />}
          {table.status === 'playing' && table.view && table.view.kind !== 'holdem' && <BoardView table={table} busy={client.busy} onMove={client.move} prefs={prefs} speakingSeat={speakingSeat} />}
          {table.status === 'finished' && <Finished table={table} client={client} prefs={prefs} />}
          {table.status === 'abandoned' && (
            <div className="pw-center-note">
              <p>{table.outcome?.summary || s.room.abandoned}</p>
              <Link className="pw-btn pw-btn-soft" to="/app/games">
                {s.room.backToLobby}
              </Link>
            </div>
          )}
        </main>
        {!compact && side}
      </div>

      {compact && panelOpen && (
        <div className={`pw-drawer ${narrow ? '' : 'is-side'}`} onClick={(e) => e.target === e.currentTarget && setPanelOpen(false)}>
          <div className="pw-drawer-sheet">{side}</div>
        </div>
      )}

      {voice.blocked && (
        <button type="button" className="pw-voice-unlock" onClick={voice.unlock}>
          <span className="pw-voice is-inline">
            <i style={{ '--i': 0 } as CSSProperties} />
            <i style={{ '--i': 1 } as CSSProperties} />
            <i style={{ '--i': 2 } as CSSProperties} />
          </span>
          {s.room.tapVoice}
        </button>
      )}

      <div className="pw-toasts" aria-live="polite">
        {client.toasts.map((t) => (
          <div key={t.id} className={`pw-toast is-${t.kind}`} onClick={() => client.dismiss(t.id)}>
            {t.kind === 'error' && <b>{s.room.moveFailed}</b>}
            <span>{t.text}</span>
          </div>
        ))}
      </div>

      {confirmLeave && (
        <div className="pw-modal" role="dialog" aria-modal="true" onClick={(e) => e.target === e.currentTarget && setConfirmLeave(false)}>
          <div className="pw-modal-card pw-confirm">
            <p>{s.room.leaveConfirm}</p>
            <div className="pw-modal-actions">
              <button type="button" className="pw-btn pw-btn-ghost" onClick={() => setConfirmLeave(false)} autoFocus>
                {s.room.stay}
              </button>
              <button type="button" className="pw-btn pw-btn-danger" onClick={leave}>
                {s.room.leaveYes}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  )
}

function ConnBadge({ conn, mock }: { conn: TableClient['conn']; mock: boolean }) {
  const { s } = usePlayT()
  if (mock) return <span className="pw-conn is-live">DEMO</span>
  if (conn === 'live') return <span className="pw-conn is-live">{s.room.live}</span>
  return (
    <span className={`pw-conn is-${conn}`}>
      <Spinner />
      {conn === 'offline' ? s.room.offline : s.room.reconnecting}
    </span>
  )
}

// ── lobby: seats, invite, start ───────────────────────────────────────────
function LobbyStage({ table, client }: { table: TableView; client: TableClient }) {
  const { s, lang } = usePlayT()
  const agents = useAgents()
  const [copied, setCopied] = useState(false)
  const [picker, setPicker] = useState<number | null>(null)
  const [starting, setStarting] = useState(false)
  const link = `${location.origin}/join/${table.code}`
  const copy = async () => {
    try {
      await navigator.clipboard.writeText(link)
    } catch {
      const ta = document.createElement('textarea')
      ta.value = link
      document.body.appendChild(ta)
      ta.select()
      document.execCommand('copy')
      ta.remove()
    }
    setCopied(true)
    setTimeout(() => setCopied(false), 1800)
  }
  const start = async () => {
    setStarting(true)
    await client.start()
    setStarting(false)
  }
  return (
    <div className="pw-lobby-stage">
      <div className="pw-lobby-head">
        <div>
          <span className="pw-eyebrow">{table.game.name}</span>
          <h2>{s.room.lobbyTitle}</h2>
        </div>
        <div className="pw-invite">
          <small>{s.room.invite}</small>
          <code>{table.code}</code>
          <button type="button" className="pw-btn pw-btn-soft pw-btn-sm" onClick={copy}>
            {copied ? `✓ ${s.room.copied}` : s.room.copyLink}
          </button>
        </div>
      </div>
      <div className="pw-lobby-seats">
        {table.seats.map((st) => {
          const ag = st.agent_id ? agents.find((a) => a.id === st.agent_id) : undefined
          const canEdit = table.is_host && !st.is_me && st.kind !== 'human'
          return (
            <div key={st.seat} className={`pw-lobby-seat kind-${st.kind} ${st.is_me ? 'is-me' : ''}`} style={{ '--i': st.seat } as CSSProperties}>
              <span className="pw-lobby-n">#{st.seat + 1}</span>
              {st.kind === 'open' ? (
                <span className="pw-av pw-av-empty pw-av-pulse" style={{ width: 64, height: 64 }} />
              ) : (
                <Avatar name={st.name} src={st.avatar} seat={st.seat} size={64} agent={st.kind === 'agent'} />
              )}
              <b>
                {st.kind === 'open' ? s.room.openSeat : lang === 'zh' && ag ? ag.name_zh : st.name}
                {st.kind === 'agent' && <em className="pw-ai">AI</em>}
              </b>
              <small>
                {st.is_me ? s.room.you : st.kind === 'agent' ? (lang === 'zh' ? ag?.title_zh : ag?.title) || '' : st.kind === 'open' ? s.newTable.openHint : ''}
                {st.is_me && table.is_host ? ` · ${s.room.host}` : ''}
              </small>
              {canEdit && (
                <div className="pw-lobby-seat-actions">
                  {st.kind === 'agent' && (
                    <button type="button" className="pw-link-btn" onClick={() => client.setSeat(st.seat, 'open')}>
                      {s.room.makeOpen}
                    </button>
                  )}
                  <button type="button" className="pw-link-btn" onClick={() => setPicker(picker === st.seat ? null : st.seat)}>
                    {s.room.makeAgent}
                  </button>
                </div>
              )}
              {picker === st.seat && (
                <div className="pw-agent-pop">
                  {agents.map((a) => (
                    <button
                      key={a.id}
                      type="button"
                      onClick={() => {
                        client.setSeat(st.seat, 'agent', a.id)
                        setPicker(null)
                      }}
                    >
                      <Avatar name={a.name} src={a.avatar || `/play/agents/${a.id}.webp`} seat={st.seat} size={28} agent />
                      <span>{lang === 'zh' ? a.name_zh : a.name}</span>
                    </button>
                  ))}
                </div>
              )}
            </div>
          )
        })}
      </div>
      <div className="pw-lobby-foot">
        {table.is_host ? (
          <button type="button" className="pw-btn pw-btn-primary pw-btn-lg" disabled={starting} onClick={start}>
            {starting ? s.room.starting : s.room.start}
          </button>
        ) : (
          <p className="pw-muted">{s.room.waitHost}</p>
        )}
      </div>
    </div>
  )
}

// ── finished: podium + rematch ────────────────────────────────────────────
function Finished({ table, client, prefs }: { table: TableView; client: TableClient; prefs: PlayPrefs }) {
  const { s } = usePlayT()
  const nav = useNavigate()
  const [busy, setBusy] = useState(false)
  const o = table.outcome
  const order = useMemo(() => {
    if (!o) return table.seats.map((st) => ({ st, rank: 0, score: 0 }))
    return table.seats
      .map((st) => ({ st, rank: o.rank[st.seat] ?? 99, score: o.score[st.seat] ?? 0 }))
      .sort((a, b) => a.rank - b.rank || b.score - a.score)
  }, [o, table.seats])
  const podium = [order[1], order[0], order[2]].filter(Boolean)
  const rest = order.slice(3)
  const isHoldem = table.view?.kind === 'holdem'
  const fmt = (v: number) => (isHoldem ? fmtChips(v) : String(v))
  const rematch = async () => {
    setBusy(true)
    const id = await client.rematch()
    setBusy(false)
    if (id) nav(`/app/table/${id}`)
  }
  return (
    <div className="pw-finished">
      <div className="pw-confetti" aria-hidden>
        {Array.from({ length: 24 }, (_, i) => (
          <i key={i} style={{ '--l': `${(i * 37 + 11) % 100}%`, '--dl': `${((i * 7) % 11) * 0.22}s`, '--du': `${3.4 + (i % 5) * 0.55}s`, '--r': `${(i * 53) % 360}deg` } as CSSProperties} />
        ))}
      </div>
      <span className="pw-eyebrow">{table.game.name}</span>
      <h2>{s.room.finished}</h2>
      {o?.summary && <p className="pw-finished-sum">{o.summary}</p>}
      <div className="pw-podium">
        {podium.map((x) => (
          <div key={x.st.seat} className={`pw-podium-col rank-${x.rank}`}>
            <div className="pw-podium-av">
              {x.rank === 1 && <span className="pw-crown">♛</span>}
              <Avatar name={x.st.name} src={x.st.avatar} seat={x.st.seat} size={x.rank === 1 ? 96 : 72} agent={x.st.kind === 'agent'} />
            </div>
            <b>{x.st.name}</b>
            <small>{fmt(x.score)}</small>
            <div className="pw-podium-block">
              <span>{s.room.rank[x.rank - 1] || x.rank}</span>
            </div>
          </div>
        ))}
      </div>
      {rest.length > 0 && (
        <ol className="pw-rest">
          {rest.map((x) => (
            <li key={x.st.seat}>
              <span>{s.room.rank[x.rank - 1] || x.rank}</span>
              <Avatar name={x.st.name} src={x.st.avatar} seat={x.st.seat} size={26} agent={x.st.kind === 'agent'} />
              <b>{x.st.name}</b>
              <small>{fmt(x.score)}</small>
            </li>
          ))}
        </ol>
      )}
      <div className="pw-finished-actions">
        {table.is_host && (
          <button type="button" className="pw-btn pw-btn-primary pw-btn-lg" disabled={busy} onClick={rematch}>
            {s.room.rematch}
          </button>
        )}
        <Link className="pw-btn pw-btn-ghost pw-btn-lg" to="/app/games">
          {s.room.backToLobby}
        </Link>
      </div>
      {table.view && table.view.kind !== 'holdem' && (
        <div className="pw-final-board" aria-hidden>
          <BoardView table={{ ...table, legal: [], to_move: [] }} busy onMove={() => {}} prefs={prefs} />
        </div>
      )}
    </div>
  )
}
