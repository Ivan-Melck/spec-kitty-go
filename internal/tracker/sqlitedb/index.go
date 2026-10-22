package sqlitedb

import (
	"encoding/binary"
	"fmt"
)

func localOnPage(size, usable, maxLocal int) int {
	l := localSize(size, usable, maxLocal)
	if l < size {
		return l + 4
	}
	return l
}

// buildIndex bulk-loads an index b-tree from sorted keys.
func (w *writer) buildIndex(root int, keys [][]any) error {
	maxLocal := (w.usable-12)*64/255 - 23
	payloads := make([][]byte, len(keys))
	for i, k := range keys {
		payloads[i] = EncodeRecord(k)
	}
	leafSize := func(i int) int {
		p := payloads[i]
		return len(putVarint(uint64(len(p)))) + localOnPage(len(p), w.usable, maxLocal) + 2
	}
	leafCell := func(i int) []byte {
		p := payloads[i]
		return append(putVarint(uint64(len(p))), w.spill(p, maxLocal)...)
	}
	// Plan leaves [start,end) and the divider entry following each but the
	// last.
	type span struct{ start, end int }
	var leaves []span
	var divs []int
	space := w.usable - 8
	n := len(payloads)
	for i := 0; i < n || len(leaves) == 0; {
		start, used := i, 0
		for i < n && (used+leafSize(i) <= space || i == start) {
			used += leafSize(i)
			i++
		}
		leaves = append(leaves, span{start, i})
		if i < n {
			divs = append(divs, i)
			i++
		}
	}
	if len(divs) == len(leaves) && len(divs) > 0 {
		// A trailing divider has no leaf to its right: move it into its own
		// leaf and promote the last entry of the previous leaf instead.
		last := divs[len(divs)-1]
		prev := &leaves[len(leaves)-1]
		if prev.end-prev.start < 2 {
			return fmt.Errorf("sqlitedb: index entries too large")
		}
		prev.end--
		divs[len(divs)-1] = prev.end
		leaves = append(leaves, span{last, last + 1})
	}
	if len(leaves) == 1 {
		var cells [][]byte
		for i := leaves[0].start; i < leaves[0].end; i++ {
			cells = append(cells, leafCell(i))
		}
		w.writePage(root, 0x0A, cells, 0)
		return nil
	}
	children := make([]int, len(leaves))
	for li, s := range leaves {
		var cells [][]byte
		for i := s.start; i < s.end; i++ {
			cells = append(cells, leafCell(i))
		}
		pg := w.alloc()
		w.writePage(pg, 0x0A, cells, 0)
		children[li] = pg
	}
	isize := func(i int) int { return 4 + leafSize(i) }
	icell := func(child, i int) []byte {
		var b [4]byte
		binary.BigEndian.PutUint32(b[:], uint32(child))
		return append(b[:], leafCell(i)...)
	}
	ispace := w.usable - 12
	for {
		total := 0
		for _, d := range divs {
			total += isize(d)
		}
		if total <= ispace {
			var cells [][]byte
			for j, d := range divs {
				cells = append(cells, icell(children[j], d))
			}
			w.writePage(root, 0x02, cells, children[len(children)-1])
			return nil
		}
		// Plan interior nodes: node = children[s..e] with divs[s..e-1] as
		// cells and children[e] as right pointer; divs[e] is promoted.
		type ispan struct{ start, end int }
		var nodes []ispan
		var up []int
		m := len(children)
		for s := 0; s < m; {
			e, used := s, 0
			for e+1 < m && (used+isize(divs[e]) <= ispace || e == s) {
				used += isize(divs[e])
				e++
			}
			nodes = append(nodes, ispan{s, e})
			if e+1 < m {
				up = append(up, divs[e])
			}
			s = e + 1
		}
		// A final node with a single child (no cells) is invalid: borrow.
		if last := nodes[len(nodes)-1]; last.end == last.start && len(nodes) > 1 {
			prev := &nodes[len(nodes)-2]
			if prev.end-prev.start < 2 {
				return fmt.Errorf("sqlitedb: index entries too large")
			}
			prev.end--
			up[len(up)-1] = divs[prev.end]
			nodes[len(nodes)-1].start = prev.end + 1
		}
		next := make([]int, len(nodes))
		for k, nd := range nodes {
			var cells [][]byte
			for j := nd.start; j < nd.end; j++ {
				cells = append(cells, icell(children[j], divs[j]))
			}
			pg := w.alloc()
			w.writePage(pg, 0x02, cells, children[nd.end])
			next[k] = pg
		}
		children, divs = next, up
	}
}
