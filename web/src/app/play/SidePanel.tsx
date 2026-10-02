import { useEffect, useMemo, useRef, useState, type CSSProperties } from 'react'
import { PLAYER_COLORS, type TableView } from '../../lib/playApi'
import { Avatar, VoiceWave } from './parts'
import { usePlayT } from './strings'

function hhmm(at: string) {
  const d = new Date(at)
  return isNaN(+d) ? '' : d.toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })
}

export default function SidePanel({
  table,
  onChat,
  onClose,
  stacks,
  speakingSeat = -1,
}: {
  table: TableView
  onChat: (text: string) => void
  onClose?: () => void
  stacks?: (number | string | null)[]
  speakingSeat?: number
}) {
  const { s } = usePlayT()
  const [tab, setTab] = useState<'chat' | 'log'>('chat')
  const [unread, setUnread] = useState(0)
  const lastCount = useRef(table.chat.length)
  useEffect(() => {
    const n = table.chat.length
    if (tab !== 'chat' && n > lastCount.current) setUnread((u) => u + (n - lastCount.current))
    lastCount.current = n
  }, [table.chat.length, tab])
  useEffect(() => {
    if (tab === 'chat') setUnread(0)
  }, [tab])

  return (
    <aside className="pw-side" aria-label={s.room.panel}>
      <div className="pw-side-seats">
        <div className="pw-side-title">
          <span>{s.room.players}</span>
          {onClose && (
            <button type="button" className="pw-icon-btn" onClick={onClose} aria-label={s.room.close}>
              ×
            </button>
          )}
        </div>
        <ul>
          {table.seats.map((st) => {
            const active = table.to_move.includes(st.seat)
            return (
              <li key={st.seat} className={`${active ? 'is-active' : ''} ${st.kind === 'open' ? 'is-open' : ''} ${st.away ? 'is-away' : ''}`}>
                {st.kind === 'open' ? (
                  <span className="pw-av pw-av-empty" style={{ width: 26, height: 26 }} />
                ) : (
                  <Avatar name={st.name} src={st.avatar} seat={st.seat} size={26} agent={st.kind === 'agent'} />
                )}
                <span className="pw-side-seat-name">
                  {st.kind === 'open' ? s.room.openSeat : st.name}
                  {st.is_me && <em className="pw-you">{s.room.you}</em>}
                  {st.kind === 'agent' && <em className="pw-ai">{s.room.agentBadge}</em>}
                  {st.away && <em className="pw-away-tag">{s.room.away}</em>}
                </span>
                {speakingSeat === st.seat && <VoiceWave inline />}
                {stacks?.[st.seat] != null && <span className="pw-side-seat-val">{stacks[st.seat]}</span>}
                {active && <span className="pw-wait-dot" />}
              </li>
            )
          })}
        </ul>
      </div>
      <div className="pw-tabs" role="tablist">
        <button role="tab" aria-selected={tab === 'chat'} className={tab === 'chat' ? 'on' : ''} onClick={() => setTab('chat')}>
          {s.room.chat}
          {unread > 0 && <span className="pw-unread">{unread}</span>}
        </button>
        <button role="tab" aria-selected={tab === 'log'} className={tab === 'log' ? 'on' : ''} onClick={() => setTab('log')}>
          {s.room.log}
        </button>
      </div>
      {tab === 'chat' ? <ChatTab table={table} onChat={onChat} /> : <LogTab table={table} />}
    </aside>
  )
}

function ChatTab({ table, onChat }: { table: TableView; onChat: (t: string) => void }) {
  const { s } = usePlayT()
  const [text, setText] = useState('')
  const [hi, setHi] = useState(0)
  const scroll = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)
  useEffect(() => {
    const el = scroll.current
    if (el) el.scrollTop = el.scrollHeight
  }, [table.chat.length])

  const agents = useMemo(() => {
    const seen = new Set<string>()
    return table.seats.filter((x) => x.kind === 'agent' && x.agent_id && !seen.has(x.agent_id) && seen.add(x.agent_id))
  }, [table.seats])
  const m = /(?:^|\s)@(\w*)$/.exec(text)
  const matches = m ? agents.filter((a) => a.agent_id!.startsWith(m[1].toLowerCase()) || a.name.toLowerCase().startsWith(m[1].toLowerCase())) : []
  useEffect(() => setHi(0), [m?.[1]])
  const pick = (id: string) => {
    setText((t) => t.replace(/@(\w*)$/, `@${id} `))
    input.current?.focus()
  }
  const send = () => {
    const t = text.trim()
    if (!t) return
    onChat(t)
    setText('')
  }
  return (
    <div className="pw-chat">
      <div className="pw-chat-scroll" ref={scroll}>
        {table.chat.length === 0 && <div className="pw-empty-note">{s.room.noChat}</div>}
        {table.chat.map((c) => {
          const mine = c.seat === table.my_seat && !c.agent
          return (
            <div key={c.id} className={`pw-chat-line ${mine ? 'is-mine' : ''} ${c.agent ? 'is-agent' : ''}`}>
              <Avatar name={c.name} src={c.avatar} seat={c.seat} size={28} agent={c.agent} />
              <div className="pw-chat-body">
                <div className="pw-chat-meta">
                  <b>{c.name}</b>
                  {c.agent && <em className="pw-ai">{s.room.agentBadge}</em>}
                  <time>{hhmm(c.at)}</time>
                </div>
                <div className="pw-chat-text">{renderMentions(c.text)}</div>
              </div>
            </div>
          )
        })}
      </div>
      <form
        className="pw-chat-form"
        onSubmit={(e) => {
          e.preventDefault()
          if (matches.length) pick(matches[hi].agent_id!)
          else send()
        }}
      >
        {matches.length > 0 && (
          <div className="pw-mention" role="listbox">
            {matches.map((a, i) => (
              <button
                key={a.agent_id}
                type="button"
                role="option"
                aria-selected={i === hi}
                className={i === hi ? 'on' : ''}
                onMouseDown={(e) => {
                  e.preventDefault()
                  pick(a.agent_id!)
                }}
              >
                <Avatar name={a.name} src={a.avatar} seat={a.seat} size={22} agent />
                <b>{a.name}</b>
                <small>@{a.agent_id}</small>
              </button>
            ))}
          </div>
        )}
        <input
          ref={input}
          className="pw-input"
          value={text}
          maxLength={500}
          placeholder={s.room.say}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={(e) => {
            if (!matches.length) return
            if (e.key === 'ArrowDown') {
              e.preventDefault()
              setHi((h) => (h + 1) % matches.length)
            } else if (e.key === 'ArrowUp') {
              e.preventDefault()
              setHi((h) => (h - 1 + matches.length) % matches.length)
            } else if (e.key === 'Tab') {
              e.preventDefault()
              pick(matches[hi].agent_id!)
            } else if (e.key === 'Escape') setText((t) => t + ' ')
          }}
        />
        <button type="submit" className="pw-btn pw-btn-primary pw-btn-sm" disabled={!text.trim()}>
          {s.room.send}
        </button>
      </form>
    </div>
  )
}

function renderMentions(text: string) {
  const parts = text.split(/(@\w+)/g)
  return parts.map((p, i) => (p.startsWith('@') ? <span key={i} className="pw-mention-tag">{p}</span> : p))
}

function LogTab({ table }: { table: TableView }) {
  const { s } = usePlayT()
  const scroll = useRef<HTMLDivElement>(null)
  useEffect(() => {
    const el = scroll.current
    if (el) el.scrollTop = el.scrollHeight
  }, [table.log.length])
  return (
    <div className="pw-log" ref={scroll}>
      {table.log.length === 0 && <div className="pw-empty-note">{s.room.noLog}</div>}
      {table.log.map((l) => (
        <div key={l.seq} className={`pw-log-line type-${l.type}`} style={{ '--pc': l.seat >= 0 ? PLAYER_COLORS[l.seat % 8] : 'var(--text-3)' } as CSSProperties}>
          <i />
          <span>{l.text}</span>
          <time>{hhmm(l.at)}</time>
        </div>
      ))}
    </div>
  )
}
