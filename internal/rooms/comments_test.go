package rooms

import (
	"context"
	"errors"
	"fmt"
	"math/rand"
	"strings"
	"testing"
)

func (r *rig) scriptGame(t *testing.T, owner, status, visibility string) string {
	t.Helper()
	id := fmt.Sprintf("cg-%d", rand.Int63n(1e12))
	if _, err := r.pool.Exec(context.Background(), `INSERT INTO games (id, owner_id, kind, name, status, visibility, current_version)
		VALUES ($1, $2, 'script', 'Comment Game', $3, $4, 1)`, id, owner, status, visibility); err != nil {
		t.Fatal(err)
	}
	return id
}

func TestCommentsOnAPublishedGame(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	maker, ann, bob := r.user(t, "Maker", nil), r.user(t, "Ann", nil), r.user(t, "Bob", nil)
	g := r.scriptGame(t, maker, "published", "public")

	c, err := r.svc.AddComment(ctx, ann, g, "  Love the nebula shortcut!  ")
	if err != nil || c.Body != "Love the nebula shortcut!" || !c.Mine || c.Name != "Ann" {
		t.Fatalf("add: %+v %v", c, err)
	}
	if _, err := r.svc.AddComment(ctx, maker, g, "Thanks!"); err != nil {
		t.Fatal(err)
	}
	pg, err := r.svc.Comments(ctx, bob, g, 0)
	if err != nil || pg.Total != 2 || len(pg.Comments) != 2 {
		t.Fatalf("list: %+v %v", pg, err)
	}
	if top := pg.Comments[0]; top.Body != "Thanks!" || !top.ByOwner || top.CanDelete || top.Mine {
		t.Fatalf("newest first, by the maker, not Bob's to delete: %+v", top)
	}

	// Bob may not remove Ann's comment; the maker may.
	if err := r.svc.DeleteComment(ctx, bob, g, c.ID); err == nil {
		t.Fatal("a stranger removed a comment")
	}
	if err := r.svc.DeleteComment(ctx, maker, g, c.ID); err != nil {
		t.Fatal(err)
	}
	pg, _ = r.svc.Comments(ctx, bob, g, 0)
	if pg.Total != 1 || !pg.Comments[1].Deleted || pg.Comments[1].Body != "" || pg.Comments[1].Name != "" {
		t.Fatalf("a removed comment keeps its place, not its text: %+v", pg)
	}

	// Empty, too long, too fast.
	if _, err := r.svc.AddComment(ctx, bob, g, "   "); err == nil {
		t.Fatal("empty comment accepted")
	}
	if _, err := r.svc.AddComment(ctx, bob, g, strings.Repeat("x", maxCommentRunes+1)); err == nil {
		t.Fatal("long comment accepted")
	}
	for i := 0; i < commentsPerMinute; i++ {
		if _, err := r.svc.AddComment(ctx, bob, g, fmt.Sprintf("line %d", i)); err != nil {
			t.Fatalf("comment %d: %v", i, err)
		}
	}
	if _, err := r.svc.AddComment(ctx, bob, g, "one too many"); !errors.Is(err, ErrRateLimit) {
		t.Fatalf("rate limit: %v", err)
	}
}

func TestCommentsFollowTheGamesVisibility(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	maker, ann := r.user(t, "Maker", nil), r.user(t, "Ann", nil)
	private := r.scriptGame(t, maker, "published", "private")
	draft := r.scriptGame(t, maker, "draft", "public")
	if _, err := r.svc.Comments(ctx, ann, private, 0); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a private game's thread is visible: %v", err)
	}
	if _, err := r.svc.AddComment(ctx, ann, draft, "hi"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("a stranger commented on a draft: %v", err)
	}
	if _, err := r.svc.AddComment(ctx, maker, draft, "hi"); err == nil {
		t.Fatal("drafts have no comment thread")
	}
}

func TestTablesPage(t *testing.T) {
	r := newRig(t)
	ctx := context.Background()
	a := r.user(t, "Ann", nil)
	for i := 0; i < 5; i++ {
		r.create(t, a, "me", "agent:aoi")
	}
	p1, err := r.svc.ListPage(ctx, a, "mine", 0, 2)
	if err != nil || p1.Total != 5 || len(p1.Tables) != 2 {
		t.Fatalf("page 1: %+v %v", p1, err)
	}
	p3, _ := r.svc.ListPage(ctx, a, "mine", 4, 2)
	if len(p3.Tables) != 1 {
		t.Fatalf("last page: %+v", p3)
	}
	seen := map[string]bool{}
	for off := 0; off < 5; off += 2 {
		pg, _ := r.svc.ListPage(ctx, a, "mine", off, 2)
		for _, x := range pg.Tables {
			if seen[x.ID] {
				t.Fatalf("table %s on two pages", x.ID)
			}
			seen[x.ID] = true
		}
	}
	if len(seen) != 5 {
		t.Fatalf("pages cover %d of 5 tables", len(seen))
	}
}
