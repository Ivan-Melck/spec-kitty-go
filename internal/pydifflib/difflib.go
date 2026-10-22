// Package pydifflib is a faithful port of the parts of CPython's difflib the
// CLI's suggestion texts depend on: SequenceMatcher (no isjunk, autojunk on)
// matching blocks / ratio and get_close_matches.
package pydifflib

import "sort"

// Matcher is difflib.SequenceMatcher(None, a, b) over runes.
type Matcher struct {
	a, b []rune
	b2j  map[rune][]int
}

// New returns SequenceMatcher(None, a, b, autojunk=True).
func New(a, b string) *Matcher {
	m := &Matcher{a: []rune(a), b: []rune(b)}
	m.chainB()
	return m
}

// chainB is SequenceMatcher.__chain_b: index b, then drop "popular"
// elements (autojunk) when len(b) >= 200.
func (m *Matcher) chainB() {
	m.b2j = map[rune][]int{}
	for i, r := range m.b {
		m.b2j[r] = append(m.b2j[r], i)
	}
	n := len(m.b)
	if n >= 200 {
		ntest := n/100 + 1
		for r, idx := range m.b2j {
			if len(idx) > ntest {
				delete(m.b2j, r)
			}
		}
	}
}

// findLongestMatch is find_longest_match(alo, ahi, blo, bhi) without junk.
func (m *Matcher) findLongestMatch(alo, ahi, blo, bhi int) (int, int, int) {
	besti, bestj, bestsize := alo, blo, 0
	j2len := map[int]int{}
	for i := alo; i < ahi; i++ {
		newj2len := map[int]int{}
		for _, j := range m.b2j[m.a[i]] {
			if j < blo {
				continue
			}
			if j >= bhi {
				break
			}
			k := j2len[j-1] + 1
			newj2len[j] = k
			if k > bestsize {
				besti, bestj, bestsize = i-k+1, j-k+1, k
			}
		}
		j2len = newj2len
	}
	// Extend the best match with equal non-junk elements on either side;
	// popular (autojunk) elements are not junk, so they extend here even
	// though they never seed a match (there is no isjunk, so the junk
	// extension passes are no-ops).
	for besti > alo && bestj > blo && m.a[besti-1] == m.b[bestj-1] {
		besti, bestj, bestsize = besti-1, bestj-1, bestsize+1
	}
	for besti+bestsize < ahi && bestj+bestsize < bhi && m.a[besti+bestsize] == m.b[bestj+bestsize] {
		bestsize++
	}
	return besti, bestj, bestsize
}

// Block is one matching block (a[I:I+Size] == b[J:J+Size]).
type Block struct{ I, J, Size int }

// MatchingBlocks is get_matching_blocks() (with the terminal sentinel).
func (m *Matcher) MatchingBlocks() []Block {
	type span struct{ alo, ahi, blo, bhi int }
	queue := []span{{0, len(m.a), 0, len(m.b)}}
	var blocks []Block
	for len(queue) > 0 {
		s := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		i, j, k := m.findLongestMatch(s.alo, s.ahi, s.blo, s.bhi)
		if k == 0 {
			continue
		}
		blocks = append(blocks, Block{i, j, k})
		if s.alo < i && s.blo < j {
			queue = append(queue, span{s.alo, i, s.blo, j})
		}
		if i+k < s.ahi && j+k < s.bhi {
			queue = append(queue, span{i + k, s.ahi, j + k, s.bhi})
		}
	}
	sort.Slice(blocks, func(x, y int) bool {
		if blocks[x].I != blocks[y].I {
			return blocks[x].I < blocks[y].I
		}
		return blocks[x].J < blocks[y].J
	})
	// Collapse adjacent blocks.
	var out []Block
	i1, j1, k1 := 0, 0, 0
	for _, b := range blocks {
		if i1+k1 == b.I && j1+k1 == b.J {
			k1 += b.Size
			continue
		}
		if k1 > 0 {
			out = append(out, Block{i1, j1, k1})
		}
		i1, j1, k1 = b.I, b.J, b.Size
	}
	if k1 > 0 {
		out = append(out, Block{i1, j1, k1})
	}
	return append(out, Block{len(m.a), len(m.b), 0})
}

// Ratio is ratio(): 2*M/T.
func (m *Matcher) Ratio() float64 {
	matches := 0
	for _, b := range m.MatchingBlocks() {
		matches += b.Size
	}
	t := len(m.a) + len(m.b)
	if t == 0 {
		return 1
	}
	return 2 * float64(matches) / float64(t)
}

// GetCloseMatches is difflib.get_close_matches(word, possibilities, n,
// cutoff): the best n candidates scoring >= cutoff, ordered by score then
// by the candidate string (both descending, heapq.nlargest on tuples).
func GetCloseMatches(word string, possibilities []string, n int, cutoff float64) []string {
	if n <= 0 {
		return nil
	}
	type scored struct {
		score float64
		s     string
	}
	var hits []scored
	for _, x := range possibilities {
		// real_quick_ratio / quick_ratio are upper bounds of ratio: the
		// filter result is the ratio test alone.
		if r := New(x, word).Ratio(); r >= cutoff {
			hits = append(hits, scored{r, x})
		}
	}
	sort.SliceStable(hits, func(i, j int) bool {
		if hits[i].score != hits[j].score {
			return hits[i].score > hits[j].score
		}
		return hits[i].s > hits[j].s
	})
	if len(hits) > n {
		hits = hits[:n]
	}
	out := make([]string, len(hits))
	for i, h := range hits {
		out[i] = h.s
	}
	return out
}
