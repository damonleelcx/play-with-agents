import type { Goal, Task } from '../lib/api'
import { dictFor, fmt, localeOf } from '../lib/i18n'

// How a build mission's tasks map onto the studio team. The planner names
// tasks freely, so a task's role is its explicit `role` when the server sends
// one, otherwise read from its key, title and mode.
export type Role = 'designer' | 'engineer' | 'playtester' | 'artist' | 'critic' | 'coordinator'
export const ROLES: Exclude<Role, 'coordinator'>[] = ['designer', 'engineer', 'playtester', 'artist', 'critic']

export function roleOf(t: Task): Role {
  const r = (t.role || '').toLowerCase()
  if (r === 'designer' || r === 'engineer' || r === 'playtester' || r === 'artist' || r === 'critic') return r
  const s = `${t.key} ${t.title} ${t.mode || ''} ${t.activity?.tool || ''}`.toLowerCase()
  if (/critic|critique|review|评审|审查/.test(s)) return 'critic'
  if (/illustrat|\bcover\b|封面|插画/.test(s)) return 'artist'
  if (/playtest|simulat|balance|试玩|模拟/.test(s)) return 'playtester'
  if (/engineer|module|code|implement|script|check_module|save_module|编写|实现|模块/.test(s)) return 'engineer'
  if (/design|rule|concept|save_rules|设计|规则/.test(s)) return 'designer'
  return 'coordinator'
}

// One display status per task: the engine's states, plus "retrying" for a
// ready task that has already failed once.
export type Shown = 'blocked' | 'ready' | 'running' | 'done' | 'failed' | 'retrying' | 'skipped' | 'waiting_approval' | 'cancelled'
export function shownStatus(t: Task): Shown {
  switch (t.status) {
    case 'leased':
    case 'running':
      return 'running'
    case 'succeeded':
    case 'done':
      return 'done'
    case 'ready':
      return t.attempts > 0 || t.activity?.type === 'task.retry' ? 'retrying' : 'ready'
    case 'failed':
    case 'skipped':
    case 'blocked':
    case 'cancelled':
    case 'waiting_approval':
      return t.status
    default:
      return 'blocked'
  }
}

export function workTasks(g?: Goal | null): Task[] {
  return (g?.tasks || []).filter((x) => x.kind !== 'plan')
}

export type Lane = { role: Role; total: number; done: number; state: 'idle' | 'working' | 'done' | 'failed' | 'waiting'; current?: Task }
export function lanes(tasks: Task[]): Lane[] {
  return ROLES.map((role) => {
    const mine = tasks.filter((t) => roleOf(t) === role)
    const st = mine.map(shownStatus)
    const done = st.filter((s) => s === 'done' || s === 'skipped').length
    const running = mine.find((t) => shownStatus(t) === 'running')
    const state: Lane['state'] = running ? 'working'
      : st.some((s) => s === 'failed') ? 'failed'
      : mine.length > 0 && done === mine.length ? 'done'
      : st.some((s) => s === 'retrying' || s === 'ready' || s === 'waiting_approval') ? 'waiting'
      : 'idle'
    return { role, total: mine.length, done, state, current: running || mine.find((t) => shownStatus(t) !== 'done') }
  })
}

export function progress(tasks: Task[]) {
  const done = tasks.filter((t) => t.status === 'succeeded' || t.status === 'skipped' || t.status === 'done').length
  return { done, total: tasks.length, pct: tasks.length ? Math.round((done / tasks.length) * 100) : 0 }
}

export const LIVE_STATUSES = ['planning', 'active', 'paused', 'needs_attention']

// The game a mission builds: explicit on the goal, or named in its approval.
export function gameIdOf(g: Goal | null): string | undefined {
  if (!g) return undefined
  if (g.game_id) return g.game_id
  for (const a of g.approvals || []) if (a.args?.game_id) return String(a.args.game_id)
  return undefined
}

export function fmtTime(s: string | null | undefined, lang: string) {
  if (!s) return ''
  const d = new Date(s)
  if (isNaN(d.getTime())) return ''
  return d.toLocaleString(localeOf(lang), { month: 'short', day: 'numeric', hour: '2-digit', minute: '2-digit' })
}

export function ago(at: string, lang: string) {
  const s = Math.max(0, Math.round((Date.now() - new Date(at).getTime()) / 1000))
  const m = Math.floor(s / 60)
  const h = Math.floor(m / 60)
  const d = Math.floor(h / 24)
  const a = dictFor(lang).common.ago
  return d > 0 ? fmt(a.d, { n: d }) : h > 0 ? fmt(a.h, { n: h }) : m > 0 ? fmt(a.m, { n: m }) : a.now
}
