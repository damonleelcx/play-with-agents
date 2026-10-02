import { api } from '../lib/api'
import { useI18n, type Lang } from '../lib/i18n'
import { useSession } from '../lib/session'

// EN / 中文 toggle. For a signed-in user the choice is also saved to their
// preferences, so Vera's emails and replies follow it.
export default function LangSwitch() {
  const { lang, setLang } = useI18n()
  const { user } = useSession()
  const pick = (l: Lang) => {
    setLang(l)
    if (user) api.put('/api/settings', { preferences: { language: l } }).catch(() => {})
  }
  return (
    <div className="lang-switch" role="group" aria-label="Language / 语言">
      <button aria-pressed={lang === 'en'} onClick={() => pick('en')}>EN</button>
      <button aria-pressed={lang === 'zh'} onClick={() => pick('zh')}>中文</button>
    </div>
  )
}
