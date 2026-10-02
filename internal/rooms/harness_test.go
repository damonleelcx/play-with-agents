package rooms

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math/rand"
	"net/url"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/db"
	"github.com/damonleelcx/play-with-agents/internal/games"
)

// The rooms tests use their own database (play_rooms_test, created next to
// the configured one) because other packages' tests TRUNCATE users, which
// cascades into tables mid-test when packages run in parallel.
const defaultTestURL = "postgres://play@127.0.0.1:55860/play?sslmode=disable"

var (
	setupOnce sync.Once
	setupURL  string
	setupErr  error
)

func testURL() string {
	for _, k := range []string{"PLAY_TEST_DATABASE_URL", "ACT_TEST_DATABASE_URL"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}
	return defaultTestURL
}

func prepareDB() (string, error) {
	base := testURL()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := pgx.Connect(ctx, base)
	if err != nil {
		return "", err
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, `CREATE DATABASE play_rooms_test`); err != nil && !isDuplicateDB(err) {
		return base, nil // no rights to create one: share the configured database
	}
	u, err := url.Parse(base)
	if err != nil {
		return "", err
	}
	u.Path = "/play_rooms_test"
	return u.String(), nil
}

// isDuplicateDB: 42P04 duplicate_database, i.e. an earlier run created it.
func isDuplicateDB(err error) bool {
	var pe *pgconn.PgError
	return errors.As(err, &pe) && pe.Code == "42P04"
}

func testPool(t *testing.T) *pgxpool.Pool {
	t.Helper()
	setupOnce.Do(func() { setupURL, setupErr = prepareDB() })
	if setupErr != nil {
		t.Skipf("test database unreachable: %v", setupErr)
	}
	ctx := context.Background()
	cfg, err := pgxpool.ParseConfig(setupURL)
	if err != nil {
		t.Fatal(err)
	}
	cfg.MaxConns = 20
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		t.Skipf("test database unreachable: %v", err)
	}
	pctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	if err := pool.Ping(pctx); err != nil {
		pool.Close()
		t.Skipf("test database unreachable: %v", err)
	}
	if err := db.Migrate(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `TRUNCATE table_jobs, table_chat, table_events, table_moves, table_spectators, table_seats, tables,
		game_versions, games, user_preferences, users CASCADE`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type rig struct {
	pool *pgxpool.Pool
	svc  *Service
}

// newRig returns a service whose agents never think (unless a test says
// otherwise) and whose clock-free tables are fast to drive.
func newRig(t *testing.T) *rig {
	pool := testPool(t)
	svc := New(pool)
	svc.Think = func(string, bool) time.Duration { return 0 }
	return &rig{pool: pool, svc: svc}
}

func (r *rig) user(t *testing.T, name string, prefs map[string]any) string {
	t.Helper()
	var id string
	err := r.pool.QueryRow(context.Background(), `INSERT INTO users (email, password_hash, name, email_verified_at)
		VALUES ($1, 'x', $2, now()) RETURNING id`, fmt.Sprintf("%s-%d@example.com", name, rand.Int63()), name).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	if prefs != nil {
		raw, _ := json.Marshal(prefs)
		if _, err := r.pool.Exec(context.Background(), `INSERT INTO user_preferences (user_id, data) VALUES ($1, $2)`, id, raw); err != nil {
			t.Fatal(err)
		}
	}
	return id
}

func (r *rig) create(t *testing.T, host string, seats ...string) *TableView {
	t.Helper()
	req := CreateRequest{GameID: "race21"}
	for _, k := range seats {
		sp := SeatSpec{Kind: k}
		if len(k) > 6 && k[:6] == "agent:" {
			sp = SeatSpec{Kind: "agent", AgentID: k[6:]}
		}
		req.Seats = append(req.Seats, sp)
	}
	v, err := r.svc.Create(context.Background(), host, req)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (r *rig) start(t *testing.T, host, id string) *TableView {
	t.Helper()
	v, err := r.svc.Start(context.Background(), host, id)
	if err != nil {
		t.Fatal(err)
	}
	return v
}

func (r *rig) count(t *testing.T, sql string, args ...any) int {
	t.Helper()
	var n int
	if err := r.pool.QueryRow(context.Background(), sql, args...).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n
}

// dueNow makes every ready job runnable immediately.
func (r *rig) dueNow(t *testing.T) {
	if _, err := r.pool.Exec(context.Background(), `UPDATE table_jobs SET run_after=now() WHERE status='ready'`); err != nil {
		t.Fatal(err)
	}
}

func (r *rig) claim(t *testing.T, owner string) *job {
	t.Helper()
	j, err := r.svc.claim(context.Background(), owner)
	if err != nil {
		t.Fatal(err)
	}
	return j
}

// waitFor polls cond until it holds or the bound runs out.
func waitFor(t *testing.T, d time.Duration, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(d)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out after %s waiting for %s", d, what)
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// randBrain plays a random legal move, using the rng it is given.
type randBrain struct{ calls atomic.Int64 }

func (b *randBrain) Choose(ctx context.Context, g games.Game, st games.State, seat games.Seat, p games.Persona, rng *rand.Rand) (games.Move, error) {
	b.calls.Add(1)
	legal, err := g.Legal(st, seat)
	if err != nil || len(legal) == 0 {
		return games.Move{}, fmt.Errorf("no legal moves")
	}
	spec := legal[rng.Intn(len(legal))]
	return games.Move{Type: spec.Type, Args: spec.Args}, nil
}

// fakeChatter records requests and answers with a scripted line.
type fakeChatter struct {
	mu   sync.Mutex
	reqs []ChatRequest
	line string
}

func (f *fakeChatter) Line(_ context.Context, req ChatRequest) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reqs = append(f.reqs, req)
	return f.line, nil
}
