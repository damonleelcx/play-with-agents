import { useEffect, useRef, useState } from 'react'
import { Link, useNavigate, useSearchParams } from 'react-router-dom'
import { api, type User } from '../../lib/api'
import { useI18n } from '../../lib/i18n'
import { useSession } from '../../lib/session'
import AuthLayout from './AuthLayout'

// Two states: arriving from the emailed link (?token=…), or waiting for it.
export default function Verify() {
  const { t, lang } = useI18n()
  const { user, setUser, loading, signOut } = useSession()
  const nav = useNavigate()
  const [q] = useSearchParams()
  const token = q.get('token')
  const [state, setState] = useState<'idle' | 'verifying' | 'done' | 'error'>(token ? 'verifying' : 'idle')
  const [error, setError] = useState('')
  const [cooldown, setCooldown] = useState(0)
  const [notice, setNotice] = useState('')
  const ran = useRef(false)

  useEffect(() => {
    if (!token || ran.current) return
    ran.current = true // StrictMode runs effects twice; a token is single-use
    api
      .post<User>('/api/auth/verify', { token })
      .then((u) => {
        setUser(u)
        setState('done')
      })
      .catch((e) => {
        setError(e.message)
        setState('error')
      })
  }, [token, setUser])

  useEffect(() => {
    if (!token && !loading && user?.email_verified) nav('/app', { replace: true })
  }, [token, loading, user, nav])

  useEffect(() => {
    if (cooldown <= 0) return
    const id = setTimeout(() => setCooldown(cooldown - 1), 1000)
    return () => clearTimeout(id)
  }, [cooldown])

  const resend = async () => {
    setNotice('')
    try {
      await api.post('/api/auth/resend')
      setNotice(t.auth.resent)
      setCooldown(60)
    } catch (e: any) {
      setNotice(e.message)
    }
  }

  if (state === 'verifying') return <AuthLayout title={t.auth.verifying} mood="think"><div className="auth-spinner" /></AuthLayout>
  if (state === 'done')
    return (
      <AuthLayout title={t.auth.verified} sub={t.auth.verifiedSub} mood="laugh">
        <Link to="/app" className="btn btn-ember btn-lg auth-submit">{t.auth.continue}</Link>
      </AuthLayout>
    )
  if (state === 'error')
    return (
      <AuthLayout title={t.common.error} sub={lang === 'zh' ? '此链接无效或已过期。' : error} mood="think">
        {user ? (
          <button className="btn btn-ink btn-lg auth-submit" onClick={resend} disabled={cooldown > 0}>{t.auth.resend}</button>
        ) : (
          <Link to="/signin" className="btn btn-ink btn-lg auth-submit">{t.auth.backToSignin}</Link>
        )}
        {notice && <div className="alert alert-ok" style={{ marginTop: 14 }}>{notice}</div>}
      </AuthLayout>
    )

  if (!loading && !user)
    return (
      <AuthLayout title={t.auth.verifyTitle} sub={t.auth.verifyHelp}>
        <Link to="/signin" className="btn btn-ink btn-lg auth-submit">{t.auth.signin}</Link>
      </AuthLayout>
    )

  return (
    <AuthLayout
      title={t.auth.verifyTitle}
      sub={<>{t.auth.verifySub} <strong>{user?.email}</strong>. {t.auth.verifyHelp}</>}
    >
      <div className="mail-art" aria-hidden="true"><span /></div>
      {user && !user.mail_enabled && <div className="alert alert-info">{t.auth.mailOff}</div>}
      {notice && <div className="alert alert-ok" role="status">{notice}</div>}
      <button className="btn btn-ink btn-lg auth-submit" onClick={resend} disabled={cooldown > 0}>
        {cooldown > 0 ? `${t.auth.resend} (${cooldown}s)` : t.auth.resend}
      </button>
      <p className="auth-switch">
        <button className="linklike" onClick={async () => { await signOut(); nav('/signin') }}>{t.auth.signout}</button>
      </p>
    </AuthLayout>
  )
}
