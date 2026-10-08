import { useEffect, useMemo, useState } from 'react'
import { Link, useParams } from 'react-router-dom'
import { Img } from '../../components/Aoi'
import { uiLocale } from '../../lib/i18n'
import { md } from '../../lib/md'
import { playApi, type GameCard, type GameDetail as Detail } from '../../lib/playApi'
import { MOCK_GAMES } from './fixtures'
import Comments from './Comments'
import NewTableDialog from './NewTableDialog'
import { Spinner } from './parts'
import { usePlayT } from './strings'
import { useAgents } from './TableRoom'

const HOLDEM_RULES = `**No-limit Texas Hold'em** for 2 to 9 players. Chips are play money.

1. Each player gets two private cards. Five community cards come in three stages: the **flop** (3), the **turn** (1) and the **river** (1).
2. Betting goes clockwise. You can **fold**, **check**, **call**, **bet / raise** (any amount up to your stack) or go **all-in**.
3. The best five-card hand from your two cards and the board wins the pot. If everyone else folds, you win without a showdown.
4. Blinds double on a schedule, so the game always ends.`

export default function GameDetail() {
  const { id = '' } = useParams()
  const { s, f } = usePlayT()
  const agents = useAgents()
  const [g, setG] = useState<Detail | null>(null)
  const [err, setErr] = useState('')
  const [play, setPlay] = useState(false)

  useEffect(() => {
    setG(null)
    setErr('')
    playApi
      .game(id)
      .then(setG)
      .catch((e) => {
        const fallback = [...MOCK_GAMES.builtin, ...MOCK_GAMES.mine, ...MOCK_GAMES.community].find((x) => x.id === id) as unknown as GameCard | undefined
        if (fallback) setG({ ...fallback, rules_md: id === 'holdem' ? HOLDEM_RULES : '', versions: [] })
        else setErr(e?.message || 'Not found')
      })
  }, [id])
  const html = useMemo(() => (g?.rules_md ? md(g.rules_md) : ''), [g?.rules_md])

  if (err)
    return (
      <div className="pw-page">
        <div className="pw-center-note">
          <p>{err}</p>
          <Link to="/app/games" className="pw-btn pw-btn-soft">
            {s.detail.back}
          </Link>
        </div>
      </div>
    )
  if (!g)
    return (
      <div className="pw-page">
        <div className="pw-center-note">
          <Spinner />
        </div>
      </div>
    )

  const latest = g.versions?.[0]?.report
  return (
    <div className="pw-page pw-detail">
      <Link to="/app/games" className="pw-back">
        ‹ {s.detail.back}
      </Link>
      <header className="pw-detail-head">
        <div className="pw-game-cover is-big"><Img srcs={g.cover ? [g.cover] : []} alt="" fallback={<span className="pw-game-mono">{g.name.slice(0, 2)}</span>} /></div>
        <div>
          <span className="pw-eyebrow">
            {g.kind === 'builtin' ? s.detail.builtin : g.owner_name ? `${s.lobby.by} ${g.owner_name}` : ''} · v{g.version}
          </span>
          <h1>{g.id === 'holdem' ? s.lobby.holdemName : g.name}</h1>
          <p className="pw-muted">{g.summary}</p>
          <div className="pw-chips-row">
            <span className="pw-pill">{f(s.detail.seats, { min: g.min_seats, max: g.max_seats })}</span>
            <span className="pw-pill">{g.hidden_info ? s.detail.hidden : s.detail.perfect}</span>
            <span className="pw-pill">
              {g.plays} {s.lobby.plays}
            </span>
            {g.status !== 'published' && <span className={`pw-pill st-${g.status}`}>{s.lobby.status[g.status]}</span>}
          </div>
          <button type="button" className="pw-btn pw-btn-ember pw-btn-lg" disabled={g.status === 'building'} onClick={() => setPlay(true)}>
            {s.detail.play}
          </button>
        </div>
      </header>
      <div className="pw-detail-grid">
        <div className="pw-detail-main">
          <section className="pw-card-panel">
            <h3 className="pw-h3">{s.detail.rules}</h3>
            {html ? <div className="pw-md" dangerouslySetInnerHTML={{ __html: html }} /> : <p className="pw-muted">{s.detail.noRules}</p>}
          </section>
          <Comments gameId={g.id} published={g.status === 'published'} />
        </div>
        <aside className="pw-detail-side">
          {latest && (
            <section className="pw-card-panel">
              <h3 className="pw-h3">{s.detail.playtest}</h3>
              <Report report={latest} />
            </section>
          )}
          {g.versions?.length > 0 && (
            <section className="pw-card-panel">
              <h3 className="pw-h3">{s.detail.versions}</h3>
              <ul className="pw-versions">
                {g.versions.map((v) => (
                  <li key={v.version}>
                    <b>v{v.version}</b>
                    <small>{new Date(v.created_at).toLocaleDateString(uiLocale())}</small>
                    {v.report?.summary && <span>{String(v.report.summary)}</span>}
                  </li>
                ))}
              </ul>
            </section>
          )}
        </aside>
      </div>
      {play && <NewTableDialog games={[g]} agents={agents} initialGameId={g.id} onClose={() => setPlay(false)} />}
    </div>
  )
}

// The playtest report is free-form JSON; show its scalar fields and the seat
// win rates when present.
function Report({ report }: { report: any }) {
  const { s } = usePlayT()
  if (typeof report !== 'object' || !report) return <p className="pw-muted">{String(report)}</p>
  const scalars = Object.entries(report).filter(([k, v]) => k !== 'summary' && (typeof v === 'number' || typeof v === 'string' || typeof v === 'boolean'))
  const wins: number[] | undefined = Array.isArray(report.seat_wins) ? report.seat_wins : Array.isArray(report.win_rates) ? report.win_rates : undefined
  return (
    <div className="pw-report">
      {report.summary && <p>{String(report.summary)}</p>}
      <dl>
        {scalars.slice(0, 8).map(([k, v]) => (
          <div key={k}>
            <dt>{k.replace(/_/g, ' ')}</dt>
            <dd>{typeof v === 'number' ? (Number.isInteger(v) ? v : (v as number).toFixed(2)) : String(v)}</dd>
          </div>
        ))}
      </dl>
      {wins && (
        <div className="pw-winbars">
          <small>{s.detail.seatWins}</small>
          {wins.map((w, i) => {
            const total = wins.reduce((a, b) => a + b, 0) || 1
            const pct = w <= 1 && total <= wins.length ? w * 100 : (w / total) * 100
            return (
              <div key={i}>
                <span>#{i + 1}</span>
                <i style={{ width: `${pct}%`, background: `var(--p${i % 8})` }} />
                <b>{pct.toFixed(0)}%</b>
              </div>
            )
          })}
        </div>
      )}
    </div>
  )
}
