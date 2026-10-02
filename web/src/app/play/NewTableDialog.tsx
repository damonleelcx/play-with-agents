import { useEffect, useMemo, useState, type CSSProperties } from 'react'
import { useNavigate } from 'react-router-dom'
import { playApi, type Agent, type GameCard, type NewSeat } from '../../lib/playApi'
import { Avatar } from './parts'
import { usePlayT } from './strings'
import { usePlayPrefs } from './usePrefs'

type SeatChoice = { kind: 'me' | 'agent' | 'open'; agent_id?: string }

export default function NewTableDialog({
  games,
  agents,
  initialGameId = 'holdem',
  onClose,
}: {
  games: GameCard[]
  agents: Agent[]
  initialGameId?: string
  onClose: () => void
}) {
  const { s, f, lang } = usePlayT()
  const prefs = usePlayPrefs()
  const nav = useNavigate()
  const [gameId, setGameId] = useState(initialGameId)
  const game = games.find((g) => g.id === gameId) || games[0]
  const min = game?.min_seats || 2
  const max = game?.max_seats || 9
  const [name, setName] = useState('')
  const orderedAgents = useMemo(() => {
    const fav = prefs.favorite_agents
    return [...agents].sort((a, b) => (fav.includes(b.id) ? 1 : 0) - (fav.includes(a.id) ? 1 : 0))
  }, [agents, prefs.favorite_agents])
  const defaultSeats = (n: number): SeatChoice[] =>
    Array.from({ length: n }, (_, i) => (i === 0 ? { kind: 'me' } : { kind: 'agent', agent_id: orderedAgents[(i - 1) % Math.max(1, orderedAgents.length)]?.id }))
  const [count, setCount] = useState(() => Math.min(max, Math.max(min, gameId === 'holdem' ? 6 : min)))
  const [seats, setSeats] = useState<SeatChoice[]>(() => defaultSeats(count))
  const [active, setActive] = useState(1)
  const [opts, setOpts] = useState({ starting_stack: 1000, small_blind: 10, big_blind: 20, blinds_double_every: 10, max_hands: 0 })
  const [clock, setClock] = useState<number>(prefs.turn_seconds)
  const [busy, setBusy] = useState(false)
  const [err, setErr] = useState('')
  useEffect(() => setClock(prefs.turn_seconds), [prefs.turn_seconds])

  useEffect(() => {
    const c = Math.min(max, Math.max(min, count))
    if (c !== count) setCount(c)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [gameId])
  useEffect(() => {
    setSeats((cur) => {
      const next = cur.slice(0, count)
      while (next.length < count) next.push(defaultSeats(count)[next.length])
      return next
    })
    if (active >= count) setActive(count - 1)
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [count])
  useEffect(() => {
    const onKey = (e: KeyboardEvent) => e.key === 'Escape' && onClose()
    window.addEventListener('keydown', onKey)
    return () => window.removeEventListener('keydown', onKey)
  }, [onClose])

  const setSeat = (i: number, c: SeatChoice) =>
    setSeats((cur) =>
      cur.map((x, j) => {
        if (j === i) return c
        // only one "me"
        if (c.kind === 'me' && x.kind === 'me') return { kind: 'open' }
        return x
      }),
    )
  const agentName = (a?: Agent) => (a ? (lang === 'zh' ? a.name_zh || a.name : a.name) : '')
  const cur = seats[active]
  const curAgent = cur?.kind === 'agent' ? agents.find((a) => a.id === cur.agent_id) : undefined

  const create = async () => {
    setBusy(true)
    setErr('')
    try {
      const body = {
        game_id: gameId,
        name: name.trim() || undefined,
        turn_seconds: clock,
        options: gameId === 'holdem' ? opts : undefined,
        seats: seats.map((x): NewSeat => (x.kind === 'agent' ? { kind: 'agent', agent_id: x.agent_id } : { kind: x.kind })),
      }
      const t = await playApi.createTable(body)
      nav(`/app/table/${t.id}`)
    } catch (e: any) {
      setErr(e?.message || 'Could not create the table')
      setBusy(false)
    }
  }
  const num = (k: keyof typeof opts, label: string, hint?: string) => (
    <label className="pw-field">
      <span>{label}</span>
      <input
        className="pw-input"
        inputMode="numeric"
        value={opts[k]}
        onChange={(e) => setOpts((o) => ({ ...o, [k]: Math.max(0, Number(e.target.value.replace(/\D/g, '')) || 0) }))}
      />
      {hint && <small>{hint}</small>}
    </label>
  )

  return (
    <div className="pw-modal" role="dialog" aria-modal="true" aria-label={s.newTable.title} onClick={(e) => e.target === e.currentTarget && onClose()}>
      <div className="pw-modal-card pw-newtable">
        <div className="pw-modal-head">
          <h3>{s.newTable.title}</h3>
          <button type="button" className="pw-icon-btn" onClick={onClose} aria-label={s.newTable.cancel}>
            ×
          </button>
        </div>
        <div className="pw-modal-body">
          <div className="pw-grid2">
            <label className="pw-field">
              <span>{s.newTable.game}</span>
              <select className="pw-input" value={gameId} onChange={(e) => setGameId(e.target.value)}>
                {games.map((g) => (
                  <option key={g.id} value={g.id}>
                    {g.id === 'holdem' ? s.lobby.holdemName : g.name}
                  </option>
                ))}
              </select>
            </label>
            <label className="pw-field">
              <span>{s.newTable.name}</span>
              <input className="pw-input" value={name} maxLength={60} placeholder={s.newTable.namePh} onChange={(e) => setName(e.target.value)} />
            </label>
          </div>

          <div className="pw-field">
            <span>
              {s.newTable.seats} <b className="pw-count">{count}</b>
            </span>
            <div className="pw-seat-count">
              {Array.from({ length: max - min + 1 }, (_, i) => min + i).map((n) => (
                <button key={n} type="button" className={n === count ? 'on' : ''} onClick={() => setCount(n)}>
                  {n}
                </button>
              ))}
            </div>
          </div>

          <div className="pw-seatlist">
            {seats.map((x, i) => {
              const a = x.kind === 'agent' ? agents.find((y) => y.id === x.agent_id) : undefined
              return (
                <button key={i} type="button" className={`pw-seatpick kind-${x.kind} ${active === i ? 'on' : ''}`} onClick={() => setActive(i)}>
                  {x.kind === 'agent' && a ? (
                    <Avatar name={a.name} src={a.avatar || `/play/agents/${a.id}.webp`} seat={i} size={40} agent />
                  ) : x.kind === 'me' ? (
                    <span className="pw-av pw-av-initials" style={{ width: 40, height: 40, '--av': 'var(--blue-500)' } as CSSProperties}>
                      ★
                    </span>
                  ) : (
                    <span className="pw-av pw-av-empty" style={{ width: 40, height: 40 }} />
                  )}
                  <small>{f(s.newTable.seatN, { n: i + 1 })}</small>
                  <b>{x.kind === 'me' ? s.newTable.me : x.kind === 'agent' ? agentName(a) : s.newTable.open}</b>
                </button>
              )
            })}
          </div>

          {cur && (
            <div className="pw-seat-editor">
              <div className="pw-seg" role="radiogroup">
                {(['me', 'agent', 'open'] as const).map((k) => (
                  <button
                    key={k}
                    type="button"
                    role="radio"
                    aria-checked={cur.kind === k}
                    className={cur.kind === k ? 'on' : ''}
                    onClick={() => setSeat(active, k === 'agent' ? { kind: 'agent', agent_id: cur.agent_id || orderedAgents[0]?.id } : { kind: k })}
                  >
                    {k === 'me' ? s.newTable.me : k === 'agent' ? s.newTable.agent : `${s.newTable.open} · ${s.newTable.openHint}`}
                  </button>
                ))}
              </div>
              {cur.kind === 'agent' && (
                <>
                  <div className="pw-agent-grid" role="listbox" aria-label={s.newTable.pickAgent}>
                    {orderedAgents.map((a) => (
                      <button
                        key={a.id}
                        type="button"
                        role="option"
                        aria-selected={cur.agent_id === a.id}
                        className={cur.agent_id === a.id ? 'on' : ''}
                        onClick={() => setSeat(active, { kind: 'agent', agent_id: a.id })}
                      >
                        <Avatar name={a.name} src={a.avatar || `/play/agents/${a.id}.webp`} seat={active} size={52} agent />
                        <b>{agentName(a)}</b>
                        <small>{lang === 'zh' ? a.title_zh : a.title}</small>
                      </button>
                    ))}
                  </div>
                  {curAgent && (
                    <div className="pw-agent-blurb">
                      <p>{lang === 'zh' ? curAgent.bio_zh : curAgent.bio}</p>
                      <div className="pw-style-bars">
                        {(
                          [
                            ['tightness', s.newTable.tight],
                            ['aggression', s.newTable.aggr],
                            ['bluff', s.newTable.bluff],
                            ['talk', s.newTable.talk],
                          ] as const
                        ).map(([k, label]) => (
                          <span key={k}>
                            <small>{label}</small>
                            <i style={{ '--v': curAgent.style?.[k] ?? 0.5 } as CSSProperties} />
                          </span>
                        ))}
                      </div>
                    </div>
                  )}
                </>
              )}
            </div>
          )}

          {gameId === 'holdem' && (
            <fieldset className="pw-fieldset">
              <legend>{s.newTable.options}</legend>
              <div className="pw-grid3">
                {num('starting_stack', s.newTable.stack)}
                {num('small_blind', s.newTable.sb)}
                {num('big_blind', s.newTable.bb)}
                {num('blinds_double_every', s.newTable.double, s.newTable.doubleUnit)}
                {num('max_hands', s.newTable.maxHands, s.newTable.maxHandsHint)}
              </div>
            </fieldset>
          )}

          <div className="pw-field">
            <span>{s.newTable.clock}</span>
            <div className="pw-seg">
              {[15, 30, 60, 0].map((v) => (
                <button key={v} type="button" className={clock === v ? 'on' : ''} onClick={() => setClock(v)}>
                  {v ? `${v}s` : s.newTable.noClock}
                </button>
              ))}
            </div>
          </div>
          {err && <div className="pw-alert">{err}</div>}
        </div>
        <div className="pw-modal-actions">
          <button type="button" className="pw-btn pw-btn-ghost" onClick={onClose}>
            {s.newTable.cancel}
          </button>
          <button type="button" className="pw-btn pw-btn-primary" disabled={busy || !seats.some((x) => x.kind === 'me')} onClick={create}>
            {busy ? s.newTable.creating : s.newTable.create}
          </button>
        </div>
      </div>
    </div>
  )
}
