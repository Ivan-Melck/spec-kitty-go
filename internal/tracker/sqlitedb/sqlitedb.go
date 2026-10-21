// Package sqlitedb is a minimal, dependency-free reader/writer for the
// SQLite 3 database file format, sufficient for the tracker cache that
// upstream keeps in ~/.spec-kitty/trackers/<scope>.db (three rowid tables
// with automatic PRIMARY KEY / UNIQUE indexes).
//
// It is not a SQL engine. Read loads every row of every table; Write
// serialises a complete database (schema table, table b-trees and the
// automatic index b-trees) from scratch and atomically replaces the file.
// Files written here pass `PRAGMA integrity_check` and are read and updated
// by the real SQLite library (Python's sqlite3), and files written by SQLite
// in rollback-journal mode are readable here.
package sqlitedb

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
)

// PageSize is the page size used for newly written databases (SQLite's
// default).
const PageSize = 4096

// Row is one table row: its rowid and column values (nil, int64, float64,
// string or []byte).
type Row struct {
	RowID  int64
	Values []any
}

// MasterEntry is a row of sqlite_master.
type MasterEntry struct {
	Type, Name, TblName string
	RootPage            int64
	SQL                 any // string or nil
}

// File is a database read from disk.
type File struct {
	ChangeCounter uint32
	SchemaCookie  uint32
	Master        []MasterEntry
	tables        map[string][]Row
}

// HasTable reports whether the schema defines table name.
func (f *File) HasTable(name string) bool {
	_, ok := f.tables[name]
	return ok
}

// Rows returns the rows of table name ordered by rowid.
func (f *File) Rows(name string) []Row { return f.tables[name] }

// ErrCorrupt reports a malformed database file.
var ErrCorrupt = errors.New("file is not a database")

// Read parses the database at path. A missing or empty file yields an empty
// database (SQLite treats a zero-length file as a new database).
func Read(path string) (*File, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return &File{tables: map[string][]Row{}}, nil
		}
		return nil, err
	}
	return Parse(data)
}

// Parse decodes a database image.
func Parse(data []byte) (*File, error) {
	f := &File{tables: map[string][]Row{}}
	if len(data) == 0 {
		return f, nil
	}
	if len(data) < 100 || !bytes.Equal(data[:16], []byte("SQLite format 3\x00")) {
		return nil, ErrCorrupt
	}
	ps := int(binary.BigEndian.Uint16(data[16:18]))
	if ps == 1 {
		ps = 65536
	}
	if ps < 512 || ps&(ps-1) != 0 {
		return nil, ErrCorrupt
	}
	if enc := binary.BigEndian.Uint32(data[56:60]); enc != 0 && enc != 1 {
		return nil, fmt.Errorf("sqlitedb: unsupported text encoding %d", enc)
	}
	r := &reader{data: data, pageSize: ps, usable: ps - int(data[20])}
	f.ChangeCounter = binary.BigEndian.Uint32(data[24:28])
	f.SchemaCookie = binary.BigEndian.Uint32(data[40:44])
	master, err := r.table(1)
	if err != nil {
		return nil, err
	}
	for _, row := range master {
		if len(row.Values) < 5 {
			return nil, ErrCorrupt
		}
		e := MasterEntry{SQL: row.Values[4]}
		e.Type, _ = row.Values[0].(string)
		e.Name, _ = row.Values[1].(string)
		e.TblName, _ = row.Values[2].(string)
		if n, ok := row.Values[3].(int64); ok {
			e.RootPage = n
		}
		f.Master = append(f.Master, e)
	}
	for _, e := range f.Master {
		if e.Type != "table" || e.RootPage <= 0 {
			continue
		}
		rows, err := r.table(int(e.RootPage))
		if err != nil {
			return nil, err
		}
		f.tables[e.Name] = rows
	}
	return f, nil
}

type reader struct {
	data     []byte
	pageSize int
	usable   int
}

func (r *reader) page(n int) ([]byte, error) {
	start := (n - 1) * r.pageSize
	if n < 1 || start+r.pageSize > len(r.data) {
		return nil, ErrCorrupt
	}
	return r.data[start : start+r.pageSize], nil
}

func (r *reader) table(root int) ([]Row, error) {
	var rows []Row
	seen := map[int]bool{}
	var walk func(n int) error
	walk = func(n int) error {
		if seen[n] {
			return ErrCorrupt
		}
		seen[n] = true
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
		switch kind {
		case 0x0D:
			ptrs := off + 8
			for i := 0; i < ncell; i++ {
				c := int(binary.BigEndian.Uint16(p[ptrs+2*i:]))
				if c >= len(p) {
					return ErrCorrupt
				}
				size, k := getVarint(p[c:])
				c += k
				rowid, k := getVarint(p[c:])
				c += k
				payload, err := r.payload(p, c, int(size), r.usable-35)
				if err != nil {
					return err
				}
				vals, err := DecodeRecord(payload)
				if err != nil {
					return err
				}
				rows = append(rows, Row{RowID: int64(rowid), Values: vals})
			}
		case 0x05:
			ptrs := off + 12
			for i := 0; i < ncell; i++ {
				c := int(binary.BigEndian.Uint16(p[ptrs+2*i:]))
				if c+4 > len(p) {
					return ErrCorrupt
				}
				if err := walk(int(binary.BigEndian.Uint32(p[c:]))); err != nil {
					return err
				}
			}
			if err := walk(int(binary.BigEndian.Uint32(p[off+8:]))); err != nil {
				return err
			}
		default:
			return ErrCorrupt
		}
		return nil
	}
	if err := walk(root); err != nil {
		return nil, err
	}
	return rows, nil
}

// payload assembles a cell payload of total size starting at p[c:], following
// the overflow chain when it does not fit locally (maxLocal is X).
func (r *reader) payload(p []byte, c, size, maxLocal int) ([]byte, error) {
	local := localSize(size, r.usable, maxLocal)
	if c+local > len(p) {
		return nil, ErrCorrupt
	}
	out := make([]byte, 0, size)
	out = append(out, p[c:c+local]...)
	if local == size {
		return out, nil
	}
	if c+local+4 > len(p) {
		return nil, ErrCorrupt
	}
	next := int(binary.BigEndian.Uint32(p[c+local:]))
	for len(out) < size {
		op, err := r.page(next)
		if err != nil {
			return nil, err
		}
		next = int(binary.BigEndian.Uint32(op[:4]))
		n := size - len(out)
		if n > r.usable-4 {
			n = r.usable - 4
		}
		out = append(out, op[4:4+n]...)
	}
	return out, nil
}

// localSize is the number of payload bytes stored on the b-tree page.
func localSize(size, usable, maxLocal int) int {
	if size <= maxLocal {
		return size
	}
	minLocal := (usable-12)*32/255 - 23
	k := minLocal + (size-minLocal)%(usable-4)
	if k <= maxLocal {
		return k
	}
	return minLocal
}

func getVarint(b []byte) (uint64, int) {
	var v uint64
	for i := 0; i < 9 && i < len(b); i++ {
		if i == 8 {
			return v<<8 | uint64(b[i]), 9
		}
		v = v<<7 | uint64(b[i]&0x7f)
		if b[i]&0x80 == 0 {
			return v, i + 1
		}
	}
	return v, len(b)
}

func putVarint(v uint64) []byte {
	if v <= 0x7f {
		return []byte{byte(v)}
	}
	if v>>56 != 0 {
		out := make([]byte, 9)
		out[8] = byte(v)
		v >>= 8
		for i := 7; i >= 0; i-- {
			out[i] = byte(v&0x7f) | 0x80
			v >>= 7
		}
		return out
	}
	var tmp [9]byte
	n := 0
	for v != 0 {
		tmp[n] = byte(v&0x7f) | 0x80
		v >>= 7
		n++
	}
	tmp[0] &= 0x7f
	out := make([]byte, n)
	for i := 0; i < n; i++ {
		out[i] = tmp[n-1-i]
	}
	return out
}

// DecodeRecord decodes a record-format payload.
func DecodeRecord(b []byte) ([]any, error) {
	hsize, k := getVarint(b)
	if int(hsize) > len(b) || int(hsize) < k {
		return nil, ErrCorrupt
	}
	var types []uint64
	for pos := k; pos < int(hsize); {
		t, n := getVarint(b[pos:])
		types = append(types, t)
		pos += n
	}
	body := int(hsize)
	vals := make([]any, 0, len(types))
	for _, t := range types {
		var v any
		var n int
		switch {
		case t == 0:
		case t >= 1 && t <= 6:
			n = []int{0, 1, 2, 3, 4, 6, 8}[t]
			if body+n > len(b) {
				return nil, ErrCorrupt
			}
			var x int64
			for i := 0; i < n; i++ {
				x = x<<8 | int64(b[body+i])
			}
			shift := uint(64 - 8*n)
			v = x << shift >> shift
		case t == 7:
			n = 8
			if body+n > len(b) {
				return nil, ErrCorrupt
			}
			v = math.Float64frombits(binary.BigEndian.Uint64(b[body:]))
		case t == 8:
			v = int64(0)
		case t == 9:
			v = int64(1)
		case t >= 12 && t%2 == 0:
			n = int(t-12) / 2
			if body+n > len(b) {
				return nil, ErrCorrupt
			}
			v = append([]byte(nil), b[body:body+n]...)
		case t >= 13:
			n = int(t-13) / 2
			if body+n > len(b) {
				return nil, ErrCorrupt
			}
			v = string(b[body : body+n])
		default:
			return nil, ErrCorrupt
		}
		vals = append(vals, v)
		body += n
	}
	return vals, nil
}

// EncodeRecord encodes values in the record format (schema format 4).
func EncodeRecord(vals []any) []byte {
	var hdr, body []byte
	for _, v := range vals {
		switch t := v.(type) {
		case nil:
			hdr = append(hdr, putVarint(0)...)
		case int:
			st, b := encodeInt(int64(t))
			hdr = append(hdr, putVarint(st)...)
			body = append(body, b...)
		case int64:
			st, b := encodeInt(t)
			hdr = append(hdr, putVarint(st)...)
			body = append(body, b...)
		case float64:
			hdr = append(hdr, 7)
			var b [8]byte
			binary.BigEndian.PutUint64(b[:], math.Float64bits(t))
			body = append(body, b[:]...)
		case string:
			hdr = append(hdr, putVarint(uint64(len(t))*2+13)...)
			body = append(body, t...)
		case []byte:
			hdr = append(hdr, putVarint(uint64(len(t))*2+12)...)
			body = append(body, t...)
		default:
			panic(fmt.Sprintf("sqlitedb: unsupported value %T", v))
		}
	}
	// The header size varint counts itself.
	n := len(hdr) + 1
	if len(putVarint(uint64(n))) > 1 {
		n = len(hdr) + len(putVarint(uint64(len(hdr)+2)))
	}
	out := append(putVarint(uint64(n)), hdr...)
	return append(out, body...)
}

func encodeInt(v int64) (uint64, []byte) {
	switch {
	case v == 0:
		return 8, nil
	case v == 1:
		return 9, nil
	case v >= -128 && v <= 127:
		return 1, []byte{byte(v)}
	case v >= -32768 && v <= 32767:
		return 2, []byte{byte(v >> 8), byte(v)}
	case v >= -8388608 && v <= 8388607:
		return 3, []byte{byte(v >> 16), byte(v >> 8), byte(v)}
	case v >= math.MinInt32 && v <= math.MaxInt32:
		return 4, []byte{byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	case v >= -(1<<47) && v < 1<<47:
		return 5, []byte{byte(v >> 40), byte(v >> 32), byte(v >> 24), byte(v >> 16), byte(v >> 8), byte(v)}
	}
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], uint64(v))
	return 6, b[:]
}

// Compare orders two values like SQLite's BINARY collation:
// NULL < numbers < text < blob.
func Compare(a, b any) int {
	ca, cb := class(a), class(b)
	if ca != cb {
		if ca < cb {
			return -1
		}
		return 1
	}
	switch ca {
	case 0:
		return 0
	case 1:
		fa, fb := num(a), num(b)
		ia, aInt := a.(int64)
		ib, bInt := b.(int64)
		if aInt && bInt {
			switch {
			case ia < ib:
				return -1
			case ia > ib:
				return 1
			}
			return 0
		}
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		}
		return 0
	case 2:
		return bytes.Compare([]byte(a.(string)), []byte(b.(string)))
	}
	return bytes.Compare(a.([]byte), b.([]byte))
}

func class(v any) int {
	switch v.(type) {
	case nil:
		return 0
	case int64, float64, int:
		return 1
	case string:
		return 2
	}
	return 3
}

func num(v any) float64 {
	switch t := v.(type) {
	case int64:
		return float64(t)
	case int:
		return float64(t)
	case float64:
		return t
	}
	return 0
}

// TableSpec describes one rowid table and its automatic indexes.
type TableSpec struct {
	Name string
	// SQL is the CREATE TABLE text stored in sqlite_master.
	SQL string
	// Indexes lists the key columns (positions) of each automatic index
	// sqlite_autoindex_<Name>_<n>, in order.
	Indexes [][]int
}

// Write serialises a complete database with the given schema and rows and
// atomically replaces path (a new file is created 0644; an existing file
// keeps its mode).
func Write(path string, specs []TableSpec, data map[string][]Row, changeCounter, schemaCookie uint32) error {
	img, err := Build(specs, data, changeCounter, schemaCookie)
	if err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
	}
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	if _, err := tmp.Write(img); err != nil {
		tmp.Close()
		os.Remove(name)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Chmod(name, mode); err != nil {
		os.Remove(name)
		return err
	}
	if err := os.Rename(name, path); err != nil {
		os.Remove(name)
		return err
	}
	return nil
}

// Build returns the database image for Write.
func Build(specs []TableSpec, data map[string][]Row, changeCounter, schemaCookie uint32) ([]byte, error) {
	w := &writer{usable: PageSize}
	w.alloc() // page 1
	type rootRef struct {
		spec  TableSpec
		table int
		index []int
	}
	var refs []rootRef
	for _, s := range specs {
		ref := rootRef{spec: s, table: w.alloc()}
		for range s.Indexes {
			ref.index = append(ref.index, w.alloc())
		}
		refs = append(refs, ref)
	}
	var master []Row
	for _, ref := range refs {
		rows := append([]Row(nil), data[ref.spec.Name]...)
		sort.Slice(rows, func(i, j int) bool { return rows[i].RowID < rows[j].RowID })
		if err := w.buildTable(ref.table, rows); err != nil {
			return nil, err
		}
		master = append(master, Row{RowID: int64(len(master) + 1), Values: []any{"table", ref.spec.Name, ref.spec.Name, int64(ref.table), ref.spec.SQL}})
		for i, cols := range ref.spec.Indexes {
			keys := make([][]any, 0, len(rows))
			for _, r := range rows {
				k := make([]any, 0, len(cols)+1)
				for _, c := range cols {
					var v any
					if c < len(r.Values) {
						v = r.Values[c]
					}
					k = append(k, v)
				}
				keys = append(keys, append(k, r.RowID))
			}
			sort.SliceStable(keys, func(a, b int) bool { return compareKeys(keys[a], keys[b]) < 0 })
			if err := w.buildIndex(ref.index[i], keys); err != nil {
				return nil, err
			}
			name := fmt.Sprintf("sqlite_autoindex_%s_%d", ref.spec.Name, i+1)
			master = append(master, Row{RowID: int64(len(master) + 1), Values: []any{"index", name, ref.spec.Name, int64(ref.index[i]), nil}})
		}
	}
	// sqlite_master lives on page 1 (after the 100-byte file header).
	var cells [][]byte
	for _, r := range master {
		c, err := w.tableLeafCell(r)
		if err != nil {
			return nil, err
		}
		cells = append(cells, c)
	}
	if !fits(cells, w.usable-100-8) {
		return nil, fmt.Errorf("sqlitedb: schema does not fit on page 1")
	}
	w.writePage(1, 0x0D, cells, 0)
	h := w.pages[0][:100]
	copy(h, "SQLite format 3\x00")
	binary.BigEndian.PutUint16(h[16:], PageSize)
	h[18], h[19] = 1, 1
	h[20] = 0
	h[21], h[22], h[23] = 64, 32, 32
	binary.BigEndian.PutUint32(h[24:], changeCounter)
	binary.BigEndian.PutUint32(h[28:], uint32(len(w.pages)))
	binary.BigEndian.PutUint32(h[40:], schemaCookie)
	binary.BigEndian.PutUint32(h[44:], 4)
	binary.BigEndian.PutUint32(h[56:], 1)
	binary.BigEndian.PutUint32(h[92:], changeCounter)
	binary.BigEndian.PutUint32(h[96:], 3046000)
	return bytes.Join(w.pages, nil), nil
}

func compareKeys(a, b []any) int {
	for i := 0; i < len(a) && i < len(b); i++ {
		if c := Compare(a[i], b[i]); c != 0 {
			return c
		}
	}
	return len(a) - len(b)
}

type writer struct {
	pages  [][]byte
	usable int
}

func (w *writer) alloc() int {
	w.pages = append(w.pages, make([]byte, PageSize))
	return len(w.pages)
}

func fits(cells [][]byte, space int) bool {
	n := 0
	for _, c := range cells {
		n += len(c) + 2
	}
	return n <= space
}

// writePage lays out a b-tree page with the given cells (content packed at
// the end of the page). right is the right-most child for interior pages.
func (w *writer) writePage(n int, kind byte, cells [][]byte, right int) {
	p := w.pages[n-1]
	off := 0
	if n == 1 {
		off = 100
	}
	hdr := 8
	if kind == 0x05 || kind == 0x02 {
		hdr = 12
	}
	p[off] = kind
	binary.BigEndian.PutUint16(p[off+1:], 0)
	binary.BigEndian.PutUint16(p[off+3:], uint16(len(cells)))
	content := w.usable
	ptr := off + hdr
	for _, c := range cells {
		content -= len(c)
		copy(p[content:], c)
		binary.BigEndian.PutUint16(p[ptr:], uint16(content))
		ptr += 2
	}
	binary.BigEndian.PutUint16(p[off+5:], uint16(content&0xffff))
	p[off+7] = 0
	if hdr == 12 {
		binary.BigEndian.PutUint32(p[off+8:], uint32(right))
	}
}

// spill stores payload locally up to the limit and the rest in a chain of
// overflow pages, returning the on-page bytes (local part + chain pointer).
func (w *writer) spill(payload []byte, maxLocal int) []byte {
	local := localSize(len(payload), w.usable, maxLocal)
	out := append([]byte(nil), payload[:local]...)
	if local == len(payload) {
		return out
	}
	rest := payload[local:]
	first := 0
	prev := -1
	for len(rest) > 0 {
		pg := w.alloc()
		if prev < 0 {
			first = pg
		} else {
			binary.BigEndian.PutUint32(w.pages[prev-1][:4], uint32(pg))
		}
		n := len(rest)
		if n > w.usable-4 {
			n = w.usable - 4
		}
		copy(w.pages[pg-1][4:], rest[:n])
		rest = rest[n:]
		prev = pg
	}
	var b [4]byte
	binary.BigEndian.PutUint32(b[:], uint32(first))
	return append(out, b[:]...)
}

func (w *writer) tableLeafCell(r Row) ([]byte, error) {
	payload := EncodeRecord(r.Values)
	c := putVarint(uint64(len(payload)))
	c = append(c, putVarint(uint64(r.RowID))...)
	return append(c, w.spill(payload, w.usable-35)...), nil
}

type node struct {
	page   int
	maxKey int64 // table trees: largest rowid in the subtree
}

func (w *writer) buildTable(root int, rows []Row) error {
	if len(rows) == 0 {
		w.writePage(root, 0x0D, nil, 0)
		return nil
	}
	cells := make([][]byte, len(rows))
	for i, r := range rows {
		c, err := w.tableLeafCell(r)
		if err != nil {
			return err
		}
		cells[i] = c
	}
	space := w.usable - 8
	if fits(cells, space) {
		w.writePage(root, 0x0D, cells, 0)
		return nil
	}
	var level []node
	for start := 0; start < len(cells); {
		end, used := start, 0
		for end < len(cells) && (used+len(cells[end])+2 <= space || end == start) {
			used += len(cells[end]) + 2
			end++
		}
		pg := w.alloc()
		w.writePage(pg, 0x0D, cells[start:end], 0)
		level = append(level, node{page: pg, maxKey: rows[end-1].RowID})
		start = end
	}
	for {
		space := w.usable - 12
		icell := func(n node) []byte {
			var b [4]byte
			binary.BigEndian.PutUint32(b[:], uint32(n.page))
			return append(b[:], putVarint(uint64(n.maxKey))...)
		}
		// All but the last child become cells; the last is the right pointer.
		var all [][]byte
		for _, n := range level[:len(level)-1] {
			all = append(all, icell(n))
		}
		if fits(all, space) {
			w.writePage(root, 0x05, all, level[len(level)-1].page)
			return nil
		}
		var next []node
		for start := 0; start < len(level); {
			end, used := start, 0
			// take children start..end-1 as cells, child end as right pointer
			for end+1 < len(level) && used+len(icell(level[end]))+2 <= space {
				used += len(icell(level[end])) + 2
				end++
			}
			if end == start && end+1 < len(level) {
				end++
			}
			var cs [][]byte
			for _, n := range level[start:end] {
				cs = append(cs, icell(n))
			}
			pg := w.alloc()
			w.writePage(pg, 0x05, cs, level[end].page)
			next = append(next, node{page: pg, maxKey: level[end].maxKey})
			start = end + 1
		}
		level = next
	}
}
