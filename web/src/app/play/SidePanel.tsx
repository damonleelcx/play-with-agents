import { useEffect, useMemo, useRef, useState, type CSSProperties, type ReactNode } from 'react'
import { PLAYER_COLORS, REACTIONS, type ChatLine, type ChatOpts, type Presence, type SeatInfo, type TableView } from '../../lib/playApi'
import { Avatar, VoiceWave } from './parts'
import { usePlayT } from './strings'
import type { TypingNow } from './useTable'
import { uiLocale } from '../../lib/i18n'

function hhmm(at: string) {
  const d = new Date(at)
  return isNaN(+d) ? '' : d.toLocaleTimeString(uiLocale(), { hour: '2-digit', minute: '2-digit' })
}

// isHere: agents are always at the table; a person is while they have it
// open (presence), and I always am.
export function isHere(st: SeatInfo, presence: Presence | null | undefined) {
  if (st.kind === 'agent' || st.is_me) return true
  if (st.kind !== 'human') return false
  return !presence || presence.seats.includes(st.seat)
}

export default function SidePanel({
  table,
  onChat,
  onClose,
  stacks,
  speakingSeat = -1,
  typing = [],
  presence,
  onTyping,
  onReact,
}: {
  table: TableView
  onChat: (text: string, opts?: ChatOpts) => void
  onClose?: () => void
  stacks?: (number | string | null)[]
  speakingSeat?: number
  typing?: TypingNow[]
  presence?: Presence | null
  onTyping?: () => void
  onReact?: (chatId: string, emoji: string) => void
}) {
  const { s, f } = usePlayT()
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
  const watchers = presence?.watchers || []

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
            const here = isHere(st, presence)
            return (
              <li key={st.seat} className={`${active ? 'is-active' : ''} ${st.kind === 'open' ? 'is-open' : ''} ${st.away ? 'is-away' : ''}`}>
                {st.kind === 'open' ? (
                  <span className="pw-av pw-av-empty" style={{ width: 26, height: 26 }} />
                ) : (
                  <span className="pw-presence-av">
                    <Avatar name={st.name} src={st.avatar} seat={st.seat} size={26} agent={st.kind === 'agent'} />
                    <i className={`pw-online ${here ? 'is-on' : ''}`} title={here ? s.room.online : s.room.notHere} aria-label={here ? s.room.online : s.room.notHere} />
                  </span>
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
        {watchers.length > 0 && <p className="pw-watchers">{f(s.room.watching, { names: watchers.join(', ') })}</p>}
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
      {tab === 'chat' ? <ChatTab table={table} onChat={onChat} typing={typing} onTyping={onTyping} onReact={onReact} /> : <LogTab table={table} />}
    </aside>
  )
}

type Mode = { kind: 'reply'; line: ChatLine } | { kind: 'whisper'; seat: number; name: string } | null

function ChatTab({
  table,
  onChat,
  typing,
  onTyping,
  onReact,
}: {
  table: TableView
  onChat: (t: string, opts?: ChatOpts) => void
  typing: TypingNow[]
  onTyping?: () => void
  onReact?: (chatId: string, emoji: string) => void
}) {
  const { s, f } = usePlayT()
  const [text, setText] = useState('')
  const [hi, setHi] = useState(0)
  const [mode, setMode] = useState<Mode>(null)
  const [picker, setPicker] = useState<string | null>(null) // the line whose reaction picker is open
  const [whisperMenu, setWhisperMenu] = useState(false)
  const scroll = useRef<HTMLDivElement>(null)
  const input = useRef<HTMLInputElement>(null)
  const stick = useRef(true)
  useEffect(() => {
    const el = scroll.current
    if (el && stick.current) el.scrollTop = el.scrollHeight
  }, [table.chat.length, typing.length])

  // @-mentions: the agents (by id) and the other people (by first name)
  const people = useMemo(() => {
    const seen = new Set<string>()
    const out: { key: string; seat: SeatInfo; insert: string; agent: boolean }[] = []
    for (const x of table.seats) {
      if (x.kind === 'agent' && x.agent_id && !seen.has(x.agent_id)) {
        seen.add(x.agent_id)
        out.push({ key: `a-${x.agent_id}`, seat: x, insert: x.agent_id, agent: true })
      } else if (x.kind === 'human' && !x.is_me) {
        const first = x.name.split(/\s+/)[0] || x.name
        out.push({ key: `h-${x.seat}`, seat: x, insert: first, agent: false })
      }
    }
    return out
  }, [table.seats])
  const whisperable = table.seats.filter((x) => x.kind === 'human' && !x.is_me)
  const m = /(?:^|\s)@([\p{L}\p{N}_]*)$/u.exec(text)
  const q = m?.[1].toLowerCase() ?? ''
  const matches = m ? people.filter((p) => p.insert.toLowerCase().startsWith(q) || p.seat.name.toLowerCase().startsWith(q)).slice(0, 6) : []
  useEffect(() => setHi(0), [q])
  const pick = (insert: string) => {
    setText((t) => t.replace(/@([\p{L}\p{N}_]*)$/u, `@${insert} `))
    input.current?.focus()
  }
  const send = () => {
    const t = text.trim()
    if (!t) return
    const opts: ChatOpts = {}
    if (mode?.kind === 'reply') opts.reply_to = mode.line.id
    if (mode?.kind === 'whisper') opts.whisper_seat = mode.seat
    onChat(t, opts)
    setText('')
    stick.current = true
    if (mode?.kind === 'reply') setMode(null)
  }
  const byId = useMemo(() => new Map(table.chat.map((c) => [String(c.id), c])), [table.chat])
  const typers = typing.filter((x) => x.seat !== table.my_seat || x.agent)
  const typingText =
    typers.length === 0
      ? ''
      : typers.length === 1
        ? f(s.room.typing1, { name: typers[0].name })
        : typers.length === 2
          ? f(s.room.typing2, { a: typers[0].name, b: typers[1].name })
          : s.room.typingMany

  return (
    <div className="pw-chat">
      <div
        className="pw-chat-scroll"
        ref={scroll}
        onScroll={(e) => {
          const el = e.currentTarget
          stick.current = el.scrollHeight - el.scrollTop - el.clientHeight < 60
        }}
      >
        {table.chat.length === 0 && <div className="pw-empty-note">{s.room.noChat}</div>}
        {table.chat.map((c) => {
          const mine = c.seat === table.my_seat && !c.agent && table.my_seat >= 0
          const quoted = c.reply_to ? byId.get(String(c.reply_to)) : undefined
          const canWhisperBack = !c.agent && !mine && c.seat >= 0 && whisperable.some((x) => x.seat === c.seat)
          return (
            <div key={c.id} className={`pw-chat-line ${mine ? 'is-mine' : ''} ${c.agent ? 'is-agent' : ''} ${c.whisper ? 'is-whisper' : ''}`}>
              <Avatar name={c.name} src={c.avatar} seat={c.seat} size={28} agent={c.agent} />
              <div className="pw-chat-body">
                <div className="pw-chat-meta">
                  <b>{c.name}</b>
                  {c.agent && <em className="pw-ai">{s.room.agentBadge}</em>}
                  {c.whisper && (
                    <em className="pw-whisper-tag">
                      🔒 {c.to_seat === table.my_seat ? s.room.whisperedYou : f(s.room.whisperedTo, { name: c.to || '' })}
                    </em>
                  )}
                  <time>{hhmm(c.at)}</time>
                </div>
                {quoted && (
                  <button type="button" className="pw-chat-quote" onClick={() => document.getElementById(`pw-line-${quoted.id}`)?.scrollIntoView({ block: 'center', behavior: 'smooth' })}>
                    <b>{quoted.name}</b> {clipText(quoted.text, 60)}
                  </button>
                )}
                <div className="pw-chat-text" id={`pw-line-${c.id}`}>
                  {renderText(c.text)}
                </div>
                {!!c.reactions?.length && (
                  <div className="pw-reactions">
                    {c.reactions.map((r) => (
                      <button
                        key={r.emoji}
                        type="button"
                        className={`pw-reaction ${r.mine ? 'is-mine' : ''}`}
                        title={r.names.join(', ')}
                        onClick={() => onReact?.(String(c.id), r.emoji)}
                        aria-pressed={r.mine}
                      >
                        <span>{r.emoji}</span>
                        <small>{r.count}</small>
                      </button>
                    ))}
                  </div>
                )}
                <div className="pw-chat-actions">
                  {onReact && (
                    <button type="button" className="pw-chat-act" onClick={() => setPicker(picker === String(c.id) ? null : String(c.id))} aria-label={s.room.react} aria-expanded={picker === String(c.id)}>
                      ☺+
                    </button>
                  )}
                  {!c.whisper && (
                    <button
                      type="button"
                      className="pw-chat-act"
                      onClick={() => {
                        setMode({ kind: 'reply', line: c })
                        input.current?.focus()
                      }}
                    >
                      ↩ {s.room.reply}
                    </button>
                  )}
                  {canWhisperBack && (
                    <button
                      type="button"
                      className="pw-chat-act"
                      onClick={() => {
                        setMode({ kind: 'whisper', seat: c.seat, name: c.name })
                        input.current?.focus()
                      }}
                    >
                      🔒 {s.room.whisper}
                    </button>
                  )}
                </div>
                {picker === String(c.id) && (
                  <div className="pw-react-pick" role="menu">
                    {REACTIONS.map((e) => (
                      <button
                        key={e}
                        type="button"
                        role="menuitem"
                        onClick={() => {
                          onReact?.(String(c.id), e)
                          setPicker(null)
                        }}
                      >
                        {e}
                      </button>
                    ))}
                  </div>
                )}
              </div>
            </div>
          )
        })}
      </div>
      <div className="pw-typing" aria-live="polite">
        {typingText && (
          <>
            <span className="pw-typing-dots" aria-hidden>
              <i />
              <i />
              <i />
            </span>
            {typingText}
          </>
        )}
      </div>
      {mode && (
        <div className={`pw-chat-mode is-${mode.kind}`}>
          {mode.kind === 'reply' ? (
            <span>
              ↩ {f(s.room.replyingTo, { name: mode.line.name })}: <i>{clipText(mode.line.text, 40)}</i>
            </span>
          ) : (
            <span>🔒 {f(s.room.whisperOnly, { name: mode.name })}</span>
          )}
          <button type="button" className="pw-link-btn" onClick={() => setMode(null)} aria-label={s.room.cancel}>
            ×
          </button>
        </div>
      )}
      <form
        className="pw-chat-form"
        onSubmit={(e) => {
          e.preventDefault()
          if (matches.length) pick(matches[hi].insert)
          else send()
        }}
      >
        {matches.length > 0 && (
          <div className="pw-mention" role="listbox">
            {matches.map((a, i) => (
              <button
                key={a.key}
                type="button"
                role="option"
                aria-selected={i === hi}
                className={i === hi ? 'on' : ''}
                onMouseDown={(e) => {
                  e.preventDefault()
                  pick(a.insert)
                }}
              >
                <Avatar name={a.seat.name} src={a.seat.avatar} seat={a.seat.seat} size={22} agent={a.agent} />
                <b>{a.seat.name}</b>
                <small>@{a.insert}</small>
              </button>
            ))}
          </div>
        )}
        {whisperMenu && (
          <div className="pw-mention pw-whisper-menu" role="menu">
            <small className="pw-whisper-head">{s.room.whisperPick}</small>
            {whisperable.length === 0 && <p className="pw-empty-note">{s.room.noWhisper}</p>}
            {whisperable.map((x) => (
              <button
                key={x.seat}
                type="button"
                role="menuitem"
                onClick={() => {
                  setMode({ kind: 'whisper', seat: x.seat, name: x.name })
                  setWhisperMenu(false)
                  input.current?.focus()
                }}
              >
                <Avatar name={x.name} src={x.avatar} seat={x.seat} size={22} />
                <b>{x.name}</b>
              </button>
            ))}
          </div>
        )}
        {whisperable.length > 0 && (
          <button
            type="button"
            className={`pw-icon-btn pw-whisper-btn ${mode?.kind === 'whisper' ? 'on' : ''}`}
            onClick={() => setWhisperMenu((v) => !v)}
            aria-label={s.room.whisper}
            aria-expanded={whisperMenu}
            title={s.room.whisper}
          >
            🔒
          </button>
        )}
        <input
          ref={input}
          className="pw-input"
          value={text}
          maxLength={500}
          enterKeyHint="send"
          placeholder={mode?.kind === 'whisper' ? f(s.room.whisperTo, { name: mode.name }) : s.room.say}
          onChange={(e) => {
            setText(e.target.value)
            // a whisper is nobody else's business, not even that it is being written
            if (e.target.value.trim() && mode?.kind !== 'whisper') onTyping?.()
          }}
          onKeyDown={(e) => {
            if (e.key === 'Escape' && !matches.length) {
              setMode(null)
              setWhisperMenu(false)
              return
            }
            if (!matches.length) return
            if (e.key === 'ArrowDown') {
              e.preventDefault()
              setHi((h) => (h + 1) % matches.length)
            } else if (e.key === 'ArrowUp') {
              e.preventDefault()
              setHi((h) => (h - 1 + matches.length) % matches.length)
            } else if (e.key === 'Tab') {
              e.preventDefault()
              pick(matches[hi].insert)
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

export function clipText(t: string, n: number) {
  const r = Array.from(t)
  return r.length > n ? r.slice(0, n - 1).join('') + '…' : t
}

// renderText shows a line as plain text: React escapes it, links stay text
// (never an anchor, so never a javascript: link), @mentions are highlighted.
export function renderText(text: string): ReactNode[] {
  const parts = text.split(/(@[\p{L}\p{N}_]+|\bhttps?:\/\/\S+)/u)
  return parts.map((p, i) =>
    p.startsWith('@') ? (
      <span key={i} className="pw-mention-tag">
        {p}
      </span>
    ) : /^https?:\/\//.test(p) ? (
      <span key={i} className="pw-chat-url">
        {p}
      </span>
    ) : (
      p
    ),
  )
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
