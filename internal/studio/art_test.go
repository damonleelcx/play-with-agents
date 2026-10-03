package studio

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/art"
	"github.com/damonleelcx/play-with-agents/internal/tools"
)

// fakeImages is a DashScope stand-in: ok decides whether tasks succeed.
func fakeImages(t *testing.T, ok bool) (*art.Client, *atomic.Int32) {
	t.Helper()
	var submits atomic.Int32
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			submits.Add(1)
			_, _ = w.Write([]byte(`{"output":{"task_id":"t1","task_status":"PENDING"}}`))
		case strings.HasPrefix(r.URL.Path, "/tasks/"):
			if !ok {
				_, _ = w.Write([]byte(`{"output":{"task_status":"FAILED","code":"InternalError","message":"no"}}`))
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"output": map[string]any{"task_status": "SUCCEEDED",
				"results": []map[string]string{{"url": srv.URL + "/img.png"}}}})
		case r.URL.Path == "/img.png":
			im := image.NewRGBA(image.Rect(0, 0, 1280, 720))
			for i := range im.Pix {
				im.Pix[i] = uint8(i)
			}
			var b bytes.Buffer
			_ = png.Encode(&b, im)
			_, _ = w.Write(b.Bytes())
		}
	}))
	t.Cleanup(srv.Close)
	c := art.NewClient("test-key", "")
	c.BaseURL, c.PollEvery, c.PollFor = srv.URL, time.Millisecond, 50*time.Millisecond
	return c, &submits
}

func withArtist(t *testing.T, a Artist) {
	ConfigureArt(a)
	t.Cleanup(func() { ConfigureArt(Artist{}) })
}

// illustrateEnv starts a build with saved rules and returns how to invoke
// the Artist's tool in it.
func illustrateEnv(t *testing.T, r *rig) (gameID, goalID string, rename func(string), illustrate func() map[string]any) {
	ctx := context.Background()
	goalID, gameID, err := r.svc.StartBuild(ctx, r.uid, r.conv, "a dragon gem race", "", "en")
	if err != nil {
		t.Fatal(err)
	}
	env := &tools.Env{Pool: r.pool, UserID: r.uid, GoalID: goalID, Lang: "en"}
	invoke := func(name string, args map[string]any) map[string]any {
		t.Helper()
		raw, _ := json.Marshal(args)
		res, err := tools.Invoke(ctx, env, name, raw, false)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		return res.Output
	}
	rename = func(name string) {
		invoke(ToolSaveRules, map[string]any{"name": name, "summary": "Race to the gems.", "rules_md": strings.Repeat("Dragons move and collect gems. ", 10),
			"min_seats": 2, "max_seats": 2, "hidden_info": false})
	}
	rename("Gem Rush")
	return gameID, goalID, rename, func() map[string]any { return invoke(ToolIllustrate, map[string]any{}) }
}

func TestIllustrateWithoutImagesIsProceduralAndKeptUntilRenamed(t *testing.T) {
	r := newRig(t, "pass")
	withArtist(t, Artist{})
	gameID, goalID, rename, illustrate := illustrateEnv(t, r)
	ctx := context.Background()

	out := illustrate()
	if out["stored"] != true || out["source"] != "procedural" {
		t.Fatalf("first cover: %v", out)
	}
	c, err := art.Info(ctx, r.pool, gameID)
	if err != nil || c == nil || c.Version != 1 || c.Name != "Gem Rush" || c.ContentType != art.ContentType {
		t.Fatalf("stored cover: %+v %v", c, err)
	}
	if g := r.goal(t, goalID); g.Usage.Images != 0 {
		t.Fatalf("no key, yet %d image calls counted", g.Usage.Images)
	}
	// A revision with the same name keeps the cover.
	if out = illustrate(); out["stored"] != false {
		t.Fatalf("same name regenerated: %v", out)
	}
	// A rename makes a new one.
	rename("Gem Rush Deluxe")
	if out = illustrate(); out["stored"] != true || num(out["version"]) != 2 {
		t.Fatalf("after rename: %v", out)
	}
	if c, _ = art.Info(ctx, r.pool, gameID); c.Name != "Gem Rush Deluxe" {
		t.Fatalf("cover made for %q", c.Name)
	}
}

func TestIllustrateFailureFallsBackWithinBudget(t *testing.T) {
	r := newRig(t, "pass")
	images, submits := fakeImages(t, false)
	withArtist(t, Artist{Images: images})
	gameID, goalID, _, illustrate := illustrateEnv(t, r)

	out := illustrate() // never an error: the build goes on
	if out["stored"] != true || out["source"] != "procedural" || num(out["images"]) != art.MaxAttempts {
		t.Fatalf("failed generation: %v", out)
	}
	if n := submits.Load(); n != art.MaxAttempts {
		t.Fatalf("%d image calls, want %d", n, art.MaxAttempts)
	}
	g := r.goal(t, goalID)
	if g.Usage.Images != art.MaxAttempts || g.Usage.CostUSD < float64(art.MaxAttempts)*art.CostPerImageUSD {
		t.Fatalf("usage %+v", g.Usage)
	}
	var events int
	_ = r.pool.QueryRow(context.Background(), `SELECT count(*) FROM events WHERE goal_id=$1 AND type='usage.image'`, goalID).Scan(&events)
	if events != 1 {
		t.Fatalf("%d usage.image events", events)
	}
	// Retried (a procedural cover with images on): the budget is spent, so
	// no further image calls.
	if out = illustrate(); out["source"] != "procedural" || submits.Load() != art.MaxAttempts {
		t.Fatalf("over budget: %v, %d calls", out, submits.Load())
	}
	if c, _ := art.Info(context.Background(), r.pool, gameID); c == nil || c.Source != "procedural" {
		t.Fatalf("cover %+v", c)
	}
}

func TestIllustrateGenerates(t *testing.T) {
	r := newRig(t, "pass")
	images, submits := fakeImages(t, true)
	withArtist(t, Artist{Images: images})
	gameID, goalID, _, illustrate := illustrateEnv(t, r)
	out := illustrate()
	if out["source"] != "generated" || num(out["images"]) != 1 || submits.Load() != 1 {
		t.Fatalf("generation: %v (%d calls)", out, submits.Load())
	}
	c, _ := art.Info(context.Background(), r.pool, gameID)
	if c == nil || c.Source != "generated" || c.Model != art.DefaultModel || !strings.Contains(c.Prompt, "Gem Rush") || !strings.Contains(c.Prompt, "no text") {
		t.Fatalf("cover %+v", c)
	}
	if g := r.goal(t, goalID); g.Usage.Images != 1 {
		t.Fatalf("images %d", g.Usage.Images)
	}
	// Same name: kept, no new call.
	if out = illustrate(); out["stored"] != false || submits.Load() != 1 {
		t.Fatalf("regenerated without a rename: %v", out)
	}
}
