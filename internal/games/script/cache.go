package script

import (
	"bytes"
	"hash/maphash"
	"maps"
	"sync"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// memo remembers recent toMove and legal results. Callers almost always ask
// for the same position twice in a row: a client fetches the legal moves and
// then submits one, and the AI search calls Legal and then Apply, which
// re-checks legality. Module functions are pure (Check enforces replay
// determinism), so a remembered result is exactly what a new call would
// return. Entries hold a copy of the state and are verified byte for byte,
// so a hash collision can never return another position's moves.
type memo struct {
	seed maphash.Seed
	mu   sync.Mutex
	m    map[memoKey]*memoEntry
}

const (
	memoEntries  = 256      // worst case 256 × 16 KiB per loaded game
	memoMaxState = 16 << 10 // larger states are not worth keeping around
)

type memoKey struct {
	h    uint64
	kind byte // 't' toMove, 'l' legal
	seat games.Seat
}

type memoEntry struct {
	state  []byte
	toMove []games.Seat
	legal  []games.MoveSpec
}

func newMemo() *memo {
	return &memo{seed: maphash.MakeSeed(), m: make(map[memoKey]*memoEntry, memoEntries)}
}

func (c *memo) key(st []byte, kind byte, seat games.Seat) memoKey {
	return memoKey{h: maphash.Bytes(c.seed, st), kind: kind, seat: seat}
}

func (c *memo) get(k memoKey, st []byte) *memoEntry {
	c.mu.Lock()
	defer c.mu.Unlock()
	if e := c.m[k]; e != nil && bytes.Equal(e.state, st) {
		return e
	}
	return nil
}

func (c *memo) put(k memoKey, st []byte, e *memoEntry) {
	if len(st) > memoMaxState {
		return
	}
	e.state = bytes.Clone(st)
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.m) >= memoEntries {
		// Evict about a quarter, arbitrarily: map order is random enough,
		// and the useful entries are the ones written just now.
		n := 0
		for k := range c.m {
			delete(c.m, k)
			if n++; n >= memoEntries/4 {
				break
			}
		}
	}
	c.m[k] = e
}

// cloneSpecs copies the parts of a spec a caller could mutate, so a cached
// result is never shared with the outside.
func cloneSpecs(in []games.MoveSpec) []games.MoveSpec {
	out := make([]games.MoveSpec, len(in))
	for i, sp := range in {
		sp.Args = maps.Clone(sp.Args)
		sp.UI = maps.Clone(sp.UI)
		if sp.Range != nil {
			r := *sp.Range
			sp.Range = &r
		}
		out[i] = sp
	}
	return out
}
