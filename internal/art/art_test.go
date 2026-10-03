package art

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/jpeg"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func decode(t *testing.T, b []byte) image.Image {
	t.Helper()
	img, format, err := image.Decode(bytes.NewReader(b))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if format != "jpeg" {
		t.Fatalf("format %s, want jpeg", format)
	}
	return img
}

func TestProceduralDeterministicAndBounded(t *testing.T) {
	a1, err := Procedural("gem-rush-ab12c", "Gem Rush")
	if err != nil {
		t.Fatal(err)
	}
	a2, _ := Procedural("gem-rush-ab12c", "Gem Rush")
	if !bytes.Equal(a1, a2) {
		t.Fatal("same id and name gave different bytes")
	}
	b, _ := Procedural("lantern-tower-x9", "Gem Rush")
	if bytes.Equal(a1, b) {
		t.Fatal("different ids gave the same cover")
	}
	for _, c := range []struct{ id, name string }{
		{"gem-rush-ab12c", "Gem Rush"},
		{"koi-pond-7", "锦鲤池塘"}, // no glyphs in the embedded font: drawn without text
		{"x", ""},
		{"long-name-q", strings.Repeat("Supercalifragilistic ", 8)},
	} {
		raw, err := Procedural(c.id, c.name)
		if err != nil {
			t.Fatalf("%s: %v", c.id, err)
		}
		if len(raw) == 0 || len(raw) > MaxBytes {
			t.Fatalf("%s: %d bytes, want 1..%d", c.id, len(raw), MaxBytes)
		}
		if b := decode(t, raw).Bounds(); b.Dx() != Width || b.Dy() != Height {
			t.Fatalf("%s: %dx%d, want %dx%d", c.id, b.Dx(), b.Dy(), Width, Height)
		}
	}
}

func TestNormalizeCropsScalesAndShrinks(t *testing.T) {
	// A noisy 4:3 PNG: incompressible enough to exercise the size loop.
	src := image.NewRGBA(image.Rect(0, 0, 1600, 1200))
	r := &rng{s: 7}
	for i := range src.Pix {
		src.Pix[i] = uint8(r.next())
	}
	var buf bytes.Buffer
	if err := png.Encode(&buf, src); err != nil {
		t.Fatal(err)
	}
	out, err := Normalize(buf.Bytes())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) > MaxBytes {
		t.Fatalf("%d bytes > %d", len(out), MaxBytes)
	}
	if b := decode(t, out).Bounds(); b.Dx() != Width || b.Dy() != Height {
		t.Fatalf("%dx%d", b.Dx(), b.Dy())
	}
	if _, err := Normalize([]byte("not an image")); err == nil {
		t.Fatal("garbage decoded")
	}
}

// fakeDashScope serves the task API. status decides each poll's answer.
type fakeDashScope struct {
	srv      *httptest.Server
	submits  atomic.Int32
	polls    atomic.Int32
	models   []string
	status   func(model string, poll int) string
	img      []byte
	authSeen atomic.Bool
}

func newFake(t *testing.T, status func(model string, poll int) string) *fakeDashScope {
	f := &fakeDashScope{status: status}
	var b bytes.Buffer
	im := image.NewRGBA(image.Rect(0, 0, Width, Height))
	for i := range im.Pix {
		im.Pix[i] = 90
	}
	_ = jpeg.Encode(&b, im, nil)
	f.img = b.Bytes()
	var model string
	f.srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/services/aigc/text2image/image-synthesis"):
			if r.Header.Get("X-DashScope-Async") != "enable" {
				http.Error(w, `{"code":"bad","message":"async header"}`, 400)
				return
			}
			if r.Header.Get("Authorization") == "Bearer test-key" {
				f.authSeen.Store(true)
			}
			var body struct {
				Model string `json:"model"`
				Input struct {
					Prompt string `json:"prompt"`
				} `json:"input"`
				Parameters struct {
					Size string `json:"size"`
					N    int    `json:"n"`
				} `json:"parameters"`
			}
			_ = json.NewDecoder(r.Body).Decode(&body)
			if body.Parameters.Size != "1280*720" || body.Parameters.N != 1 || body.Input.Prompt == "" {
				http.Error(w, `{"code":"bad","message":"params"}`, 400)
				return
			}
			model = body.Model
			f.models = append(f.models, body.Model)
			f.submits.Add(1)
			_, _ = w.Write([]byte(`{"output":{"task_id":"t-` + body.Model + `","task_status":"PENDING"}}`))
		case r.Method == http.MethodGet && strings.HasPrefix(r.URL.Path, "/tasks/"):
			n := int(f.polls.Add(1))
			st := f.status(model, n)
			out := map[string]any{"task_status": st}
			if st == "SUCCEEDED" {
				out["results"] = []map[string]string{{"url": f.srv.URL + "/img.jpg"}}
			}
			if st == "FAILED" {
				out["code"], out["message"] = "InternalError", "boom"
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"output": out})
		case r.URL.Path == "/img.jpg":
			_, _ = w.Write(f.img)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeDashScope) client() *Client {
	c := NewClient("test-key", "")
	c.BaseURL = f.srv.URL
	c.PollEvery = 5 * time.Millisecond
	c.PollFor = 60 * time.Millisecond
	return c
}

func TestMakeGeneratesAfterPolling(t *testing.T) {
	f := newFake(t, func(_ string, poll int) string {
		if poll < 3 {
			return "RUNNING"
		}
		return "SUCCEEDED"
	})
	res, err := Make(context.Background(), f.client(), "g", "Gem Rush", Prompt("a gem mine"), 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "generated" || res.Model != DefaultModel || res.Attempts != 1 {
		t.Fatalf("got source=%s model=%s attempts=%d notes=%v", res.Source, res.Model, res.Attempts, res.Notes)
	}
	if !f.authSeen.Load() {
		t.Fatal("the key was not sent as a bearer token")
	}
	if b := decode(t, res.Bytes).Bounds(); b.Dx() != Width || b.Dy() != Height {
		t.Fatalf("%dx%d", b.Dx(), b.Dy())
	}
}

func TestPollIsBounded(t *testing.T) {
	f := newFake(t, func(string, int) string { return "RUNNING" }) // never finishes
	c := f.client()
	start := time.Now()
	_, err := c.Generate(context.Background(), DefaultModel, "x", "")
	if err == nil || !strings.Contains(err.Error(), "did not finish") {
		t.Fatalf("err = %v", err)
	}
	if time.Since(start) > 2*time.Second {
		t.Fatalf("poll ran %s, bound is %s", time.Since(start), c.PollFor)
	}
	if n := f.polls.Load(); n > int32(c.PollFor/c.PollEvery)+1 {
		t.Fatalf("%d polls, more than the bound", n)
	}
}

func TestMakeFallsBackToSecondModelThenProcedural(t *testing.T) {
	// The primary model fails; the fallback succeeds.
	f := newFake(t, func(model string, _ int) string {
		if model == DefaultModel {
			return "FAILED"
		}
		return "SUCCEEDED"
	})
	res, err := Make(context.Background(), f.client(), "g", "Gem Rush", "p", 2)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "generated" || res.Model != DefaultFallbackModel || res.Attempts != 2 {
		t.Fatalf("source=%s model=%s attempts=%d", res.Source, res.Model, res.Attempts)
	}

	// Everything fails: two attempts, never more, then the procedural cover.
	g := newFake(t, func(string, int) string { return "FAILED" })
	res, err = Make(context.Background(), g.client(), "g", "Gem Rush", "p", 5)
	if err != nil {
		t.Fatal(err)
	}
	if res.Source != "procedural" || res.Attempts != MaxAttempts || g.submits.Load() != MaxAttempts || len(res.Notes) != 2 {
		t.Fatalf("source=%s attempts=%d submits=%d notes=%v", res.Source, res.Attempts, g.submits.Load(), res.Notes)
	}
	want, _ := Procedural("g", "Gem Rush")
	if !bytes.Equal(res.Bytes, want) {
		t.Fatal("fallback is not the procedural cover")
	}

	// One attempt left in the budget: one call only.
	h := newFake(t, func(string, int) string { return "FAILED" })
	res, _ = Make(context.Background(), h.client(), "g", "Gem Rush", "p", 1)
	if res.Attempts != 1 || h.submits.Load() != 1 {
		t.Fatalf("attempts=%d submits=%d with 1 allowed", res.Attempts, h.submits.Load())
	}
}

func TestMakeWithoutKeyOrBudgetIsProcedural(t *testing.T) {
	res, err := Make(context.Background(), NewClient("", ""), "g", "Gem Rush", "p", 2)
	if err != nil || res.Source != "procedural" || res.Attempts != 0 {
		t.Fatalf("no key: %+v %v", res, err)
	}
	var nilClient *Client
	if res, err = Make(context.Background(), nilClient, "g", "Gem Rush", "p", 2); err != nil || res.Source != "procedural" {
		t.Fatalf("nil client: %v", err)
	}
	f := newFake(t, func(string, int) string { return "SUCCEEDED" })
	res, _ = Make(context.Background(), f.client(), "g", "Gem Rush", "p", 0)
	if res.Source != "procedural" || f.submits.Load() != 0 {
		t.Fatalf("no budget: source=%s submits=%d", res.Source, f.submits.Load())
	}
}

func TestSubmitErrorDoesNotLeakKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(401)
		_, _ = w.Write([]byte(`{"code":"InvalidApiKey","message":"Invalid API-key provided."}`))
	}))
	defer srv.Close()
	c := NewClient("sk-secret-value", "")
	c.BaseURL = srv.URL
	_, err := c.Generate(context.Background(), DefaultModel, "x", "")
	if err == nil || strings.Contains(err.Error(), "sk-secret-value") {
		t.Fatalf("err = %v", err)
	}
}
