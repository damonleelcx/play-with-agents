package rooms

import (
	"context"
	"encoding/json"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"
)

// Signal is one thing a table's stream sends: a new version (clients
// refetch the view), a chat line, someone typing, the reactions on a line,
// or who is here.
type Signal struct {
	Kind    string    // "table" | "chat" | "typing" | "reaction" | "presence"
	Version int64     // for "table"
	Chat    *ChatLine // for "chat"
	// Audience, when set, is everyone who may receive the signal (a
	// whisper, or a reaction on one); the stream drops it for anyone else.
	Audience []string
	Typing   *Typing      // for "typing"
	From     string       // for "typing": the person typing, who is not told
	Reaction *ReactionSet // for "reaction"
	Presence *Presence    // for "presence"
}

// For reports whether userID may receive the signal.
func (sig Signal) For(userID string) bool {
	return len(sig.Audience) == 0 || slices.Contains(sig.Audience, userID)
}

// Hub fans table notifications out to this process's stream subscribers.
type Hub struct {
	mu   sync.Mutex
	subs map[string]map[chan Signal]struct{}
}

func NewHub() *Hub { return &Hub{subs: map[string]map[chan Signal]struct{}{}} }

// Subscribe returns a channel of signals for tableID and its cancel func.
// The channel is buffered; a subscriber too slow to drain it misses
// signals, which is safe: a "table" signal only says "refetch", and the
// view carries the last 60 chat lines.
func (h *Hub) Subscribe(tableID string) (<-chan Signal, func()) {
	c := make(chan Signal, 32)
	h.mu.Lock()
	if h.subs[tableID] == nil {
		h.subs[tableID] = map[chan Signal]struct{}{}
	}
	h.subs[tableID][c] = struct{}{}
	h.mu.Unlock()
	return c, func() {
		h.mu.Lock()
		delete(h.subs[tableID], c)
		if len(h.subs[tableID]) == 0 {
			delete(h.subs, tableID)
		}
		h.mu.Unlock()
	}
}

func (h *Hub) has(tableID string) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	return len(h.subs[tableID]) > 0
}

// Publish delivers sig to every subscriber of tableID without blocking.
func (h *Hub) Publish(tableID string, sig Signal) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for c := range h.subs[tableID] {
		select {
		case c <- sig:
		default:
		}
	}
}

// Subscribe is a shortcut for s.Hub().Subscribe.
func (s *Service) Subscribe(tableID string) (<-chan Signal, func()) { return s.hub.Subscribe(tableID) }

// Listen holds one LISTEN play_table connection for the process and turns
// notifications into hub signals and worker wake-ups. It reconnects (with a
// pause) when the connection drops, until ctx is cancelled.
//
// Payloads: a bare table id means "the table changed"; a JSON object
// {table_id, chat} carries a chat line, so every pod can push chat without
// a database read.
func (s *Service) Listen(ctx context.Context) {
	for ctx.Err() == nil {
		if err := s.listenOnce(ctx); err != nil && ctx.Err() == nil {
			slog.Warn("rooms: listen connection lost", "err", err)
		}
		sleepCtx(ctx, time.Second)
	}
}

func (s *Service) listenOnce(ctx context.Context) error {
	pc, err := s.Pool.Acquire(ctx)
	if err != nil {
		return err
	}
	// The connection will carry LISTEN state: take it out of the pool for
	// good rather than hand a listening connection to another query.
	conn := pc.Hijack()
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, `LISTEN `+NotifyChannel); err != nil {
		return err
	}
	for {
		n, err := conn.WaitForNotification(ctx)
		if err != nil {
			return err
		}
		s.dispatch(ctx, n.Payload)
	}
}

func (s *Service) dispatch(ctx context.Context, payload string) {
	s.poke() // a commit elsewhere may have enqueued a job
	if strings.HasPrefix(payload, "{") {
		var cn chatNotice
		if json.Unmarshal([]byte(payload), &cn) != nil || cn.TableID == "" {
			return
		}
		switch {
		case cn.Chat != nil:
			s.hub.Publish(cn.TableID, Signal{Kind: "chat", Chat: cn.Chat})
		case cn.Whisper != nil && len(cn.Audience) > 0:
			s.hub.Publish(cn.TableID, Signal{Kind: "chat", Chat: cn.Whisper, Audience: cn.Audience})
		case cn.Typing != nil:
			s.hub.Publish(cn.TableID, Signal{Kind: "typing", Typing: cn.Typing, From: cn.From})
		case cn.Reaction != nil:
			s.hub.Publish(cn.TableID, Signal{Kind: "reaction", Reaction: cn.Reaction, Audience: cn.Audience})
		case cn.Presence:
			if !s.hub.has(cn.TableID) {
				return
			}
			if p, err := s.Presence(ctx, cn.TableID); err == nil {
				s.hub.Publish(cn.TableID, Signal{Kind: "presence", Presence: p})
			}
		}
		return
	}
	if !s.hub.has(payload) {
		return
	}
	var v int64
	if err := s.Pool.QueryRow(ctx, `SELECT version FROM tables WHERE id=$1`, payload).Scan(&v); err != nil {
		return
	}
	s.hub.Publish(payload, Signal{Kind: "table", Version: v})
}
