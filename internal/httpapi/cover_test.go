package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/art"
	"github.com/damonleelcx/play-with-agents/internal/rooms"
)

// GET /api/games/{id}/cover follows GameDetail's visibility: built-ins,
// the owner's own games, and published public or unlisted ones.
func TestGameCoverVisibilityAndCaching(t *testing.T) {
	r := newAPIRig(t, nil)
	ctx := context.Background()
	ann, bob := r.user(t, "Ann", true), r.user(t, "Bob", true)
	n := rand.Int63() % 1_000_000
	mk := func(slug, status, vis string) string {
		id := fmt.Sprintf("%s-%d", slug, n)
		if _, err := r.pool.Exec(ctx, `INSERT INTO games (id, owner_id, kind, name, status, visibility) VALUES ($1,$2,'script',$3,$4,$5)`,
			id, ann.ID, "Cover "+slug, status, vis); err != nil {
			t.Fatal(err)
		}
		b, err := art.Procedural(id, "Cover "+slug)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := art.Save(ctx, r.pool, id, "Cover "+slug, &art.Result{Bytes: b, ContentType: art.ContentType, Source: "procedural"}); err != nil {
			t.Fatal(err)
		}
		return id
	}
	private := mk("private", "draft", "private")
	public := mk("public", "published", "public")
	unlisted := mk("unlisted", "published", "unlisted")
	unpublished := mk("drafty", "draft", "public")
	var none string
	none = fmt.Sprintf("bare-%d", n)
	if _, err := r.pool.Exec(ctx, `INSERT INTO games (id, owner_id, kind, name, status, visibility) VALUES ($1,$2,'script','Bare','published','public')`, none, ann.ID); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _, _ = r.pool.Exec(context.Background(), `DELETE FROM games WHERE owner_id=$1`, ann.ID) })

	cases := []struct {
		who  *rigUser
		id   string
		want int
	}{
		{&ann, private, 200}, {&bob, private, 404},
		{&ann, unpublished, 200}, {&bob, unpublished, 404},
		{&bob, public, 200}, {&bob, unlisted, 200},
		{&bob, none, 404}, {&bob, "no-such-game-x", 404},
		{nil, public, 401},
	}
	for _, c := range cases {
		res := r.do(t, c.who, http.MethodGet, "/api/games/"+c.id+"/cover", "")
		body := readAll(res)
		if res.StatusCode != c.want {
			t.Errorf("%s as %v: %d, want %d", c.id, c.who != nil, res.StatusCode, c.want)
			continue
		}
		if c.want == 200 {
			want, _ := art.Procedural(c.id, "Cover "+strings.Split(c.id, "-")[0])
			if res.Header.Get("Content-Type") != "image/jpeg" || !bytes.Equal([]byte(body), want) || res.Header.Get("ETag") == "" {
				t.Errorf("%s: %s, %d bytes, etag %q", c.id, res.Header.Get("Content-Type"), len(body), res.Header.Get("ETag"))
			}
		}
	}

	// The versioned URL is immutable; the ETag revalidates.
	res := r.do(t, &bob, http.MethodGet, "/api/games/"+public+"/cover?v=1", "")
	etag := res.Header.Get("ETag")
	readAll(res)
	if cc := res.Header.Get("Cache-Control"); !strings.Contains(cc, "immutable") || !strings.Contains(cc, "private") {
		t.Fatalf("cache-control %q", cc)
	}
	req, _ := http.NewRequest(http.MethodGet, r.srv.URL+"/api/games/"+public+"/cover", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: bob.cookie})
	req.Header.Set("If-None-Match", etag)
	res2, err := (&http.Client{Timeout: 10 * time.Second}).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res2.Body.Close()
	if res2.StatusCode != http.StatusNotModified {
		t.Fatalf("revalidation: %d", res2.StatusCode)
	}
	// A new cover is a new version and a new ETag.
	b, _ := art.Procedural(public, "Renamed")
	if v, _ := art.Save(ctx, r.pool, public, "Renamed", &art.Result{Bytes: b, ContentType: art.ContentType, Source: "procedural"}); v != 2 {
		t.Fatalf("second save is v%d", v)
	}
	res = r.do(t, &bob, http.MethodGet, "/api/games/"+public+"/cover?v=1", "")
	readAll(res)
	if res.Header.Get("ETag") == etag || strings.Contains(res.Header.Get("Cache-Control"), "immutable") {
		t.Fatalf("stale version served as current: %q %q", res.Header.Get("ETag"), res.Header.Get("Cache-Control"))
	}

	// Built-in Hold'em without a stored cover: its shipped one.
	_, _ = r.pool.Exec(ctx, `DELETE FROM game_covers WHERE game_id='holdem'`)
	noFollow := &http.Client{Timeout: 10 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, _ = http.NewRequest(http.MethodGet, r.srv.URL+"/api/games/holdem/cover", nil)
	req.AddCookie(&http.Cookie{Name: cookieName, Value: bob.cookie})
	res3, err := noFollow.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	res3.Body.Close()
	if res3.StatusCode != http.StatusFound || res3.Header.Get("Location") != "/play/covers/holdem.webp" {
		t.Fatalf("holdem: %d %q", res3.StatusCode, res3.Header.Get("Location"))
	}

	// The lobby cards carry the URLs.
	res = r.do(t, &ann, http.MethodGet, "/api/games", "")
	var list rooms.GameList
	if err := json.Unmarshal([]byte(readAll(res)), &list); err != nil {
		t.Fatal(err)
	}
	covers := map[string]string{}
	for _, g := range append(append(list.Builtin, list.Mine...), list.Community...) {
		covers[g.ID] = g.Cover
	}
	if covers["holdem"] != "/play/covers/holdem.webp" || covers[public] != "/api/games/"+public+"/cover?v=2" ||
		covers[private] != "/api/games/"+private+"/cover?v=1" || covers[none] != "" {
		t.Fatalf("card covers: holdem=%q public=%q private=%q none=%q", covers["holdem"], covers[public], covers[private], covers[none])
	}
	res = r.do(t, &bob, http.MethodGet, "/api/games/"+unlisted, "")
	var d rooms.GameDetail
	_ = json.Unmarshal([]byte(readAll(res)), &d)
	if d.Cover != "/api/games/"+unlisted+"/cover?v=1" {
		t.Fatalf("detail cover %q", d.Cover)
	}
}
