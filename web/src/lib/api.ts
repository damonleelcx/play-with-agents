// Thin client for the ACT API. Every mutating call carries X-ACT: the server
// rejects state changes without it, which is the CSRF defence.

export class ApiError extends Error {
  status: number
  constructor(status: number, message: string) {
    super(message)
    this.status = status
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const init: RequestInit = { method, credentials: 'same-origin', headers: { 'X-ACT': '1' } }
  if (body instanceof FormData) {
    init.body = body
  } else if (body !== undefined) {
    ;(init.headers as Record<string, string>)['Content-Type'] = 'application/json'
    init.body = JSON.stringify(body)
  }
  const res = await fetch(path, init)
  const text = await res.text()
  const data = text ? safeJSON(text) : null
  if (!res.ok) throw new ApiError(res.status, (data && data.error) || res.statusText)
  return data as T
}

function safeJSON(t: string) {
  try {
    return JSON.parse(t)
  } catch {
    return { error: t }
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
  role: 'client' | 'attorney' | 'physician' | 'admin'
  email_verified: boolean
  licensed: boolean
  admin: boolean
  language: 'en' | 'zh'
  mail_enabled: boolean
  created_at: string
}

export type Message = {
  id: number
  role: 'user' | 'assistant' | 'event'
  content: string
  meta: Record<string, any>
  created_at: string
  pending?: boolean
}

export type Conversation = { id: string; title: string; archived: boolean; updated_at: string; last?: string }

export type Task = {
  id: string
  key: string
  title: string
  kind: 'plan' | 'llm' | 'wait' | 'finish'
  status: string
  attempts: number
  deps: string[]
  error: string
  run_after: string
  started_at?: string
  finished_at?: string
  summary: string
  mode?: string
  activity?: { type: string; tool: string; at: string } | null
}

export type Approval = {
  id: string
  goal_id: string
  goal_title: string
  tool: string
  args: Record<string, any>
  preview: string
  gate: 'G1' | 'G2'
  required_role: string
  status: 'pending' | 'approved' | 'rejected' | 'superseded'
  note: string
  created_at: string
  decided_at?: string
  can_decide: boolean
  own: boolean
  client?: { name: string; email: string }
}

export type Goal = {
  id: string
  title: string
  objective: string
  domain: 'legal' | 'medical' | 'medlegal' | 'general'
  skill: string
  status: 'planning' | 'active' | 'paused' | 'needs_attention' | 'completed' | 'failed' | 'cancelled'
  criteria: string[]
  milestones: string[]
  usage: { iterations: number; tool_calls: number; prompt_tokens: number; completion_tokens: number; cost_usd: number }
  limits: Record<string, number>
  attention_reason: string
  conversation_id: string
  created_at: string
  updated_at: string
  progress?: { done: number; total: number }
  tasks?: Task[]
  documents?: { id: string; title: string; kind: string; version: number; created_at: string }[]
  approvals?: Approval[]
}

export type TimelineEvent = { id: number; goal_id: string; task_id: string; type: string; data: Record<string, any>; created_at: string }

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
    headers: { 'Content-Type': 'application/json', 'X-ACT': '1' },
    body: JSON.stringify({ content, client_msg_id: clientMsgId }),
    signal,
  })
  if (!res.ok || !res.body) {
    const t = await res.text()
    on.error?.(safeJSON(t)?.error || res.statusText)
    return
  }
  const reader = res.body.getReader()
  const dec = new TextDecoder()
  let buf = ''
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
      else if (ev === 'done') on.done?.(d.id)
      else if (ev === 'error') on.error?.(d.error)
    }
  }
}

export function uid() {
  return (crypto as any).randomUUID ? crypto.randomUUID() : Math.random().toString(36).slice(2) + Date.now().toString(36)
}
