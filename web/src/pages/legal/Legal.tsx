import { useEffect } from 'react'
import { Link } from 'react-router-dom'
import { IconArrowL, LogoMark } from '../../components/Icons'
import LangSwitch from '../../components/LangSwitch'
import { useI18n } from '../../lib/i18n'
import { privacy, terms } from './content'
import '../../styles/legal.css'

export default function Legal({ kind }: { kind: 'terms' | 'privacy' }) {
  const { lang, t } = useI18n()
  const doc = (kind === 'terms' ? terms : privacy)[lang]
  const other = kind === 'terms' ? { to: '/privacy', label: privacy[lang].title } : { to: '/terms', label: terms[lang].title }
  useEffect(() => {
    document.title = `${doc.title} · Play with Agents`
    window.scrollTo(0, 0)
  }, [doc.title])
  return (
    <div className="legal">
      <header className="legal-top">
        <Link to="/" className="legal-logo"><LogoMark size={28} /><span>Play <i>with</i> Agents</span></Link>
        <LangSwitch />
      </header>
      <main className="legal-body">
        <Link to="/" className="legal-back"><IconArrowL size={16} /> {t.legal.home}</Link>
        <p className="legal-kicker">{kind === 'terms' ? '01' : '02'} · Play with Agents</p>
        <h1>{doc.title}</h1>
        <p className="legal-updated">{doc.updated}</p>
        <p className="legal-intro">{doc.intro}</p>
        <div className="legal-note">♠ {t.legal.note}</div>
        <nav className="legal-toc" aria-label={t.legal.contents}>
          <small>{t.legal.contents}</small>
          {doc.sections.map((s, i) => <a key={s.h} href={`#s${i}`}>{s.h}</a>)}
        </nav>
        {doc.sections.map((s, i) => (
          <section key={s.h} id={`s${i}`}>
            <h2>{s.h}</h2>
            {s.p.map((para, j) => <p key={j}>{para}</p>)}
          </section>
        ))}
        <footer className="legal-foot">
          <Link to={other.to}>{other.label} →</Link>
          <p>© 2026 Play with Agents · support@heros-agent.space</p>
        </footer>
      </main>
    </div>
  )
}
