import { useEffect, useRef, useState } from 'react'
import { Link } from 'react-router-dom'
import ErosionSphere from '../components/ErosionSphere'
import LangSwitch from '../components/LangSwitch'
import {
  IconArrow, IconArrowL, IconArrowUR, IconBell, IconBriefcase, IconCalendar, IconCar, IconDoc, IconFlask, IconFolder,
  IconHeart, IconLock, IconPill, IconScale, IconShield, IconSpark, IconUser, LogoMark,
} from '../components/Icons'
import { useI18n } from '../lib/i18n'
import { useSession } from '../lib/session'
import '../styles/landing.css'

// Five sections: hero, meet Vera, what she does, how it works, start + FAQ.
export default function Landing() {
  const { t, lang } = useI18n()
  const { user } = useSession()
  const [scene, setScene] = useState(0)
  const [tab, setTab] = useState(0)
  const [navSolid, setNavSolid] = useState(false)

  useEffect(() => {
    document.title = lang === 'zh' ? 'ACT · 维拉 —— 为你辩护，也为你守护' : 'ACT · Vera — counsel & care'
  }, [lang])

  // The hero cards cycle through the three kinds of help, like a carousel.
  useEffect(() => {
    const id = window.setInterval(() => setScene((s) => (s + 1) % 3), 7000)
    return () => window.clearInterval(id)
  }, [scene])

  useEffect(() => {
    const on = () => setNavSolid(window.scrollY > 40)
    on()
    window.addEventListener('scroll', on, { passive: true })
    return () => window.removeEventListener('scroll', on)
  }, [])

  useReveal()

  const card = t.hero.cards[scene]
  const startHref = user ? '/app' : '/signup'
  const tabIcons = [
    [IconScale, IconBriefcase, IconDoc, IconCalendar],
    [IconHeart, IconFlask, IconPill, IconBell],
    [IconCar, IconShield, IconBriefcase, IconFolder],
  ]

  return (
    <div className={`landing lang-${lang}`}>
      <header className={`l-nav ${navSolid ? 'solid' : ''}`}>
        <a href="#top" className="l-logo" aria-label="ACT">
          <LogoMark />
          <span>ACT</span>
        </a>
        <nav className="l-links" aria-label="Sections">
          <a href="#vera">{t.nav.vera}</a>
          <a href="#what">{t.nav.what}</a>
          <a href="#how">{t.nav.how}</a>
          <a href="#faq">{t.nav.faq}</a>
        </nav>
        <div className="l-actions">
          <LangSwitch />
          {!user && <Link to="/signin" className="l-signin">{t.nav.signin}</Link>}
          <Link to={startHref} className="btn btn-ink btn-sm">{user ? t.nav.app : t.nav.start}</Link>
        </div>
      </header>

      {/* ── 1 · Hero ─────────────────────────────────────────────── */}
      <section className="hero" id="top">
        <div className="hero-stage">
          <div className="hero-sphere">
            <ErosionSphere variant="light" />
          </div>

          <h1 className="hero-words">
            <span className="w-left">{t.hero.left}</span>
            <span className="w-right">
              <span>{t.hero.right1}</span>
              <span className="w-indent">{t.hero.right2}</span>
            </span>
            <span className="sr-only">{t.hero.tagline}</span>
          </h1>

          <a href="#vera" className="glass meet-card">
            <img src="/vera/vera-avatar.webp" alt="" width={84} height={84} />
            <span>
              <strong>{t.hero.meetTitle}</strong>
              <small>{t.hero.meetSub}</small>
            </span>
          </a>

          <div className="glass rows-card" key={`rows-${scene}`}>
            {card.rows.map((r, i) => (
              <div className="row" key={i}>
                <span className="row-name">{r[0]}</span>
                <span className={`tag ${i === 0 ? 'tag-hot' : 'tag-warm'}`}>{r[1]}</span>
                <span className="row-state">{r[2]}</span>
                <span className="row-time">{r[3]}</span>
              </div>
            ))}
          </div>

          <div className="glass panel-card" key={`panel-${scene}`}>
            <div className="panel-head">
              <div>
                <strong>{card.panel}</strong>
                <small>{card.panelSub}</small>
              </div>
              <span className="panel-go" aria-hidden="true"><IconArrowUR size={16} /></span>
            </div>
            <div className="panel-items">
              {card.items.map((it, i) => (
                <div className="mini" key={i}>
                  <small>{it[0]}</small>
                  <strong>{it[1]}</strong>
                  <span className="mini-foot">
                    <span>{it[2]}</span>
                    <i className={`dot dot-${it[3]}`} />
                  </span>
                </div>
              ))}
            </div>
          </div>

          <div className="hero-focus">
            <h2>{t.hero.focusTitle}</h2>
            <p>{t.hero.sub}</p>
            <div className="hero-cta">
              <Link to={startHref} className="btn btn-ember">{t.cta.button} <IconArrow size={18} /></Link>
            </div>
          </div>

          <div className="hero-carousel" role="group" aria-label="Scenes">
            <button className="round" onClick={() => setScene((scene + 2) % 3)} aria-label="Previous"><IconArrowL size={18} /></button>
            <div className="dots">
              {t.hero.scenes.map((s, i) => (
                <button key={s} className={i === scene ? 'on' : ''} onClick={() => setScene(i)} aria-pressed={i === scene}>
                  <span>{s}</span>
                </button>
              ))}
            </div>
            <button className="round" onClick={() => setScene((scene + 1) % 3)} aria-label="Next"><IconArrow size={18} /></button>
          </div>

          <p className="hero-caption">{t.hero.caption}</p>
        </div>
      </section>

      {/* ── 2 · Meet Vera ───────────────────────────────────────── */}
      <section className="meet" id="vera">
        <div className="meet-figure reveal">
          <div className="arch" />
          <div className="orbit orbit-1" />
          <div className="orbit orbit-2" />
          <img className="vera-full" src="/vera/vera-full.webp" alt={lang === 'zh' ? '维拉的全身像：白大褂、藏青马甲与百褶裙、青色领结、听诊器与天平徽章' : 'Vera, full length: white coat over a navy vest and pleated skirt, teal bow, stethoscope and a scales-of-justice pin'} width={365} height={981} loading="lazy" />
          <div className="name-tag glass">
            <strong>Vera · 维拉</strong>
            <small>{lang === 'zh' ? '法律与健康 · AI 伙伴' : 'Counsel & care companion · AI'}</small>
          </div>
          <div className="quote-bubble glass">{t.meet.quote}</div>
        </div>

        <div className="meet-copy">
          <p className="eyebrow reveal">{t.meet.eyebrow}</p>
          <h2 className="display reveal">
            {lang === 'zh' ? <>她的名字，意为<em>“真实”</em>。</> : <>Her name means <em>truth.</em></>}
          </h2>
          <p className="lead reveal">{t.meet.body}</p>

          <ol className="values reveal">
            {t.meet.values.map(([h, d], i) => (
              <li key={h}>
                <span className="v-num">{String(i + 1).padStart(2, '0')}</span>
                <div>
                  <strong>{h}</strong>
                  <p>{d}</p>
                </div>
              </li>
            ))}
          </ol>

          <div className="moods reveal">
            {(['smile', 'think', 'laugh'] as const).map((m, i) => (
              <figure key={m}>
                <img src={`/vera/vera-face-${m}.webp`} alt="" width={96} height={96} loading="lazy" />
                <figcaption>{t.meet.moods[i]}</figcaption>
              </figure>
            ))}
          </div>

          <div className="chips reveal">
            {t.meet.chips.map((c) => <span key={c} className="chip">{c}</span>)}
          </div>
        </div>
      </section>

      {/* ── 3 · What she does ───────────────────────────────────── */}
      <section className="what" id="what">
        <div className="what-head">
          <div>
            <p className="eyebrow reveal">{t.what.eyebrow}</p>
            <h2 className="display reveal">{t.what.title}</h2>
          </div>
          <div className="tabs reveal" role="tablist">
            {t.what.tabs.map((name, i) => (
              <button key={name} role="tab" aria-selected={tab === i} className={tab === i ? 'on' : ''} onClick={() => setTab(i)}>
                {name}
              </button>
            ))}
          </div>
        </div>
        <div className="what-grid" key={tab}>
          {t.what.items[tab].map(([h, d], i) => {
            const Icon = tabIcons[tab][i]
            return (
              <article className="what-card" key={h} style={{ animationDelay: `${i * 70}ms` }}>
                <span className={`what-icon tone-${tab}`}><Icon size={22} /></span>
                <h3>{h}</h3>
                <p>{d}</p>
              </article>
            )
          })}
          <aside className="what-vera">
            <img src="/vera/vera-props.webp" alt="" loading="lazy" />
          </aside>
        </div>
      </section>

      {/* ── 4 · How it works ────────────────────────────────────── */}
      <section className="how" id="how">
        <div className="how-panel">
          <div className="how-copy">
            <p className="eyebrow light reveal">{t.how.eyebrow}</p>
            <h2 className="display light reveal">{t.how.title}</h2>
            <ol className="steps">
              {t.how.steps.map(([h, d], i) => (
                <li key={h} className="reveal">
                  <span className="s-num">{i + 1}</span>
                  <div>
                    <strong>{h}</strong>
                    <p>{d}</p>
                  </div>
                </li>
              ))}
            </ol>
          </div>
          <div className="how-visual">
            <ErosionSphere variant="dark" className="how-sphere" />
            <div className="timeline-card reveal">
              <div className="tl-head">
                <img src="/vera/vera-face-smile.webp" alt="" width={36} height={36} />
                <strong>{t.how.timelineTitle}</strong>
              </div>
              <ul>
                {t.how.timeline.map(([when, what], i) => (
                  <li key={when} className={i === t.how.timeline.length - 1 ? 'next' : ''}>
                    <span className="tl-dot" />
                    <small>{when}</small>
                    <span>{what}</span>
                  </li>
                ))}
              </ul>
            </div>
          </div>
        </div>
        <div className="trust">
          {t.how.trust.map(([h, d], i) => {
            const Icon = [IconUser, IconShield, IconLock, IconSpark][i]
            return (
              <div className="trust-card reveal" key={h}>
                <Icon size={22} />
                <strong>{h}</strong>
                <p>{d}</p>
              </div>
            )
          })}
        </div>
      </section>

      {/* ── 5 · Start + FAQ ─────────────────────────────────────── */}
      <section className="start" id="faq">
        <div className="cta-card reveal">
          <div className="cta-copy">
            <h2 className="display">{t.cta.title}</h2>
            <p>{t.cta.sub}</p>
            <div className="cta-actions">
              <Link to={startHref} className="btn btn-ember btn-lg">{t.cta.button} <IconArrow size={18} /></Link>
              {!user && <Link to="/signin" className="btn btn-ghost btn-lg">{t.cta.secondary}</Link>}
            </div>
          </div>
          <img className="cta-portrait" src="/vera/vera-portrait.webp" alt="" loading="lazy" />
        </div>

        <div className="faq">
          <h2 className="display small reveal">{t.faq.title}</h2>
          <div className="faq-list">
            {t.faq.items.map(([q, a]) => (
              <details key={q} className="reveal">
                <summary>{q}<span aria-hidden="true" /></summary>
                <p>{a}</p>
              </details>
            ))}
          </div>
        </div>

        <footer className="l-footer">
          <div className="f-brand">
            <LogoMark size={24} />
            <span>ACT</span>
            <small>{t.brand.expand}</small>
          </div>
          <p className="f-disclaimer">{t.footer.disclaimer}</p>
          <nav className="f-links" aria-label="Legal">
            <Link to="/privacy">{t.footer.links[0]}</Link>
            <Link to="/terms">{t.footer.links[1]}</Link>
            <a href="mailto:support@heros-agent.space">{t.footer.links[2]}</a>
          </nav>
          <div className="f-bottom">
            <span>{t.footer.rights}</span>
            <LangSwitch />
          </div>
        </footer>
      </section>
    </div>
  )
}

// Fade sections in as they scroll into view.
function useReveal() {
  const done = useRef(false)
  useEffect(() => {
    if (done.current) return
    done.current = true
    const els = document.querySelectorAll('.reveal')
    const io = new IntersectionObserver(
      (entries) => entries.forEach((e) => e.isIntersecting && (e.target.classList.add('in'), io.unobserve(e.target))),
      { rootMargin: '0px 0px -8% 0px' },
    )
    els.forEach((el) => io.observe(el))
    return () => io.disconnect()
  }, [])
}
