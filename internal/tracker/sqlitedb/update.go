package sqlitedb

import (
	"encoding/binary"
	"fmt"
	"os"
	"sort"
)

// Update rewrites the given tables (and their automatic indexes) of the
// database at path in place, the way a SQLite transaction leaves the file:
//
//   - pages of every other schema object (foreign tables, user indexes,
//     views' rows in sqlite_master, triggers) are kept byte for byte at their
//     page numbers, as are their sqlite_master rows (rowid, sql);
//   - the header keeps the page size, reserved bytes, user_version,
//     application_id, auto-vacuum / incremental-vacuum fields, text encoding
//     and default cache size; the change counter, schema cookie,
//     version-valid-for and page count are updated; the file never shrinks;
//   - pages released by the rewritten tables join the freelist, and the
//     freelist's pages are reused first (lowest page number first).
//
// Cell placement inside the rewritten tables' pages follows sqlitedb's bulk
// loader rather than SQLite's incremental balancing, so those pages are not
// byte-identical to SQLite's; their content is. A missing or empty file is
// written from scratch (Write).
func Update(path string, specs []TableSpec, data map[string][]Row, changeCounter, schemaCookie uint32) error {
	old, err := os.ReadFile(path)
	if err != nil && !os.IsNotExist(err) {
		return err
	}
	img, err := UpdateImage(old, specs, data, changeCounter, schemaCookie)
	if err != nil {
		return err
	}
	return replaceFile(path, img)
}

// UpdateImage is Update on a database image.
func UpdateImage(old []byte, specs []TableSpec, data map[string][]Row, changeCounter, schemaCookie uint32) ([]byte, error) {
	if len(old) == 0 {
		return Build(specs, data, changeCounter, schemaCookie)
	}
	f, err := Parse(old)
	if err != nil {
		return nil, err
	}
	_ = f
	ps := int(binary.BigEndian.Uint16(old[16:18]))
	if ps == 1 {
		ps = 65536
	}
	r := &reader{data: old, pageSize: ps, usable: ps - int(old[20])}
	npages := len(old) / ps
	if hdr := int(binary.BigEndian.Uint32(old[28:32])); hdr > 0 && hdr <= npages &&
		binary.BigEndian.Uint32(old[24:28]) == binary.BigEndian.Uint32(old[92:96]) {
		npages = hdr
	}
	p1, err := r.page(1)
	if err != nil {
		return nil, err
	}
	if p1[100] != 0x0D {
		return nil, fmt.Errorf("sqlitedb: sqlite_master spans several pages")
	}
	master, err := r.table(1)
	if err != nil {
		return nil, err
	}
	ours := map[string]bool{}
	for _, s := range specs {
		ours[s.Name] = true
		for i := range s.Indexes {
			ours[AutoIndexName(s.Name, i)] = true
		}
	}
	// pages to release: the rewritten objects' trees and the freelist
	free := map[int]bool{}
	for _, row := range master {
		name, _ := row.Values[1].(string)
		root, _ := row.Values[3].(int64)
		if !ours[name] || root <= 0 {
			continue
		}
		pages, err := r.treePages(int(root))
		if err != nil {
			return nil, err
		}
		for _, p := range pages {
			free[p] = true
		}
	}
	fl, err := r.freelistPages(int(binary.BigEndian.Uint32(old[32:36])))
	if err != nil {
		return nil, err
	}
	for _, p := range fl {
		free[p] = true
	}
	w := &writer{usable: r.usable, pageSize: ps}
	for i := 0; i < npages; i++ {
		pg := make([]byte, ps)
		copy(pg, old[i*ps:])
		w.pages = append(w.pages, pg)
	}
	for p := range free {
		w.pool = append(w.pool, p)
	}
	sort.Ints(w.pool)
	objs, err := w.buildObjects(specs, data)
	if err != nil {
		return nil, err
	}
	built := map[string][]any{}
	for _, o := range objs {
		built[o[1].(string)] = o
	}
	var rows []Row
	var maxID int64
	for _, row := range master {
		if row.RowID > maxID {
			maxID = row.RowID
		}
		name, _ := row.Values[1].(string)
		if o, ok := built[name]; ok {
			vals := append([]any(nil), row.Values...)
			vals[3] = o[3]
			rows = append(rows, Row{RowID: row.RowID, Values: vals})
			delete(built, name)
			continue
		}
		rows = append(rows, row)
	}
	for _, o := range objs {
		if _, pending := built[o[1].(string)]; pending {
			maxID++
			rows = append(rows, Row{RowID: maxID, Values: o})
		}
	}
	sort.Slice(rows, func(i, j int) bool { return rows[i].RowID < rows[j].RowID })
	var cells [][]byte
	for _, row := range rows {
		c, err := w.tableLeafCell(row)
		if err != nil {
			return nil, err
		}
		cells = append(cells, c)
	}
	if !fits(cells, w.usable-100-8) {
		return nil, fmt.Errorf("sqlitedb: schema does not fit on page 1")
	}
	hdr := append([]byte(nil), w.pages[0][:100]...)
	clear(w.pages[0])
	copy(w.pages[0], hdr)
	w.writePage(1, 0x0D, cells, 0)
	// leftover pages form the freelist: one trunk per (usable/4 - 2) leaves
	trunk0 := 0
	if len(w.pool) > 0 {
		perTrunk := w.usable/4 - 2
		pool := w.pool
		w.pool = nil
		var trunks []int
		var leaves [][]int
		for len(pool) > 0 {
			t := pool[0]
			pool = pool[1:]
			n := min(perTrunk, len(pool))
			trunks = append(trunks, t)
			leaves = append(leaves, pool[:n])
			pool = pool[n:]
		}
		for i, t := range trunks {
			pg := w.pages[t-1]
			clear(pg)
			next := 0
			if i+1 < len(trunks) {
				next = trunks[i+1]
			}
			binary.BigEndian.PutUint32(pg[0:], uint32(next))
			binary.BigEndian.PutUint32(pg[4:], uint32(len(leaves[i])))
			for j, l := range leaves[i] {
				binary.BigEndian.PutUint32(pg[8+4*j:], uint32(l))
			}
		}
		trunk0 = trunks[0]
		total := 0
		for i := range trunks {
			total += 1 + len(leaves[i])
		}
		binary.BigEndian.PutUint32(w.pages[0][36:], uint32(total))
	} else {
		binary.BigEndian.PutUint32(w.pages[0][36:], 0)
	}
	h := w.pages[0]
	binary.BigEndian.PutUint32(h[32:], uint32(trunk0))
	binary.BigEndian.PutUint32(h[24:], changeCounter)
	binary.BigEndian.PutUint32(h[28:], uint32(len(w.pages)))
	binary.BigEndian.PutUint32(h[40:], schemaCookie)
	binary.BigEndian.PutUint32(h[92:], changeCounter)
	binary.BigEndian.PutUint32(h[96:], 3046000)
	out := make([]byte, 0, len(w.pages)*ps)
	for _, pg := range w.pages {
		out = append(out, pg...)
	}
	return out, nil
}

// treePages lists the pages of the b-tree rooted at root (interior, leaf
// and overflow pages).
func (r *reader) treePages(root int) ([]int, error) {
	var out []int
	seen := map[int]bool{}
	var walk func(n int) error
	walk = func(n int) error {
		if seen[n] {
			return ErrCorrupt
		}
		seen[n] = true
		out = append(out, n)
		p, err := r.page(n)
		if err != nil {
			return err
		}
		off := 0
		if n == 1 {
			off = 100
		}
		kind := p[off]
		ncell := int(binary.BigEndian.Uint16(p[off+3:]))
		hdr := 8
		if kind == 0x05 || kind == 0x02 {
			hdr = 12
		}
		indexMax := (r.usable-12)*64/255 - 23
		for i := 0; i < ncell; i++ {
			c := int(binary.BigEndian.Uint16(p[off+hdr+2*i:]))
			if c+4 > len(p) {
				return ErrCorrupt
			}
			switch kind {
			case 0x05:
				if err := walk(int(binary.BigEndian.Uint32(p[c:]))); err != nil {
					return err
				}
			case 0x0D:
				size, k := getVarint(p[c:])
				c += k
				_, k = getVarint(p[c:])
				c += k
				if err := r.overflowPages(p, c, int(size), r.usable-35, seen, &out); err != nil {
					return err
				}
			case 0x02, 0x0A:
				if kind == 0x02 {
					if err := walk(int(binary.BigEndian.Uint32(p[c:]))); err != nil {
						return err
					}
					c += 4
				}
				size, k := getVarint(p[c:])
				c += k
				if err := r.overflowPages(p, c, int(size), indexMax, seen, &out); err != nil {
					return err
				}
			default:
				return ErrCorrupt
			}
		}
		if hdr == 12 {
			return walk(int(binary.BigEndian.Uint32(p[off+8:])))
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return out, nil
}

func (r *reader) overflowPages(p []byte, c, size, maxLocal int, seen map[int]bool, out *[]int) error {
	local := localSize(size, r.usable, maxLocal)
	if local == size {
		return nil
	}
	if c+local+4 > len(p) {
		return ErrCorrupt
	}
	next := int(binary.BigEndian.Uint32(p[c+local:]))
	for left := size - local; left > 0; left -= r.usable - 4 {
		if seen[next] {
			return ErrCorrupt
		}
		seen[next] = true
		*out = append(*out, next)
		op, err := r.page(next)
		if err != nil {
			return err
		}
		next = int(binary.BigEndian.Uint32(op[:4]))
	}
	return nil
}

// freelistPages lists the trunk and leaf pages of the freelist.
func (r *reader) freelistPages(trunk int) ([]int, error) {
	var out []int
	seen := map[int]bool{}
	for trunk != 0 {
		if seen[trunk] {
			return nil, ErrCorrupt
		}
		seen[trunk] = true
		p, err := r.page(trunk)
		if err != nil {
			return nil, err
		}
		out = append(out, trunk)
		n := int(binary.BigEndian.Uint32(p[4:]))
		if 8+4*n > len(p) {
			return nil, ErrCorrupt
		}
		for i := 0; i < n; i++ {
			out = append(out, int(binary.BigEndian.Uint32(p[8+4*i:])))
		}
		trunk = int(binary.BigEndian.Uint32(p[:4]))
	}
	return out, nil
}
