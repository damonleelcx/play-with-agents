// Dev-only scratch entry (web/play-preview.html): renders the play surfaces
// without the app shell so they can be checked in mock mode.
//   /play-preview.html?mock=holdem         (holdem-showdown | board | cards | checkers | lobby | finished)
//   /play-preview.html?v=lobby&mock=1      the games lobby with mock data
//   /play-preview.html?v=card              TableCard
import { StrictMode } from 'react'
import { createRoot } from 'react-dom/client'
import { BrowserRouter, Route, Routes, useSearchParams } from 'react-router-dom'
import { I18nProvider } from '../../lib/i18n'
import '../../styles/tokens.css'
import '../../styles/base.css'
import { GameDetail, JoinByCode, Lobby, TableCard, TableRoom } from './index'

function Preview() {
  const [sp] = useSearchParams()
  const v = sp.get('v')
  if (v === 'lobby') return <Lobby />
  if (v === 'detail') return <GameDetail />
  if (v === 'card')
    return (
      <div style={{ padding: 24, display: 'flex', gap: 16, flexWrap: 'wrap' }}>
        <div style={{ width: 340 }}><TableCard tableId="demo" mock="holdem" /></div>
        <div style={{ width: 340 }}><TableCard tableId="demo" mock="lobby" /></div>
        <div style={{ width: 340 }}><TableCard tableId="demo" mock="board" /></div>
      </div>
    )
  return <TableRoom tableId="demo" mock={sp.get('mock') || 'holdem'} />
}

createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <I18nProvider>
      <BrowserRouter>
        <div style={{ height: '100dvh', display: 'flex', flexDirection: 'column' }}>
          <Routes>
            <Route path="/play-preview.html" element={<Preview />} />
            <Route path="/app/games" element={<Lobby />} />
            <Route path="/app/games/:id" element={<GameDetail />} />
            <Route path="/app/table/:id" element={<TableRoom />} />
            <Route path="/app/join/:code" element={<JoinByCode />} />
          </Routes>
        </div>
      </BrowserRouter>
    </I18nProvider>
  </StrictMode>,
)
