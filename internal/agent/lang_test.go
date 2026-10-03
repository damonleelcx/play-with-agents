package agent

import (
	"context"
	"os"
	"strings"
	"testing"
	"unicode"

	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/llm"
)

// The reply prompt names the language the player wrote in, or their chosen
// language when the message cannot tell (kanji-only text for a player who
// chose Japanese).
func TestReplyLanguageInPrompt(t *testing.T) {
	r := newRig(t, map[string]any{"intent": "chat", "confidence": 0.9}, nil)
	for _, c := range []struct{ text, pref, want string }{
		{"안녕 아오이! 오늘 한 판 할까?", "en", "REPLY LANGUAGE: Korean"},
		{"こんにちは！今日も一局やろう", "en", "REPLY LANGUAGE: Japanese"},
		{"了解", "ja", "REPLY LANGUAGE: Japanese"},
		{"了解", "en", "REPLY LANGUAGE: Simplified Chinese"},
		{"ok", "ko", "REPLY LANGUAGE: Korean"},
	} {
		if _, err := r.agent.Turn(context.Background(), User{ID: r.uid, Name: "Sam", Lang: c.pref}, r.conv, c.text, "", &sink{}); err != nil {
			t.Fatal(err)
		}
		sys := r.fake.systemPrompt()
		if !strings.Contains(sys, c.want) {
			t.Fatalf("%q (pref %s): prompt lacks %q", c.text, c.pref, c.want)
		}
		if strings.Contains(c.want, "Japanese") && (!strings.Contains(sys, "やっほー") || !strings.Contains(sys, "Captain Bram → ブラム船長")) {
			t.Fatalf("Japanese prompt lacks the Japanese voice samples or names")
		}
	}
}

// textSink collects the streamed reply.
type textSink struct{ b strings.Builder }

func (s *textSink) Meta(map[string]any) {}
func (s *textSink) Delta(d string)      { s.b.WriteString(d) }

// One live chat turn per language against the real model: Aoi must answer in
// the language she was spoken to in. Runs only with a key, like the route
// eval:
//
//	set -a; . ./.env; set +a; go test ./internal/agent -run TestLiveChatLanguages -v
func TestLiveChatLanguages(t *testing.T) {
	key := os.Getenv("PLAY_LLM_API_KEY")
	if key == "" || testing.Short() {
		t.Skip("live: needs PLAY_LLM_API_KEY (and is skipped under -short)")
	}
	pool := testPool(t)
	store := &engine.Store{Pool: pool}
	client := llm.New(envOr("PLAY_LLM_BASE_URL", "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"), key)
	a := &Agent{Store: store, Model: &engine.Model{Client: client, Store: store},
		LLM: envOr("PLAY_LLM_MODEL", "qwen3.8-plus"), FastLLM: envOr("PLAY_LLM_FAST_MODEL", "qwen3.8-flash"),
		Tables: &fakeTables{}, Studio: &fakeStudio{}, Catalog: fakeCatalog{}}
	isLang := map[string]func(rune) bool{
		"en": func(r rune) bool { return r < 128 && unicode.IsLetter(r) },
		"zh": func(r rune) bool { return unicode.Is(unicode.Han, r) },
		"ko": func(r rune) bool { return unicode.Is(unicode.Hangul, r) },
		"ja": func(r rune) bool { return unicode.Is(unicode.Hiragana, r) || unicode.Is(unicode.Katakana, r) },
	}
	for _, c := range []struct{ lang, text string }{
		{"en", "Hi Aoi! Who's the toughest player at your table?"},
		{"zh", "你好葵！你们桌上谁最难对付？"},
		{"ko", "안녕 아오이! 너희 테이블에서 제일 무서운 플레이어는 누구야?"},
		{"ja", "こんにちは葵！テーブルで一番手強いのは誰？"},
	} {
		uid, conv := newPlayer(t, pool, nil)
		s := &textSink{}
		if _, err := a.Turn(context.Background(), User{ID: uid, Name: "Sam", Lang: "en"}, conv, c.text, "", s); err != nil {
			t.Fatal(err)
		}
		reply := s.b.String()
		t.Logf("%s → %s", c.lang, reply)
		n, total := 0, 0
		for _, r := range reply {
			if unicode.IsLetter(r) {
				total++
				if isLang[c.lang](r) {
					n++
				}
			}
		}
		// Mostly in the language: a name in Latin letters or a "よし!" is fine.
		if total == 0 || float64(n)/float64(total) < 0.5 {
			t.Errorf("%s: reply is not in %s (%d/%d letters): %q", c.lang, c.lang, n, total, reply)
		}
	}
}
