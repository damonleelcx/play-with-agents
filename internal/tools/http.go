package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

const userAgent = "ACT-Vera/1.0 (+https://act.heros-agent.space)"

// getJSON fetches and decodes, classifying 429/5xx/network failures as
// transient so the contract's retry policy applies to them and only them.
func getJSON(ctx context.Context, env *Env, url string, headers map[string]string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", userAgent)
	req.Header.Set("Accept", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := client(env).Do(req)
	if err != nil {
		return &Transient{err}
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return errNotFound
	}
	if res.StatusCode == 429 || res.StatusCode >= 500 {
		return &Transient{fmt.Errorf("%s: HTTP %d", url, res.StatusCode)}
	}
	if res.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(res.Body, 512))
		return fmt.Errorf("%s: HTTP %d %s", url, res.StatusCode, b)
	}
	return json.NewDecoder(io.LimitReader(res.Body, 8<<20)).Decode(out)
}

func getText(ctx context.Context, env *Env, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("User-Agent", userAgent)
	res, err := client(env).Do(req)
	if err != nil {
		return "", &Transient{err}
	}
	defer res.Body.Close()
	if res.StatusCode == 404 {
		return "", errNotFound
	}
	if res.StatusCode == 429 || res.StatusCode >= 500 {
		return "", &Transient{fmt.Errorf("HTTP %d", res.StatusCode)}
	}
	b, err := io.ReadAll(io.LimitReader(res.Body, 4<<20))
	return string(b), err
}

var errNotFound = fmt.Errorf("not found")

func client(env *Env) *http.Client {
	if env != nil && env.HTTP != nil {
		return env.HTTP
	}
	return http.DefaultClient
}

var (
	reScript = regexp.MustCompile(`(?is)<(script|style|nav|header|footer)[^>]*>.*?</(script|style|nav|header|footer)>`)
	reTag    = regexp.MustCompile(`(?s)<[^>]+>`)
	reSpace  = regexp.MustCompile(`[ \t]+`)
	reLines  = regexp.MustCompile(`\n\s*\n+`)
)

func htmlToText(s string) string {
	s = reScript.ReplaceAllString(s, " ")
	s = strings.NewReplacer("<br>", "\n", "<br/>", "\n", "</p>", "\n", "</div>", "\n", "</li>", "\n").Replace(s)
	s = reTag.ReplaceAllString(s, " ")
	s = strings.NewReplacer("&nbsp;", " ", "&amp;", "&", "&lt;", "<", "&gt;", ">", "&quot;", `"`, "&#39;", "'", "&sect;", "§").Replace(s)
	s = reSpace.ReplaceAllString(s, " ")
	s = reLines.ReplaceAllString(s, "\n\n")
	return strings.TrimSpace(s)
}
