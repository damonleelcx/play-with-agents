import { useCallback, useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react'
import { useNavigate, useParams } from 'react-router-dom'
import { IconAlert, IconClip, IconSend, IconStop } from '../components/Icons'
import { api, streamMessage, uid, type Approval, type Goal, type Message } from '../lib/api'
import { useI18n } from '../lib/i18n'
import { md } from '../lib/md'
import { useSession } from '../lib/session'
import { useLive } from './live'
import { ApprovalCard, GoalCard, VeraFace, type Mood } from './parts'

export default function Chat({ onChanged }: { onChanged: () => void }) {
  const { id } = useParams()
  const { t, f, lang } = useI18n()
  const { user } = useSession()
  const { tick } = useLive()
  const nav = useNavigate()
  const [messages, setMessages] = useState<Message[]>([])
  const [goals, setGoals] = useState<Goal[]>([])
  const [approvals, setApprovals] = useState<Approval[]>([])
  const [text, setText] = useState('')
  const [streaming, setStreaming] = useState(false)
  const [mood, setMood] = useState<Mood>('smile')
  const [upload, setUpload] = useState('')
  const [enterToSend, setEnterToSend] = useState(true)
  const abort = useRef<AbortController | null>(null)
  const list = useRef<HTMLDivElement>(null)
  const box = useRef<HTMLTextAreaElement>(null)
  const pinned = useRef(true)

  useEffect(() => {
    api.get<{ preferences: Record<string, any> }>('/api/settings').then((s) => setEnterToSend(s.preferences?.enter_to_send !== false)).catch(() => {})
  }, [])

  const load = useCallback(async () => {
    if (!id) {
      setMessages([])
      setGoals([])
      setApprovals([])
      return
    }
    const [m, g, a] = await Promise.all([
      api.get<Message[]>(`/api/conversations/${id}/messages`).catch(() => null),
      api.get<Goal[]>('/api/goals').catch(() => [] as Goal[]),
      api.get<Approval[]>('/api/approvals').catch(() => [] as Approval[]),
    ])
    if (m === null) {
      nav('/app', { replace: true })
      return
    }
    const mine = g.filter((x) => x.conversation_id === id)
    // Full detail (tasks) for the cases in this conversation.
    const detailed = await Promise.all(mine.slice(0, 6).map((x) => api.get<Goal>(`/api/goals/${x.id}`).catch(() => x)))
    setMessages((prev) => (streaming ? prev : m))
    setGoals(detailed)
    const ids = new Set(mine.map((x) => x.id))
    setApprovals(a.filter((x) => ids.has(x.goal_id) && x.status === 'pending'))
    const last = [...m].reverse().find((x) => x.role === 'assistant')
    if (last?.meta?.kind === 'complete') setMood('laugh')
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, nav])

  useEffect(() => {
    if (!streaming) load()
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [id, tick])

  // Keep the view pinned to the bottom unless the reader scrolled up.
  useLayoutEffect(() => {
    const el = list.current
    if (el && pinned.current) el.scrollTop = el.scrollHeight
  }, [messages, goals, approvals])
  const onScroll = () => {
    const el = list.current
    if (el) pinned.current = el.scrollHeight - el.scrollTop - el.clientHeight < 120
  }

  const send = async (content?: string) => {
    const body = (content ?? text).trim()
    if (!body || streaming) return
    let convId = id
    // A first message creates the conversation, but the URL only changes once
    // the reply has streamed: changing it now would remount this view and
    // drop the stream on the floor.
    const created = !convId
    if (!convId) {
      const c = await api.post<{ id: string }>('/api/conversations')
      convId = c.id
    }
    setText('')
    pinned.current = true
    const now = new Date().toISOString()
    const userMsg: Message = { id: -Date.now(), role: 'user', content: body, meta: {}, created_at: now }
    const reply: Message = { id: -Date.now() - 1, role: 'assistant', content: '', meta: {}, created_at: now, pending: true }
    setMessages((m) => [...m, userMsg, reply])
    setStreaming(true)
    setMood('think')
    const ctl = new AbortController()
    abort.current = ctl
    const patch = (fn: (m: Message) => Message) => setMessages((ms) => ms.map((m) => (m.id === reply.id ? fn(m) : m)))
    try {
      await streamMessage(convId!, body, uid(), {
        meta: (meta) => {
          patch((m) => ({ ...m, meta: { ...m.meta, ...meta } }))
          if (meta.intent === 'med.emergency') setMood('think')
        },
        delta: (d) => patch((m) => ({ ...m, content: m.content + d })),
        done: () => patch((m) => ({ ...m, pending: false })),
        error: (e) => patch((m) => ({ ...m, pending: false, content: m.content || e, meta: { ...m.meta, error: true } })),
      }, ctl.signal)
    } catch {
      patch((m) => ({ ...m, pending: false }))
    } finally {
      setStreaming(false)
      setMood('smile')
      abort.current = null
      onChanged()
      if (created) nav(`/app/c/${convId}`, { replace: true })
      // The server now has the authoritative copy (and any case it opened).
      else setTimeout(load, 300)
    }
  }

  const stop = () => abort.current?.abort()

  const onKey = (e: React.KeyboardEvent<HTMLTextAreaElement>) => {
    if (e.key === 'Enter' && !e.shiftKey && !e.nativeEvent.isComposing && enterToSend) {
      e.preventDefault()
      send()
    }
  }

  useLayoutEffect(() => {
    const el = box.current
    if (!el) return
    el.style.height = 'auto'
    el.style.height = Math.min(el.scrollHeight, 220) + 'px'
  }, [text])

  const onFile = async (file: File) => {
    setUpload(t.app.uploading)
    try {
      const r = await api.upload<{ id: string; title: string }>('/api/documents', file)
      setUpload(f(t.app.uploaded, { name: r.title }))
      setText((x) => (x ? x + '\n' : '') + (lang === 'zh' ? `我上传了「${r.title}」。` : `I've uploaded “${r.title}”. `))
      box.current?.focus()
    } catch (e: any) {
      setUpload(e.message)
    }
    setTimeout(() => setUpload(''), 5000)
  }

  // Cases appear after the message that opened them.
  const goalAfter = useMemo(() => {
    const m = new Map<number, Goal[]>()
    for (const g of goals) {
      const msg = messages.find((x) => x.meta?.goal_id === g.id && x.meta?.kind === 'goal_created')
      const key = msg ? msg.id : Number.MAX_SAFE_INTEGER
      m.set(key, [...(m.get(key) || []), g])
    }
    return m
  }, [goals, messages])

  const empty = messages.length === 0
  const firstName = (user?.name || '').split(' ')[0]

  return (
    <div className="chat">
      <div className="chat-scroll" ref={list} onScroll={onScroll}>
        {empty ? (
          <div className="chat-empty">
            <div className="ce-portrait">
              <img src="/vera/vera-portrait.webp" alt="Vera" />
            </div>
            <h1>{f(t.app.greeting, { name: firstName ? (lang === 'zh' ? firstName : firstName) : lang === 'zh' ? '' : 'there' })}</h1>
            <p>{t.app.greetingSub}</p>
            <div className="ce-suggest">
              {t.app.suggestions.map((s) => (
                <button key={s} className="chip-btn" onClick={() => send(s)}>{s}</button>
              ))}
            </div>
          </div>
        ) : (
          <div className="thread">
            {messages.map((m) => (
              <div key={m.id}>
                <Bubble m={m} mood={m.pending ? 'think' : mood} />
                {(goalAfter.get(m.id) || []).map((g) => <GoalCard key={g.id} goal={g} />)}
              </div>
            ))}
            {(goalAfter.get(Number.MAX_SAFE_INTEGER) || []).map((g) => <GoalCard key={g.id} goal={g} />)}
            {approvals.map((a) => <ApprovalCard key={a.id} a={a} onDone={load} />)}
          </div>
        )}
      </div>

      <div className="composer-wrap">
        {upload && <div className="upload-note">{upload}</div>}
        <div className="composer">
          <label className="attach" title={t.app.attach}>
            <IconClip size={20} />
            <input type="file" accept=".pdf,.docx,.txt,.md,.csv,.eml,image/*" onChange={(e) => { const fl = e.target.files?.[0]; if (fl) onFile(fl); e.target.value = '' }} hidden />
            <span className="sr-only">{t.app.attach}</span>
          </label>
          <textarea ref={box} rows={1} value={text} onChange={(e) => setText(e.target.value)} onKeyDown={onKey} placeholder={t.app.placeholder} aria-label={t.app.placeholder} maxLength={12000} />
          {streaming ? (
            <button className="send stop" onClick={stop} aria-label={t.app.stop}><IconStop size={18} /></button>
          ) : (
            <button className="send" onClick={() => send()} disabled={!text.trim()} aria-label={t.app.send}><IconSend size={18} /></button>
          )}
        </div>
        <p className="composer-note">{t.app.disclaimer}</p>
      </div>
    </div>
  )
}

function Bubble({ m, mood }: { m: Message; mood: Mood }) {
  const { t } = useI18n()
  const [copied, setCopied] = useState(false)
  if (m.role === 'user') return <div className="msg user"><div className="bubble">{m.content}</div></div>
  const html = md(m.content)
  const emergency = m.meta?.intent === 'med.emergency'
  const update = m.meta?.kind === 'update' || m.meta?.kind === 'reminder' || m.meta?.kind === 'complete'
  return (
    <div className={`msg vera ${emergency ? 'emergency' : ''} ${update ? 'update' : ''}`}>
      <VeraFace mood={m.meta?.kind === 'complete' ? 'laugh' : mood} pulse={m.pending} />
      <div className="vera-body">
        <div className="vera-name">Vera {emergency && <span className="em-tag"><IconAlert size={13} /> {t.app.emergency}</span>}</div>
        {m.pending && !m.content ? (
          <div className="typing" aria-label={t.app.thinking}><i /><i /><i /></div>
        ) : (
          <div className="prose" dangerouslySetInnerHTML={{ __html: html }} />
        )}
        {!m.pending && m.content && (
          <button className="copy" onClick={() => { navigator.clipboard?.writeText(m.content); setCopied(true); setTimeout(() => setCopied(false), 1500) }}>
            {copied ? t.app.copied : t.app.copy}
          </button>
        )}
      </div>
    </div>
  )
}
