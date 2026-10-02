// Package config reads every knob the process has from the environment. There
// is no config file: a deployment should be readable from its manifest alone.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Addr         string
	DatabaseURL  string
	PublicOrigin string // absolute origin every emailed link is built against
	SessionTTL   time.Duration
	CookieSecure bool

	LLMBaseURL   string
	LLMAPIKey    string
	LLMModel     string // reasoning, drafting, planning
	LLMFastModel string // routing, judging, summarising

	SMTPHost    string
	SMTPPort    int
	SMTPUser    string
	SMTPPass    string
	SMTPFrom    string
	SMTPReplyTo string

	// AdminEmails are operators (moderation of community games), comma separated.
	// An address counts only once its owner has verified it.
	AdminEmails []string

	WorkerCount   int
	LeaseDuration time.Duration

	// Table workers play agents' turns, run turn clocks and write table talk
	// (internal/rooms). Short jobs, so a short lease.
	TableWorkers int
	TableLease   time.Duration

	// Spending ceilings in tokens. Per-goal limits live on the goal; these
	// bound a whole account and the whole deployment per UTC day.
	AccountDailyTokens    int
	DeploymentDailyTokens int

	// Aoi's voice (Fish Audio). No key or no voice id means no voice: the
	// speech endpoint reports disabled and the client hides its controls.
	TTSAPIKey  string
	TTSVoiceID string // a published fish.audio model id; never defaulted
	TTSModel   string // the synthesis backbone (tts.DefaultModel when empty)
}

func Load() (Config, error) {
	c := Config{
		Addr:                  env("PLAY_ADDR", ":8080"),
		DatabaseURL:           os.Getenv("PLAY_DATABASE_URL"),
		PublicOrigin:          strings.TrimRight(os.Getenv("PLAY_PUBLIC_ORIGIN"), "/"),
		SessionTTL:            envDur("PLAY_SESSION_TTL", 30*24*time.Hour),
		CookieSecure:          envBool("PLAY_COOKIE_SECURE", true),
		LLMBaseURL:            env("PLAY_LLM_BASE_URL", "https://token-plan.cn-beijing.maas.aliyuncs.com/compatible-mode/v1"),
		LLMAPIKey:             os.Getenv("PLAY_LLM_API_KEY"),
		LLMModel:              env("PLAY_LLM_MODEL", "qwen3.8-max"),
		LLMFastModel:          env("PLAY_LLM_FAST_MODEL", "qwen3.8-flash"),
		SMTPHost:              os.Getenv("PLAY_SMTP_HOST"),
		SMTPPort:              envInt("PLAY_SMTP_PORT", 587),
		SMTPUser:              os.Getenv("PLAY_SMTP_USERNAME"),
		SMTPPass:              os.Getenv("PLAY_SMTP_PASSWORD"),
		SMTPFrom:              os.Getenv("PLAY_SMTP_FROM"),
		SMTPReplyTo:           os.Getenv("PLAY_SMTP_REPLY_TO"),
		WorkerCount:           envInt("PLAY_WORKERS", 2),
		LeaseDuration:         envDur("PLAY_LEASE", 90*time.Second),
		TableWorkers:          envInt("PLAY_TABLE_WORKERS", 4),
		TableLease:            envDur("PLAY_TABLE_LEASE", 15*time.Second),
		AccountDailyTokens:    envInt("PLAY_ACCOUNT_DAILY_TOKENS", 1_500_000),
		DeploymentDailyTokens: envInt("PLAY_DEPLOYMENT_DAILY_TOKENS", 40_000_000),
		TTSAPIKey:             os.Getenv("PLAY_TTS_API_KEY"),
		TTSVoiceID:            os.Getenv("PLAY_TTS_VOICE_ID"),
		TTSModel:              env("PLAY_TTS_MODEL", "s2.1-pro-free"),
	}
	for _, e := range strings.Split(os.Getenv("PLAY_ADMIN_EMAILS"), ",") {
		if e = strings.ToLower(strings.TrimSpace(e)); e != "" {
			c.AdminEmails = append(c.AdminEmails, e)
		}
	}
	// The relay only lets a login send as its own address (spoof protection),
	// so the login IS the From address unless one is set explicitly.
	if c.SMTPFrom == "" {
		c.SMTPFrom = c.SMTPUser
	}
	if c.DatabaseURL == "" {
		return c, fmt.Errorf("PLAY_DATABASE_URL is required")
	}
	return c, nil
}

// MailEnabled is false unless every piece a real message needs is present. A
// half-configured relay would fail per message; this fails once, at startup.
func (c Config) MailEnabled() bool {
	return c.SMTPHost != "" && c.SMTPUser != "" && c.SMTPPass != "" && c.SMTPFrom != "" && c.PublicOrigin != ""
}

func env(k, d string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return d
}

func envInt(k string, d int) int {
	if v, err := strconv.Atoi(os.Getenv(k)); err == nil {
		return v
	}
	return d
}

func envBool(k string, d bool) bool {
	if v, err := strconv.ParseBool(os.Getenv(k)); err == nil {
		return v
	}
	return d
}

func envDur(k string, d time.Duration) time.Duration {
	if v, err := time.ParseDuration(os.Getenv(k)); err == nil {
		return v
	}
	return d
}
