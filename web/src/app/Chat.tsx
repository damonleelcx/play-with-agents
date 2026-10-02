import { memo, useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useLocation, useNavigate, useParams } from 'react-router-dom'
import { AoiFace, Img, guessMood, type Mood } from '../components/Aoi'
import { IconAlert, IconChevronD, IconRefresh, IconSend, IconSpeaker, IconStop } from '../components/Icons'
import { api, streamMessage, uid, type Card, type Message } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { md } from '../lib/md'
import { useSession } from '../lib/session'
import { speak, stop as stopVoice, useVoice } from '../lib/voice'
import { CardBoundary, GameCard, MissionCard } from './cards'
import { useLive } from './live'
import { TableCard } from './play'
import { usePrefs } from './prefs'

const MOODS = new Set(['neutral', 'smile', 'wink', 'surprised', 'angry', 'sad'])
function moodOf(m: Message): Mood {
  const x = m.meta?.mood
  return typeof x === 'string' && MOODS.has(x) ? (x as Mood) : guessMood(m.content)
}

// Cards Aoi attached to a message. Older messages that opened a mission
// before cards existed still get one.
function cardsOf(m: Message): Card[] {
  const cs = Array.isArray(m.meta?.cards) ? (m.meta.cards as Card[]) : []
  if (cs.length === 0 && m.meta?.goal_id && m.meta?.kind === 'goal_created') return [{ kind: 'mission', goal_id: m.meta.goal_id }]
  return cs
}

export default function Chat({ onChanged }: { onChanged: () => void }) {
  const { convId } = useParams()
  const { t, f } = useI18n()
  const { user } = useSession()
  const { tick } = useLive()
  const nav = useNavigate()
  const loc = useLocation()
  const [messages, setMessages] = useState<Message[]>([])
  const [loaded, setLoaded] = useState(!convId)
  const [loadErr, setLoadErr] = useState(false)
  const [text, setText] = useState('')
  const [streaming, setStreaming] = useState(false)
  const [pinnedView, setPinnedView] = useState(true)
  const abort = useRef<AbortController | null>(null)
  const list = useRef<HTMLDivElement>(null)
  const box = useRef<HTMLTextAreaElement>(null)
  const pinned = useRef(true)
  const streamingRef = useRef(false)
  const createdConv = useRef<string | undefined>(undefined)
  const voice = useVoice()
  const { prefs } = usePrefs()
  const voiceOn = voice.enabled && prefs.aoi_voice !== false
  const autoplay = useRef(false)
  autoplay.current = voiceOn && prefs.voice_autoplay === true

  // "Revise this game" and "New game" arrive with a prompt starter.
  useEffect(() => {
    const pre = (loc.state as any)?.prefill
    if (typeof pre === 'string' && pre) {
      setText(pre)
      nav(loc.pathname, { replace: true, state: null })
      requestAnimationFrame(() => {
        const el = box.current
        if (el) { el.focus(); el.setSelectionRange(pre.length, pre.length) }
      })
    }
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [loc.state])

  const load = useCallback(async () => {
    if (!convId) {
      setMessages([])
      setLoaded(true)
      return
    }
    try {
      const m = await api.get<Message[]>(`/api/conversations/${convId}/messages`)
      // Keep a failed exchange (and its Retry) on screen across refreshes:
      // the server never stored it.
      if (!streamingRef.current)
        setMessages((prev) => {
          const failed = prev.filter((x) => x.id < 0 && x.meta?.error)
          const keep = new Set<number>(failed.flatMap((x) => [x.id, x.meta?.retry?.userId]))
          return [...(m || []), ...prev.filter((x) => keep.has(x.id))]
        })
      setLoadErr(false)
    } catch (e: any) {
      if (e.status === 404) nav('/app', { replace: true })
      else setLoadErr(true)
    } finally {
      setLoaded(true)
    }
  }, [convId, nav])

  useEffect(() => {
    if (!streamingRef.current) load()
  }, [load, tick])

  // Stay pinned to the bottom unless the reader scrolled up.
  useLayoutEffect(() => {
    const el = list.current
    if (el && pinned.current) el.scrollTop = el.scrollHeight
  }, [messages])
  // Cards load after their message and grow the thread; stay anchored to the
  // bottom while the reader hasn't scrolled away.
  useEffect(() => {
    const el = list.current
    const inner = el?.firstElementChild
    if (!el || !inner || typeof ResizeObserver === 'undefined') return
    const ro = new ResizeObserver(() => { if (pinned.current) el.scrollTop = el.scrollHeight })
    ro.observe(inner)
    return () => ro.disconnect()
  }, [loaded, messages.length === 0])
  const onScroll = () => {
    const el = list.current
    if (!el) return
    const p = el.scrollHeight - el.scrollTop - el.clientHeight < 120
    pinned.current = p
    if (p !== pinnedView) setPinnedView(p)
  }
  const jump = () => {
    const el = list.current
    if (el) el.scrollTo({ top: el.scrollHeight, behavior: 'smooth' })
    pinned.current = true
    setPinnedView(true)
  }

  const send = async (content?: string, retryOf?: { userId: number; replyId: number; clientId: string }) => {
    const body = (content ?? text).trim()
    if (!body || streamingRef.current) return
    let id = convId || createdConv.current
    const created = !convId
    pinned.current = true
    setPinnedView(true)
    const now = new Date().toISOString()
    const clientId = retryOf?.clientId || uid()
    const userMsg: Message = { id: -Date.now(), role: 'user', content: body, meta: { client_msg_id: clientId }, created_at: now }
    const reply: Message = { id: -Date.now() - 1, role: 'assistant', content: '', meta: {}, created_at: now, pending: true }
    setMessages((ms) => [...ms.filter((m) => !retryOf || (m.id !== retryOf.userId && m.id !== retryOf.replyId)), userMsg, reply])
    if (!content) setText('')
    streamingRef.current = true
    setStreaming(true)
    const patch = (fn: (m: Message) => Message) => setMessages((ms) => ms.map((m) => (m.id === reply.id ? fn(m) : m)))
    let failed = false
    let full = ''
    let finalId = reply.id
    const fail = (e: string) => (failed = true) && patch((m) => ({ ...m, pending: false, meta: { ...m.meta, error: e || 'error', retry: { text: body, clientId, userId: userMsg.id } } }))
    try {
      // A first message creates the conversation, but the URL only changes
      // once the reply has streamed: changing it now would remount this view.
      if (!id) {
        const c = await api.post<{ id: string }>('/api/conversations')
        id = c.id
        createdConv.current = c.id // a retry after a failure reuses it
      }
      const ctl = new AbortController()
      abort.current = ctl
      await streamMessage(id!, body, clientId, {
        meta: (meta) => patch((m) => ({ ...m, meta: { ...m.meta, ...meta } })),
        delta: (d) => { full += d; patch((m) => ({ ...m, content: m.content + d })) },
        done: (mid) => {
          if (typeof mid === 'number' && mid > 0) finalId = mid
          patch((m) => ({ ...m, pending: false, id: finalId }))
        },
        error: (e) => fail(e),
      }, ctl.signal)
    } catch (e: any) {
      if (e?.name === 'AbortError') patch((m) => ({ ...m, pending: false }))
      else fail(e?.message || 'offline')
    } finally {
      streamingRef.current = false
      setStreaming(false)
      abort.current = null
      onChanged()
      // Read the new reply aloud once it is complete (never history).
      if (!failed && full.trim() && autoplay.current) speak(full, `m${finalId}`)
      if (created && id && !failed) nav(`/app/c/${id}`, { replace: true })
      else if (!failed) window.setTimeout(load, 400) // the server now holds the authoritative copy (and its cards)
    }
  }

  const stop = () => abort.current?.abort()

  const onKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing) {
      e.preventDefault()
      send()
    }
  }

  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 200) + 'px'
    el.style.overflowY = el.scrollHeight > 200 ? 'auto' : 'hidden'
  }, [text])

  const lastAoi = useMemo(() => [...messages].reverse().find((m) => m.role === 'assistant' && !m.pending && m.content), [messages])
  const headMood: Mood = streaming ? 'neutral' : lastAoi ? moodOf(lastAoi) : 'smile'
  const empty = loaded && messages.length === 0 && !loadErr
  const firstName = (user?.name || '').trim().split(/\s+/)[0]

  return (
    <div className="aoi-chat">
      <header className="aoi-chat-head">
        <AoiFace mood={headMood} size={38} ring pulse={streaming && !voice.speaking} speaking={voice.speaking} />
        <div className="ch-text">
          <strong>Aoi <span className="ch-zh">葵</span></strong>
          <small className={streaming || voice.speaking ? 'typing-label' : ''}>{streaming ? t.chat.typing + '…' : voice.speaking ? t.chat.speaking + '…' : t.chat.role}</small>
        </div>
      </header>

      <div className="chat-scroll" ref={list} onScroll={onScroll}>
        {loadErr && messages.length === 0 ? (
          <div className="chat-error"><IconAlert size={18} /> {t.chat.loadError} <button className="btn btn-soft btn-sm" onClick={load}><IconRefresh size={15} /> {t.common.retry}</button></div>
        ) : empty ? (
          <div className="chat-empty">
            <div className="ce-portrait">
              <div className="ce-halo" />
              <Img srcs={['/play/aoi/aoi-portrait.webp', '/play/agents/aoi.webp']} alt="Aoi" className="ce-img"
                fallback={<span className="ce-fallback">葵</span>} />
              <span className="ce-face"><AoiFace mood="smile" size={44} /></span>
            </div>
            <h1>{firstName ? f(t.chat.hello, { name: firstName }) : t.chat.helloAnon}</h1>
            <p>{t.chat.intro}</p>
            <div className="ce-suggest">
              {t.chat.suggestions.map((s, i) => (
                <button key={s} className="chip-btn" style={{ animationDelay: `${120 + i * 70}ms` }} onClick={() => send(s)}>
                  <span className="chip-emoji" aria-hidden="true">{['🂡', '🎓', '🛠️', '👋'][i]}</span>{s}
                </button>
              ))}
            </div>
          </div>
        ) : (
          <div className="thread">
            {messages.map((m, i) => (
              <Row key={m.id} m={m} streaming={streaming} groupStart={i === 0 || messages[i - 1].role !== m.role}
                voiceOn={voiceOn} voiceKey={voice.speaking || voice.loading ? voice.key : null} voiceLoading={voice.loading}
                onRetry={(r) => send(r.text, { userId: r.userId, replyId: m.id, clientId: r.clientId })} />
            ))}
          </div>
        )}
      </div>

      {!pinnedView && !empty && (
        <button className="jump" onClick={jump}><IconChevronD size={16} /> {t.chat.jump}</button>
      )}

      <div className="composer-wrap">
        {voiceOn && voice.blocked && (
          <button className="voice-chip" onClick={() => voice.unlock()}><IconSpeaker size={15} /> {t.chat.enableVoice}</button>
        )}
        <div className={`composer ${streaming ? 'busy' : ''}`}>
          <textarea ref={box} rows={1} value={text} onChange={(e) => setText(e.target.value)} onKeyDown={onKey}
            placeholder={t.chat.placeholder} aria-label={t.chat.placeholder} maxLength={12000} />
          {streaming ? (
            <button className="send stop" onClick={stop} aria-label={t.chat.stop} title={t.chat.stop}><IconStop size={16} /></button>
          ) : (
            <button className="send" onClick={() => send()} disabled={!text.trim()} aria-label={t.chat.send} title={t.chat.send}><IconSend size={18} /></button>
          )}
        </div>
        <p className="composer-note"><span className="hide-sm">{t.chat.hint} · </span>{t.chat.note}</p>
      </div>
    </div>
  )
}

type RetryInfo = { text: string; clientId: string; userId: number }

const Row = memo(function Row({ m, streaming, groupStart, onRetry, voiceOn, voiceKey, voiceLoading }: {
  m: Message; streaming: boolean; groupStart: boolean; onRetry: (r: RetryInfo) => void; voiceOn: boolean; voiceKey: string | null; voiceLoading: boolean
}) {
  const { t } = useI18n()
  const key = `m${m.id}`
  const mine = voiceKey === key
  const [copied, setCopied] = useState(false)
  if (m.role === 'event') return <div className="cmsg event"><span>{m.content}</span></div>
  if (m.role === 'user')
    return <div className={`cmsg user ${groupStart ? 'gs' : ''}`}><div className="cbubble">{m.content}</div></div>
  const html = m.content ? md(m.content) : ''
  const cards = cardsOf(m)
  const err = m.meta?.error
  const mood: Mood = m.pending ? 'neutral' : moodOf(m)
  return (
    <div className={`cmsg from-aoi ${groupStart ? 'gs' : ''} ${err ? 'failed' : ''}`}>
      <div className="aoi-col">{groupStart && <AoiFace mood={mood} size={34} pulse={m.pending && streaming} speaking={mine && !voiceLoading} />}</div>
      <div className="aoi-body">
        {groupStart && <div className="aoi-name">Aoi</div>}
        {m.pending && !m.content ? (
          <div className="typing" aria-label={t.chat.thinking}><i /><i /><i /></div>
        ) : html ? (
          <div className={`prose ${m.pending ? 'streaming' : ''}`} dangerouslySetInnerHTML={{ __html: html }} />
        ) : null}
        {err && (
          <div className="msg-fail">
            <IconAlert size={15} />
            <span>{err === 'offline' || err === 'interrupted' ? t.chat.offline : `${t.chat.failed} ${err === 'error' ? '' : err}`}</span>
            {m.meta.retry && <button className="btn btn-soft btn-sm" disabled={streaming} onClick={() => onRetry(m.meta.retry)}><IconRefresh size={14} /> {t.chat.retry}</button>}
          </div>
        )}
        {cards.length > 0 && (
          <div className="msg-cards">
            {cards.map((c, i) => (
              <CardBoundary key={i} fallback={<div className="xcard"><p className="muted">{t.cards.unavailable}</p></div>}>
                {c.kind === 'table' ? <TableCard tableId={c.table_id} />
                  : c.kind === 'mission' ? <MissionCard goalId={c.goal_id} />
                  : c.kind === 'game' ? <GameCard gameId={c.game_id} />
                  : null}
              </CardBoundary>
            ))}
          </div>
        )}
        {!m.pending && m.content && !err && (
          <div className="msg-tools">
          {voiceOn && (
            <button className={`speak ${mine ? 'on' : ''} ${mine && voiceLoading ? 'loading' : ''}`} onClick={() => (mine ? stopVoice() : speak(m.content, key))}
              aria-label={mine ? t.chat.stopSpeaking : t.chat.speak} title={mine ? t.chat.stopSpeaking : t.chat.speak}>
              {mine && !voiceLoading ? <IconStop size={13} /> : <IconSpeaker size={15} />}
            </button>
          )}
          <button className="copy" onClick={() => { navigator.clipboard?.writeText(m.content); setCopied(true); window.setTimeout(() => setCopied(false), 1500) }}>
            {copied ? t.chat.copied : t.chat.copy}
          </button>
          </div>
        )}
      </div>
    </div>
  )
})
