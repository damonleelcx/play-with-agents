// Play with Agents: typed client for the Games / Agents / Tables API
// (docs/00-architecture.md is the contract).
import { api, uid } from './api'

// ── shapes ────────────────────────────────────────────────────────────────

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

export type GameDetail = GameCard & {
  rules_md: string
  versions: { version: number; created_at: string; report: any }[]
  goal_id?: string
}

export type GamesList = { builtin: GameCard[]; mine: GameCard[]; community: GameCard[] }

export type AgentStyle = { tightness: number; aggression: number; bluff: number; talk: number }
export type Agent = {
  id: string
  name: string
  name_zh: string
  title: string
  title_zh: string
  bio: string
  bio_zh: string
  avatar: string
  style: AgentStyle
}

export type TableSummary = {
  id: string
  name: string
  code: string
  game_name: string
  status: TableStatus
  seats_taken: number
  seats_total: number
  updated_at: string
}

export type TableStatus = 'lobby' | 'playing' | 'finished' | 'abandoned'

export type Range = { arg: string; min: number; max: number; step?: number }
export type MoveSpec = {
  type: string
  label: string
  args?: Record<string, any>
  range?: Range
  ui?: Record<string, any>
}
export type Move = { type: string; args?: Record<string, any> }

export type SeatInfo = {
  seat: number
  kind: 'human' | 'agent' | 'open'
  name: string
  avatar: string
  agent_id?: string
  is_me?: boolean
}

export type LogEntry = { seq: number; type: string; seat: number; text: string; at: string }
export type ChatLine = { id: string; seat: number; name: string; avatar: string; text: string; at: string; agent: boolean }
export type Outcome = { rank: number[]; score: number[]; summary: string }

export type TableView = {
  id: string
  name: string
  code: string
  game: { id: string; name: string; kind: string }
  status: TableStatus
  host_id: string
  is_host: boolean
  my_seat: number
  version: number
  seats: SeatInfo[]
  to_move: number[]
  deadline: string | null
  turn_seconds: number
  legal: MoveSpec[]
  view: { kind: 'holdem' | 'board' | string; data: any; status?: string } | null
  log: LogEntry[]
  chat: ChatLine[]
  outcome: Outcome | null
}

// ── Hold'em view data ─────────────────────────────────────────────────────

export type HoldemPlayer = {
  seat: number
  stack: number
  bet: number
  total_bet: number
  status: 'active' | 'folded' | 'allin' | 'out'
  cards: string[] | null
  last_action: string
  hand_name?: string
}
export type HoldemData = {
  hand_no: number
  hands_left: number | null
  street: 'preflop' | 'flop' | 'turn' | 'river' | 'showdown' | 'over'
  board: string[]
  pots: { amount: number; eligible: number[] }[]
  pot_total: number
  button: number
  sb_seat: number
  bb_seat: number
  small_blind: number
  big_blind: number
  current_bet: number
  min_raise_to: number
  to_act: number
  players: HoldemPlayer[]
  last_hand: null | {
    hand_no: number
    board: string[]
    winners: { seat: number; amount: number; hand_name: string; cards: string[] | null }[]
    shown: { seat: number; cards: string[]; hand_name: string }[]
  }
}

// ── Board view data ───────────────────────────────────────────────────────

export type BoardPiece = { shape?: 'disc' | 'square' | 'ring' | 'king' | 'text'; color?: string; glyph?: string; label?: string }
export type BoardCell = { piece?: BoardPiece | null; mark?: string; text?: string } | null
export type BoardCard = { face?: string; color?: string; hidden?: boolean }
export type BoardZone = { id: string; label?: string; owner?: number; layout?: 'row' | 'fan' | 'stack'; cards: BoardCard[] }
export type BoardData = {
  title?: string
  board?: { rows: number; cols: number; style?: 'grid' | 'checker' | 'go' | 'plain'; cells: BoardCell[][] }
  zones?: BoardZone[]
  players?: { seat: number; score?: number | string; info?: string; color?: string }[]
  counters?: { label: string; value: string | number }[]
  message?: string
}

// ── calls ─────────────────────────────────────────────────────────────────

export type NewSeat = { kind: 'me' | 'agent' | 'open'; agent_id?: string }
export type NewTable = {
  game_id: string
  name?: string
  options?: Record<string, any>
  turn_seconds?: number
  seats: NewSeat[]
}

export const playApi = {
  games: () => api.get<GamesList>('/api/games'),
  game: (id: string) => api.get<GameDetail>(`/api/games/${encodeURIComponent(id)}`),
  agents: () => api.get<Agent[]>('/api/agents'),
  tables: (scope: 'mine' | 'open') => api.get<TableSummary[]>(`/api/tables?scope=${scope}`),
  table: (id: string) => api.get<TableView>(`/api/tables/${encodeURIComponent(id)}`),
  createTable: (body: NewTable) => api.post<TableView>('/api/tables', body),
  join: (code: string) => api.post<TableView>('/api/tables/join', { code }),
  setSeat: (id: string, seat: number, body: { kind: 'agent'; agent_id: string } | { kind: 'open' }) =>
    api.put<TableView>(`/api/tables/${encodeURIComponent(id)}/seats/${seat}`, body),
  start: (id: string) => api.post<TableView>(`/api/tables/${encodeURIComponent(id)}/start`),
  move: (id: string, version: number, move: Move) =>
    api.post<TableView>(`/api/tables/${encodeURIComponent(id)}/moves`, { client_move_id: uid(), version, move }),
  chat: (id: string, text: string) =>
    api.post<{ ok: boolean }>(`/api/tables/${encodeURIComponent(id)}/chat`, { text, client_msg_id: uid() }),
  leave: (id: string) => api.post<{ ok: boolean }>(`/api/tables/${encodeURIComponent(id)}/leave`),
  rematch: (id: string) => api.post<TableView>(`/api/tables/${encodeURIComponent(id)}/rematch`),
  streamURL: (id: string) => `/api/tables/${encodeURIComponent(id)}/stream`,
}

// Player colours p0..p7 (the contract) and a resolver for board colours.
export const PLAYER_COLORS = ['#4f8cff', '#ff5d73', '#ffc04d', '#3ddc97', '#b07cff', '#ff8f40', '#3fd2ff', '#e6e6e6']
export function resolveColor(c?: string, fallback = 'var(--text-2)'): string {
  if (!c) return fallback
  const m = /^p([0-7])$/.exec(c)
  if (m) return PLAYER_COLORS[+m[1]]
  return c
}

// postMove needs the 409 body (the current table), which the shared helper
// drops, so it talks to fetch directly with the same CSRF header.
export type MoveResult =
  | { ok: true; table: TableView }
  | { ok: false; status: number; error: string; table?: TableView }

export async function postMove(id: string, version: number, move: Move): Promise<MoveResult> {
  try {
    const res = await fetch(`/api/tables/${encodeURIComponent(id)}/moves`, {
      method: 'POST',
      credentials: 'same-origin',
      headers: { 'Content-Type': 'application/json', 'X-Play': '1' },
      body: JSON.stringify({ client_move_id: uid(), version, move }),
    })
    const text = await res.text()
    let data: any = null
    try {
      data = text ? JSON.parse(text) : null
    } catch {
      data = { error: text }
    }
    if (res.ok) return { ok: true, table: data as TableView }
    return { ok: false, status: res.status, error: data?.error || res.statusText, table: data?.table }
  } catch (e: any) {
    return { ok: false, status: 0, error: e?.message || 'Network error' }
  }
}
