package studio

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"github.com/damonleelcx/play-with-agents/internal/games/script"
)

// CommunityOwner is how the lobby credits the seeded games.
const CommunityOwner = "Aoi's studio"

// seedRules are the rules documents of the bundled examples (the modules
// carry their own name and summary).
var seedRules = map[string]string{
	"tictactoe": `## Overview
Two players take turns marking a 3×3 grid. Three of your marks in a row — across, down or diagonally — wins.

## Turns
X (the first player) moves first. On your turn, place your mark in any empty square.

## End
The game ends when someone has three in a row (they win) or the grid is full (a draw).`,
	"connect-four": `## Overview
Two players drop discs into a 7-column, 6-row upright grid. Four of your discs in a line wins.

## Turns
The first player moves first. On your turn, drop one disc into any column that is not full; it falls to the lowest empty space.

## End
Four in a row horizontally, vertically or diagonally wins. If the grid fills with no line of four, the game is a draw.`,
	"reversi": `## Overview
Two players on an 8×8 board. Place a disc so that it outflanks a straight line of your opponent's discs; every outflanked disc flips to your colour. Most discs at the end wins.

## Setup
Four discs start in the centre, two of each colour on the diagonals. Dark moves first.

## Turns
A move must flip at least one disc, along any of the eight directions. If you have no such move you pass; if neither player can move, the game ends.

## End
When neither player can move, the player with more discs wins; equal counts are a draw.`,
	"lantern-market": `## Overview
A set-collection card game for 2–4 players with hidden hands. Collect lanterns of one colour and sell them in sets: bigger sets score far more.

## Components
35 lantern cards in five colours (7 each). Four lie face up in the market.

## Turns
Do one thing: take a market lantern (everyone sees it), draw blind from the deck (only you see it), or sell every lantern of one colour in your hand. Selling n lanterns scores 1+2+…+n; the first player to sell 3 or more of a colour also wins that colour's festival bonus (+3). A hand holds at most 6 lanterns; with 6 you must sell.

## End
When the deck and the market are both empty, every other player gets one last chance to sell, then the game ends. Each unsold lantern costs 1 point. Highest score wins.`,
	"comet-run": `## Overview
A race for 2–4 players on a map of space. Each player flies two ships around the planet Vesta and back to the dock; the first to bring both ships home wins.

## Board
A loop of 20 spaces around the planet, starting and ending at the Dock. At the Fork (space 5) a shortcut leaves the loop through the Nebula (three spaces) and rejoins it at space 12: four steps instead of seven.

## Turns
Each player holds four thrusters: 1, 2, 3 and 4. On your turn play one of them and move one of your ships exactly that many spaces forward along the arrows; at the Fork you choose the loop or the Nebula. Once you have used all four you get them all back. Passing or reaching the Dock brings the ship home. If your ship lands on a space with exactly one rival ship, that ship is bumped back to the Dock. If none of your ships can move, you pass.

## End
The first player with both ships home wins. If nobody has after 60 rounds, the player whose ships have the fewest steps left to the Dock wins (a tie goes to the earlier player in turn order).`,
	"story-tiles": `## Overview
A storytelling tile game for 2 players with hidden hands. Twelve unique story cards (characters, places, events and twists) are played onto a 5×5 board; the story so far is the story lines of the cards played, in order.

## Components
12 story cards. Each has a name, one line of story, a value (0–2) and an effect. Each player holds 3 cards; the other 6 form the deck.

## Turns
A random player starts. On your turn, play one card from your hand onto any open square, then draw a card if the deck has any. "A Storm Rolls In" seals the empty squares around it; "But It Was a Dream" claims the adjacent opponent card of lowest value (the first in reading order on a tie).

## Scoring
When all 12 cards are played, every card on the board scores its value plus its effect for whoever owns it then. Adjacent means the four orthogonal neighbours. "The story card before it" is the card played just before it.

## End
Highest total wins; equal totals share the win.`,
}

// SeedCommunity puts the bundled example modules on the community shelf as
// published public games with no owner ("From Aoi's studio"). It is
// idempotent: an unchanged example is left alone; a changed one becomes a
// new version (tables keep the version they started on).
func SeedCommunity(ctx context.Context, pool *pgxpool.Pool) error {
	for _, e := range script.Examples() {
		id := strings.ReplaceAll(e.ID, "_", "-")
		g, err := script.Load(id, e.Source, script.Options{PoolSize: 1})
		if err != nil {
			return fmt.Errorf("seed %s: %w", id, err)
		}
		meta, _ := json.Marshal(g.Meta())
		rules := seedRules[id]
		if rules == "" {
			rules = e.Summary
		}
		err = pgx.BeginFunc(ctx, pool, func(tx pgx.Tx) error {
			var owner string
			var cur int
			err := tx.QueryRow(ctx, `SELECT coalesce(owner_id::text,''), current_version FROM games WHERE id=$1 FOR UPDATE`, id).Scan(&owner, &cur)
			switch {
			case errors.Is(err, pgx.ErrNoRows):
				if _, err := tx.Exec(ctx, `INSERT INTO games (id, owner_id, kind, name, summary, rules_md, status, visibility, current_version)
					VALUES ($1, NULL, 'script', $2, $3, $4, 'published', 'public', 0)`, id, e.Name, e.Summary, rules); err != nil {
					return err
				}
			case err != nil:
				return err
			case owner != "":
				return nil // someone's own game took the id first; leave it
			}
			var src string
			err = tx.QueryRow(ctx, `SELECT source FROM game_versions WHERE game_id=$1 AND version=$2`, id, cur).Scan(&src)
			if err == nil && src == e.Source {
				return nil
			}
			if err != nil && !errors.Is(err, pgx.ErrNoRows) {
				return err
			}
			var next int
			if err := tx.QueryRow(ctx, `SELECT coalesce(max(version),0)+1 FROM game_versions WHERE game_id=$1`, id).Scan(&next); err != nil {
				return err
			}
			report, _ := json.Marshal(map[string]any{"seed": true, "markdown": "Bundled with the platform: a reference module from Aoi's studio."})
			if _, err := tx.Exec(ctx, `INSERT INTO game_versions (game_id, version, source, meta, report) VALUES ($1,$2,$3,$4,$5)`,
				id, next, e.Source, meta, report); err != nil {
				return err
			}
			_, err = tx.Exec(ctx, `UPDATE games SET current_version=$2, name=$3, summary=$4, rules_md=$5, status='published', updated_at=now() WHERE id=$1`,
				id, next, e.Name, e.Summary, rules)
			return err
		})
		if err != nil {
			return fmt.Errorf("seed %s: %w", id, err)
		}
	}
	return nil
}
