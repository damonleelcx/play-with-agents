// Command act runs ACT: the web tier, the workers and the scheduler.
//
//	act serve    web + workers + scheduler in one process (development)
//	act web      web tier only
//	act worker   workers + scheduler only
//	act migrate    apply migrations and exit
//	act mailcheck  prove the mail relay accepts our login, send nothing
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
	"github.com/damonleelcx/play-with-agents/internal/httpapi"
	"github.com/damonleelcx/play-with-agents/internal/llm"
	"github.com/damonleelcx/play-with-agents/internal/mail"
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
		slog.Warn("MODEL_DISABLED: ACT_LLM_API_KEY is empty; chat and background work will fail until it is set")
	}
	client := llm.New(cfg.LLMBaseURL, cfg.LLMAPIKey)
	// A failing primary falls back to the fast model rather than stopping.
	client.Fallback = map[string]string{cfg.LLMModel: cfg.LLMFastModel}
	store := &engine.Store{Pool: pool}
	model := &engine.Model{Client: client, Store: store, AccountDailyTokens: cfg.AccountDailyTokens, DeploymentDailyTokens: cfg.DeploymentDailyTokens}
	onCall := os.Getenv("ACT_ONCALL_EMAIL")
	hub := httpapi.NewHub()
	wake := make(chan struct{}, 16)

	var wg sync.WaitGroup
	start := func(f func()) { wg.Add(1); go func() { defer wg.Done(); f() }() }

	start(func() { engine.Listen(ctx, pool, wake, hub.Notify) })

	if mode == "serve" || mode == "worker" {
		planner := &engine.Planner{Store: store, Model: model, LLM: cfg.LLMModel}
		httpc := &http.Client{Timeout: 60 * time.Second}
		for i := 0; i < cfg.WorkerCount; i++ {
			w := &engine.Worker{ID: engine.NewWorkerID(), Store: store, Model: model, Planner: planner, LLM: cfg.LLMModel,
				Mailer: mailer, HTTP: httpc, Lease: cfg.LeaseDuration, CourtListener: cfg.CourtListenerToken, OnCallEmail: onCall, Wake: wake}
			start(func() { w.Run(ctx) })
		}
		sched := &engine.Scheduler{Store: store, Model: model, FastLLM: cfg.LLMFastModel, Mailer: mailer, PublicOrigin: cfg.PublicOrigin}
		start(func() { sched.Run(ctx) })
	}

	var srv *http.Server
	var serveErr error
	if mode == "serve" || mode == "web" {
		authSvc := &auth.Service{Pool: pool, Mailer: mailer, PublicOrigin: originOr(cfg.PublicOrigin, cfg.Addr), SessionTTL: cfg.SessionTTL, AdminEmails: cfg.AdminEmails}
		ag := &agent.Agent{Store: store, Model: model, LLM: cfg.LLMModel, FastLLM: cfg.LLMFastModel, Mailer: mailer, OnCallEmail: onCall}
		api := &httpapi.Server{Pool: pool, Auth: authSvc, Store: store, Agent: ag, Static: web.FS(), CookieSecure: cfg.CookieSecure,
			MailEnabled: mailer.Enabled(), Hub: hub}
		srv = &http.Server{Addr: cfg.Addr, Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second}
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
