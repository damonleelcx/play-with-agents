package auth

import (
	"strings"
	"testing"

	mailer "github.com/damonleelcx/play-with-agents/internal/mail"
)

// Verify and reset emails exist natively in every language, signed by Aoi.
func TestMailTemplatesEveryLanguage(t *testing.T) {
	want := map[string][]string{
		"en": {"Hi Sam", "Aoi"},
		"zh": {"Sam你好", "葵"},
		"ko": {"Sam 님, 안녕하세요!", "아오이"},
		"ja": {"Sam さん、こんにちは！", "葵"},
	}
	for lang, parts := range want {
		for _, m := range []mailer.Message{verifyMail(lang, "Sam", "https://x/verify?t=1"), resetMail(lang, "Sam", "https://x/reset?t=1")} {
			for _, p := range parts {
				if !strings.Contains(m.Text, p) {
					t.Fatalf("%s mail lacks %q:\n%s", lang, p, m.Text)
				}
			}
			if !strings.Contains(m.Text, "https://x/") || !strings.Contains(m.HTML, `lang="`+lang+`"`) || !strings.HasSuffix(m.Subject, "· Play with Agents") {
				t.Fatalf("%s mail: %+v", lang, m)
			}
		}
	}
	// No name: still a natural greeting, no dangling particle.
	if m := verifyMail("ko", "", "l"); !strings.HasPrefix(m.Text, "안녕하세요!") {
		t.Fatalf("ko without a name: %q", m.Text)
	}
	if m := resetMail("ja", "", "l"); !strings.HasPrefix(m.Text, "こんにちは！") {
		t.Fatalf("ja without a name: %q", m.Text)
	}
}
