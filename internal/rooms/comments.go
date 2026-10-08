package rooms

import (
	"context"
	"errors"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/damonleelcx/play-with-agents/internal/games"
)

// Comments under a game's page: anyone who can see a published game may read
// and write them; the author and the game's owner may remove one.

const (
	maxCommentRunes   = 1000
	commentsPage      = 30
	commentsPerMinute = 5
	commentsPerDay    = 200
)

// Comment is one line under a game, as a viewer sees it.
type Comment struct {
	ID        int64     `json:"id"`
	Name      string    `json:"name"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	Deleted   bool      `json:"deleted,omitempty"`
	Mine      bool      `json:"mine,omitempty"`
	CanDelete bool      `json:"can_delete,omitempty"`
	ByOwner   bool      `json:"by_owner,omitempty"` // the game's maker
}

// CommentPage is a page of comments, newest first.
type CommentPage struct {
	Comments []Comment `json:"comments"`
	Total    int       `json:"total"`
	More     bool      `json:"more"`
}

// commentable reports the game's owner ("" for built-ins and seeded games)
// if userID may read its comments: the game is published and visible to
// them. Drafts have no comment thread.
func (s *Service) commentable(ctx context.Context, userID, gameID string) (string, error) {
	if _, ok := games.Builtin(gameID); ok {
		var n int
		if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM games WHERE id=$1`, gameID).Scan(&n); err != nil || n == 0 {
			return "", ErrNotFound
		}
		return "", nil
	}
	var owner, status, vis string
	err := s.Pool.QueryRow(ctx, `SELECT coalesce(owner_id::text,''), status, visibility FROM games WHERE id=$1 AND kind='script'`, gameID).
		Scan(&owner, &status, &vis)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrNotFound
	}
	if err != nil {
		return "", err
	}
	if owner != userID && (status != "published" || vis == "private") {
		return "", ErrNotFound // do not confirm a private game exists
	}
	if status != "published" {
		return "", badInput("comments open once the game is published")
	}
	return owner, nil
}

// Comments is a page of a game's comments, newest first; before (an id)
// pages back.
func (s *Service) Comments(ctx context.Context, userID, gameID string, before int64) (*CommentPage, error) {
	owner, err := s.commentable(ctx, userID, gameID)
	if err != nil {
		return nil, err
	}
	out := &CommentPage{Comments: []Comment{}}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM game_comments WHERE game_id=$1 AND deleted_at IS NULL`, gameID).Scan(&out.Total); err != nil {
		return nil, err
	}
	if before <= 0 {
		before = 1<<62 - 1
	}
	rows, err := s.Pool.Query(ctx, `SELECT c.id, coalesce(nullif(u.name,''), 'Player'), c.body, c.created_at, c.deleted_at IS NOT NULL, c.user_id::text
		FROM game_comments c JOIN users u ON u.id = c.user_id
		WHERE c.game_id=$1 AND c.id < $2 ORDER BY c.id DESC LIMIT $3`, gameID, before, commentsPage+1)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var c Comment
		var author string
		if err := rows.Scan(&c.ID, &c.Name, &c.Body, &c.CreatedAt, &c.Deleted, &author); err != nil {
			return nil, err
		}
		c.Mine = author == userID
		c.ByOwner = owner != "" && author == owner
		c.CanDelete = !c.Deleted && (c.Mine || (owner != "" && owner == userID))
		if c.Deleted {
			c.Body, c.Name = "", ""
		}
		out.Comments = append(out.Comments, c)
	}
	if len(out.Comments) > commentsPage {
		out.Comments, out.More = out.Comments[:commentsPage], true
	}
	return out, rows.Err()
}

// AddComment posts a comment as userID.
func (s *Service) AddComment(ctx context.Context, userID, gameID, body string) (*Comment, error) {
	body = cleanText(body)
	if body == "" {
		return nil, badInput("write something first")
	}
	if len([]rune(body)) > maxCommentRunes {
		return nil, badInput("keep it under %d characters", maxCommentRunes)
	}
	owner, err := s.commentable(ctx, userID, gameID)
	if err != nil {
		return nil, err
	}
	var c Comment
	err = pgx.BeginFunc(ctx, s.Pool, func(tx pgx.Tx) error {
		// One person's comments are serialised so the rate check holds.
		if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtext('comment:' || $1))`, userID); err != nil {
			return err
		}
		var minute, day int
		if err := tx.QueryRow(ctx, `SELECT count(*) FILTER (WHERE created_at > now() - interval '1 minute'), count(*)
			FROM game_comments WHERE user_id=$1 AND created_at > now() - interval '1 day'`, userID).Scan(&minute, &day); err != nil {
			return err
		}
		if minute >= commentsPerMinute || day >= commentsPerDay {
			return ErrRateLimit
		}
		return tx.QueryRow(ctx, `INSERT INTO game_comments (game_id, user_id, body) VALUES ($1, $2, $3)
			RETURNING id, created_at, (SELECT coalesce(nullif(name,''), 'Player') FROM users WHERE id=$2)`, gameID, userID, body).
			Scan(&c.ID, &c.CreatedAt, &c.Name)
	})
	if err != nil {
		return nil, err
	}
	c.Body, c.Mine, c.CanDelete, c.ByOwner = body, true, true, owner != "" && owner == userID
	return &c, nil
}

// DeleteComment removes a comment: its author or the game's owner may.
func (s *Service) DeleteComment(ctx context.Context, userID, gameID string, id int64) error {
	owner, err := s.commentable(ctx, userID, gameID)
	if err != nil {
		return err
	}
	var author string
	err = s.Pool.QueryRow(ctx, `SELECT user_id::text FROM game_comments WHERE id=$1 AND game_id=$2 AND deleted_at IS NULL`, id, gameID).Scan(&author)
	if errors.Is(err, pgx.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if author != userID && (owner == "" || owner != userID) {
		return badInput("only the comment's author or the game's maker can remove it")
	}
	_, err = s.Pool.Exec(ctx, `UPDATE game_comments SET deleted_at=now(), body='' WHERE id=$1`, id)
	return err
}
