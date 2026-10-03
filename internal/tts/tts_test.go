package tts

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// fakeFish is a fish.audio stand-in that records what it was sent.
type fakeFish struct {
	srv    *httptest.Server
	calls  atomic.Int64
	mu     sync.Mutex
	header http.Header
	body   map[string]any
	status int
	audio  []byte
	delay  time.Duration
}

func newFakeFish(t *testing.T) *fakeFish {
	f := &fakeFish{status: 200, audio: []byte("ID3fake-mp3")}
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.calls.Add(1)
		time.Sleep(f.delay)
		f.mu.Lock()
		f.header = r.Header.Clone()
		_ = json.NewDecoder(r.Body).Decode(&f.body)
		f.mu.Unlock()
		if f.status != 200 {
			http.Error(w, `{"message":"invalid api key"}`, f.status)
			return
		}
		w.Header().Set("Content-Type", "audio/mpeg")
		_, _ = w.Write(f.audio)
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func TestNewFishNeedsKeyAndVoice(t *testing.T) {
	if _, err := NewFish("", "", "voice", ""); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("no key: %v", err)
	}
	if _, err := NewFish("", "key", " ", ""); !errors.Is(err, ErrNotConfigured) {
		t.Errorf("no voice: %v", err)
	}
	f, err := NewFish("", "key", "voice", "")
	if err != nil || f.Endpoint != DefaultEndpoint || f.Model != DefaultModel {
		t.Fatalf("defaults: %+v %v", f, err)
	}
}

func TestSpeakMP3SendsVoiceModelHeaderAndKey(t *testing.T) {
	ff := newFakeFish(t)
	f, _ := NewFish(ff.srv.URL, "secret-key", "df5c6c19dca944918dcbd6f1368fd02f", "s2.1-pro-free")
	audio, ct, err := f.SpeakMP3(context.Background(), "  Table's up — tap the card!  ")
	if err != nil {
		t.Fatal(err)
	}
	if string(audio) != "ID3fake-mp3" || ct != "audio/mpeg" {
		t.Fatalf("got %q %s", audio, ct)
	}
	ff.mu.Lock()
	defer ff.mu.Unlock()
	if ff.header.Get("model") != "s2.1-pro-free" {
		t.Errorf("the backbone must travel as the model header, got %q", ff.header.Get("model"))
	}
	if ff.header.Get("Authorization") != "Bearer secret-key" {
		t.Errorf("auth header %q", ff.header.Get("Authorization"))
	}
	if ff.body["reference_id"] != "df5c6c19dca944918dcbd6f1368fd02f" || ff.body["format"] != "mp3" || ff.body["text"] != "Table's up — tap the card!" {
		t.Errorf("body %v", ff.body)
	}
	if _, inBody := ff.body["model"]; inBody {
		t.Error("model must not be in the body (it is ignored there)")
	}
}

func TestSpeakMP3ReportsVendorErrorsAndSilence(t *testing.T) {
	ff := newFakeFish(t)
	f, _ := NewFish(ff.srv.URL, "k", "v", "")
	ff.status = 401
	if _, _, err := f.SpeakMP3(context.Background(), "hi"); err == nil || !strings.Contains(err.Error(), "401") || !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("401: %v", err)
	}
	ff.status, ff.audio = 200, nil
	if _, _, err := f.SpeakMP3(context.Background(), "hi"); err == nil {
		t.Error("an empty 200 was served as audio")
	}
	if _, _, err := f.SpeakMP3(context.Background(), "   "); err == nil {
		t.Error("blank text was sent")
	}
}

func TestTrainsOnRequestsIsAnAllowlist(t *testing.T) {
	for model, trains := range map[string]bool{"": true, "s2.1-pro-free": true, "s1": true, "s9-future": true, "s2.1-pro": false, "s2-pro": false} {
		if got := TrainsOnRequests(model); got != trains {
			t.Errorf("TrainsOnRequests(%q) = %v", model, got)
		}
	}
}

func TestClean(t *testing.T) {
	cases := []struct{ in, want string }{
		{"**Side pots** in one line: _you can only win what you put in._ 🎉", "Side pots in one line: you can only win what you put in."},
		{"# Rules\n- one\n- two\n\n> quoted", "Rules\none\ntwo\nquoted"},
		{"See [the rules](https://play.example/rules) or https://x.y/z now", "See the rules or now"},
		{"Run `code` and\n```js\nconst x = 1\n```\nthen go", "Run code and\nthen go"},
		{"I hold A♠ K♥ 👀🃏", "I hold A♠ K♥"},
		{"👍🏽❤️", ""},
		{"好的！我们来一局吧😄", "好的！我们来一局吧"},
	}
	for _, c := range cases {
		if got := Clean(c.in, 600); got != c.want {
			t.Errorf("Clean(%q)\n got %q\nwant %q", c.in, got, c.want)
		}
	}
	long := strings.Repeat("Mika bluffs again. ", 50) // 950 chars
	got := Clean(long, MaxSpokenChars)
	if n := len([]rune(got)); n > MaxSpokenChars || n < MaxSpokenChars/2 || !strings.HasSuffix(got, ".") {
		t.Errorf("long text capped to %d chars ending %q", n, got[len(got)-5:])
	}
}

func TestServiceCachesAndCollapsesConcurrentRequests(t *testing.T) {
	ff := newFakeFish(t)
	ff.delay = 50 * time.Millisecond
	f, _ := NewFish(ff.srv.URL, "k", "v", "")
	s := NewService(f, 2)
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, _, err := s.Speak(ctx, "**Deal** me in!"); err != nil {
				t.Error(err)
			}
		}()
	}
	wg.Wait()
	if n := ff.calls.Load(); n != 1 {
		t.Fatalf("5 concurrent identical lines made %d vendor calls", n)
	}
	// Same words after cleaning are the same line.
	if _, _, _ = s.Speak(ctx, "Deal me in!"); ff.calls.Load() != 1 {
		t.Fatal("a cached line was synthesised again")
	}
	// LRU: capacity 2, so a third distinct line evicts the oldest.
	_, _, _ = s.Speak(ctx, "two")
	_, _, _ = s.Speak(ctx, "three")
	if s.Len() != 2 {
		t.Fatalf("cache holds %d lines, capacity 2", s.Len())
	}
	before := ff.calls.Load()
	_, _, _ = s.Speak(ctx, "Deal me in!")
	if ff.calls.Load() != before+1 {
		t.Fatal("the evicted line was still served from cache")
	}
	if _, _, err := s.Speak(ctx, "🎉🎉"); !errors.Is(err, ErrEmpty) {
		t.Fatalf("emoji-only line: %v", err)
	}
	// Failures are not cached.
	ff.status = 500
	if _, _, err := s.Speak(ctx, "fails"); err == nil {
		t.Fatal("vendor failure not reported")
	}
	ff.status = 200
	if _, _, err := s.Speak(ctx, "fails"); err != nil {
		t.Fatalf("a failure was cached: %v", err)
	}
}

// One real call against fish.audio, when a key is present (the local .env has
// one): proves the key, the voice id and the backbone header together.
//
//	set -a; . ./.env; set +a; go test ./internal/tts -run TestLiveFish -v
func TestLiveFish(t *testing.T) {
	key := os.Getenv("PLAY_TTS_API_KEY")
	if key == "" || testing.Short() {
		t.Skip("needs PLAY_TTS_API_KEY")
	}
	f, err := NewFish("", key, os.Getenv("PLAY_TTS_VOICE_ID"), os.Getenv("PLAY_TTS_MODEL"))
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	audio, ct, err := f.SpeakMP3(ctx, "Hi! I'm Aoi. Let's play.")
	if err != nil {
		t.Fatal(err)
	}
	t.Logf("fish.audio: %d bytes of %s, model %s, trains on input: %v", len(audio), ct, f.Model, TrainsOnRequests(f.Model))
	if len(audio) < 1000 || !strings.HasPrefix(ct, "audio/") {
		t.Fatalf("%d bytes of %s does not look like speech", len(audio), ct)
	}
}

func TestSpeakChargedChargesOnlyVendorCalls(t *testing.T) {
	ff := newFakeFish(t)
	f, _ := NewFish(ff.srv.URL, "k", "v", "")
	s := NewService(f, 10)
	ctx := context.Background()
	charged := 0
	charge := func(n int) error { charged += n; return nil }

	if _, _, err := s.SpeakCharged(ctx, "**Deal** me in!", charge); err != nil {
		t.Fatal(err)
	}
	if charged != len("Deal me in!") {
		t.Fatalf("charged %d chars, want the cleaned length %d", charged, len("Deal me in!"))
	}
	if _, _, err := s.SpeakCharged(ctx, "Deal me in!", charge); err != nil || charged != len("Deal me in!") {
		t.Fatalf("a cached replay was charged (total %d, err %v)", charged, err)
	}

	capped := errors.New("daily voice allowance used up")
	before := ff.calls.Load()
	if _, _, err := s.SpeakCharged(ctx, "a new line", func(int) error { return capped }); !errors.Is(err, capped) {
		t.Fatalf("over the cap: %v", err)
	}
	if ff.calls.Load() != before {
		t.Fatal("the vendor was called although the allowance refused the line")
	}
	// The refusal is not cached: with allowance it is spoken.
	if _, _, err := s.SpeakCharged(ctx, "a new line", charge); err != nil {
		t.Fatal(err)
	}
}
