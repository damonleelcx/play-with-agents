package script

import (
	"errors"
	"fmt"
)

// Error classes. Every module fault matches ErrModule; the more specific
// classes also match their own sentinel so callers (the playtester, the
// Engineer agent's tool) can tell a crash from a timeout from a malformed
// return value.
var (
	// ErrModule: the module threw, or otherwise misbehaved. A throw inside
	// apply for a move that legal() offered is a module bug, not an illegal
	// move, because legality has already been checked by the runtime.
	ErrModule = errors.New("game module error")
	// ErrTimeout: a call exceeded its time budget and was interrupted.
	ErrTimeout = errors.New("game module timed out")
	// ErrInvalidOutput: a call returned a value that breaks the contract
	// (wrong shape, seat out of range, state too large, ...).
	ErrInvalidOutput = errors.New("game module returned invalid output")
)

// ModuleError describes a failed call into the module.
type ModuleError struct {
	Func    string // module function, e.g. "apply"
	Message string // JS error message with the position of the throw
	Stack   string // JS stack trace, truncated
	Timeout bool
}

func (e *ModuleError) Error() string {
	if e.Timeout {
		return fmt.Sprintf("%s: %s timed out: %s", ErrTimeout, e.Func, e.Message)
	}
	return fmt.Sprintf("%s: %s threw: %s", ErrModule, e.Func, e.Message)
}

func (e *ModuleError) Is(target error) bool {
	return target == ErrModule || (e.Timeout && target == ErrTimeout)
}

// OutputError is a contract violation in a value the module returned. Path
// points at the offending part, e.g. "view.board.cells[2][7].piece.color".
type OutputError struct {
	Func    string
	Path    string
	Message string
}

func (e *OutputError) Error() string {
	if e.Path == "" {
		return fmt.Sprintf("%s: %s: %s", ErrInvalidOutput, e.Func, e.Message)
	}
	return fmt.Sprintf("%s: %s: %s %s", ErrInvalidOutput, e.Func, e.Path, e.Message)
}

func (e *OutputError) Is(target error) bool {
	return target == ErrInvalidOutput || target == ErrModule
}

func outErr(fn, path, format string, a ...any) error {
	return &OutputError{Func: fn, Path: path, Message: fmt.Sprintf(format, a...)}
}
