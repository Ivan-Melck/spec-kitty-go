package planning

// Copy of internal/cli/difflib.go (unexported there).

// seqRatio is difflib.SequenceMatcher(None, a, b).ratio() for short
// strings (no autojunk below 200 characters).
func seqRatio(a, b string) float64 {
	ra, rb := []rune(a), []rune(b)
	total := len(ra) + len(rb)
	if total == 0 {
		return 1
	}
	return 2 * float64(matchedRunes(ra, rb)) / float64(total)
}

// matchedRunes is the sum of SequenceMatcher.get_matching_blocks sizes.
func matchedRunes(a, b []rune) int {
	b2j := map[rune][]int{}
	for j, r := range b {
		b2j[r] = append(b2j[r], j)
	}
	longest := func(alo, ahi, blo, bhi int) (int, int, int) {
		besti, bestj, bestsize := alo, blo, 0
		j2len := map[int]int{}
		for i := alo; i < ahi; i++ {
			newj2len := map[int]int{}
			for _, j := range b2j[a[i]] {
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
		return besti, bestj, bestsize
	}
	type span struct{ alo, ahi, blo, bhi int }
	queue := []span{{0, len(a), 0, len(b)}}
	total := 0
	for len(queue) > 0 {
		s := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		i, j, k := longest(s.alo, s.ahi, s.blo, s.bhi)
		if k == 0 {
			continue
		}
		total += k
		if s.alo < i && s.blo < j {
			queue = append(queue, span{s.alo, i, s.blo, j})
		}
		if i+k < s.ahi && j+k < s.bhi {
			queue = append(queue, span{i + k, s.ahi, j + k, s.bhi})
		}
	}
	return total
}

// closestMatch is difflib.get_close_matches(word, possibilities, n=1,
// cutoff=cutoff)[0] ("" when nothing scores at least cutoff; ties: the
// larger string).
func closestMatch(word string, possibilities []string, cutoff float64) string {
	best, bestScore := "", -1.0
	for _, p := range possibilities {
		r := seqRatio(p, word)
		if r < cutoff {
			continue
		}
		if r > bestScore || (r == bestScore && p > best) {
			best, bestScore = p, r
		}
	}
	return best
}
