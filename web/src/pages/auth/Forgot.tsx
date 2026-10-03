import { useState } from 'react'
import { Link } from 'react-router-dom'
import { api } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import AuthLayout, { useAuthError, useSubmit } from './AuthLayout'

export default function Forgot() {
  const { t, lang } = useI18n()
  const nice = useAuthError()
  const [email, setEmail] = useState('')
  const [sent, setSent] = useState(false)
  const { busy, error, submit } = useSubmit(async () => {
    await api.post('/api/auth/forgot', { email: email.trim(), language: lang })
    setSent(true)
  })
  return (
    <AuthLayout page="forgot" title={t.auth.forgotTitle} sub={sent ? undefined : t.auth.forgotSub} mood={sent ? 'smile' : 'surprised'}>
      {sent ? (
        <>
          <div className="mail-art" aria-hidden="true"><span /></div>
          <div className="alert alert-ok" role="status">{t.auth.forgotSent}</div>
          <Link to="/signin" className="btn btn-primary btn-lg auth-submit">{t.auth.backToSignin}</Link>
        </>
      ) : (
        <form className="auth-form" onSubmit={submit}>
          {error && <div className="alert alert-error" role="alert">{nice(error)}</div>}
          <label className="field">
            <span>{t.auth.email}</span>
            <input className="input" type="email" value={email} onChange={(e) => setEmail(e.target.value)} autoComplete="email" required autoFocus inputMode="email" />
          </label>
          <button className="btn btn-primary btn-lg auth-submit" disabled={busy}>{busy ? <span className="spin" /> : t.auth.sendLink}</button>
          <p className="auth-switch"><Link to="/signin">{t.auth.backToSignin}</Link></p>
        </form>
      )}
    </AuthLayout>
  )
}
