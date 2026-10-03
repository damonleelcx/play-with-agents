// Package tts gives Aoi her voice: Fish Audio speech synthesis in one
// published voice — the same voice as the other heros-agent.space products —
// returned as MP3 for the browser, with a text cleaner and an in-memory cache
// in front so the same line is never paid for twice. See "Aoi's voice" in
// docs/00-architecture.md.
package tts

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// Fish renders speech through Fish Audio, in one named voice.
//
// The voice is chosen by `reference_id`, a published model id from fish.audio.
// It is not a parameter picked from a list but a specific published voice, so
// it is configuration (PLAY_TTS_VOICE_ID) and this code never defaults it:
// guessing would give Aoi a different voice from the rest of the estate.
//
// The `model` header selects the synthesis BACKBONE, separately from the
// voice. It travels as a header, not in the body: in the body it is accepted
// and ignored, which would silently bill a different backbone.
type Fish struct {
	Endpoint string
	APIKey   string
	VoiceID  string // Fish's reference_id: which voice
	Model    string // the backbone: s2.1-pro-free | s2.1-pro | s2-pro | s1
	Client   *http.Client
}

const (
	DefaultEndpoint = "https://api.fish.audio/v1/tts"
	// DefaultModel is the free backbone. See TrainsOnRequests for what it
	// costs instead of money.
	DefaultModel = "s2.1-pro-free"

	// maxVendorChars caps one request at this layer too. The HTTP handler caps
	// far lower; an uncapped synthesis path would be a vendor bill with a
	// network endpoint in front of it, so this side does not rely on callers.
	maxVendorChars = 3000
)

// ErrNotConfigured is returned by NewFish when there is no key or no voice:
// the deployment simply has no voice, which callers report as "disabled".
var ErrNotConfigured = errors.New("speech is not configured")

// paidBackbones are the Fish backbones whose terms do NOT permit using requests
// to improve the vendor's models.
//
// An allowlist rather than a denylist, on purpose: a backbone this table has
// not heard of is treated as one that trains. Over-warning costs caution
// nobody needed; under-warning costs something that cannot be taken back.
// `s1` is absent because its terms were not checked, so it warns.
var paidBackbones = map[string]bool{
	"s2.1-pro": true,
	"s2-pro":   true,
}

// TrainsOnRequests reports whether the backbone may be trained on the text
// sent to it. Speech is a second vendor and a second egress for what Aoi says;
// the process logs this at startup (tts_trains_on_input) so it is never a
// surprise. An empty model resolves the way NewFish resolves it.
func TrainsOnRequests(model string) bool { return !paidBackbones[resolveModel(model)] }

func resolveModel(model string) string {
	if strings.TrimSpace(model) == "" {
		return DefaultModel
	}
	return strings.TrimSpace(model)
}

// NewFish builds the client. Without a key or a voice id it returns
// ErrNotConfigured, so a deployment with no speech secret runs without a
// voice instead of failing.
func NewFish(endpoint, apiKey, voiceID, model string) (*Fish, error) {
	if strings.TrimSpace(apiKey) == "" || strings.TrimSpace(voiceID) == "" {
		return nil, ErrNotConfigured
	}
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	return &Fish{
		Endpoint: endpoint, APIKey: strings.TrimSpace(apiKey), VoiceID: strings.TrimSpace(voiceID), Model: resolveModel(model),
		// Synthesis runs faster than real time, but a long line legitimately
		// takes seconds; a tight timeout would read as "the vendor is broken".
		Client: &http.Client{Timeout: 45 * time.Second},
	}, nil
}

// fishRequest carries only the fields this product sets; everything else
// takes the vendor's defaults.
type fishRequest struct {
	Text        string `json:"text"`
	ReferenceID string `json:"reference_id,omitempty"`
	Format      string `json:"format"`
	MP3Bitrate  int    `json:"mp3_bitrate,omitempty"`
	// Latency trades quality for time-to-first-byte: "balanced", because
	// someone is waiting to hear a reply.
	Latency string `json:"latency"`
	// Normalize expands numbers and units into words ("120 chips"), which is
	// how a table talks.
	Normalize bool `json:"normalize"`
}

// SpeakMP3 synthesises text and returns MP3 bytes and their content type.
// MP3 rather than WAV: a browser's <audio> plays it everywhere, and it is a
// fraction of the size for the same seconds.
func (f *Fish) SpeakMP3(ctx context.Context, text string) ([]byte, string, error) {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil, "", errors.New("tts: nothing to speak")
	}
	if rs := []rune(text); len(rs) > maxVendorChars {
		text = string(rs[:maxVendorChars])
	}
	body, err := json.Marshal(fishRequest{Text: text, ReferenceID: f.VoiceID, Format: "mp3", MP3Bitrate: 128, Latency: "balanced", Normalize: true})
	if err != nil {
		return nil, "", fmt.Errorf("tts: encode request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, f.Endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, "", fmt.Errorf("tts: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+f.APIKey)
	req.Header.Set("Content-Type", "application/json")
	// The backbone travels as a HEADER; see the type comment.
	req.Header.Set("model", f.Model)

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("tts: fish.audio unreachable: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		// The vendor's own reason, passed through: the difference between "the
		// voice is broken" and "the key expired".
		detail, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
		return nil, "", fmt.Errorf("tts: fish.audio answered %d: %s", resp.StatusCode, strings.TrimSpace(string(detail)))
	}
	// 16 MiB is minutes of 128 kbps audio; anything larger is not an answer
	// to a 600-character request.
	audio, err := io.ReadAll(io.LimitReader(resp.Body, 16<<20))
	if err != nil {
		return nil, "", fmt.Errorf("tts: reading audio: %w", err)
	}
	if len(audio) == 0 {
		// Silence is what a broken voice sounds like; report it, never serve it.
		return nil, "", errors.New("tts: fish.audio answered 200 with no audio")
	}
	ct := resp.Header.Get("Content-Type")
	if ct == "" || !strings.HasPrefix(ct, "audio/") {
		ct = "audio/mpeg"
	}
	return audio, ct, nil
}
