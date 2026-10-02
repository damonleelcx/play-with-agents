// Thin client for the Play with Agents API. Every mutating call carries the
// custom header: the server rejects state changes without it (CSRF defence).
export class ApiError extends Error {
  status: number
  data: any
  constructor(status: number, message: string, data?: any) {
    super(message)
    this.status = status
    this.data = data
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: 'same-origin', headers: { 'X-Play': '1' } }
  if (body instanceof FormData) {
    init.body = body
  } else if (body !== undefined) {
    ;(init.headers as Record<string, string>)['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }
  let res: Response
  try {
    res = await fetch(path, init)
  } catch {
    throw new ApiError(0, 'offline')
  }
  const text = await res.text()
  const data = text ? safeJSON(text) : null
  if (!res.ok) {
    // A dev proxy with no backend answers 502/504 with an HTML page.
    const msg = res.status >= 502 && res.status <= 504 ? 'offline' : (data && data.error) || res.statusText
    throw new ApiError(res.status, msg, data)
  }
  return data as T
}

function safeJSON(t: string) {
  try {
    return JSON.parse(t)
  } catch {
    return { error: t.length > 200 ? 'unexpected response' : t }
  }
}

export const api = {
  get: <T>(p: string) => request<T>('GET', p),
  post: <T>(p: string, b?: unknown) => request<T>('POST', p, b ?? {}),
  put: <T>(p: string, b?: unknown) => request<T>('PUT', p, b ?? {}),
  patch: <T>(p: string, b?: unknown) => request<T>('PATCH', p, b ?? {}),
  del: <T>(p: string) => request<T>('DELETE', p),
  upload: <T>(p: string, f: File) => {
    const fd = new FormData()
    fd.append('file', f)
    return request<T>('POST', p, fd)
  },
}

export type User = {
  id: string
  email: string
  name: string
  role?: string
  email_verified: boolean
  admin?: boolean
  language: 'en' | 'zh'
  mail_enabled: boolean
  created_at: string
}

export type Card = { kind: 'table'; table_id: string } | { kind: 'mission'; goal_id: string } | { kind: 'game'; game_id: string }

export type Message = {
  id: number
  role: 'user' | 'assistant' | 'event'
  content: string
  meta: Record<string, any> & { cards?: Card[]; mood?: string }
  created_at: string
  pending?: boolean
}

export type Conversation = { id: string; title: string; archived: boolean; updated_at: string; last?: string | null }

export type Task = {
  id: string
  key: string
  title: string
  kind: string
  status: string
  attempts: number
  deps: string[] | null
  error: string
  run_after: string
  started_at?: string | null
  finished_at?: string | null
  summary: string
  mode?: string
  role?: string
  activity?: { type: string; tool: string; at: string } | null
}

export type Approval = {
  id: string
  goal_id: string
  goal_title: string
  tool: string
  args: Record<string, any>
  preview: string
  gate: string
  status: 'pending' | 'approved' | 'rejected' | 'superseded'
  note: string
  created_at: string
  decided_at?: string | null
  can_decide: boolean
  own: boolean
}

export type GoalStatus = 'planning' | 'active' | 'paused' | 'needs_attention' | 'completed' | 'failed' | 'cancelled'

export type Goal = {
  id: string
  title: string
  objective: string
  domain?: string
  kind?: string
  skill: string
  status: GoalStatus
  criteria: string[] | null
  milestones: string[] | null
  usage: { iterations?: number; tool_calls?: number; prompt_tokens?: number; completion_tokens?: number; cost_usd?: number } | null
  limits: Record<string, number> | null
  attention_reason: string
  conversation_id: string
  created_at: string
  updated_at: string
  game_id?: string
  tasks?: Task[]
  approvals?: Approval[]
}

export type TimelineEvent = { id: number; goal_id: string; task_id: string | null; type: string; data: Record<string, any>; created_at: string }
export type Timeline = {
  events: TimelineEvent[] | null
  summaries: { summary: string; upto_event_id: number; created_at: string }[] | null
  next: { title: string; status: string; at: string }[] | null
}

export type GameCard = {
  id: string
  kind: 'builtin' | 'script'
  name: string
  summary: string
  min_seats: number
  max_seats: number
  hidden_info: boolean
  status: 'building' | 'draft' | 'published'
  visibility: 'private' | 'unlisted' | 'public'
  owner_name?: string
  version: number
  plays: number
  cover?: string
}
export type GameDetail = GameCard & { rules_md?: string; versions?: { version: number; created_at: string; report: any }[]; goal_id?: string }
export type GamesList = { builtin: GameCard[]; mine: GameCard[]; community: GameCard[] }

export type Agent = {
  id: string
  name: string
  name_zh?: string
  title?: string
  title_zh?: string
  bio?: string
  bio_zh?: string
  avatar: string
  style?: { tightness: number; aggression: number; bluff: number; talk: number }
}

export type SettingsData = { user: User; preferences: Record<string, any> }

// streamMessage posts a chat message and reads the server-sent reply.
export async function streamMessage(
  convId: string,
  content: string,
  clientMsgId: string,
  on: { meta?: (m: any) => void; delta?: (t: string) => void; done?: (id: number) => void; error?: (e: string) => void },
  signal?: AbortSignal,
) {
  const res = await fetch(`/api/conversations/${convId}/messages`, {
    method: 'POST',
    credentials: 'same-origin',
    headers: { 'Content-Type': 'application/json', 'X-Play': '1' },
    body: JSON.stringify({ content, client_msg_id: clientMsgId }),
    signal,
  })
  if (!res.ok || !res.body) {
    const t = await res.text()
    on.error?.(res.status >= 502 && res.status <= 504 ? 'offline' : safeJSON(t)?.error || res.statusText)
    return
  }
  const reader = res.body.getReader()
  const dec = new TextDecoder()
  let buf = ''
  let finished = false
  for (;;) {
    const { value, done } = await reader.read()
    if (done) break
    buf += dec.decode(value, { stream: true })
    let i
    while ((i = buf.indexOf('\n\n')) >= 0) {
      const frame = buf.slice(0, i)
      buf = buf.slice(i + 2)
      let ev = 'message'
      let data = ''
      for (const line of frame.split('\n')) {
        if (line.startsWith('event:')) ev = line.slice(6).trim()
        else if (line.startsWith('data:')) data += line.slice(5).trim()
      }
      const d = data ? safeJSON(data) : {}
      if (ev === 'meta') on.meta?.(d)
      else if (ev === 'delta') on.delta?.(d.t || '')
      else if (ev === 'done') {
        finished = true
        on.done?.(d.id)
      } else if (ev === 'error') {
        finished = true
        on.error?.(d.error)
      }
    }
  }
  if (!finished) on.error?.('interrupted')
}

export function uid() {
  return (crypto as any).randomUUID ? crypto.randomUUID() : Math.random().toString(36).slice(2) + Date.now().toString(36)
}
