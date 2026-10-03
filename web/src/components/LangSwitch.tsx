import { api } from '../lib/api'
import { LANG_NAMES, LANGS, LOCALES, useI18n, type Lang } from '../lib/i18n'
import { useSession } from '../lib/session'

// EN · 中 · 한 · 日: a compact segmented language picker. Each button carries
// its language's full native name for screen readers and as a tooltip, and
// is marked up in that language so it is read with the right voice. For a
// signed-in user the choice is also saved to their preferences, so Aoi's
// emails and replies follow it.
export default function LangSwitch() {
  const { lang, setLang, t } = useI18n()
  const { user } = useSession()
  const pick = (l: Lang) => {
    if (l === lang) return
    setLang(l)
    if (user) api.put('/api/settings', { preferences: { language: l } }).catch(() => {})
  }
  return (
    <div className="lang-switch" role="group" aria-label={t.common.language === 'Language' ? 'Language' : `${t.common.language} · Language`}>
      {LANGS.map((l) => (
        <button key={l} type="button" lang={LOCALES[l]} aria-pressed={lang === l} aria-label={LANG_NAMES[l].native}
          title={LANG_NAMES[l].native} onClick={() => pick(l)}>
          {LANG_NAMES[l].short}
        </button>
      ))}
    </div>
  )
}
