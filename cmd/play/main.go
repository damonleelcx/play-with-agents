// Command play runs Play with Agents: the web tier, the mission and table
// workers, and the scheduler. Every knob is a PLAY_* environment variable
// (internal/config).
//
//	play serve      web + workers + scheduler in one process (development)
//	play web        web tier only
//	play worker     workers + scheduler only
//	play migrate    apply migrations and exit
//	play mailcheck  prove the mail relay accepts our login, send nothing
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/damonleelcx/play-with-agents/internal/agent"
	"github.com/damonleelcx/play-with-agents/internal/auth"
	"github.com/damonleelcx/play-with-agents/internal/config"
	"github.com/damonleelcx/play-with-agents/internal/db"
	"github.com/damonleelcx/play-with-agents/internal/engine"
	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/games/ai"
	"github.com/damonleelcx/play-with-agents/internal/games/holdem"
	"github.com/damonleelcx/play-with-agents/internal/games/script"
	"github.com/damonleelcx/play-with-agents/internal/httpapi"
	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/mail"
	"github.com/damonleelcx/play-with-agents/internal/rooms"
	"github.com/damonleelcx/play-with-agents/internal/rooms/talk"
	"github.com/damonleelcx/play-with-agents/internal/studio"
	"github.com/damonleelcx/play-with-agents/internal/tts"
	"github.com/damonleelcx/play-with-agents/internal/web"
)

func main() {
	slog.SetDefault(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo})))
	mode := "serve"
	if len(os.Args) > 1 {
		mode = os.Args[1]
	}
	if err := run(mode); err != nil {
		slog.Error("fatal", "err", err)
		os.Exit(1)
	}
}

func run(mode string) error {
	cfg, err := config.Load()
	if err != nil {
		return err
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	pool, err := db.Open(ctx, cfg.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	if err := db.Migrate(ctx, pool); err != nil {
		return err
	}
	if mode == "migrate" {
		slog.Info("migrations applied")
		return nil
	}

	if mode == "mailcheck" {
		if !cfg.MailEnabled() {
			return fmt.Errorf("mail is not configured")
		}
		m := &mail.SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Pass: cfg.SMTPPass, From: cfg.SMTPFrom}
		if err := m.Check(ctx); err != nil {
			return err
		}
		slog.Info("mailcheck ok: connected, STARTTLS certificate verified, authenticated; nothing sent", "host", cfg.SMTPHost, "from", cfg.SMTPFrom)
		return nil
	}
	var mailer mail.Mailer = mail.Log{}
	if cfg.MailEnabled() {
		mailer = &mail.SMTP{Host: cfg.SMTPHost, Port: cfg.SMTPPort, User: cfg.SMTPUser, Pass: cfg.SMTPPass, From: cfg.SMTPFrom, ReplyTo: cfg.SMTPReplyTo}
	} else {
		slog.Warn("MAIL_DISABLED: verification and reset links are logged, not sent", "missing_origin", cfg.PublicOrigin == "", "missing_smtp", cfg.SMTPHost == "" || cfg.SMTPUser == "")
	}
	if cfg.LLMAPIKey == "" {
		slog.Warn("MODEL_DISABLED: PLAY_LLM_API_KEY is empty; chat with Aoi and studio builds will fail until it is set")
	}
	// Aoi's voice. Optional: without a key (or a voice id) the speech
	// endpoint reports disabled. Whether the backbone may train on what she
	// says is logged every start, because speech is a second vendor seeing
	// the same text the model does.
	var speech *tts.Service
	if fish, err := tts.NewFish(tts.DefaultEndpoint, cfg.TTSAPIKey, cfg.TTSVoiceID, cfg.TTSModel); err == nil {
		speech = tts.NewService(fish, 200)
		slog.Info("voice enabled", "tts_model", fish.Model, "tts_voice", fish.VoiceID, "tts_trains_on_input", tts.TrainsOnRequests(fish.Model))
	} else {
		slog.Warn("VOICE_DISABLED: PLAY_TTS_API_KEY or PLAY_TTS_VOICE_ID is empty; Aoi has no voice", "missing_key", cfg.TTSAPIKey == "", "missing_voice", cfg.TTSVoiceID == "")
	}
	client := llm.New(cfg.LLMBaseURL, cfg.LLMAPIKey)
	// A failing primary falls back to the fast model rather than stopping.
	client.Fallback = map[string]string{cfg.LLMModel: cfg.LLMFastModel}
	store := &engine.Store{Pool: pool}
	model := &engine.Model{Client: client, Store: store, AccountDailyTokens: cfg.AccountDailyTokens, DeploymentDailyTokens: cfg.DeploymentDailyTokens}
	hub := httpapi.NewHub()
	wake := make(chan struct{}, 16)

	var wg sync.WaitGroup
	start := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }

	start(func() { engine.Listen(ctx, pool, wake, hub.Notify) })

	// Tables (internal/rooms). Its one LISTEN play_table connection fans
	// changes out to SSE streams (serve/web) and wakes table workers
	// (serve/worker), so it runs in every mode.
	tables := rooms.New(pool)
	tables.Lease = cfg.TableLease
	tables.Chatter = &talk.ModelChatter{Model: model, LLM: cfg.LLMFastModel}
	tables.Loader = rooms.SourceLoader(pool, func(id, src string) (games.Game, error) {
		g, err := script.Load(id, src, script.Options{})
		if err != nil {
			return nil, err
		}
		return g, nil
	})
	holdemBrain, anyBrain := holdem.Brain{}, ai.New(ai.Options{})
	tables.BrainFor = func(g games.Game) games.Brain {
		if _, ok := g.(*holdem.Game); ok {
			return holdemBrain
		}
		return anyBrain
	}
	start(func() { tables.Listen(ctx) })

	// The game studio (internal/studio): its tools, roles, playbook and plan
	// hook are registered by the import; the service starts builds for Aoi
	// and the API. The bundled example games go on the community shelf.
	studioSvc := &studio.Service{Pool: pool, Store: store}
	if err := studio.SeedCommunity(ctx, pool); err != nil {
		slog.Error("seed community games", "err", err)
	}

	if mode == "serve" || mode == "worker" {
		planner := &engine.Planner{Store: store, Model: model, LLM: cfg.LLMModel}
		for i := 0; i < cfg.WorkerCount; i++ {
			w := &engine.Worker{ID: engine.NewWorkerID(), Store: store, Model: model, Planner: planner, LLM: cfg.LLMModel,
				Mailer: mailer, Lease: cfg.LeaseDuration, Wake: wake}
			start(func() { w.Run(ctx) })
		}
		sched := &engine.Scheduler{Store: store, Model: model, FastLLM: cfg.LLMFastModel, Mailer: mailer, PublicOrigin: cfg.PublicOrigin}
		start(func() { sched.Run(ctx) })
		start(func() { tables.RunWorkers(ctx, cfg.TableWorkers) })
	}

	var srv *http.Server
	var serveErr error
	if mode == "serve" || mode == "web" {
		authSvc := &auth.Service{Pool: pool, Mailer: mailer, PublicOrigin: originOr(cfg.PublicOrigin, cfg.Addr), SessionTTL: cfg.SessionTTL, AdminEmails: cfg.AdminEmails,
			BeforeDelete: tables.ReleaseUser}
		// Aoi's capabilities. Each is optional and nil-safe: until a service
		// is wired here she tells the player it is not open yet. The rooms
		// service (tables, catalog) and the studio plug in as
		// agent.Tables, agent.Catalog and agent.Studio.
		ag := &agent.Agent{Store: store, Model: model, LLM: cfg.LLMModel, FastLLM: cfg.LLMFastModel, Mailer: mailer,
			Tables: aoiTables{tables}, Studio: studioSvc, Catalog: aoiCatalog{tables}}
		api := &httpapi.Server{Pool: pool, Auth: authSvc, Store: store, Agent: ag, Static: web.FS(), CookieSecure: cfg.CookieSecure,
			MailEnabled: mailer.Enabled(), Hub: hub, Rooms: tables, Speech: speech,
			SpeechDailyChars: cfg.TTSDailyChars}
		// ReadTimeout bounds a slow client's request (headers and body);
		// net/http lifts it once the body is read, so it never cuts a long
		// response (httpapi TestStreamOutlivesReadTimeout guards that).
		// IdleTimeout closes idle keep-alive connections. There is no
		// WriteTimeout: the SSE streams and Aoi's streamed replies are long
		// responses by design.
		srv = &http.Server{Addr: cfg.Addr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second,
			ReadTimeout: 30 * time.Second, IdleTimeout: 120 * time.Second}
		start(func() {
			slog.Info("listening", "addr", cfg.Addr, "mode", mode)
			if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				// e.g. the port is taken: exit non-zero, don't look like a clean stop.
				serveErr = fmt.Errorf("http server: %w", err)
				stop()
			}
		})
	}
	if mode != "serve" && mode != "web" && mode != "worker" {
		return fmt.Errorf("unknown mode %q (serve | web | worker | migrate)", mode)
	}

	<-ctx.Done()
	slog.Info("shutting down: finishing in-flight steps")
	if srv != nil {
		sctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		_ = srv.Shutdown(sctx)
		cancel()
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(25 * time.Second):
		slog.Warn("shutdown timed out; leases will expire and tasks resume elsewhere from their checkpoints")
	}
	return serveErr
}

func originOr(origin, addr string) string {
	if origin != "" {
		return origin
	}
	return "http://localhost" + addr
}
