// Public surface of the play module. The app shell mounts:
//   /app/games          → <Lobby />
//   /app/games/:id      → <GameDetail />
//   /app/table/:id      → <TableRoom />   (?mock=holdem|holdem-showdown|board|cards|checkers|lobby|finished)
//   /app/join/:code     → <JoinByCode />
// and renders <TableCard tableId=… /> for {kind:"table"} chat cards.
export { default as Lobby } from './Lobby'
export { default as GameDetail } from './GameDetail'
export { default as TableRoom } from './TableRoom'
export { default as JoinByCode } from './JoinByCode'
export { default as TableCard } from './TableCard'
export { usePlayPrefs, type PlayPrefs } from './usePrefs'
