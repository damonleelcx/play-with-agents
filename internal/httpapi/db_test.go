package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/auth"
	"github.com/damonleelcx/play-with-agents/internal/db"
	_ "github.com/damonleelcx/play-with-agents/internal/games/holdem" // the built-in game the tables use
	"github.com/damonleelcx/play-with-agents/internal/mail"
	"github.com/damonleelcx/play-with-agents/internal/rooms"
	"github.com/damonleelcx/play-with-agents/internal/tts"
)

// These tests need Postgres (play-pg); they use a database of their own,
// play_httpapi_test, and skip when none is reachable.

func testDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	base := os.Getenv("PLAY_TEST_DATABASE_URL")
	if base == "" {
		base = "postgres://play@127.0.0.1:55860/play?sslmode=disable"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	url, err := db.EnsureDatabase(ctx, base, "play_httpapi_test")
	if err != nil {
		t.Skipf("no test database at %s (%v)", base, err)
	}
	pool, err := pgxpool.New(context.Background(), url)
	if err != nil {
		t.Fatal(err)
	}
	if err := db.Migrate(context.Background(), pool); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(pool.Close)
	return pool
}

type rigUser struct {
	*auth.User
	cookie string
}

type apiRig struct {
	pool *pgxpool.Pool
	s    *Server
	srv  *httptest.Server
}

func newAPIRig(t *testing.T, voice tts.Synthesizer) *apiRig {
	pool := testDB(t)
	r := &apiRig{pool: pool}
	tables := rooms.New(pool)
	r.s = &Server{Pool: pool, Auth: &auth.Service{Pool: pool, Mailer: mail.Log{}, SessionTTL: time.Hour},
		Static: fstest.MapFS{"index.html": {Data: []byte("<html>")}}, Hub: NewHub(), Rooms: tables}
	if voice != nil {
		r.s.Speech = tts.NewService(voice, 200)
	}
	r.srv = httptest.NewServer(r.s.Handler())
	t.Cleanup(r.srv.Close)
	return r
}

const testPassword = "correct horse battery"

func (r *apiRig) user(t *testing.T, name string, verified bool) rigUser {
	t.Helper()
	ctx := context.Background()
	var id string
	email := fmt.Sprintf("%s-%d@example.com", strings.ToLower(name), rand.Int63())
	err := r.pool.QueryRow(ctx, `INSERT INTO users (email, password_hash, name, email_verified_at)
		VALUES ($1, $2, $3, CASE WHEN $4 THEN now() END) RETURNING id`, email, auth.HashPassword(testPassword), name, verified).Scan(&id)
	if err != nil {
		t.Fatal(err)
	}
	raw, _, err := r.s.Auth.CreateSession(ctx, id, "test", "127.0.0.1")
	if err != nil {
		t.Fatal(err)
	}
	u, err := r.s.Auth.UserByID(ctx, id)
	if err != nil {
		t.Fatal(err)
	}
	return rigUser{User: u, cookie: raw}
}

// do sends a request as u (nil: signed out).
func (r *apiRig) do(t *testing.T, u *rigUser, method, path, body string) *http.Response {
	t.Helper()
	req, _ := http.NewRequest(method, r.srv.URL+path, strings.NewReader(body))
	req.Header.Set("X-Play", "1")
	req.Header.Set("Content-Type", "application/json")
	if u != nil {
		req.AddCookie(&http.Cookie{Name: cookieName, Value: u.cookie})
	}
	res, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func readAll(res *http.Response) string {
	defer res.Body.Close()
	b, _ := io.ReadAll(res.Body)
	return string(b)
}

type fakeVoice struct{ got []string }

func (f *fakeVoice) SpeakMP3(_ context.Context, text string) ([]byte, string, error) {
	f.got = append(f.got, text)
	return []byte("ID3mp3"), "audio/mpeg", nil
}

func TestSpeechSpeaksOnlyStoredAoiLines(t *testing.T) {
	voice := &fakeVoice{}
	r := newAPIRig(t, voice)
	r.s.SpeechDailyChars = 40
	ctx := context.Background()
	ann, bob := r.user(t, "Ann", true), r.user(t, "Bob", true)

	var conv string
	var aoiMsg, annMsg int64
	if err := r.pool.QueryRow(ctx, `INSERT INTO conversations (user_id) VALUES ($1) RETURNING id`, ann.ID).Scan(&conv); err != nil {
		t.Fatal(err)
	}
	_ = r.pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, role, content) VALUES ($1,'user','read my words') RETURNING id`, conv).Scan(&annMsg)
	_ = r.pool.QueryRow(ctx, `INSERT INTO messages (conversation_id, role, content) VALUES ($1,'assistant','**Table''s up!** 🎉') RETURNING id`, conv).Scan(&aoiMsg)

	tv, err := r.s.Rooms.Create(ctx, ann.ID, rooms.CreateRequest{GameID: "holdem", Seats: []rooms.SeatSpec{{Kind: "me"}, {Kind: "agent", AgentID: "aoi"}}})
	if err != nil {
		t.Fatal(err)
	}
	var aoiLine, renLine int64
	_ = r.pool.QueryRow(ctx, `INSERT INTO table_chat (table_id, seat, agent_id, name, text) VALUES ($1, 1, 'aoi', 'Aoi', 'Nice hand!') RETURNING id`, tv.ID).Scan(&aoiLine)
	_ = r.pool.QueryRow(ctx, `INSERT INTO table_chat (table_id, seat, agent_id, name, text) VALUES ($1, 1, 'ren', 'Ren', 'Hm.') RETURNING id`, tv.ID).Scan(&renLine)

	post := func(u *rigUser, body string) (int, string) {
		res := r.do(t, u, http.MethodPost, "/api/speech", body)
		return res.StatusCode, readAll(res)
	}
	cases := []struct {
		who  *rigUser
		body string
		want int
	}{
		{&ann, `{"text":"say anything I like"}`, 400}, // free text is gone
		{&ann, `{}`, 400},
		{&ann, fmt.Sprintf(`{"message_id":%d,"sample":"en"}`, aoiMsg), 400},
		{&ann, `{"sample":"fr"}`, 400},
		{&ann, fmt.Sprintf(`{"message_id":%d}`, annMsg), 404}, // not Aoi's
		{&bob, fmt.Sprintf(`{"message_id":%d}`, aoiMsg), 404}, // not Bob's conversation
		{&ann, fmt.Sprintf(`{"table_id":%q,"chat_id":%d}`, tv.ID, renLine), 404},
		{&bob, fmt.Sprintf(`{"table_id":%q,"chat_id":%d}`, tv.ID, aoiLine), 403},
		{&ann, fmt.Sprintf(`{"table_id":%q}`, tv.ID), 400},
	}
	for _, c := range cases {
		if code, body := post(c.who, c.body); code != c.want {
			t.Errorf("%s as %s: %d %s, want %d", c.body, c.who.Name, code, body, c.want)
		}
	}
	if len(voice.got) != 0 {
		t.Fatalf("refused requests reached the vendor: %q", voice.got)
	}

	if code, body := post(&ann, fmt.Sprintf(`{"message_id":%d}`, aoiMsg)); code != 200 || body != "ID3mp3" {
		t.Fatalf("Aoi's reply: %d %q", code, body)
	}
	if code, _ := post(&ann, fmt.Sprintf(`{"table_id":%q,"chat_id":%d}`, tv.ID, aoiLine)); code != 200 {
		t.Fatalf("Aoi's table line: %d", code)
	}
	if strings.Join(voice.got, "|") != "Table's up!|Nice hand!" {
		t.Fatalf("vendor got %q", voice.got)
	}
	// 11 + 10 characters used of 40: the sample (70+) no longer fits today.
	if code, body := post(&ann, `{"sample":"en"}`); code != http.StatusTooManyRequests || !strings.Contains(body, "allowance") {
		t.Fatalf("over the daily allowance: %d %s", code, body)
	}
	if len(voice.got) != 2 {
		t.Fatal("the vendor was called over the allowance")
	}
	var used int
	_ = r.pool.QueryRow(ctx, `SELECT chars FROM tts_usage WHERE user_id=$1`, ann.ID).Scan(&used)
	if used != 21 {
		t.Fatalf("tts_usage %d, want 21", used)
	}
	// Replays are cached and free; the per-minute limit still applies.
	limited := false
	for i := 0; i < 25 && !limited; i++ {
		code, _ := post(&ann, fmt.Sprintf(`{"message_id":%d}`, aoiMsg))
		limited = code == http.StatusTooManyRequests
		if !limited && code != 200 {
			t.Fatalf("replay %d: %d", i, code)
		}
	}
	if !limited || len(voice.got) != 2 {
		t.Fatalf("rate limit %v, vendor calls %d", limited, len(voice.got))
	}
	// Bob has his own allowance and gets the sample.
	if code, _ := post(&bob, `{"sample":"zh"}`); code != 200 {
		t.Fatalf("Bob's sample: %d", code)
	}

	off := &Server{limiter: newLimiter()}
	rec := httptest.NewRecorder()
	off.speak(rec, httptest.NewRequest(http.MethodPost, "/api/speech", strings.NewReader(`{"sample":"en"}`)), ann.User)
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("no voice configured: %d", rec.Code)
	}
}

// Verifying an address never signs anyone in: only a browser already signed
// in to that very account gets its user back.
func TestVerifyDoesNotCreateASession(t *testing.T) {
	r := newAPIRig(t, nil)
	ctx := context.Background()
	victim, other := r.user(t, "Victim", false), r.user(t, "Other", true)
	token := func() string {
		raw := fmt.Sprintf("tok-%d", rand.Int63())
		if _, err := r.pool.Exec(ctx, `INSERT INTO email_tokens (token_hash, user_id, purpose, expires_at) VALUES ($1, $2, 'verify', now() + interval '1 hour')`,
			auth.Hash(raw), victim.ID); err != nil {
			t.Fatal(err)
		}
		return raw
	}
	check := func(u *rigUser, wantSignedIn bool) {
		t.Helper()
		res := r.do(t, u, http.MethodPost, "/api/auth/verify", fmt.Sprintf(`{"token":%q}`, token()))
		body := readAll(res)
		if res.StatusCode != 200 {
			t.Fatalf("verify: %d %s", res.StatusCode, body)
		}
		for _, c := range res.Cookies() {
			if c.Name == cookieName {
				t.Fatalf("verify set a session cookie (as %v)", u)
			}
		}
		var out struct {
			Verified bool            `json:"verified"`
			SignedIn bool            `json:"signed_in"`
			User     json.RawMessage `json:"user"`
		}
		_ = json.Unmarshal([]byte(body), &out)
		if !out.Verified || out.SignedIn != wantSignedIn || (len(out.User) > 0) != wantSignedIn {
			t.Fatalf("verify answered %s, want signed_in=%v", body, wantSignedIn)
		}
	}
	check(nil, false)    // a stranger's browser: confirmed, sign in to continue
	check(&other, false) // signed in as someone else: untouched
	check(&victim, true) // the account's own session
	if u, _ := r.s.Auth.UserByID(ctx, victim.ID); !u.EmailVerified {
		t.Fatal("the address was not marked verified")
	}
}

// The table stream re-checks access: a spectator who is removed stops
// receiving anything.
func TestTableStreamClosesWhenAccessIsLost(t *testing.T) {
	r := newAPIRig(t, nil)
	old := streamRecheck
	streamRecheck = 0
	t.Cleanup(func() { streamRecheck = old })
	ctx := context.Background()
	ann, bob := r.user(t, "Ann", true), r.user(t, "Bob", true)
	tv, err := r.s.Rooms.Create(ctx, ann.ID, rooms.CreateRequest{GameID: "holdem", Seats: []rooms.SeatSpec{{Kind: "me"}, {Kind: "agent"}}})
	if err != nil {
		t.Fatal(err)
	}
	if jv, err := r.s.Rooms.Join(ctx, bob.ID, tv.Code); err != nil || jv.MySeat != -1 {
		t.Fatalf("Bob should spectate: %v", err)
	}
	res := r.do(t, &bob, http.MethodGet, "/api/tables/"+tv.ID+"/stream", "")
	if res.StatusCode != 200 {
		t.Fatalf("stream: %d", res.StatusCode)
	}
	defer res.Body.Close()
	events := make(chan string, 16)
	go func() {
		sc := bufio.NewScanner(res.Body)
		for sc.Scan() {
			if ev, ok := strings.CutPrefix(sc.Text(), "event: "); ok {
				events <- ev
			}
		}
		close(events)
	}()
	next := func() (string, bool) {
		select {
		case ev, ok := <-events:
			return ev, ok
		case <-time.After(5 * time.Second):
			t.Fatal("timed out waiting for the stream")
			return "", false
		}
	}
	if ev, _ := next(); ev != "table" {
		t.Fatalf("first event %q", ev)
	}
	line := &rooms.ChatLine{ID: 1, Name: "Ann", Text: "hi"}
	r.s.Rooms.Hub().Publish(tv.ID, rooms.Signal{Kind: "chat", Chat: line})
	if ev, _ := next(); ev != "chat" {
		t.Fatalf("a spectator should get chat, got %q", ev)
	}
	if _, err := r.pool.Exec(ctx, `DELETE FROM table_spectators WHERE table_id=$1 AND user_id=$2`, tv.ID, bob.ID); err != nil {
		t.Fatal(err)
	}
	r.s.Rooms.Hub().Publish(tv.ID, rooms.Signal{Kind: "chat", Chat: line})
	if ev, _ := next(); ev != "closed" {
		t.Fatalf("after losing access the stream sent %q, want closed", ev)
	}
	if ev, ok := next(); ok {
		t.Fatalf("the stream kept going after closing: %q", ev)
	}
}

// DeleteAccount checks the password first, then lets the rooms service hand
// everything over, then deletes; a failed hand-over keeps the account.
func TestDeleteAccountHandsOverFirst(t *testing.T) {
	r := newAPIRig(t, nil)
	ctx := context.Background()
	ann := r.user(t, "Ann", true)
	var called []string
	r.s.Auth.BeforeDelete = func(ctx context.Context, id string) error {
		called = append(called, id)
		return r.s.Rooms.ReleaseUser(ctx, id)
	}
	if res := r.do(t, &ann, http.MethodPost, "/api/account/delete", `{"password":"wrong one entirely"}`); res.StatusCode != 400 || len(called) != 0 {
		t.Fatalf("wrong password: %d, hook calls %d", res.StatusCode, len(called))
	}
	r.s.Auth.BeforeDelete = func(context.Context, string) error { return errors.New("db down") }
	if res := r.do(t, &ann, http.MethodPost, "/api/account/delete", fmt.Sprintf(`{"password":%q}`, testPassword)); res.StatusCode != 500 {
		t.Fatalf("failed hand-over: %d", res.StatusCode)
	}
	if _, err := r.s.Auth.UserByID(ctx, ann.ID); err != nil {
		t.Fatal("the account was deleted although the hand-over failed")
	}
	// The failed attempt signed every session out; sign in again.
	raw, _, _ := r.s.Auth.CreateSession(ctx, ann.ID, "test", "127.0.0.1")
	ann.cookie = raw
	r.s.Auth.BeforeDelete = func(ctx context.Context, id string) error {
		called = append(called, id)
		return r.s.Rooms.ReleaseUser(ctx, id)
	}
	if res := r.do(t, &ann, http.MethodPost, "/api/account/delete", fmt.Sprintf(`{"password":%q}`, testPassword)); res.StatusCode != 200 {
		t.Fatalf("delete: %d %s", res.StatusCode, readAll(res))
	}
	if len(called) != 1 || called[0] != ann.ID {
		t.Fatalf("hook calls %v", called)
	}
	if _, err := r.s.Auth.UserByID(ctx, ann.ID); err == nil {
		t.Fatal("the account still exists")
	}
}

// A stream outlives the server's ReadTimeout (cmd/play sets one): net/http
// lifts the read deadline once the request body is in.
func TestStreamOutlivesReadTimeout(t *testing.T) {
	s := &Server{}
	h := s.middleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fl := w.(http.Flusher)
		for i := 0; i < 4; i++ {
			select {
			case <-r.Context().Done():
				return
			case <-time.After(150 * time.Millisecond):
			}
			fmt.Fprintf(w, "event: tick\ndata: %d\n\n", i)
			fl.Flush()
		}
	}))
	srv := httptest.NewUnstartedServer(h)
	srv.Config.ReadTimeout = 200 * time.Millisecond
	srv.Start()
	defer srv.Close()
	res, err := http.Get(srv.URL + "/api/x")
	if err != nil {
		t.Fatal(err)
	}
	body := readAll(res)
	if n := strings.Count(body, "event: tick"); n != 4 {
		t.Fatalf("the stream was cut after %d of 4 events by the read deadline:\n%s", n, body)
	}
}
