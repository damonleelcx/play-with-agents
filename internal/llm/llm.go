// Package llm is a small client for OpenAI-compatible chat completions: plain
// calls, tool calls and streaming, with bounded retries and a fallback model.
package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"strings"
	"time"
)

type Message struct {
	Role       string     `json:"role"`
	Content    string     `json:"content"`
	ToolCalls  []ToolCall `json:"tool_calls,omitempty"`
	ToolCallID string     `json:"tool_call_id,omitempty"`
	Name       string     `json:"name,omitempty"`
}

type ToolCall struct {
	ID       string `json:"id"`
	Type     string `json:"type"`
	Function struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	} `json:"function"`
}

type ToolDef struct {
	Type     string `json:"type"`
	Function struct {
		Name        string          `json:"name"`
		Description string          `json:"description"`
		Parameters  json.RawMessage `json:"parameters"`
	} `json:"function"`
}

func NewToolDef(name, desc string, schema json.RawMessage) ToolDef {
	var t ToolDef
	t.Type = "function"
	t.Function.Name = name
	t.Function.Description = desc
	t.Function.Parameters = schema
	return t
}

type Request struct {
	Model       string
	Messages    []Message
	Tools       []ToolDef
	JSON        bool
	Temperature float64
	MaxTokens   int
}

type Usage struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
}

type Response struct {
	Message Message
	Usage   Usage
	Model   string
	Latency time.Duration
}

// Client talks to one endpoint. Fallback is tried when the primary model fails
// after its retries — a model outage degrades quality, it does not stop work.
type Client struct {
	BaseURL  string
	APIKey   string
	Fallback map[string]string
	HTTP     *http.Client
	Retries  int
}

func New(baseURL, key string) *Client {
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"),
		APIKey:  key,
		HTTP:    &http.Client{Timeout: 180 * time.Second},
		Retries: 3,
	}
}

func (c *Client) Configured() bool { return c != nil && c.APIKey != "" }

// ErrPermanent marks failures a retry cannot fix (bad request, auth).
var ErrPermanent = errors.New("permanent model error")

func (c *Client) body(r Request, stream bool) map[string]any {
	// OpenAI-compatible JSON mode (Qwen included) rejects a request whose
	// messages never mention JSON — with a 400, which a retry cannot fix.
	// Guarantee it here rather than trusting every prompt author to remember.
	if r.JSON {
		mentions := false
		for _, m := range r.Messages {
			if strings.Contains(strings.ToLower(m.Content), "json") {
				mentions = true
				break
			}
		}
		if !mentions {
			r.Messages = append(append([]Message(nil), r.Messages...), Message{Role: "system", Content: "Respond with a single JSON object."})
		}
	}
	msgs := make([]any, len(r.Messages))
	for i, m := range r.Messages {
		msgs[i] = m
		// An image message is encoded as "\x00image:<data url>\x00<text>" so
		// the plain Message type can carry it; it goes out as multi-part.
		if strings.HasPrefix(m.Content, "\x00image:") {
			parts := strings.SplitN(strings.TrimPrefix(m.Content, "\x00image:"), "\x00", 2)
			content := []any{map[string]any{"type": "image_url", "image_url": map[string]string{"url": parts[0]}}}
			if len(parts) == 2 {
				content = append(content, map[string]any{"type": "text", "text": parts[1]})
			}
			msgs[i] = map[string]any{"role": m.Role, "content": content}
		}
	}
	b := map[string]any{"model": r.Model, "messages": msgs, "stream": stream}
	if len(r.Tools) > 0 {
		b["tools"] = r.Tools
	}
	if r.JSON {
		b["response_format"] = map[string]string{"type": "json_object"}
	}
	if r.Temperature > 0 {
		b["temperature"] = r.Temperature
	}
	if r.MaxTokens > 0 {
		b["max_tokens"] = r.MaxTokens
	}
	// Qwen's hybrid models think by default on this host; thinking output is
	// neither shown nor needed, and it multiplies latency.
	if strings.Contains(c.BaseURL, "aliyuncs.com") {
		b["enable_thinking"] = false
	}
	if stream {
		b["stream_options"] = map[string]bool{"include_usage": true}
	}
	return b
}

func (c *Client) Chat(ctx context.Context, r Request) (*Response, error) {
	resp, err := c.chatWithRetry(ctx, r)
	if err != nil && c.Fallback != nil && c.Fallback[r.Model] != "" && ctx.Err() == nil {
		r.Model = c.Fallback[r.Model]
		return c.chatWithRetry(ctx, r)
	}
	return resp, err
}

func (c *Client) chatWithRetry(ctx context.Context, r Request) (*Response, error) {
	var last error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			if err := sleep(ctx, backoff(attempt)); err != nil {
				return nil, err
			}
		}
		resp, err := c.chatOnce(ctx, r)
		if err == nil {
			return resp, nil
		}
		last = err
		if errors.Is(err, ErrPermanent) || ctx.Err() != nil {
			break
		}
	}
	return nil, last
}

func (c *Client) post(ctx context.Context, body map[string]any) (*http.Response, error) {
	if !c.Configured() {
		return nil, fmt.Errorf("%w: no API key configured", ErrPermanent)
	}
	buf, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.BaseURL+"/chat/completions", bytes.NewReader(buf))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+c.APIKey)
	req.Header.Set("Content-Type", "application/json")
	res, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	if res.StatusCode >= 300 {
		msg, _ := io.ReadAll(io.LimitReader(res.Body, 2048))
		res.Body.Close()
		err := fmt.Errorf("model HTTP %d: %s", res.StatusCode, strings.TrimSpace(string(msg)))
		if res.StatusCode == 400 || res.StatusCode == 401 || res.StatusCode == 403 || res.StatusCode == 404 {
			return nil, fmt.Errorf("%w: %v", ErrPermanent, err)
		}
		return nil, err
	}
	return res, nil
}

func (c *Client) chatOnce(ctx context.Context, r Request) (*Response, error) {
	start := time.Now()
	res, err := c.post(ctx, c.body(r, false))
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var out struct {
		Model   string `json:"model"`
		Choices []struct {
			Message Message `json:"message"`
		} `json:"choices"`
		Usage Usage `json:"usage"`
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return nil, fmt.Errorf("decode model response: %w", err)
	}
	if len(out.Choices) == 0 {
		return nil, errors.New("model returned no choices")
	}
	return &Response{Message: out.Choices[0].Message, Usage: out.Usage, Model: out.Model, Latency: time.Since(start)}, nil
}

// Stream calls onDelta with each content fragment and returns the whole
// message. Tool calls are not streamed; streaming is for user-facing prose.
func (c *Client) Stream(ctx context.Context, r Request, onDelta func(string)) (*Response, error) {
	start := time.Now()
	var res *http.Response
	var err error
	for attempt := 0; attempt <= c.Retries; attempt++ {
		if attempt > 0 {
			if err = sleep(ctx, backoff(attempt)); err != nil {
				return nil, err
			}
		}
		res, err = c.post(ctx, c.body(r, true))
		if err == nil || errors.Is(err, ErrPermanent) {
			break
		}
	}
	if err != nil {
		return nil, err
	}
	defer res.Body.Close()
	var sb strings.Builder
	var usage Usage
	sc := bufio.NewScanner(res.Body)
	sc.Buffer(make([]byte, 0, 64*1024), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if data == "[DONE]" {
			break
		}
		var chunk struct {
			Choices []struct {
				Delta struct {
					Content string `json:"content"`
				} `json:"delta"`
			} `json:"choices"`
			Usage *Usage `json:"usage"`
		}
		if json.Unmarshal([]byte(data), &chunk) != nil {
			continue
		}
		if chunk.Usage != nil {
			usage = *chunk.Usage
		}
		for _, ch := range chunk.Choices {
			if ch.Delta.Content != "" {
				sb.WriteString(ch.Delta.Content)
				onDelta(ch.Delta.Content)
			}
		}
	}
	if err := sc.Err(); err != nil && sb.Len() == 0 {
		return nil, err
	}
	return &Response{Message: Message{Role: "assistant", Content: sb.String()}, Usage: usage, Model: r.Model, Latency: time.Since(start)}, nil
}

// backoff is exponential with full jitter: 1s, 2s, 4s… capped at 20s.
func backoff(attempt int) time.Duration {
	d := time.Second << (attempt - 1)
	if d > 20*time.Second {
		d = 20 * time.Second
	}
	return time.Duration(rand.Int63n(int64(d))) + d/2
}

func sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// ExtractJSON finds the first JSON object in s. Models occasionally wrap JSON
// in a code fence even when asked not to.
func ExtractJSON(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "```json")
	s = strings.TrimPrefix(s, "```")
	s = strings.TrimSuffix(s, "```")
	i := strings.IndexByte(s, '{')
	j := strings.LastIndexByte(s, '}')
	if i >= 0 && j > i {
		return s[i : j+1]
	}
	return s
}
