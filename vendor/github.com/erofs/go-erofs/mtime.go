package erofs

// mtimePolicy derives the superblock timestamp for compact inodes from the
// mtimes of the inodes written. observe is called once per inode; result
// returns the seconds and nanoseconds to store in the superblock.
type mtimePolicy interface {
	observe(sec uint64, nsec uint32)
	result() (sec uint64, nsec uint32)
}

// mostCommonMtime selects the most frequent whole-second mtime, preferring
// the smallest value on a tie, or 0 if no mtime is a whole second. It suits
// layouts where a compact inode's mtime equals the superblock timestamp.
type mostCommonMtime struct {
	counts map[uint64]int
}

func newMostCommonMtime() *mostCommonMtime {
	return &mostCommonMtime{counts: make(map[uint64]int)}
}

func (p *mostCommonMtime) observe(sec uint64, nsec uint32) {
	if nsec == 0 {
		p.counts[sec]++
	}
}

func (p *mostCommonMtime) result() (uint64, uint32) {
	var best uint64
	var bestN int
	for t, n := range p.counts {
		if n > bestN || (n == bestN && t < best) {
			best, bestN = t, n
		}
	}
	return best, 0
}

// compactMtime reports whether e's mtime can be stored in a compact inode
// and returns the value for its i_mtime field. A compact inode reports the
// superblock timestamp plus that field, which this layout leaves 0, so the
// mtime must equal the superblock timestamp.
func (w *erofsWriter) compactMtime(e *erofsEntry) (uint32, bool) {
	return 0, e.mtime == w.buildTime && e.mtimeNs == w.buildTimeNs
}
