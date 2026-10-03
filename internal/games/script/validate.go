package script

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"sort"
	"strings"

	"github.com/damonleelcx/play-with-agents/internal/games"
	"github.com/damonleelcx/play-with-agents/internal/games/ai"
)

// Validation is strict on purpose: the modules are written by an LLM
// Engineer, and a precise error ("view.board.cells[2] has 6 cells, want 7")
// is what lets it fix the module in one round instead of shipping a game
// the client cannot render.

type obj = map[string]any

func decode(fn, raw string) (any, error) {
	var v any
	if err := json.Unmarshal([]byte(raw), &v); err != nil {
		return nil, outErr(fn, "", "returned invalid JSON: %v", err)
	}
	return v, nil
}

func asInt(v any) (int, bool) {
	f, ok := v.(float64)
	if !ok || math.IsInf(f, 0) || f != math.Trunc(f) || math.Abs(f) > 1<<40 {
		return 0, false
	}
	return int(f), true
}

func typeName(v any) string {
	switch v.(type) {
	case nil:
		return "null"
	case bool:
		return "a boolean"
	case float64:
		return "a number"
	case string:
		return "a string"
	case []any:
		return "an array"
	case obj:
		return "an object"
	}
	return fmt.Sprintf("%T", v)
}

// keys rejects unknown keys, which are almost always typos ("colour",
// "cell" for "cells") that would otherwise be silently ignored by the client.
func keys(fn, path string, o obj, allowed ...string) error {
	var bad []string
	for k := range o {
		if !slices.Contains(allowed, k) {
			bad = append(bad, k)
		}
	}
	if len(bad) == 0 {
		return nil
	}
	sort.Strings(bad)
	return outErr(fn, path, "has unknown key(s) %s; allowed: %s", strings.Join(bad, ", "), strings.Join(allowed, ", "))
}

func object(fn, path string, v any) (obj, error) {
	o, ok := v.(obj)
	if !ok {
		return nil, outErr(fn, path, "must be an object, got %s", typeName(v))
	}
	return o, nil
}

func array(fn, path string, v any) ([]any, error) {
	a, ok := v.([]any)
	if !ok {
		return nil, outErr(fn, path, "must be an array, got %s", typeName(v))
	}
	return a, nil
}

func optString(fn, path string, o obj, k string) error {
	if v, ok := o[k]; ok {
		if _, ok := v.(string); !ok {
			return outErr(fn, path+"."+k, "must be a string, got %s", typeName(v))
		}
	}
	return nil
}

func reqString(fn, path string, o obj, k string) (string, error) {
	s, ok := o[k].(string)
	if !ok || s == "" {
		return "", outErr(fn, path+"."+k, "must be a non-empty string, got %s", typeName(o[k]))
	}
	return s, nil
}

func optEnum(fn, path string, o obj, k string, values ...string) error {
	v, ok := o[k]
	if !ok {
		return nil
	}
	s, _ := v.(string)
	if !slices.Contains(values, s) {
		return outErr(fn, path+"."+k, "must be one of %s, got %v", strings.Join(values, "|"), v)
	}
	return nil
}

func seatValue(fn, path string, v any, seats int, allowSpectator bool) (int, error) {
	s, ok := asInt(v)
	lo := 0
	if allowSpectator {
		lo = -1
	}
	if !ok || s < lo || s >= seats {
		return 0, outErr(fn, path, "must be a seat number 0..%d, got %v", seats-1, v)
	}
	return s, nil
}

// ── meta ────────────────────────────────────────────────────────────────────

func parseMeta(raw json.RawMessage) (games.Meta, error) {
	const fn = "meta"
	var m games.Meta
	if len(raw) == 0 || string(raw) == "null" {
		return m, fmt.Errorf("script: game.meta is missing; add meta: { name, summary, minSeats, maxSeats, hiddenInfo, turnSeconds }")
	}
	v, err := decode(fn, string(raw))
	if err != nil {
		return m, err
	}
	o, err := object(fn, "game.meta", v)
	if err != nil {
		return m, err
	}
	if err := keys(fn, "game.meta", o, "name", "summary", "minSeats", "maxSeats", "hiddenInfo", "turnSeconds", "options", "rules"); err != nil {
		return m, err
	}
	if m.Name, err = reqString(fn, "game.meta", o, "name"); err != nil {
		return m, err
	}
	for _, k := range []string{"summary", "rules"} {
		if err := optString(fn, "game.meta", o, k); err != nil {
			return m, err
		}
	}
	m.Summary, _ = o["summary"].(string)
	m.RulesMD, _ = o["rules"].(string)
	minS, ok1 := asInt(o["minSeats"])
	maxS, ok2 := asInt(o["maxSeats"])
	if !ok1 || !ok2 || minS < 1 || maxS > 12 || minS > maxS {
		return m, outErr(fn, "game.meta", "needs integer minSeats and maxSeats with 1 <= minSeats <= maxSeats <= 12, got %v and %v", o["minSeats"], o["maxSeats"])
	}
	m.MinSeats, m.MaxSeats = minS, maxS
	if h, ok := o["hiddenInfo"]; ok {
		b, isBool := h.(bool)
		if !isBool {
			return m, outErr(fn, "game.meta.hiddenInfo", "must be true or false, got %s", typeName(h))
		}
		m.HiddenInfo = b
	}
	m.TurnSeconds = 30
	if t, ok := o["turnSeconds"]; ok {
		n, ok := asInt(t)
		if !ok || n < 0 || n > 3600 {
			return m, outErr(fn, "game.meta.turnSeconds", "must be an integer number of seconds (0 = no clock), got %v", t)
		}
		m.TurnSeconds = n
	}
	if opts, ok := o["options"]; ok && opts != nil {
		oo, err := object(fn, "game.meta.options", opts)
		if err != nil {
			return m, err
		}
		if len(oo) > 0 {
			m.Options = oo
		}
	}
	return m, nil
}

// ── toMove / legal / move ───────────────────────────────────────────────────

func parseToMove(raw string, seats int) ([]games.Seat, error) {
	const fn = "toMove"
	v, err := decode(fn, raw)
	if err != nil {
		return nil, err
	}
	a, err := array(fn, "result", v)
	if err != nil {
		return nil, err
	}
	out := make([]games.Seat, 0, len(a))
	for i, x := range a {
		s, err := seatValue(fn, fmt.Sprintf("result[%d]", i), x, seats, false)
		if err != nil {
			return nil, err
		}
		if slices.Contains(out, s) {
			return nil, outErr(fn, "result", "lists seat %d twice", s)
		}
		out = append(out, s)
	}
	return out, nil
}

func parseLegal(raw string, maxLegal int) ([]games.MoveSpec, error) {
	const fn = "legal"
	v, err := decode(fn, raw)
	if err != nil {
		return nil, err
	}
	a, err := array(fn, "result", v)
	if err != nil {
		return nil, err
	}
	if len(a) > maxLegal {
		return nil, outErr(fn, "result", "has %d moves, over the limit of %d; use a range for numeric choices", len(a), maxLegal)
	}
	out := make([]games.MoveSpec, 0, len(a))
	seen := make(map[string]bool, len(a))
	for i, x := range a {
		p := fmt.Sprintf("[%d]", i)
		o, err := object(fn, p, x)
		if err != nil {
			return nil, err
		}
		if err := keys(fn, p, o, "type", "label", "args", "ui", "range"); err != nil {
			return nil, err
		}
		var sp games.MoveSpec
		if sp.Type, err = reqString(fn, p, o, "type"); err != nil {
			return nil, err
		}
		if sp.Label, err = reqString(fn, p, o, "label"); err != nil {
			return nil, err
		}
		if av, ok := o["args"]; ok && av != nil {
			if sp.Args, err = object(fn, p+".args", av); err != nil {
				return nil, err
			}
		}
		if uv, ok := o["ui"]; ok && uv != nil {
			if sp.UI, err = parseUI(fn, p+".ui", uv); err != nil {
				return nil, err
			}
		}
		if rv, ok := o["range"]; ok && rv != nil {
			if sp.Range, err = parseRange(fn, p+".range", rv); err != nil {
				return nil, err
			}
		}
		key := ai.MoveKey(games.Move{Type: sp.Type, Args: sp.Args})
		if sp.Range != nil {
			key += "|range:" + sp.Range.Arg
		}
		if seen[key] {
			return nil, outErr(fn, p, "duplicates an earlier move (%s); every legal move must be distinct", key)
		}
		seen[key] = true
		out = append(out, sp)
	}
	return out, nil
}

func parseRange(fn, path string, v any) (*games.Range, error) {
	o, err := object(fn, path, v)
	if err != nil {
		return nil, err
	}
	if err := keys(fn, path, o, "arg", "min", "max", "step"); err != nil {
		return nil, err
	}
	var r games.Range
	if r.Arg, err = reqString(fn, path, o, "arg"); err != nil {
		return nil, err
	}
	var ok1, ok2 bool
	r.Min, ok1 = asInt(o["min"])
	r.Max, ok2 = asInt(o["max"])
	if !ok1 || !ok2 || r.Min > r.Max {
		return nil, outErr(fn, path, "needs integer min <= max, got %v and %v", o["min"], o["max"])
	}
	if sv, ok := o["step"]; ok {
		s, ok := asInt(sv)
		if !ok || s < 1 {
			return nil, outErr(fn, path+".step", "must be a positive integer, got %v", sv)
		}
		r.Step = s
	}
	return &r, nil
}

func parseUI(fn, path string, v any) (obj, error) {
	o, err := object(fn, path, v)
	if err != nil {
		return nil, err
	}
	if err := keys(fn, path, o, "cell", "from", "to", "zone", "index"); err != nil {
		return nil, err
	}
	for _, k := range []string{"cell", "from", "to"} {
		if c, ok := o[k]; ok {
			a, isArr := c.([]any)
			if !isArr || len(a) != 2 {
				return nil, outErr(fn, path+"."+k, "must be [row, col], got %v", c)
			}
			for _, x := range a {
				if n, ok := asInt(x); !ok || n < 0 {
					return nil, outErr(fn, path+"."+k, "must be [row, col] with non-negative integers, got %v", c)
				}
			}
		}
	}
	_, hasFrom := o["from"]
	_, hasTo := o["to"]
	if hasFrom != hasTo {
		return nil, outErr(fn, path, "needs both from and to (or neither)")
	}
	if z, ok := o["zone"]; ok {
		if s, ok := z.(string); !ok || s == "" {
			return nil, outErr(fn, path+".zone", "must be a non-empty zone id, got %v", z)
		}
		if n, ok := asInt(o["index"]); !ok || n < 0 {
			return nil, outErr(fn, path+".index", "must be a non-negative integer card index when zone is set, got %v", o["index"])
		}
	} else if _, ok := o["index"]; ok {
		return nil, outErr(fn, path, "has index without zone")
	}
	return o, nil
}

func parseMove(raw string) (games.Move, error) {
	const fn = "defaultMove"
	v, err := decode(fn, raw)
	if err != nil {
		return games.Move{}, err
	}
	o, err := object(fn, "result", v)
	if err != nil {
		return games.Move{}, err
	}
	// A legal() entry is accepted too: returning legal[0] is the obvious
	// implementation and its extra keys are harmless.
	if err := keys(fn, "result", o, "type", "args", "label", "ui", "range"); err != nil {
		return games.Move{}, err
	}
	var m games.Move
	if m.Type, err = reqString(fn, "result", o, "type"); err != nil {
		return games.Move{}, err
	}
	if av, ok := o["args"]; ok && av != nil {
		if m.Args, err = object(fn, "result.args", av); err != nil {
			return games.Move{}, err
		}
	}
	return m, nil
}

// ── apply events ────────────────────────────────────────────────────────────

func parseEvents(raw string, actor games.Seat, seats, maxEvents int) ([]games.Event, error) {
	const fn = "apply"
	if raw == "" || raw == "null" {
		return nil, nil
	}
	v, err := decode(fn, raw)
	if err != nil {
		return nil, err
	}
	a, err := array(fn, "events", v)
	if err != nil {
		return nil, err
	}
	if len(a) > maxEvents {
		return nil, outErr(fn, "events", "has %d entries, over the limit of %d", len(a), maxEvents)
	}
	out := make([]games.Event, 0, len(a))
	for i, x := range a {
		p := fmt.Sprintf("events[%d]", i)
		o, err := object(fn, p, x)
		if err != nil {
			return nil, err
		}
		if err := keys(fn, p, o, "type", "seat", "text", "data", "only"); err != nil {
			return nil, err
		}
		e := games.Event{Seat: actor}
		if e.Type, err = reqString(fn, p, o, "type"); err != nil {
			return nil, err
		}
		if sv, ok := o["seat"]; ok && sv != nil {
			if e.Seat, err = seatValue(fn, p+".seat", sv, seats, true); err != nil {
				return nil, err
			}
		}
		if err := optString(fn, p, o, "text"); err != nil {
			return nil, err
		}
		e.Text, _ = o["text"].(string)
		if err := checkPlaceholders(fn, p+".text", e.Text, seats); err != nil {
			return nil, err
		}
		if dv, ok := o["data"]; ok && dv != nil {
			if e.Data, err = object(fn, p+".data", dv); err != nil {
				return nil, err
			}
		}
		if ov, ok := o["only"]; ok && ov != nil {
			oa, err := array(fn, p+".only", ov)
			if err != nil {
				return nil, err
			}
			e.Only = make([]games.Seat, 0, len(oa))
			for j, s := range oa {
				seat, err := seatValue(fn, fmt.Sprintf("%s.only[%d]", p, j), s, seats, false)
				if err != nil {
					return nil, err
				}
				e.Only = append(e.Only, seat)
			}
		}
		out = append(out, e)
	}
	return out, nil
}

// checkPlaceholders verifies {s:N} references point at real seats; the rooms
// service would otherwise leave the raw placeholder in front of players.
func checkPlaceholders(fn, path, text string, seats int) error {
	for rest := text; ; {
		i := strings.Index(rest, "{s:")
		if i < 0 {
			return nil
		}
		rest = rest[i+3:]
		j := strings.IndexByte(rest, '}')
		if j < 0 {
			return outErr(fn, path, "has an unterminated {s:N} placeholder: %q", text)
		}
		var n int
		if _, err := fmt.Sscanf(rest[:j], "%d", &n); err != nil || fmt.Sprint(n) != rest[:j] || n < 0 || n >= seats {
			return outErr(fn, path, "has placeholder {s:%s}; N must be a seat number 0..%d", rest[:j], seats-1)
		}
		rest = rest[j+1:]
	}
}

// ── outcome ─────────────────────────────────────────────────────────────────

func parseOutcome(raw string, seats int) (*games.Outcome, error) {
	const fn = "outcome"
	v, err := decode(fn, raw)
	if err != nil {
		return nil, err
	}
	o, err := object(fn, "result", v)
	if err != nil {
		return nil, err
	}
	if err := keys(fn, "result", o, "rank", "score", "summary"); err != nil {
		return nil, err
	}
	ra, err := array(fn, "rank", o["rank"])
	if err != nil {
		return nil, err
	}
	sa, err := array(fn, "score", o["score"])
	if err != nil {
		return nil, err
	}
	if len(ra) != seats || len(sa) != seats {
		return nil, outErr(fn, "result", "rank and score need one entry per seat (%d), got %d and %d", seats, len(ra), len(sa))
	}
	out := &games.Outcome{Rank: make([]int, seats), Score: make([]float64, seats)}
	hasFirst := false
	for i := range seats {
		r, ok := asInt(ra[i])
		if !ok || r < 1 || r > seats {
			return nil, outErr(fn, fmt.Sprintf("rank[%d]", i), "must be an integer 1..%d (1 = winner), got %v", seats, ra[i])
		}
		hasFirst = hasFirst || r == 1
		out.Rank[i] = r
		f, ok := sa[i].(float64)
		if !ok {
			return nil, outErr(fn, fmt.Sprintf("score[%d]", i), "must be a number, got %s", typeName(sa[i]))
		}
		out.Score[i] = f
	}
	if !hasFirst {
		return nil, outErr(fn, "rank", "has no seat ranked 1")
	}
	if err := optString(fn, "result", o, "summary"); err != nil {
		return nil, err
	}
	out.Summary, _ = o["summary"].(string)
	if err := checkPlaceholders(fn, "summary", out.Summary, seats); err != nil {
		return nil, err
	}
	return out, nil
}

// ── view ────────────────────────────────────────────────────────────────────

// maxBoardSide bounds rows and cols; the renderer is built for real boards,
// not data dumps.
const maxBoardSide = 32

// validateView checks the "board" view schema from docs/00-architecture.md
// and returns view.message (the View.Status).
func validateView(raw string, seats, maxBytes int) (string, error) {
	const fn = "view"
	if len(raw) > maxBytes {
		return "", outErr(fn, "", "returned %d bytes, over the %d-byte limit", len(raw), maxBytes)
	}
	v, err := decode(fn, raw)
	if err != nil {
		return "", err
	}
	o, err := object(fn, "view", v)
	if err != nil {
		return "", err
	}
	if err := keys(fn, "view", o, "title", "board", "zones", "players", "counters", "message"); err != nil {
		return "", err
	}
	if err := optString(fn, "view", o, "title"); err != nil {
		return "", err
	}
	if err := optString(fn, "view", o, "message"); err != nil {
		return "", err
	}
	msg, _ := o["message"].(string)
	if err := checkPlaceholders(fn, "view.message", msg, seats); err != nil {
		return "", err
	}
	if b, ok := o["board"]; ok && b != nil {
		if err := validateBoard(b); err != nil {
			return "", err
		}
	}
	if z, ok := o["zones"]; ok && z != nil {
		if err := validateZones(z, seats); err != nil {
			return "", err
		}
	}
	if p, ok := o["players"]; ok && p != nil {
		if err := validatePlayers(p, seats); err != nil {
			return "", err
		}
	}
	if c, ok := o["counters"]; ok && c != nil {
		if err := validateCounters(c); err != nil {
			return "", err
		}
	}
	return msg, nil
}

func validateBoard(v any) error {
	const fn = "view"
	b, err := object(fn, "view.board", v)
	if err != nil {
		return err
	}
	if err := keys(fn, "view.board", b, "rows", "cols", "style", "cells"); err != nil {
		return err
	}
	rows, ok1 := asInt(b["rows"])
	cols, ok2 := asInt(b["cols"])
	if !ok1 || !ok2 || rows < 1 || cols < 1 || rows > maxBoardSide || cols > maxBoardSide {
		return outErr(fn, "view.board", "needs integer rows and cols in 1..%d, got %v and %v", maxBoardSide, b["rows"], b["cols"])
	}
	if err := optEnum(fn, "view.board", b, "style", "grid", "checker", "go", "plain"); err != nil {
		return err
	}
	cells, err := array(fn, "view.board.cells", b["cells"])
	if err != nil {
		return err
	}
	if len(cells) != rows {
		return outErr(fn, "view.board.cells", "has %d rows, but board.rows is %d", len(cells), rows)
	}
	for r, row := range cells {
		p := fmt.Sprintf("view.board.cells[%d]", r)
		ra, err := array(fn, p, row)
		if err != nil {
			return err
		}
		if len(ra) != cols {
			return outErr(fn, p, "has %d cells, but board.cols is %d", len(ra), cols)
		}
		for c, cell := range ra {
			if cell == nil {
				continue
			}
			if err := validateCell(fmt.Sprintf("%s[%d]", p, c), cell); err != nil {
				return err
			}
		}
	}
	return nil
}

func validateCell(path string, v any) error {
	const fn = "view"
	c, err := object(fn, path, v)
	if err != nil {
		return err
	}
	if err := keys(fn, path, c, "piece", "mark", "text"); err != nil {
		return err
	}
	if err := optString(fn, path, c, "mark"); err != nil {
		return err
	}
	if err := optString(fn, path, c, "text"); err != nil {
		return err
	}
	pv, ok := c["piece"]
	if !ok || pv == nil {
		return nil
	}
	pp := path + ".piece"
	p, err := object(fn, pp, pv)
	if err != nil {
		return err
	}
	if err := keys(fn, pp, p, "shape", "color", "glyph", "label"); err != nil {
		return err
	}
	if err := optEnum(fn, pp, p, "shape", "disc", "square", "ring", "king", "text"); err != nil {
		return err
	}
	for _, k := range []string{"color", "glyph", "label"} {
		if err := optString(fn, pp, p, k); err != nil {
			return err
		}
	}
	return nil
}

func validateZones(v any, seats int) error {
	const fn = "view"
	zs, err := array(fn, "view.zones", v)
	if err != nil {
		return err
	}
	ids := map[string]bool{}
	for i, zv := range zs {
		p := fmt.Sprintf("view.zones[%d]", i)
		z, err := object(fn, p, zv)
		if err != nil {
			return err
		}
		if err := keys(fn, p, z, "id", "label", "owner", "layout", "cards"); err != nil {
			return err
		}
		id, err := reqString(fn, p, z, "id")
		if err != nil {
			return err
		}
		if ids[id] {
			return outErr(fn, p+".id", "duplicates zone id %q", id)
		}
		ids[id] = true
		if err := optString(fn, p, z, "label"); err != nil {
			return err
		}
		if ov, ok := z["owner"]; ok && ov != nil {
			if _, err := seatValue(fn, p+".owner", ov, seats, false); err != nil {
				return err
			}
		}
		if err := optEnum(fn, p, z, "layout", "row", "fan", "stack"); err != nil {
			return err
		}
		cards, err := array(fn, p+".cards", z["cards"])
		if err != nil {
			return err
		}
		for j, cv := range cards {
			cp := fmt.Sprintf("%s.cards[%d]", p, j)
			c, err := object(fn, cp, cv)
			if err != nil {
				return err
			}
			if err := keys(fn, cp, c, "face", "color", "hidden"); err != nil {
				return err
			}
			if err := optString(fn, cp, c, "face"); err != nil {
				return err
			}
			if err := optString(fn, cp, c, "color"); err != nil {
				return err
			}
			hidden := false
			if hv, ok := c["hidden"]; ok {
				b, isBool := hv.(bool)
				if !isBool {
					return outErr(fn, cp+".hidden", "must be true or false, got %s", typeName(hv))
				}
				hidden = b
			}
			// The view is sent to this seat's browser as is: a face on a
			// hidden card is readable by anyone who opens the dev tools.
			if face, _ := c["face"].(string); hidden && face != "" {
				return outErr(fn, cp, "is hidden but has face %q: that leaks hidden information to the client; omit the face", face)
			}
		}
	}
	return nil
}

func validatePlayers(v any, seats int) error {
	const fn = "view"
	ps, err := array(fn, "view.players", v)
	if err != nil {
		return err
	}
	for i, pv := range ps {
		p := fmt.Sprintf("view.players[%d]", i)
		o, err := object(fn, p, pv)
		if err != nil {
			return err
		}
		if err := keys(fn, p, o, "seat", "score", "info", "color"); err != nil {
			return err
		}
		if _, err := seatValue(fn, p+".seat", o["seat"], seats, false); err != nil {
			return err
		}
		if sv, ok := o["score"]; ok && sv != nil {
			switch sv.(type) {
			case float64, string:
			default:
				return outErr(fn, p+".score", "must be a number or string, got %s", typeName(sv))
			}
		}
		if err := optString(fn, p, o, "info"); err != nil {
			return err
		}
		if err := optString(fn, p, o, "color"); err != nil {
			return err
		}
	}
	return nil
}

func validateCounters(v any) error {
	const fn = "view"
	cs, err := array(fn, "view.counters", v)
	if err != nil {
		return err
	}
	for i, cv := range cs {
		p := fmt.Sprintf("view.counters[%d]", i)
		o, err := object(fn, p, cv)
		if err != nil {
			return err
		}
		if err := keys(fn, p, o, "label", "value"); err != nil {
			return err
		}
		if _, err := reqString(fn, p, o, "label"); err != nil {
			return err
		}
		switch o["value"].(type) {
		case float64, string:
		default:
			return outErr(fn, p+".value", "must be a number or string, got %s", typeName(o["value"]))
		}
	}
	return nil
}
