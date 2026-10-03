import { useCallback, useEffect, useState, type CSSProperties } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { playApi, type GameCard, type GamesList, type TableSummary } from '../../lib/playApi'
import { MOCK_GAMES, ROSTER } from './fixtures'
import { Img } from '../../components/Aoi'
import { uiLocale } from '../../lib/i18n'
import NewTableDialog from './NewTableDialog'
import { Avatar, PlayingCard } from './parts'
import { usePlayT } from './strings'
import { useAgents } from './TableRoom'
import { usePlayPrefs } from './usePrefs'

const HOLDEM_FALLBACK: GameCard = MOCK_GAMES.builtin[0] as unknown as GameCard

export default function Lobby() {
  const { s, agentText } = usePlayT()
  const prefs = usePlayPrefs()
  const nav = useNavigate()
  const [sp] = useSearchParams()
  const mock = sp.has('mock')
  const agents = useAgents()
  const [games, setGames] = useState<GamesList | null>(null)
  const [openTables, setOpenTables] = useState<TableSummary[]>([])
  const [myTables, setMyTables] = useState<TableSummary[]>([])
  const [err, setErr] = useState(false)
  const [dialog, setDialog] = useState<string | null>(null)
  const [code, setCode] = useState('')
  // The hero's cover art: shown once it has loaded, dropped for the felt art if it fails.
  const [heroArt, setHeroArt] = useState<{ ok?: string; bad?: string }>({})

  const load = useCallback(() => {
    setErr(false)
    if (mock) {
      setGames(MOCK_GAMES as unknown as GamesList)
      const now = new Date().toISOString()
      setOpenTables([{ id: 'demo', name: 'Yuki’s late game', code: 'YK42Q9', game_name: "Texas Hold'em", status: 'lobby', seats_taken: 4, seats_total: 6, updated_at: now }])
      setMyTables([
        { id: 'demo', name: 'Friday night hold’em', code: 'AOI7K2', game_name: "Texas Hold'em", status: 'playing', seats_taken: 6, seats_total: 6, updated_at: now },
        { id: 'demo2', name: 'Connect Four with Ren', code: 'C4REN1', game_name: 'Connect Four', status: 'finished', seats_taken: 2, seats_total: 2, updated_at: now },
      ])
      return
    }
    playApi.games().then(setGames).catch(() => setErr(true))
    playApi.tables('open').then((t) => setOpenTables(t || [])).catch(() => {})
    playApi.tables('mine').then((t) => setMyTables(t || [])).catch(() => {})
  }, [mock])
  useEffect(load, [load])
  useEffect(() => {
    const onVis = () => document.visibilityState === 'visible' && load()
    document.addEventListener('visibilitychange', onVis)
    return () => document.removeEventListener('visibilitychange', onVis)
  }, [load])

  const all: GameCard[] = [...(games?.builtin || [HOLDEM_FALLBACK]), ...(games?.mine || []), ...(games?.community || [])]
  const playable = all.filter((g) => g.status !== 'building')
  const holdem = all.find((g) => g.id === 'holdem') || HOLDEM_FALLBACK
  const otherBuiltins = (games?.builtin || []).filter((g) => g.id !== 'holdem')

  const join = (e: React.FormEvent) => {
    e.preventDefault()
    const c = code.trim().toUpperCase()
    if (c) nav(`/app/join/${encodeURIComponent(c)}`)
  }

  return (
    <div className={`pw-page pw-lobby felt-${prefs.felt}`}>
      <header className="pw-lobby-top">
        <div>
          <span className="pw-eyebrow">{s.lobby.eyebrow}</span>
          <h1>{s.lobby.title}</h1>
          <p className="pw-muted">{s.lobby.sub}</p>
        </div>
        <div className="pw-lobby-cta">
          <form className="pw-joinform" onSubmit={join}>
            <input
              className="pw-input"
              value={code}
              maxLength={8}
              placeholder={s.join.ph}
              aria-label={s.lobby.joinCode}
              onChange={(e) => setCode(e.target.value.toUpperCase().replace(/[^A-Z0-9]/g, ''))}
            />
            <button type="submit" className="pw-btn pw-btn-ghost" disabled={!code.trim()}>
              {s.lobby.joinCode}
            </button>
          </form>
          <button type="button" className="pw-btn pw-btn-primary" onClick={() => setDialog('holdem')}>
            + {s.lobby.newTable}
          </button>
        </div>
      </header>

      {err && (
        <div className="pw-alert">
          {s.lobby.loadError}{' '}
          <button type="button" className="pw-link-btn" onClick={load}>
            {s.lobby.retry}
          </button>
        </div>
      )}

      {/* hero: Texas Hold'em */}
      <section className="pw-hero">
        <div className={`pw-hero-felt${holdem.cover && heroArt.ok === holdem.cover ? ' has-art' : ''}`}>
          {holdem.cover && heroArt.bad !== holdem.cover && (
            <img className="pw-hero-art" src={holdem.cover} alt="" decoding="async" draggable={false}
              onLoad={() => setHeroArt({ ok: holdem.cover })} onError={() => setHeroArt({ bad: holdem.cover })} />
          )}
          <div className="pw-felt-light" />
          <div className="pw-hero-cards" aria-hidden>
            <PlayingCard card="As" size="lg" fourColor={prefs.four_color_deck} />
            <PlayingCard card="Kh" size="lg" fourColor={prefs.four_color_deck} />
            <PlayingCard hidden size="lg" back={prefs.card_back} />
          </div>
          <div className="pw-hero-chips" aria-hidden>
            {[0, 1, 2, 3, 4].map((i) => (
              <span key={i} className={`pw-chip chip-${['gold', 'navy', 'red', 'green', 'violet'][i]}`} style={{ '--i': i } as CSSProperties} />
            ))}
          </div>
        </div>
        <div className="pw-hero-text">
          <span className="pw-eyebrow is-warm">★ {s.lobby.featured}</span>
          <h2>{s.lobby.holdemName}</h2>
          <p>{s.lobby.holdemSummary}</p>
          <div className="pw-hero-agents">
            {(agents.length ? agents : ROSTER).slice(0, 6).map((a, i) => (
              <span key={a.id} className="pw-hero-agent" style={{ '--i': i } as CSSProperties} title={agentText(a, 'name')}>
                <Avatar name={a.name} src={a.avatar || `/play/agents/${a.id}.webp`} seat={i} size={40} agent />
              </span>
            ))}
            <small>
              {holdem.min_seats}–{holdem.max_seats} {s.lobby.seats} · {holdem.plays.toLocaleString(uiLocale())} {s.lobby.plays}
            </small>
          </div>
          <div className="pw-hero-actions">
            <button type="button" className="pw-btn pw-btn-ember pw-btn-lg" onClick={() => setDialog('holdem')}>
              {s.lobby.playNow}
            </button>
            <Link className="pw-btn pw-btn-ghost pw-btn-lg" to="/app/games/holdem">
              {s.detail.rules}
            </Link>
          </div>
        </div>
      </section>

      {/* tables */}
      <div className="pw-two">
        <TableList title={s.lobby.myTables} items={myTables} action={s.lobby.open} onOpen={(t) => nav(`/app/table/${t.id}`)} />
        <TableList title={s.lobby.openTables} items={openTables} action={s.lobby.join} onOpen={(t) => nav(`/app/join/${t.code}`)} />
      </div>

      {otherBuiltins.length > 0 && <GameGrid title={s.lobby.classics} games={otherBuiltins} empty="" onPlay={setDialog} />}
      <GameGrid title={s.lobby.myGames} games={games?.mine || []} empty={s.lobby.noGames} onPlay={setDialog} />
      <GameGrid title={s.lobby.community} games={games?.community || []} empty={s.lobby.noCommunity} onPlay={setDialog} />

      {dialog && <NewTableDialog games={playable.length ? playable : [holdem]} agents={agents} initialGameId={dialog} onClose={() => setDialog(null)} />}
    </div>
  )
}

function TableList({ title, items, action, onOpen }: { title: string; items: TableSummary[]; action: string; onOpen: (t: TableSummary) => void }) {
  const { s } = usePlayT()
  return (
    <section className="pw-section">
      <h3 className="pw-h3">{title}</h3>
      {items.length === 0 ? (
        <div className="pw-empty">{s.lobby.noTables}</div>
      ) : (
        <ul className="pw-tables">
          {items.map((t) => (
            <li key={t.id + t.code}>
              <span className={`pw-status-dot st-${t.status}`} />
              <span className="pw-tables-main">
                <b>{t.name}</b>
                <small>
                  {t.game_name} · {s.card.status[t.status]} · {t.seats_taken}/{t.seats_total}
                </small>
              </span>
              <span className="pw-seatbar" aria-hidden>
                {Array.from({ length: t.seats_total }, (_, i) => (
                  <i key={i} className={i < t.seats_taken ? 'on' : ''} />
                ))}
              </span>
              <button type="button" className="pw-btn pw-btn-soft pw-btn-sm" onClick={() => onOpen(t)}>
                {action}
              </button>
            </li>
          ))}
        </ul>
      )}
    </section>
  )
}

function GameGrid({ title, games, empty, onPlay }: { title: string; games: GameCard[]; empty: string; onPlay: (id: string) => void }) {
  const { s } = usePlayT()
  if (!games.length && !empty) return null
  return (
    <section className="pw-section">
      <h3 className="pw-h3">{title}</h3>
      {games.length === 0 ? (
        <div className="pw-empty">{empty}</div>
      ) : (
        <div className="pw-games">
          {games.map((g, i) => (
            <article key={g.id} className="pw-game" style={{ '--hue': (i * 57 + g.name.length * 23) % 360 } as CSSProperties}>
              <Link to={`/app/games/${g.id}`} className="pw-game-cover">
                <Img srcs={g.cover ? [g.cover] : []} alt="" lazy fallback={<span className="pw-game-mono">{g.name.slice(0, 2)}</span>} />
                {g.status !== 'published' && <span className={`pw-pill st-${g.status}`}>{s.lobby.status[g.status]}</span>}
              </Link>
              <div className="pw-game-body">
                <Link to={`/app/games/${g.id}`} className="pw-game-name">
                  {g.name}
                </Link>
                <p>{g.summary}</p>
                <div className="pw-game-meta">
                  <small>
                    {g.min_seats === g.max_seats ? g.min_seats : `${g.min_seats}–${g.max_seats}`} {s.lobby.seats} · {g.plays} {s.lobby.plays}
                    {g.owner_name ? ` · ${s.lobby.by} ${g.owner_name}` : ''}
                  </small>
                  <button type="button" className="pw-btn pw-btn-soft pw-btn-sm" disabled={g.status === 'building'} onClick={() => onPlay(g.id)}>
                    {s.detail.play}
                  </button>
                </div>
              </div>
            </article>
          ))}
        </div>
      )}
    </section>
  )
}
