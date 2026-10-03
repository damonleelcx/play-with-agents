import { useEffect, useRef, useState, type CSSProperties } from 'react'
import { Link } from 'react-router-dom'
import HoloCard from '../components/HoloCard'
import LangSwitch from '../components/LangSwitch'
import { useI18n } from '../lib/i18n'
import { useSession } from '../lib/session'
import Img from './landing/Img'
import PokerTable from './landing/PokerTable'
import {
  IArrow, IBulb, IChat, ICheck, IChip, IClock, IClose, IDice, IEyeOff, IMenu, IMoon, ISend, IShuffle, ISpark, ISun, Logo, useReveal,
} from './landing/parts'
import { landingStrings } from './landing/strings'
import { useLandingTheme } from './landing/theme'
import '../styles/landing.css'

// Play with Agents, the landing page. Five sections:
// 1 hero (studio-grey panel, giant headline, Aoi's holo card in front),
// 2 meet Aoi, 3 the table is always full, 4 Texas Hold'em tonight,
// 5 make your own game + the final call to action.

const PLAYER_COLORS: Record<string, string> = {
  aoi: 'var(--p6)', ren: 'var(--p3)', mika: 'var(--p1)', bram: 'var(--p2)', nova: 'var(--p4)', lin: 'var(--p5)',
}
const PROMISE_ICONS = [IChip, IShuffle, IEyeOff, IBulb, IClock]
const STEP_ICONS = [IChat, ISpark, IDice]

export default function Landing() {
  const { lang } = useI18n()
  const t = landingStrings[lang] ?? landingStrings.en
  const { user } = useSession()
  const { mode, toggle } = useLandingTheme(!!user)
  const [menu, setMenu] = useState(false)
  const [dock, setDock] = useState(false)
  const heroRef = useRef<HTMLElement>(null)
  const menuBtn = useRef<HTMLButtonElement>(null)
  const menuFirst = useRef<HTMLAnchorElement>(null)

  useEffect(() => {
    document.title = t.meta.title
  }, [t])

  useReveal([lang])

  // the floating dock appears once the hero has scrolled away
  useEffect(() => {
    const el = heroRef.current
    if (!el || !('IntersectionObserver' in window)) return
    const io = new IntersectionObserver(([e]) => setDock(!e.isIntersecting), { rootMargin: '-35% 0px 0px 0px' })
    io.observe(el)
    return () => io.disconnect()
  }, [])

  useEffect(() => {
    if (!menu) return
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        setMenu(false)
        menuBtn.current?.focus()
      }
    }
    document.addEventListener('keydown', onKey)
    document.body.style.overflow = 'hidden'
    menuFirst.current?.focus()
    return () => {
      document.removeEventListener('keydown', onKey)
      document.body.style.overflow = ''
    }
  }, [menu])

  const authActions = (compact = false) =>
    user ? (
      <Link className="pill pill-ink" to="/app">
        {t.nav.app} <IArrow width={16} height={16} />
      </Link>
    ) : (
      <>
        {!compact && (
          <Link className="pill pill-grey" to="/signin">
            {t.nav.signin}
          </Link>
        )}
        <Link className="pill pill-ink" to="/signup">
          {t.nav.start}
        </Link>
      </>
    )

  const startHref = user ? '/app' : '/signup'

  // sun in dark (go light), moon in light (go dark)
  const themeBtn = (
    <button
      type="button"
      className="theme-btn"
      aria-label={mode === 'light' ? t.nav.toDark : t.nav.toLight}
      title={mode === 'light' ? t.nav.toDark : t.nav.toLight}
      onClick={toggle}
    >
      {mode === 'light' ? <IMoon width={18} height={18} /> : <ISun width={18} height={18} />}
    </button>
  )

  return (
    <div className={`lp lang-${lang}`}>
      <a className="lp-skip" href="#main">
        {lang === 'zh' ? '跳到正文' : 'Skip to content'}
      </a>

      {/* ───────────── 1 · HERO ───────────── */}
      <header className="hero" id="top" ref={heroRef}>
        <div className="hero-panel">
          <nav className="hero-nav" aria-label={lang === 'zh' ? '主导航' : 'Primary'}>
            <a href="#top" className="lp-logo" aria-label={t.nav.home}>
              <Logo className="lp-logo-mark" />
              <span className="lp-logo-word">
                PLAY WITH
                <br />
                AGENTS
              </span>
            </a>
            <ul className="hero-links">
              {t.nav.links.map(([id, label]) => (
                <li key={id}>
                  <a href={`#${id}`}>{label}</a>
                </li>
              ))}
            </ul>
            <div className="hero-actions">
              <LangSwitch />
              {themeBtn}
              <span className="hero-auth">{authActions()}</span>
              <button
                ref={menuBtn}
                type="button"
                className="round-btn"
                aria-label={t.nav.menu}
                aria-expanded={menu}
                aria-controls="lp-menu"
                onClick={() => setMenu(true)}
              >
                <IMenu width={22} height={22} />
              </button>
            </div>
          </nav>

          <p className="hero-tag mono">
            {t.hero.tagline[0]}
            <br />
            {t.hero.tagline[1]}
          </p>

          <div className="hero-stage">
            <h1 className="hero-title">
              <span className="sr-only">{t.hero.srTitle}</span>
              <span className="hero-word w1" aria-hidden="true">{t.hero.words[0]}</span>
              <span className="hero-word w2" aria-hidden="true">{t.hero.words[1]}</span>
            </h1>
            <div className="hero-card">
              <HoloCard
                key={lang}
                title={t.card.title}
                subtitle={t.card.subtitle}
                kicker={t.card.kicker}
                plate={t.card.plate}
                back={t.card.back}
                backSub={t.card.backSub}
                caption={t.card.caption}
                captionTouch={t.card.captionTouch}
                label={t.card.label}
                portraitAlt={t.card.portraitAlt}
                images={['/play/aoi/aoi-card.webp', '/play/aoi/aoi-portrait.webp']}
              />
            </div>
            {t.hero.pills.map((p, i) => (
              <span key={p} className={`float-pill fp${i}`} aria-hidden="true">
                {p}
              </span>
            ))}
          </div>

          <div className="hero-foot">
            <div className="hero-foot-l">
              <p className="mono">{t.hero.bottom}</p>
              <ul className="chips" aria-label={lang === 'zh' ? '亮点' : 'Highlights'}>
                {t.hero.chips.map((c) => (
                  <li key={c}>{c}</li>
                ))}
              </ul>
            </div>
            <Link to={startHref} className="bubble">
              <span className="bubble-ava">
                <Img srcs={['/play/agents/aoi.webp', '/play/aoi/aoi-portrait.webp']} alt={t.hero.avatarAlt} eager fallback={<span className="bubble-ava-fb">葵</span>} />
                <i className="bubble-dot" aria-hidden="true" />
              </span>
              <span className="bubble-text mono">
                {t.hero.bubble[0]} <b>{t.hero.bubble[1]}</b>
              </span>
              <span className="bubble-go" aria-hidden="true">
                <IArrow width={16} height={16} />
              </span>
              <span className="sr-only">{t.hero.bubbleCta}</span>
            </Link>
          </div>
        </div>
      </header>

      <main id="main">
        {/* ───────────── 2 · MEET AOI ───────────── */}
        <section id="meet" className="sec meet" aria-labelledby="meet-h">
          <div className="wrap meet-grid">
            <div className="meet-figure rv">
              <div className="meet-halo" aria-hidden="true" />
              <span className="meet-kanji" aria-hidden="true">葵</span>
              <Img
                srcs={['/play/aoi/aoi-full.webp', '/play/aoi/aoi-outfit-default.webp']}
                alt={t.meet.fullAlt}
                className="meet-full"
                fallback={
                  <div className="meet-full-fb" role="img" aria-label={t.meet.fullAlt}>
                    <span>葵</span>
                  </div>
                }
              />
              <ul className="meet-verbs" aria-label={lang === 'zh' ? '她会做的事' : 'What she does'}>
                {t.meet.verbs.map((v, i) => (
                  <li key={v} style={{ '--i': i } as CSSProperties}>{v}</li>
                ))}
              </ul>
            </div>

            <div className="meet-copy">
              <p className="eyebrow rv">
                <span>02</span> {t.meet.eyebrow}
              </p>
              <h2 id="meet-h" className="h2 rv">
                {t.meet.title[0]}
                <br />
                <em>{t.meet.title[1]}</em>
              </h2>
              <p className="lead rv">{t.meet.lead}</p>

              <ol className="traits rv">
                {t.meet.traits.map(([k, d], i) => (
                  <li key={k}>
                    <span className="mono">0{i + 1}</span>
                    <b>{k}</b>
                    <span>{d}</span>
                  </li>
                ))}
              </ol>

              <div className="profile rv">
                <div className="profile-head">
                  <h3>{t.meet.profile.title}</h3>
                  <span className="mono">AOI · 葵 · 001</span>
                </div>
                <dl>
                  {t.meet.profile.rows.map(([k, v]) => (
                    <div key={k}>
                      <dt>{k}</dt>
                      <dd>{v}</dd>
                    </div>
                  ))}
                </dl>
                <p className="profile-quote">{t.meet.profile.quote}</p>
              </div>
            </div>
          </div>

          <div className="wrap meet-strips">
            <div className="strip rv">
              <div className="strip-head">
                <h3>{t.meet.faces.title}</h3>
                <p>{t.meet.faces.note}</p>
              </div>
              <ul className="faces">
                {t.meet.faces.items.map(([id, label]) => (
                  <li key={id}>
                    <div className="tile">
                      <Img srcs={[`/play/aoi/aoi-face-${id}.webp`]} alt={`${t.hero.avatarAlt} · ${label}`} fallback={<span className="tile-fb">葵</span>} />
                    </div>
                    <span>{label}</span>
                  </li>
                ))}
              </ul>
            </div>
            <div className="strip rv">
              <div className="strip-head">
                <h3>{t.meet.outfits.title}</h3>
                <p>{t.meet.outfits.note}</p>
              </div>
              <ul className="outfits">
                {t.meet.outfits.items.map(([id, label]) => (
                  <li key={id}>
                    <div className="tile tall">
                      <Img srcs={[`/play/aoi/aoi-outfit-${id}.webp`]} alt={`${t.hero.avatarAlt} · ${label}`} fallback={<span className="tile-fb">葵</span>} />
                    </div>
                    <span>{label}</span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </section>

        {/* ───────────── 3 · THE TABLE IS ALWAYS FULL ───────────── */}
        <section id="table" className="sec table" aria-labelledby="table-h">
          <div className="wrap">
            <div className="sec-head">
              <p className="eyebrow rv">
                <span>03</span> {t.table.eyebrow}
              </p>
              <h2 id="table-h" className="h2 rv">
                {t.table.title.map((l, i) => (
                  <span key={i} className={i === 2 ? 'accent' : undefined}>
                    {l}{' '}
                  </span>
                ))}
              </h2>
              <p className="lead rv">{t.table.lead}</p>
            </div>

            <ul className="roster">
              {t.table.roster.map((a, i) => (
                <li key={a.id} className="agent rv" style={{ '--pc': PLAYER_COLORS[a.id], '--i': i } as CSSProperties}>
                  <div className="agent-art">
                    <Img srcs={[`/play/agents/${a.id}.webp`]} alt={a.name} fallback={<span className="agent-fb">{a.name[0]}</span>} />
                    <span className="agent-no mono">0{i + 1}</span>
                  </div>
                  <div className="agent-body">
                    <h3>
                      {a.name} {a.native && <small>{a.native}</small>}
                    </h3>
                    <p className="agent-style">
                      <span className="mono">{t.table.plays}</span> {a.style}
                    </p>
                    <p className="agent-persona">{a.persona}</p>
                  </div>
                </li>
              ))}
            </ul>

            <div className="invite rv">
              <div className="invite-copy">
                <h3>{t.table.invite.title}</h3>
                <p>{t.table.invite.body}</p>
                <ol className="invite-steps">
                  {t.table.invite.steps.map((s, i) => (
                    <li key={s}>
                      <span className="mono">{i + 1}</span>
                      {s}
                    </li>
                  ))}
                </ol>
              </div>
              <div className="invite-visual" role="img" aria-label={t.table.invite.seatsAlt}>
                <div className="ticket" aria-hidden="true">
                  <span className="mono">{t.table.invite.codeLabel}</span>
                  <b>{t.table.invite.code}</b>
                </div>
                <div className="ring" aria-hidden="true">
                  {[
                    { k: 'you', label: t.table.invite.you },
                    { k: 'f1', label: t.table.invite.friend },
                    { k: 'ren' },
                    { k: 'f2', label: t.table.invite.friend },
                    { k: 'mika' },
                    { k: 'lin' },
                  ].map((s, i) => (
                    <span key={s.k} className={`seat ${s.label ? 'human' : 'bot'}`} style={{ '--i': i } as CSSProperties}>
                      {s.label ? (
                        <span className="seat-h">{s.label}</span>
                      ) : (
                        <Img srcs={[`/play/agents/${s.k}.webp`]} alt="" fallback={<span className="seat-h">{s.k[0].toUpperCase()}</span>} />
                      )}
                    </span>
                  ))}
                  <span className="ring-felt" />
                </div>
              </div>
            </div>
          </div>
        </section>

        {/* ───────────── 4 · TEXAS HOLD'EM ───────────── */}
        <section id="poker" className="sec poker" aria-labelledby="poker-h">
          <div className="wrap">
            <div className="poker-grid">
              <div className="sec-head">
                <p className="eyebrow rv">
                  <span>04</span> {t.poker.eyebrow}
                </p>
                <h2 id="poker-h" className="h2 rv">
                  {t.poker.title[0]}
                  <br />
                  <em>{t.poker.title[1]}</em>
                </h2>
                <p className="lead rv">{t.poker.lead}</p>
              </div>
              <div className="poker-stage rv">
                <PokerTable v={t.poker.vignette} />
              </div>
            </div>
            <ul className="promises">
              {t.poker.promises.map(([k, d], i) => {
                const Icon = PROMISE_ICONS[i]
                return (
                  <li key={k} className="rv" style={{ '--i': i } as CSSProperties}>
                    <span className="promise-ic">
                      <Icon />
                    </span>
                    <h3>{k}</h3>
                    <p>{d}</p>
                  </li>
                )
              })}
            </ul>
          </div>
        </section>

        {/* ───────────── 5 · MAKE YOUR OWN GAME ───────────── */}
        <section id="build" className="sec build" aria-labelledby="build-h">
          <div className="wrap">
            <div className="sec-head center">
              <p className="eyebrow rv">
                <span>05</span> {t.build.eyebrow}
              </p>
              <h2 id="build-h" className="h2 rv">
                {t.build.title[0]}
                <br />
                <em>{t.build.title[1]}</em>
              </h2>
              <p className="lead rv">{t.build.lead}</p>
            </div>

            <ol className="steps">
              {t.build.steps.map(([k, d], i) => {
                const Icon = STEP_ICONS[i]
                return (
                  <li key={k} className="rv" style={{ '--i': i } as CSSProperties}>
                    <span className="step-no">0{i + 1}</span>
                    <span className="step-ic">
                      <Icon />
                    </span>
                    <h3>{k}</h3>
                    <p>{d}</p>
                  </li>
                )
              })}
            </ol>

            <div className="studio rv">
              <div className="chat" role="group" aria-label={t.build.chat.title}>
                <div className="chat-head">
                  <span className="chat-ava">
                    <Img srcs={['/play/agents/aoi.webp']} alt="" fallback={<span className="bubble-ava-fb">葵</span>} />
                  </span>
                  <div>
                    <b>{t.build.chat.title}</b>
                    <span className="chat-status">{t.build.chat.status}</span>
                  </div>
                </div>
                <ul className="chat-log">
                  {t.build.chat.msgs.map(([who, text], i) => (
                    <li key={i} className={`msg ${who}`} style={{ '--i': i } as CSSProperties}>
                      <span className="sr-only">{who === 'aoi' ? t.hero.avatarAlt : t.build.chat.you}: </span>
                      {text}
                    </li>
                  ))}
                </ul>
                <div className="chat-input" aria-hidden="true">
                  <span>{t.build.chat.input}</span>
                  <span className="chat-send">
                    <ISend width={16} height={16} />
                  </span>
                </div>
              </div>
              <div className="progress">
                <div className="progress-board" aria-hidden="true">
                  {Array.from({ length: 25 }, (_, i) => (
                    <i key={i} className={[3, 7, 11, 17, 21].includes(i) ? 'egg' : i === 12 ? 'gold' : ''} />
                  ))}
                  <span className="fox f1" />
                  <span className="fox f2" />
                  <span className="fox f3" />
                </div>
                <h3>{t.build.chat.progressTitle}</h3>
                <ul>
                  {t.build.chat.progress.map((p, i) => (
                    <li key={p} style={{ '--i': i } as CSSProperties}>
                      <span className="tick">
                        <ICheck width={14} height={14} />
                      </span>
                      {p}
                    </li>
                  ))}
                </ul>
              </div>
            </div>
          </div>
        </section>

        {/* ───────────── CTA ───────────── */}
        <section className="cta" aria-labelledby="cta-h">
          <div className="cta-panel rv">
            <h2 id="cta-h" className="cta-title">
              <span>{t.cta.title[0]}</span>
              <span>{t.cta.title[1]}</span>
            </h2>
            <div className="cta-row">
              <p>{t.cta.body}</p>
              <div className="cta-actions">
                {user ? (
                  <Link to="/app" className="pill pill-ink pill-lg">
                    {t.cta.app} <IArrow width={18} height={18} />
                  </Link>
                ) : (
                  <>
                    <Link to="/signup" className="pill pill-ink pill-lg">
                      {t.cta.button} <IArrow width={18} height={18} />
                    </Link>
                    <Link to="/signin" className="pill pill-line pill-lg">
                      {t.cta.signin}
                    </Link>
                  </>
                )}
              </div>
            </div>
          </div>
        </section>
      </main>

      <footer className="lp-foot">
        <div className="wrap lp-foot-in">
          <a href="#top" className="lp-logo light" aria-label={t.nav.home}>
            <Logo className="lp-logo-mark" />
            <span className="lp-logo-word">
              PLAY WITH
              <br />
              AGENTS
            </span>
          </a>
          <p className="lp-foot-note">{t.footer.note}</p>
          <nav className="lp-foot-links" aria-label={lang === 'zh' ? '页脚' : 'Footer'}>
            <Link to="/terms">{t.footer.terms}</Link>
            <Link to="/privacy">{t.footer.privacy}</Link>
            <a href="https://heros-agent.space" rel="noopener">
              {t.footer.family}
            </a>
          </nav>
          <p className="lp-foot-copy mono">{t.footer.copy}</p>
        </div>
      </footer>

      {/* floating dock after the hero */}
      <div className={`dock ${dock ? 'is-on' : ''}`} aria-hidden={!dock}>
        <a href="#top" className="lp-logo light" aria-label={t.nav.home} tabIndex={dock ? 0 : -1}>
          <Logo className="lp-logo-mark" />
        </a>
        <ul className="dock-links">
          {t.nav.links.map(([id, label]) => (
            <li key={id}>
              <a href={`#${id}`} tabIndex={dock ? 0 : -1}>
                {label}
              </a>
            </li>
          ))}
        </ul>
        <Link to={startHref} className="pill pill-light" tabIndex={dock ? 0 : -1}>
          {user ? t.nav.app : t.nav.start}
        </Link>
      </div>

      {/* full-screen menu */}
      <div id="lp-menu" className={`lp-menu ${menu ? 'is-open' : ''}`} role="dialog" aria-modal="true" aria-label={t.nav.menuTitle}>
        <div className="lp-menu-top">
          <span className="lp-logo light">
            <Logo className="lp-logo-mark" />
          </span>
          <button
            type="button"
            className="round-btn light"
            aria-label={t.nav.close}
            onClick={() => {
              setMenu(false)
              menuBtn.current?.focus()
            }}
          >
            <IClose width={22} height={22} />
          </button>
        </div>
        <ul className="lp-menu-links">
          {t.nav.links.map(([id, label], i) => (
            <li key={id} style={{ '--i': i } as CSSProperties}>
              <a ref={i === 0 ? menuFirst : undefined} href={`#${id}`} onClick={() => setMenu(false)}>
                <span className="mono">0{i + 2}</span>
                {label}
              </a>
            </li>
          ))}
        </ul>
        <div className="lp-menu-foot">
          <span className="lp-menu-prefs">
            <LangSwitch />
            {themeBtn}
          </span>
          <span className="lp-menu-auth">{authActions()}</span>
        </div>
      </div>
    </div>
  )
}
