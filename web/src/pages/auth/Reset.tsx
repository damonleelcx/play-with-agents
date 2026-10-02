import { useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, type User } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { useSession } from '../../lib/session'
import AuthLayout, { PasswordInput, useAuthError, useSubmit } from './AuthLayout'

export default function Reset() {
  const { t } = useI18n()
  const { setUser } = useSession()
  const nav = useNavigate()
  const nice = useAuthError()
  const [q] = useSearchParams()
  const token = q.get('token') || ''
  const [pw, setPw] = useState('')
  const [pw2, setPw2] = useState('')
  const { busy, error, submit, setError } = useSubmit(async () => {
    if (pw !== pw2) {
      setError(t.auth.mismatch)
      return
    }
    const u = await api.post<User>('/api/auth/reset', { token, password: pw })
    setUser(u)
    nav('/app', { replace: true })
  })
  if (!token)
    return (
      <AuthLayout page="reset" title={t.common.error} sub={t.auth.linkInvalid} mood="sad">
        <Link to="/forgot-password" className="btn btn-primary btn-lg auth-submit">{t.auth.sendLink}</Link>
      </AuthLayout>
    )
  const expired = /expired|token/i.test(error)
  return (
    <AuthLayout page="reset" title={t.auth.resetTitle} sub={t.auth.resetSub} mood="smile">
      <form className="auth-form" onSubmit={submit}>
        {error && (
          <div className="alert alert-error" role="alert">
            {nice(error)}
            {expired && <> <Link to="/forgot-password">{t.auth.sendLink}</Link></>}
          </div>
        )}
        <PasswordInput label={t.auth.newPassword} value={pw} onChange={setPw} autoComplete="new-password" showMeter autoFocus />
        <PasswordInput label={t.auth.confirm} value={pw2} onChange={setPw2} autoComplete="new-password" />
        {pw2 && pw !== pw2 && <small className="field-err">{t.auth.mismatch}</small>}
        <button className="btn btn-primary btn-lg auth-submit" disabled={busy || [...pw].length < 10 || pw !== pw2}>{busy ? <span className="spin" /> : t.auth.reset}</button>
      </form>
    </AuthLayout>
  )
}
