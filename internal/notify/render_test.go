package notify

import (
	"strings"
	"testing"
)

// Every kind renders natively in every language, signed by Aoi in that
// language, with the link and the mission title and nothing in English
// leaking into a Korean or Japanese subject.
func TestRenderEveryLanguage(t *testing.T) {
	p := map[string]any{"goal_id": "g1", "approval_id": "a1", "tool": "publish_game", "goal_title": "Dragon Chess"}
	sig := map[string]string{"en": "Aoi", "zh": "葵", "ko": "아오이", "ja": "葵"}
	native := map[string]string{"ko": "게임 공개하기", "ja": "ゲームを公開する", "zh": "发布你的游戏", "en": "publish your game"}
	for _, kind := range []string{KindBuildReady, KindApprovalRequested} {
		for _, lang := range []string{"en", "zh", "ko", "ja"} {
			m, err := Render(kind, lang, p, "https://play.example")
			if err != nil {
				t.Fatal(err)
			}
			if !strings.Contains(m.Text, "Dragon Chess") || !strings.Contains(m.Text, "https://play.example/app/") ||
				!strings.Contains(m.Text, sig[lang]) || !strings.Contains(m.HTML, `lang="`+lang+`"`) {
				t.Fatalf("%s/%s: %+v", kind, lang, m)
			}
			if kind == KindApprovalRequested && !strings.Contains(m.Text, native[lang]) {
				t.Fatalf("%s/%s: tool name not localised: %s", kind, lang, m.Text)
			}
			if lang == "ko" || lang == "ja" {
				if strings.Contains(m.Subject, "Your") || strings.Contains(m.Subject, "waiting") {
					t.Fatalf("%s/%s subject is English: %q", kind, lang, m.Subject)
				}
			}
		}
	}
	if toolName("something_new", "ja") != "ミッションの一ステップ" || toolName("publish_game", "fr") != "publish your game" {
		t.Fatal("toolName fallbacks")
	}
}
