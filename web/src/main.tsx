import { StrictMode, Suspense, lazy } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Navigate, Route, Routes, useLocation } from 'react-router-dom'
import { I18nProvider } from './lib/i18n'
import { SessionProvider, useSession } from './lib/session'
import './styles/tokens.css'
import './styles/base.css'

// The landing page loads eagerly (it is the first paint for most visitors);
// the app and auth pages are split so the landing does not carry them.
import Landing from './pages/Landing'
const SignIn = lazy(() => import('./pages/auth/SignIn'))
const SignUp = lazy(() => import('./pages/auth/SignUp'))
const Verify = lazy(() => import('./pages/auth/Verify'))
const Forgot = lazy(() => import('./pages/auth/Forgot'))
const Reset = lazy(() => import('./pages/auth/Reset'))
const AppShell = lazy(() => import('./app/AppShell'))
const Legal = lazy(() => import('./pages/legal/Legal'))

function RequireAuth({ children }: { children: JSX.Element }) {
  const { user, loading } = useSession()
  const loc = useLocation()
  if (loading) return <div className="boot"><span className="boot-dot" /></div>
  if (!user) return <Navigate to={`/signin?next=${encodeURIComponent(loc.pathname + loc.search)}`} replace />
  if (!user.email_verified) return <Navigate to="/verify-email" replace />
  return children
}

function App() {
  return (
    <Suspense fallback={<div className="boot"><span className="boot-dot" /></div>}>
      <Routes>
        <Route path="/" element={<Landing />} />
        <Route path="/signin" element={<SignIn />} />
        <Route path="/signup" element={<SignUp />} />
        <Route path="/verify-email" element={<Verify />} />
        <Route path="/forgot-password" element={<Forgot />} />
        <Route path="/reset-password" element={<Reset />} />
        <Route path="/terms" element={<Legal kind="terms" />} />
        <Route path="/privacy" element={<Legal kind="privacy" />} />
        <Route path="/app/*" element={<RequireAuth><AppShell /></RequireAuth>} />
        <Route path="*" element={<Navigate to="/" replace />} />
      </Routes>
    </Suspense>
  )
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <I18nProvider>
      <SessionProvider>
        <BrowserRouter>
          <App />
        </BrowserRouter>
      </SessionProvider>
    </I18nProvider>
  </StrictMode>,
)
