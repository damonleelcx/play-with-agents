package httpapi

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

// Aoi's voice plays from a blob: URL (web/src/lib/voice.ts). Without
// media-src the browser falls back to default-src 'self' and refuses to play
// it, silently, so the policy is pinned here.
func TestCSPAllowsBlobMedia(t *testing.T) {
	s := &Server{Static: fstest.MapFS{"index.html": {Data: []byte("<html>")}}, Hub: NewHub()}
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/", nil))
	csp := rec.Header().Get("Content-Security-Policy")
	if !strings.Contains(csp, "media-src 'self' blob:") {
		t.Fatalf("CSP must allow blob: media for Aoi's voice, got %q", csp)
	}
	if !strings.Contains(csp, "default-src 'self'") {
		t.Fatalf("default-src must stay 'self', got %q", csp)
	}
}
