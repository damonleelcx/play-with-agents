package httpapi

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/rooms"
)

type sseEvent struct{ name, data string }

// openStream reads u's table stream into a channel of events.
func (r *apiRig) openStream(t *testing.T, u *rigUser, tableID string) <-chan sseEvent {
	t.Helper()
	res := r.do(t, u, http.MethodGet, "/api/tables/"+tableID+"/stream", "")
	if res.StatusCode != 200 {
		t.Fatalf("stream: %d", res.StatusCode)
	}
	t.Cleanup(func() { res.Body.Close() })
	out := make(chan sseEvent, 64)
	go func() {
		defer close(out)
		sc := bufio.NewScanner(res.Body)
		var ev sseEvent
		for sc.Scan() {
			line := sc.Text()
			switch {
			case strings.HasPrefix(line, "event: "):
				ev.name = strings.TrimPrefix(line, "event: ")
			case strings.HasPrefix(line, "data: "):
				ev.data = strings.TrimPrefix(line, "data: ")
			case line == "" && ev.name != "":
				out <- ev
				ev = sseEvent{}
			}
		}
	}()
	return out
}

// collect gathers events for d.
func collect(ch <-chan sseEvent, d time.Duration) []sseEvent {
	var out []sseEvent
	end := time.After(d)
	for {
		select {
		case ev, ok := <-ch:
			if !ok {
				return out
			}
			out = append(out, ev)
		case <-end:
			return out
		}
	}
}

// The stream carries chat, typing, reactions and presence; a whisper and
// the reactions on it reach only its two people, and nobody is told about
// their own typing.
func TestStreamLiveChat(t *testing.T) {
	r := newAPIRig(t, nil)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.s.Rooms.Listen(ctx)
	ann, bob, cy := r.user(t, "Ann", true), r.user(t, "Bob", true), r.user(t, "Cy", true)
	tv, err := r.s.Rooms.Create(ctx, ann.ID, rooms.CreateRequest{GameID: "holdem", Seats: []rooms.SeatSpec{{Kind: "me"}, {Kind: "open"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.s.Rooms.Join(ctx, bob.ID, tv.Code); err != nil {
		t.Fatal(err)
	}
	if jv, err := r.s.Rooms.Join(ctx, cy.ID, tv.Code); err != nil || jv.MySeat != -1 {
		t.Fatalf("Cy should spectate: %v", err)
	}
	// Let the listener come up before anything is sent.
	time.Sleep(300 * time.Millisecond)
	streams := map[string]<-chan sseEvent{"Ann": r.openStream(t, &ann, tv.ID), "Bob": r.openStream(t, &bob, tv.ID), "Cy": r.openStream(t, &cy, tv.ID)}
	time.Sleep(300 * time.Millisecond)

	post := func(u *rigUser, path, body string) int {
		res := r.do(t, u, http.MethodPost, "/api/tables/"+tv.ID+path, body)
		readAll(res)
		return res.StatusCode
	}
	if c := post(&ann, "/chat", `{"text":"hello all","client_msg_id":"p1"}`); c != 200 {
		t.Fatalf("chat %d", c)
	}
	if c := post(&ann, "/chat", `{"text":"meet me after","client_msg_id":"w1","whisper_seat":1}`); c != 200 {
		t.Fatalf("whisper %d", c)
	}
	if c := post(&bob, "/typing", `{}`); c != 200 {
		t.Fatalf("typing %d", c)
	}
	var pub, whisper int64
	_ = r.pool.QueryRow(ctx, `SELECT id FROM table_chat WHERE table_id=$1 AND text='hello all'`, tv.ID).Scan(&pub)
	_ = r.pool.QueryRow(ctx, `SELECT id FROM table_chat WHERE table_id=$1 AND text='meet me after'`, tv.ID).Scan(&whisper)
	if c := post(&cy, fmt.Sprintf("/chat/%d/react", pub), `{"emoji":"👍"}`); c != 200 {
		t.Fatalf("react %d", c)
	}
	if c := post(&bob, fmt.Sprintf("/chat/%d/react", whisper), `{"emoji":"❤️"}`); c != 200 {
		t.Fatalf("react to whisper %d", c)
	}
	if c := post(&cy, fmt.Sprintf("/chat/%d/react", whisper), `{"emoji":"❤️"}`); c != 404 {
		t.Fatalf("outsider reacting to a whisper: %d", c)
	}

	got := map[string][]sseEvent{}
	for name, ch := range streams {
		got[name] = collect(ch, 1500*time.Millisecond)
	}
	has := func(name, event, needle string) bool {
		for _, ev := range got[name] {
			if ev.name == event && strings.Contains(ev.data, needle) {
				return true
			}
		}
		return false
	}
	for _, name := range []string{"Ann", "Bob", "Cy"} {
		if !has(name, "chat", "hello all") {
			t.Errorf("%s did not get the public line", name)
		}
		if !has(name, "reaction", "👍") {
			t.Errorf("%s did not get the reaction", name)
		}
		if !has(name, "presence", `"seats":[0,1]`) {
			t.Errorf("%s never saw both players here: %v", name, got[name])
		}
	}
	for name, want := range map[string]bool{"Ann": true, "Bob": true, "Cy": false} {
		if has(name, "chat", "meet me after") != want || has(name, "reaction", "❤️") != want {
			t.Errorf("%s whisper delivery: want %v", name, want)
		}
	}
	if has("Bob", "typing", "Bob") || !has("Ann", "typing", `"name":"Bob"`) || !has("Cy", "typing", `"name":"Bob"`) {
		t.Error("typing should reach everyone but the typist")
	}
	// Presence is in the view too.
	res := r.do(t, &cy, http.MethodGet, "/api/tables/"+tv.ID, "")
	var view rooms.TableView
	_ = json.NewDecoder(res.Body).Decode(&view)
	res.Body.Close()
	if view.Presence == nil || len(view.Presence.Seats) != 2 || len(view.Presence.Watchers) != 1 || view.Presence.Watchers[0] != "Cy" {
		t.Fatalf("view presence %+v", view.Presence)
	}
	for _, l := range view.Chat {
		if l.Whisper {
			t.Fatal("Cy's view holds a whisper")
		}
	}
}
