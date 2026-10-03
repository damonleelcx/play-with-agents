//go:build race

package holdem

// raceEnabled relaxes wall-clock assertions: the race detector slows the
// engine by an order of magnitude.
const raceEnabled = true
