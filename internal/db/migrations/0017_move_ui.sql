-- The UI hint of each applied move (from/to/cell/zone...), as legal() offered
-- it: the board animates the moves that were actually played, in order,
-- instead of guessing from a before/after diff.
ALTER TABLE table_moves ADD COLUMN ui jsonb;
