package mergedriver

import (
	"regexp"
	"strconv"
	"strings"
)

// Python's \s / \S for str patterns (Unicode whitespace).
const (
	pyWS    = `\t\n\v\f\r \x1c-\x1f\x{85}\x{a0}\x{1680}\x{2000}-\x{200a}\x{2028}\x{2029}\x{202f}\x{205f}\x{3000}`
	reS     = `[` + pyWS + `]`
	reNotS  = `[^` + pyWS + `]`
	reSStar = reS + `*`
)

var (
	traceSectionBoundary = regexp.MustCompile(`^(?:#{1,6}` + reS + `+` + reNotS + `.*|<!--` + reSStar + `section:.*-->)` + reSStar + `$`)
	traceFenceMarker     = regexp.MustCompile("^(`{3,}|~{3,})")
	traceSectionID       = regexp.MustCompile(`^<!--` + reSStar + `section:(.*?)` + reSStar + `-->` + reSStar + `$`)
)

type fence struct {
	ch  byte
	run int
}

func matchTraceFence(line string) (fence, bool) {
	m := traceFenceMarker.FindString(line)
	if m == "" {
		return fence{}, false
	}
	return fence{m[0], len(m)}, true
}

// splitTraceBlocks is _split_trace_blocks.
func splitTraceBlocks(text string) [][]string {
	blocks := [][]string{{}}
	var open *fence
	for _, line := range splitLines(text) {
		if open == nil && traceSectionBoundary.MatchString(line) {
			blocks = append(blocks, []string{})
		}
		if f, ok := matchTraceFence(line); ok {
			if open == nil {
				ff := f
				open = &ff
			} else if f.ch == open.ch && f.run >= open.run {
				open = nil
			}
		}
		blocks[len(blocks)-1] = append(blocks[len(blocks)-1], line)
	}
	var out [][]string
	for _, b := range blocks {
		if len(b) > 0 {
			out = append(out, b)
		}
	}
	return out
}

func blockID(b []string) string {
	var sb strings.Builder
	for _, l := range b {
		sb.WriteString(strconv.Itoa(len(l)))
		sb.WriteByte(':')
		sb.WriteString(l)
	}
	return sb.String()
}

func joinBlocks(lines []string) string {
	if len(lines) == 0 {
		return ""
	}
	return strings.Join(lines, "\n") + "\n"
}

// UnionTraceTexts is union_trace_texts: ours' blocks then theirs' blocks,
// dropping a theirs block byte-identical to one already emitted.
func UnionTraceTexts(ours, theirs string) string {
	oursBlocks := splitTraceBlocks(ours)
	seen := map[string]bool{}
	var merged []string
	for _, b := range oursBlocks {
		seen[blockID(b)] = true
		merged = append(merged, b...)
	}
	for _, b := range splitTraceBlocks(theirs) {
		id := blockID(b)
		if seen[id] {
			continue
		}
		seen[id] = true
		merged = append(merged, b...)
	}
	return joinBlocks(merged)
}

func traceDedupKey(b []string) string {
	first := ""
	if len(b) > 0 {
		first = b[0]
	}
	if m := traceSectionID.FindStringSubmatch(first); m != nil {
		return "id:" + m[1]
	}
	return "line:" + first
}

type blockWithOrdinal struct {
	block []string
	key   string // traceDedupKey + ordinal
}

func traceBlocksWithOrdinal(text string) []blockWithOrdinal {
	counts := map[string]int{}
	var out []blockWithOrdinal
	for _, b := range splitTraceBlocks(text) {
		k := traceDedupKey(b)
		n := counts[k]
		counts[k] = n + 1
		out = append(out, blockWithOrdinal{b, k + "\x00" + strconv.Itoa(n)})
	}
	return out
}

func indexTraceBlocks(text string) map[string][]string {
	idx := map[string][]string{}
	for _, bo := range traceBlocksWithOrdinal(text) {
		if _, ok := idx[bo.key]; !ok {
			idx[bo.key] = bo.block
		}
	}
	return idx
}

func sameBlock(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// DropStaleTheirsTraceBlocks is _drop_stale_theirs_trace_blocks.
func DropStaleTheirsTraceBlocks(base, ours, theirs string) string {
	baseBy := indexTraceBlocks(base)
	oursBy := indexTraceBlocks(ours)
	var kept []string
	for _, bo := range traceBlocksWithOrdinal(theirs) {
		bb, inBase := baseBy[bo.key]
		ob, inOurs := oursBy[bo.key]
		theirsUnchanged := inBase && sameBlock(bo.block, bb)
		oursDiverged := inOurs && (!inBase || !sameBlock(ob, bb))
		if theirsUnchanged && oursDiverged {
			continue
		}
		kept = append(kept, bo.block...)
	}
	return joinBlocks(kept)
}

// MergeTraceTexts is the full traces driver merge over in-memory texts.
func MergeTraceTexts(base, ours, theirs string) string {
	return UnionTraceTexts(ours, DropStaleTheirsTraceBlocks(base, ours, theirs))
}

// RunTraces is run_traces_driver (merge-driver-traces).
func RunTraces(basePath, oursPath, theirsPath string) (Outcome, error) {
	base, ours, theirs, err := resolvePaths(basePath, oursPath, theirsPath)
	if err != nil {
		return Outcome{}, err
	}
	var texts [3]string
	for i, p := range []string{base, ours, theirs} {
		if exists(p) {
			if texts[i], err = readTextCrash(p); err != nil {
				return Outcome{}, err
			}
		}
	}
	return Outcome{}, writeText(ours, MergeTraceTexts(texts[0], texts[1], texts[2]))
}
