package art

import (
	"context"
	"fmt"
	"strings"
)

// Style is appended to every subject so all covers share one look: the
// brand's night palette, cinematic light, and no lettering.
const Style = "Cinematic digital painting, premium board-game key art, rich detail, dramatic volumetric lighting, " +
	"deep navy night palette with electric blue glow and warm ember-orange accents, shallow depth of field, " +
	"vivid, family-friendly, wide 16:9 composition, no text anywhere."

// Negative is the negative prompt of every generation.
const Negative = "text, letters, words, numbers, typography, caption, title, logo, watermark, signature, UI, frame, border, " +
	"blurry, low quality, jpeg artifacts, deformed, gore, violence, nsfw"

// Prompt joins a subject (what the cover shows) with the shared Style.
func Prompt(subject string) string {
	subject = strings.Join(strings.Fields(subject), " ")
	if r := []rune(subject); len(r) > 900 {
		subject = string(r[:900])
	}
	return strings.TrimSpace(subject + " " + Style)
}

// TemplateSubject is the subject when no model writes one: the game's name
// and summary, staged as a board on a table.
func TemplateSubject(name, summary string) string {
	s := fmt.Sprintf("A beautifully crafted tabletop board game inspired by %q", name)
	if summary = strings.TrimSpace(summary); summary != "" {
		s += " (" + strings.TrimRight(summary, ".") + ")"
	}
	return s + ": the board and its pieces on a wooden table at night, seen at a low three-quarter angle, glowing pieces mid-play."
}

// Result is a finished cover.
type Result struct {
	Bytes       []byte
	ContentType string
	// Source is "generated" (the image model) or "procedural".
	Source string
	Model  string
	Prompt string
	// Attempts is how many image tasks were submitted (each one counts
	// against the budget, successful or not).
	Attempts int
	// Notes say why a generation fell back, for the task's summary.
	Notes []string
}

// Make returns a cover for the game: generated with c when it is enabled
// and allowed attempts remain (at most MaxAttempts, primary model first,
// then the fallback), otherwise — and on any failure — the procedural
// cover. It only fails if even the procedural cover cannot be encoded.
func Make(ctx context.Context, c *Client, id, name, prompt string, allowed int) (*Result, error) {
	res := &Result{ContentType: ContentType, Prompt: prompt}
	if allowed > MaxAttempts {
		allowed = MaxAttempts
	}
	switch {
	case !c.Enabled():
		res.Notes = append(res.Notes, "no image key configured")
	case allowed <= 0:
		res.Notes = append(res.Notes, "image budget for this game used up")
	default:
		for _, m := range c.Models() {
			if res.Attempts >= allowed || ctx.Err() != nil {
				break
			}
			res.Attempts++
			raw, err := c.Generate(ctx, m, prompt, Negative)
			if err == nil {
				var b []byte
				if b, err = Normalize(raw); err == nil {
					res.Bytes, res.Source, res.Model = b, "generated", m
					return res, nil
				}
			}
			res.Notes = append(res.Notes, fmt.Sprintf("%s: %v", m, err))
		}
	}
	b, err := Procedural(id, name)
	if err != nil {
		return nil, err
	}
	res.Bytes, res.Source, res.Model = b, "procedural", ""
	return res, nil
}
