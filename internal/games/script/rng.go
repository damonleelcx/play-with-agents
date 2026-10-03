package script

// splitmix64 is the runtime's random stream. It is tiny, has a 64-bit state
// that serialises as one number, passes BigCrush, and every state is valid,
// which makes reseeding and determinization trivial.
func splitmix64(s *uint64) uint64 {
	*s += 0x9e3779b97f4a7c15
	z := *s
	z = (z ^ (z >> 30)) * 0xbf58476d1ce4e5b9
	z = (z ^ (z >> 27)) * 0x94d049bb133111eb
	return z ^ (z >> 31)
}

// unitFloat maps a 64-bit draw to [0, 1) with 53 bits of precision, the same
// distribution Math.random() promises.
func unitFloat(x uint64) float64 { return float64(x>>11) / (1 << 53) }

// seedState scrambles a seed so that nearby seeds (1, 2, 3...) start far
// apart in the sequence.
func seedState(seed uint64) uint64 {
	s := seed ^ 0x6a09e667f3bcc909
	return splitmix64(&s)
}
