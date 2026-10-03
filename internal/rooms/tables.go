package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/agents"
	"github.com/damonleelcx/play-with-agents/internal/games"
)

// maxOptionsBytes bounds what a client can make us store and hand the game.
const maxOptionsBytes = 8 << 10

// playable resolves a game the user may sit down to, with its metadata.
// Built-ins are open to everyone; a script game to its owner (drafts
// included) or to anyone once published as public or unlisted.
func (s *Service) playable(ctx context.Context, userID, gameID string) (games.Meta, int, error) {
	if g, ok := games.Builtin(gameID); ok {
		m := g.Meta()
		_, err := s.Pool.Exec(ctx, `INSERT INTO games (id, kind, name, summary, status, visibility)
			VALUES ($1, 'builtin', $2, $3, 'published', 'public') ON CONFLICT (id) DO NOTHING`, m.ID, m.Name, m.Summary)
		return m, 0, err
	}
	var owner, status, vis string
	var version int
	err := s.Pool.QueryRow(ctx, `SELECT coalesce(owner_id::text,''), status, visibility, current_version FROM games WHERE id=$1`, gameID).
		Scan(&owner, &status, &vis, &version)
	if errors.Is(err, pgx.ErrNoRows) {
		return games.Meta{}, 0, ErrNotFound
	}
	if err != nil {
		return games.Meta{}, 0, err
	}
	if owner != userID && (status != "published" || vis == "private") {
		return games.Meta{}, 0, ErrNotFound
	}
	if owner == userID && owner != "" {
		// The owner plays the newest version that passed the studio's
		// playtest (a draft, or a revision of a published game not yet
		// approved); everyone else plays the published current_version.
		if err := s.Pool.QueryRow(ctx, `SELECT greatest($2::int, coalesce(max(version),0)) FROM game_versions
			WHERE game_id=$1 AND coalesce((report->'verdict'->>'pass')::boolean, false)`, gameID, version).Scan(&version); err != nil {
			return games.Meta{}, 0, err
		}
	}
	if version == 0 {
		return games.Meta{}, 0, fmt.Errorf("%w: the game is still being built", ErrConflict)
	}
	g, err := s.Game(ctx, gameID, version)
	if err != nil {
		return games.Meta{}, 0, err
	}
	return g.Meta(), version, nil
}

// agentPicker hands out agents for empty seats: the host's favourites
// first, then the roster in order, then repeats with a number.
type agentPicker struct {
	order []string
	used  map[string]int
	lang  string
}

func newPicker(favorites []string, seats []seatRow, lang string) *agentPicker {
	p := &agentPicker{used: map[string]int{}, lang: lang}
	seen := map[string]bool{}
	for _, id := range append(append([]string{}, favorites...), agents.IDs()...) {
		if _, ok := agents.Get(id); ok && !seen[id] {
			seen[id] = true
			p.order = append(p.order, id)
		}
	}
	for _, st := range seats {
		if st.Kind == "agent" {
			p.used[st.AgentID]++
		}
	}
	return p
}

// pick returns a seat for agent id ("" chooses one).
func (p *agentPicker) pick(id string) (seatRow, error) {
	if id == "" {
		best := p.order[0]
		for _, c := range p.order {
			if p.used[c] < p.used[best] {
				best = c
			}
		}
		id = best
	}
	a, ok := agents.Get(id)
	if !ok {
		return seatRow{}, badInput("unknown agent %q", id)
	}
	p.used[id]++
	name := a.DisplayName(p.lang)
	if n := p.used[id]; n > 1 {
		name = fmt.Sprintf("%s %d", name, n)
	}
	return seatRow{Kind: "agent", AgentID: id, Name: name}, nil
}

type newTable struct {
	name, hostID, gameID string
	gameVersion          int
	options              map[string]any
	settings             Settings
	seats                []seatRow
	rematchOf            string
}

// insertTable writes a lobby table and its seats. Invite codes are random,
// so a collision with a live table is retried under a savepoint.
func insertTable(ctx context.Context, tx pgx.Tx, nt newTable) (string, error) {
	opts, _ := json.Marshal(nt.options)
	sets, _ := json.Marshal(nt.settings)
	var id string
	for attempt := 0; ; attempt++ {
		sp, err := tx.Begin(ctx)
		if err != nil {
			return "", err
		}
		err = sp.QueryRow(ctx, `INSERT INTO tables (code, name, host_id, game_id, game_version, options, settings, seed, rematch_of)
			VALUES ($1, $2, nullif($3,'')::uuid, $4, $5, $6, $7, $8, nullif($9,'')::uuid) RETURNING id`,
			newCode(), nt.name, nt.hostID, nt.gameID, nt.gameVersion, opts, sets, newSeed(), nt.rematchOf).Scan(&id)
		if err == nil {
			if err := sp.Commit(ctx); err != nil {
				return "", err
			}
			break
		}
		_ = sp.Rollback(ctx)
		if !isUniqueViolation(err) || attempt >= 8 {
			return "", err
		}
	}
	for _, st := range nt.seats {
		if _, err := tx.Exec(ctx, `INSERT INTO table_seats (table_id, seat, kind, user_id, agent_id, name)
			VALUES ($1, $2, $3, nullif($4,'')::uuid, nullif($5,''), $6)`, id, st.Seat, st.Kind, st.UserID, st.AgentID, st.Name); err != nil {
			return "", err
		}
	}
	_, err := tx.Exec(ctx, `SELECT pg_notify($1, $2)`, NotifyChannel, id)
	return id, err
}

// Create opens a table in the lobby. The host may sit in it ("me") or only
// host it (an all-agent table they watch).
func (s *Service) Create(ctx context.Context, userID string, req CreateRequest) (*TableView, error) {
	host, err := loadUser(ctx, s.Pool, userID)
	if err != nil {
		return nil, err
	}
	meta, version, err := s.playable(ctx, userID, req.GameID)
	if err != nil {
		return nil, err
	}
	if n := len(req.Seats); n < meta.MinSeats || n > meta.MaxSeats {
		return nil, badInput("%s needs %d to %d seats, got %d", meta.Name, meta.MinSeats, meta.MaxSeats, n)
	}
	settings := settingsFrom(host.Prefs, meta)
	if req.TurnSeconds != nil {
		if !validTurnSeconds(*req.TurnSeconds) {
			return nil, badInput("turn_seconds must be 0 (no clock) or 5..600")
		}
		settings.TurnSeconds = *req.TurnSeconds
	}
	options := map[string]any{}
	for k, v := range meta.Options {
		options[k] = v
	}
	for k, v := range req.Options {
		options[k] = v
	}
	if raw, _ := json.Marshal(options); len(raw) > maxOptionsBytes {
		return nil, badInput("options are too large")
	}

	picker := newPicker(settings.Favorites, nil, settings.Language)
	seats := make([]seatRow, len(req.Seats))
	me := false
	// Explicit agent choices are honoured before defaults are handed out,
	// so a default never steals the name of an agent the host picked.
	for i, sp := range req.Seats {
		if sp.Kind == "agent" && sp.AgentID != "" {
			st, err := picker.pick(sp.AgentID)
			if err != nil {
				return nil, err
			}
			seats[i] = st
		}
	}
	for i, sp := range req.Seats {
		switch sp.Kind {
		case "me":
			if me {
				return nil, badInput("only one seat can be yours")
			}
			me = true
			seats[i] = seatRow{Kind: "human", UserID: userID, Name: host.Name}
		case "agent":
			if sp.AgentID == "" {
				st, _ := picker.pick("")
				seats[i] = st
			}
		case "open":
			seats[i] = seatRow{Kind: "open"}
		default:
			return nil, badInput("seat kind must be me, agent or open")
		}
		seats[i].Seat = i
	}
	name := clip(req.Name, 60)
	if name == "" {
		name = clip(fmt.Sprintf("%s's %s", host.Name, meta.Name), 60)
	}
	var id string
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		var err error
		// The catalog id, not meta.ID: a script module names itself, but the
		// table belongs to the catalog entry it was created from.
		id, err = insertTable(ctx, tx, newTable{name: name, hostID: userID, gameID: req.GameID, gameVersion: version,
			options: options, settings: settings, seats: seats})
		return err
	})
	if err != nil {
		return nil, err
	}
	return s.View(ctx, id, userID)
}

// Join takes the first open seat of a lobby table, or makes the user a
// spectator when it is full or already playing.
func (s *Service) Join(ctx context.Context, userID, code string) (*TableView, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	if len(code) != 6 {
		return nil, ErrNotFound
	}
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT id FROM tables WHERE code=$1 AND status IN ('lobby','playing')`, code).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	u, err := loadUser(ctx, s.Pool, userID)
	if err != nil {
		return nil, err
	}
	// Two people can race for the last open seat; the version guard lets one
	// win and the loser re-reads (and spectates if the table is now full).
	for attempt := 0; attempt < 5; attempt++ {
		t, err := loadTable(ctx, s.Pool, id, false)
		if err != nil {
			return nil, err
		}
		if ok, err := s.canWatch(ctx, t, userID); err != nil || ok {
			if err != nil {
				return nil, err
			}
			return s.assemble(ctx, t, userID)
		}
		open := -1
		if t.Status == "lobby" {
			for _, st := range t.Seats {
				if st.Kind == "open" {
					open = st.Seat
					break
				}
			}
		}
		if open < 0 {
			if _, err := s.Pool.Exec(ctx, `INSERT INTO table_spectators (table_id, user_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, userID); err != nil {
				return nil, err
			}
			_, _ = s.Pool.Exec(ctx, `UPDATE tables SET last_human_at=now() WHERE id=$1`, id)
			return s.View(ctx, id, userID)
		}
		seats := cloneSeats(t.Seats)
		seats[open] = seatRow{Seat: open, Kind: "human", UserID: userID, Name: u.Name}
		_, err = s.commit(ctx, t, change{expected: t.Version, state: t.State, seats: seats, human: true,
			talk: s.talkJoin(t, u.Name)})
		if errors.Is(err, errStale) {
			continue
		}
		if err != nil {
			return nil, err
		}
		return s.View(ctx, id, userID)
	}
	return nil, fmt.Errorf("%w: the table is busy, try again", ErrConflict)
}

func cloneSeats(in []seatRow) []seatRow { return append([]seatRow(nil), in...) }

// SetSeat lets the host put an agent in a seat or open it, in the lobby.
// People's seats are theirs: the host cannot evict someone.
func (s *Service) SetSeat(ctx context.Context, userID, tableID string, seat int, spec SeatSpec) (*TableView, error) {
	t, err := loadTable(ctx, s.Pool, tableID, false)
	if err != nil {
		return nil, err
	}
	if !t.hostRights(userID) {
		return nil, ErrForbidden
	}
	if t.Status != "lobby" {
		return nil, fmt.Errorf("%w: seats can only change in the lobby", ErrConflict)
	}
	cur := t.seat(seat)
	if cur == nil {
		return nil, badInput("no seat %d", seat)
	}
	if cur.Kind == "human" {
		return nil, fmt.Errorf("%w: a person is sitting there", ErrConflict)
	}
	seats := cloneSeats(t.Seats)
	switch spec.Kind {
	case "open":
		seats[seat] = seatRow{Seat: seat, Kind: "open"}
	case "agent":
		others := cloneSeats(t.Seats)
		others[seat] = seatRow{Seat: seat, Kind: "open"}
		st, err := newPicker(t.Settings.Favorites, others, t.Settings.Language).pick(spec.AgentID)
		if err != nil {
			return nil, err
		}
		st.Seat = seat
		seats[seat] = st
	default:
		return nil, badInput("kind must be agent or open")
	}
	if _, err := s.commit(ctx, t, change{expected: t.Version, state: t.State, seats: seats, human: true}); err != nil {
		if errors.Is(err, errStale) {
			return nil, fmt.Errorf("%w: the table changed, try again", ErrConflict)
		}
		return nil, err
	}
	return s.View(ctx, tableID, userID)
}

// Start deals the game. Open seats get agents when the host asked for
// that (fill_empty_seats); otherwise they are removed, as long as enough
// players remain.
func (s *Service) Start(ctx context.Context, userID, tableID string) (*TableView, error) {
	t, err := loadTable(ctx, s.Pool, tableID, false)
	if err != nil {
		return nil, err
	}
	if !t.hostRights(userID) {
		return nil, ErrForbidden
	}
	if err := s.start(ctx, t); err != nil {
		return nil, err
	}
	return s.View(ctx, tableID, userID)
}

func (s *Service) start(ctx context.Context, t *tableRow) error {
	if t.Status != "lobby" {
		return fmt.Errorf("%w: the game has already started", ErrConflict)
	}
	g, err := s.Game(ctx, t.GameID, t.GameVersion)
	if err != nil {
		return err
	}
	meta := g.Meta()
	picker := newPicker(t.Settings.Favorites, t.Seats, t.Settings.Language)
	var seats []seatRow
	for _, st := range t.Seats {
		if st.Kind == "open" {
			if !t.Settings.FillEmpty {
				continue
			}
			st, _ = picker.pick("")
		}
		st.Seat = len(seats)
		seats = append(seats, st)
	}
	if len(seats) < meta.MinSeats {
		return badInput("%s needs at least %d players", meta.Name, meta.MinSeats)
	}
	state, err := g.Setup(games.Config{Seats: len(seats), Options: t.Options}, t.Seed)
	if err != nil {
		return fmt.Errorf("setup: %w", err)
	}
	t2 := *t
	t2.Seats = seats
	evs := []games.Event{{Type: "start", Seat: -1, Text: "The game begins."}}
	_, err = s.commit(ctx, &t2, change{expected: t.Version, g: g, state: state, status: "playing", seats: seats, human: true, events: evs})
	if errors.Is(err, errStale) {
		return fmt.Errorf("%w: the table changed, try again", ErrConflict)
	}
	return err
}

// Leave takes the user out of the table. Mid-game an agent takes over the
// seat (the host's favourite, or Aoi) and the user stays as a spectator, so
// the others' game goes on.
func (s *Service) Leave(ctx context.Context, userID, tableID string) error {
	for attempt := 0; attempt < 5; attempt++ {
		t, err := loadTable(ctx, s.Pool, tableID, false)
		if err != nil {
			return err
		}
		seat := t.seatOf(userID)
		if seat == games.Spectator {
			_, err := s.Pool.Exec(ctx, `DELETE FROM table_spectators WHERE table_id=$1 AND user_id=$2`, tableID, userID)
			return err
		}
		c := change{expected: t.Version, state: t.State, human: true}
		seats := cloneSeats(t.Seats)
		switch t.Status {
		case "lobby":
			if t.HostID == userID {
				c.status = "abandoned" // the host closed the table
			} else {
				seats[seat] = seatRow{Seat: seat, Kind: "open"}
				c.seats = seats
			}
		case "playing":
			g, err := s.Game(ctx, t.GameID, t.GameVersion)
			if err != nil {
				return err
			}
			others := cloneSeats(t.Seats)
			others[seat] = seatRow{Seat: seat, Kind: "open"}
			st, _ := newPicker(t.Settings.Favorites, others, t.Settings.Language).pick("")
			st.Seat = seat
			seats[seat] = st
			c.g, c.seats, c.spectate = g, seats, userID
			c.events = []games.Event{{Type: "takeover", Seat: seat,
				Text: fmt.Sprintf("%s left the table; {s:%d} takes over.", t.Seats[seat].Name, seat)}}
		default:
			return nil // finished tables keep their seating as a record
		}
		_, err = s.commit(ctx, t, c)
		if errors.Is(err, errStale) {
			continue
		}
		return err
	}
	return fmt.Errorf("%w: the table is busy, try again", ErrConflict)
}

// Rematch opens a new table with the same game, options and people (agents
// and everyone still seated), and starts it straight away when no seat is
// open. Calling it twice returns the same rematch. At a table without a
// host, any person seated there may call it and hosts the new table.
func (s *Service) Rematch(ctx context.Context, userID, tableID string) (*TableView, error) {
	t, err := loadTable(ctx, s.Pool, tableID, false)
	if err != nil {
		return nil, err
	}
	if !t.hostRights(userID) {
		return nil, ErrForbidden
	}
	newHost := t.HostID
	if newHost == "" {
		newHost = userID
	}
	if t.RematchID != "" {
		return s.View(ctx, t.RematchID, userID)
	}
	if t.Status != "finished" {
		return nil, fmt.Errorf("%w: the game is not over yet", ErrConflict)
	}
	var newID string
	_, err = s.commit(ctx, t, change{expected: t.Version, state: t.State,
		extra: func(ctx context.Context, tx pgx.Tx, _ int64) error {
			seats := cloneSeats(t.Seats)
			var err error
			newID, err = insertTable(ctx, tx, newTable{name: t.Name, hostID: newHost, gameID: t.GameID, gameVersion: t.GameVersion,
				options: t.Options, settings: t.Settings, seats: seats, rematchOf: t.ID})
			if err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE tables SET rematch_id=$2 WHERE id=$1`, t.ID, newID)
			return err
		}})
	if errors.Is(err, errStale) {
		// Someone else's rematch (or a late event) got there first.
		var rid string
		_ = s.Pool.QueryRow(ctx, `SELECT coalesce(rematch_id::text,'') FROM tables WHERE id=$1`, t.ID).Scan(&rid)
		if rid != "" {
			return s.View(ctx, rid, userID)
		}
		return nil, fmt.Errorf("%w: the table changed, try again", ErrConflict)
	}
	if err != nil {
		return nil, err
	}
	nt, err := loadTable(ctx, s.Pool, newID, false)
	if err != nil {
		return nil, err
	}
	full := true
	for _, st := range nt.Seats {
		full = full && st.Kind != "open"
	}
	if full {
		if err := s.start(ctx, nt); err != nil {
			return nil, err
		}
	}
	return s.View(ctx, newID, userID)
}

// List returns the user's tables: scope "open" is the ones still in the
// lobby or playing, "mine" (default) includes finished ones. Invite codes
// are private, so there is deliberately no public directory of tables.
func (s *Service) List(ctx context.Context, userID, scope string) ([]TableSummary, error) {
	statuses := []string{"lobby", "playing", "finished"}
	if scope == "open" {
		statuses = []string{"lobby", "playing"}
	}
	rows, err := s.Pool.Query(ctx, `SELECT t.id, t.name, t.code, g.name, t.status,
			(SELECT count(*) FROM table_seats x WHERE x.table_id=t.id AND x.kind <> 'open'),
			(SELECT count(*) FROM table_seats x WHERE x.table_id=t.id), t.updated_at
		FROM tables t JOIN games g ON g.id=t.game_id
		WHERE t.status = ANY($2) AND (t.host_id=$1
			OR EXISTS (SELECT 1 FROM table_seats x WHERE x.table_id=t.id AND x.user_id=$1)
			OR EXISTS (SELECT 1 FROM table_spectators x WHERE x.table_id=t.id AND x.user_id=$1))
		ORDER BY t.updated_at DESC LIMIT 50`, userID, statuses)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []TableSummary{}
	for rows.Next() {
		var ts TableSummary
		if err := rows.Scan(&ts.ID, &ts.Name, &ts.Code, &ts.GameName, &ts.Status, &ts.SeatsTaken, &ts.SeatsTotal, &ts.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, ts)
	}
	return out, rows.Err()
}
