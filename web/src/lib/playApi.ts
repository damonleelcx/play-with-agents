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
export type AgentI18n = { name: string; title: string; bio: string }
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
  // per-language name/title/bio (en, zh, ko, ja); older servers omit it
  i18n?: Partial<Record<string, AgentI18n>>
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
  // missed the clock repeatedly: turns resolve with the default move after a short grace
  away?: boolean
}

export type LogEntry = { seq: number; type: string; seat: number; text: string; at: string }
export type Reaction = { emoji: string; count: number; names: string[]; mine: boolean }
export type ChatLine = {
  id: string
  seat: number
  name: string
  avatar: string
  text: string
  at: string
  agent: boolean
  reply_to?: string | number // the line this one answers
  whisper?: boolean // private: only the sender and `to` ever receive it
  to?: string
  to_seat?: number
  reactions?: Reaction[]
}
// someone is writing a line (agents while the model writes); stop clears it
export type Typing = { seat: number; name: string; agent?: boolean; stop?: boolean }
// who has the table open now: seated people by seat, everyone else by name (agents are always here)
export type Presence = { seats: number[]; watchers: string[] }
export const REACTIONS = ['👍', '😂', '😮', '🔥', '❤️', '👏', '😢', '🎉']
export type ChatOpts = { reply_to?: string | number; whisper_seat?: number }
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
  // nobody attending (everyone away or idle): status stays playing, nothing runs until /back or a move
  paused?: boolean
  // why it is paused: 'fault' = a move kept failing; only the host may resume (a second fault closes the table)
  paused_reason?: 'idle' | 'fault'
  presence?: Presence
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

export type PieceShape = 'disc' | 'square' | 'ring' | 'king' | 'text' | 'pawn' | 'meeple' | 'cube' | 'ship' | 'star' | 'hex'
export type BoardPiece = { shape?: PieceShape; color?: string; glyph?: string; label?: string }
export type BoardCell = { piece?: BoardPiece | null; pieces?: BoardPiece[]; card?: BoardCard | null; mark?: string; text?: string; blocked?: boolean } | null
export type BoardTheme = 'plain' | 'wood' | 'felt' | 'parchment' | 'stone' | 'night' | 'space' | 'ocean' | 'forest' | 'desert' | 'snow' | 'neon'
export type GridBoard = { rows: number; cols: number; style?: 'grid' | 'checker' | 'go' | 'plain' | 'tiles' | 'hex'; theme?: BoardTheme; cells: BoardCell[][] }
export type MapSpace = NonNullable<BoardCell> & {
  id: string
  x: number
  y: number
  shape?: 'circle' | 'square' | 'hex' | 'diamond' | 'star' | 'rect' | 'pill' | 'none'
  size?: number
  label?: string
  color?: string
}
export type MapLink = { from: string; to: string; style?: 'line' | 'dashed' | 'dotted' | 'arrow' | 'road' | 'rail' | 'river' | 'bridge'; color?: string; label?: string }
export type MapRegion = { x: number; y: number; w: number; h: number; shape?: 'rect' | 'ellipse' | 'blob'; color?: string; label?: string }
export type MapBoard = { spaces: MapSpace[]; links?: MapLink[]; regions?: MapRegion[]; theme?: BoardTheme; aspect?: number; grid?: boolean }
export const isMapBoard = (b: GridBoard | MapBoard | undefined | null): b is MapBoard => !!b && Array.isArray((b as MapBoard).spaces)
// A card: a plain face, a hidden back, or a rich story card (title, story
// line, effect, kind badge, cost/value badges, accent colour, who played it).
export type BoardCard = {
  face?: string
  color?: string
  hidden?: boolean
  title?: string
  text?: string
  effect?: string
  kind?: string
  cost?: number | string
  value?: number | string
  accent?: string
  seat?: number
}
export type StoryLine = { text: string; seat?: number; title?: string }
export type BoardZone = { id: string; label?: string; owner?: number; layout?: 'row' | 'fan' | 'stack' | 'grid'; area?: 'top' | 'bottom' | 'left' | 'right' | 'center'; cards: BoardCard[] }
export type BoardData = {
  title?: string
  board?: GridBoard | MapBoard
  zones?: BoardZone[]
  players?: { seat: number; score?: number | string; info?: string; color?: string }[]
  counters?: { label: string; value: string | number }[]
  message?: string
  story?: StoryLine[]
  prompt?: string
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
  chat: (id: string, text: string, opts: ChatOpts = {}) =>
    api.post<{ ok: boolean }>(`/api/tables/${encodeURIComponent(id)}/chat`, {
      text,
      client_msg_id: uid(),
      ...(opts.reply_to ? { reply_to: Number(opts.reply_to) } : {}),
      ...(opts.whisper_seat !== undefined ? { whisper_seat: opts.whisper_seat } : {}),
    }),
  typing: (id: string) => api.post<{ ok: boolean }>(`/api/tables/${encodeURIComponent(id)}/typing`, {}),
  react: (id: string, chatId: string | number, emoji: string) =>
    api.post<{ chat_id: number; reactions: Reaction[] }>(`/api/tables/${encodeURIComponent(id)}/chat/${encodeURIComponent(String(chatId))}/react`, { emoji }),
  leave: (id: string) => api.post<{ ok: boolean }>(`/api/tables/${encodeURIComponent(id)}/leave`),
  back: (id: string) => api.post<TableView>(`/api/tables/${encodeURIComponent(id)}/back`),
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
