package consolidate

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

type blameLine struct {
	commit, text           string
	commitLine, blamedLine int
}
type diffHunk struct {
	oldStart, oldCount int
	removed, added     []string
}
type canceledSurvival struct {
	path, ref       string
	added, deletion bool
}

func (s canceledSurvival) survives() bool { return s.path != "" && (s.added || s.deletion) }

type lineAttribution struct {
	root              string
	resolved, parents map[string]string
	forward, exact    map[[3]string][]blameLine
	hunks             map[[3]string][]diffHunk
}

func newLineAttribution(root string) *lineAttribution {
	return &lineAttribution{root: root, resolved: map[string]string{}, parents: map[string]string{}, forward: map[[3]string][]blameLine{}, exact: map[[3]string][]blameLine{}, hunks: map[[3]string][]diffHunk{}}
}
func trivialLine(s string) bool {
	for _, c := range s {
		if unicode.IsLetter(c) || unicode.IsNumber(c) {
			return false
		}
	}
	return true
}
func byteWhitespace(c rune) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}
func normalizedLine(s string) string { return strings.Join(strings.FieldsFunc(s, byteWhitespace), " ") }
func (a *lineAttribution) run(args ...string) (string, error) {
	r := git(a.root, args...)
	if r.Code != 0 {
		return "", fmt.Errorf("%s", strings.TrimSpace(r.Stderr))
	}
	return r.Stdout, nil
}
func (a *lineAttribution) resolve(ref string) (string, error) {
	if s, ok := a.resolved[ref]; ok {
		return s, nil
	}
	s, e := a.run("rev-parse", "--verify", ref+"^{commit}")
	s = strings.TrimSpace(s)
	if e == nil {
		a.resolved[ref] = s
	}
	return s, e
}
func (a *lineAttribution) parent(sha string) (string, error) {
	if s, ok := a.parents[sha]; ok {
		return s, nil
	}
	s, e := a.run("rev-list", "--parents", "-n1", sha)
	if e != nil {
		return "", e
	}
	f := strings.Fields(s)
	if len(f) == 0 {
		return "", fmt.Errorf("git rev-list --parents -n1 %s produced no output", sha)
	}
	p := ""
	if len(f) > 1 {
		p = f[1]
	}
	a.parents[sha] = p
	return p, nil
}

var blameHeader = regexp.MustCompile(`^([0-9a-f]{40}|[0-9a-f]{64}) (\d+) (\d+)(?: \d+)?$`)

func (a *lineAttribution) blame(path, rev string, reverse, exact bool) ([]blameLine, error) {
	args := []string{"-c", "diff.algorithm=myers", "-c", "diff.relative=false", "blame", "--porcelain", "--no-textconv", "--ignore-revs-file", "", "--first-parent"}
	if reverse {
		args = append(args, "--reverse")
	}
	if !exact {
		args = append(args, "-w", "-M")
	}
	args = append(args, rev, "--", path)
	s, e := a.run(args...)
	if e != nil {
		return nil, e
	}
	var out []blameLine
	var current *blameLine
	for _, l := range strings.Split(s, "\n") {
		if strings.HasPrefix(l, "\t") {
			if current == nil {
				return nil, fmt.Errorf("git blame --porcelain: a content line has no header")
			}
			current.text = l[1:]
			out = append(out, *current)
			current = nil
		} else if m := blameHeader.FindStringSubmatch(l); m != nil {
			cl, _ := strconv.Atoi(m[2])
			bl, _ := strconv.Atoi(m[3])
			current = &blameLine{commit: m[1], commitLine: cl, blamedLine: bl}
		}
	}
	if current != nil {
		return nil, fmt.Errorf("git blame --porcelain: a header has no content line")
	}
	return out, nil
}
func (a *lineAttribution) finalBlame(ref, path, floor string, exact bool) ([]blameLine, error) {
	key := [3]string{ref, path, floor}
	cache := a.forward
	if exact {
		cache = a.exact
	}
	if v, ok := cache[key]; ok {
		return v, nil
	}
	sha, e := a.resolve(floor)
	if e != nil {
		return nil, e
	}
	p, e := a.parent(sha)
	if e != nil {
		return nil, e
	}
	rev := ref
	if p != "" {
		rev = p + ".." + ref
	}
	v, e := a.blame(path, rev, false, exact)
	if e == nil {
		cache[key] = v
	}
	return v, e
}

var hunkHeader = regexp.MustCompile(`^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@`)

func (a *lineAttribution) canceledHunks(sha, path string, exact bool) ([]diffHunk, error) {
	key := [3]string{sha, path, strconv.FormatBool(exact)}
	if v, ok := a.hunks[key]; ok {
		return v, nil
	}
	resolved, e := a.resolve(sha)
	if e != nil {
		return nil, e
	}
	p, e := a.parent(resolved)
	if e != nil {
		return nil, e
	}
	var out []diffHunk
	if p == "" {
		blob, e := a.blob(sha, path)
		if e != nil {
			return nil, e
		}
		if blob != "" {
			out = []diffHunk{{oldStart: 0, oldCount: 0, added: strings.Split(blob, "\n")}}
		}
	} else {
		args := []string{"-c", "diff.algorithm=myers", "-c", "diff.relative=false", "diff", "-U0", "--no-color", "--no-ext-diff", "--no-textconv", "--no-renames"}
		if !exact {
			args = append(args, "-w")
		}
		args = append(args, p, sha, "--", path)
		s, e := a.run(args...)
		if e != nil {
			return nil, e
		}
		var h *diffHunk
		for _, l := range strings.Split(s, "\n") {
			if m := hunkHeader.FindStringSubmatch(l); m != nil {
				if h != nil {
					out = append(out, *h)
				}
				start, _ := strconv.Atoi(m[1])
				count := 1
				if m[2] != "" {
					count, _ = strconv.Atoi(m[2])
				}
				h = &diffHunk{oldStart: start, oldCount: count}
			} else if h != nil && strings.HasPrefix(l, "-") {
				h.removed = append(h.removed, l[1:])
			} else if h != nil && strings.HasPrefix(l, "+") {
				h.added = append(h.added, l[1:])
			}
		}
		if h != nil {
			out = append(out, *h)
		}
	}
	a.hunks[key] = out
	return out, nil
}
func (a *lineAttribution) blob(ref, path string) (string, error) {
	state, e := stateAt(a.root, ref, path)
	if e != nil {
		return "", e
	}
	if !state.present {
		return "", nil
	}
	return a.run("cat-file", "blob", state.blob)
}
func (a *lineAttribution) finalLocation(newest, path, ref, tip string) (string, string, error) {
	s, e := stateAt(a.root, ref, path)
	if e != nil {
		return "", "", e
	}
	if s.present {
		return path, ref, nil
	}
	raw, e := a.run("-c", "diff.renameLimit=0", "-c", "diff.relative=false", "diff", "--name-status", "-z", "--find-renames", "--diff-filter=R", newest, tip, "--")
	if e != nil {
		return "", "", e
	}
	f := strings.Split(raw, "\x00")
	targets := map[string]bool{}
	for i := 0; i+2 < len(f); i += 3 {
		if strings.HasPrefix(f[i], "R") && f[i+1] == path {
			targets[f[i+2]] = true
		}
	}
	var names []string
	for p := range targets {
		names = append(names, p)
	}
	sort.Strings(names)
	if len(names) > 1 {
		return "", "", fmt.Errorf("'%s' was renamed to more than one path: %s", path, strings.Join(names, ", "))
	}
	if len(names) > 0 {
		return names[0], tip, nil
	}
	return "", ref, nil
}
func (a *lineAttribution) survival(shas []string, path, ref, floor, tip string, other map[string]bool) (s canceledSurvival, err error) {
	defer func() {
		if err != nil {
			err = probeErr("line attribution of '%s' at %s failed: %v", path, ref, err)
		}
	}()
	if len(shas) == 0 {
		return canceledSurvival{ref: ref}, nil
	}
	if tip == "" {
		tip = ref
	}
	s.path, s.ref, err = a.finalLocation(shas[0], path, ref, tip)
	if err != nil || s.path == "" {
		return s, err
	}
	canceled := map[string]bool{}
	for _, sha := range shas {
		r, e := a.resolve(sha)
		if e != nil {
			return s, e
		}
		canceled[r] = true
	}
	final, e := a.finalBlame(s.ref, s.path, floor, false)
	if e != nil {
		return s, e
	}
	for _, l := range final {
		if canceled[l.commit] && !trivialLine(l.text) {
			s.added = true
			return s, nil
		}
	}
	for _, sha := range shas {
		h, e := a.canceledHunks(sha, path, false)
		if e != nil {
			return s, e
		}
		added := map[string]bool{}
		for _, x := range h {
			for _, l := range x.added {
				if !trivialLine(l) {
					added[normalizedLine(l)] = true
				}
			}
		}
		if len(added) > 0 {
			for _, l := range final {
				delete(added, normalizedLine(l.text))
			}
			if len(added) == 0 {
				s.added = true
				return s, nil
			}
		}
	}
	for _, sha := range shas {
		kept, e := a.exactChangeKept(sha, path, final, s.ref, s.path, floor)
		if e != nil {
			return s, e
		}
		if kept {
			s.added = true
			return s, nil
		}
	}
	for _, sha := range shas {
		kept, e := a.modeOrEmptyKept(sha, path, s.ref, s.path)
		if e != nil {
			return s, e
		}
		if kept {
			s.added = true
			return s, nil
		}
	}
	for sha := range other {
		r, e := a.resolve(sha)
		if e != nil {
			return s, e
		}
		canceled[r] = true
	}
	for _, sha := range shas {
		kept, e := a.deletionInEffect(sha, path, s.ref, final, canceled)
		if e != nil {
			return s, e
		}
		if kept {
			s.deletion = true
			return s, nil
		}
	}
	for _, sha := range shas {
		p, e := a.parent(sha)
		if e != nil {
			return s, e
		}
		binary := false
		if p == "" {
			b, e := a.blob(sha, path)
			if e != nil {
				return s, e
			}
			binary = strings.Contains(b, "\x00")
		} else {
			r, e := a.run("diff", "--numstat", p, sha, "--", path)
			if e != nil {
				return s, e
			}
			for _, l := range strings.Split(r, "\n") {
				if strings.HasPrefix(l, "-\t") {
					binary = true
				}
			}
		}
		if binary {
			return s, fmt.Errorf("canceled commit %s changed binary '%s' and a later commit changed it too; line attribution cannot judge a binary file", sha, path)
		}
	}
	return s, nil
}
func (a *lineAttribution) exactChangeKept(sha, path string, final []blameLine, ref, finalPath, floor string) (bool, error) {
	h, e := a.canceledHunks(sha, path, false)
	if e != nil {
		return false, e
	}
	judged := map[string]bool{}
	nontrivial := false
	var texts []string
	for _, x := range h {
		texts = append(texts, x.removed...)
		texts = append(texts, x.added...)
	}
	for _, l := range texts {
		if !trivialLine(l) {
			nontrivial = true
		}
	}
	if nontrivial {
		for _, l := range texts {
			judged[normalizedLine(l)] = true
		}
	}
	exact, e := a.canceledHunks(sha, path, true)
	if e != nil {
		return false, e
	}
	added, removed := map[string]bool{}, map[string]bool{}
	for _, x := range exact {
		for _, l := range x.added {
			if strings.TrimFunc(l, byteWhitespace) != "" && !judged[normalizedLine(l)] {
				added[l] = true
			}
		}
		for _, l := range x.removed {
			if strings.TrimFunc(l, byteWhitespace) != "" && !judged[normalizedLine(l)] {
				removed[l] = true
			}
		}
	}
	if len(added) > 0 {
		r, e := a.resolve(sha)
		if e != nil {
			return false, e
		}
		bl, e := a.finalBlame(ref, finalPath, floor, true)
		if e != nil {
			return false, e
		}
		for _, l := range bl {
			if l.commit == r && added[l.text] {
				return true, nil
			}
		}
		return false, nil
	}
	if len(removed) == 0 {
		return false, nil
	}
	p, e := a.parent(sha)
	if e != nil || p == "" {
		return false, e
	}
	b, e := a.blob(p, path)
	if e != nil {
		return false, e
	}
	before, after := map[string]int{}, map[string]int{}
	for _, l := range strings.Split(b, "\n") {
		before[l]++
	}
	for _, l := range final {
		after[l.text]++
	}
	for l := range removed {
		if after[l] < before[l] {
			return true, nil
		}
	}
	return false, nil
}
func (a *lineAttribution) modeOrEmptyKept(sha, path, ref, finalPath string) (bool, error) {
	entry, e := a.run("ls-tree", sha, "--", path)
	if e != nil || entry == "" {
		return false, e
	}
	p, e := a.parent(sha)
	if e != nil {
		return false, e
	}
	before := ""
	if p != "" {
		before, e = a.run("ls-tree", p, "--", path)
		if e != nil {
			return false, e
		}
	}
	if before == "" {
		b, e := a.blob(sha, path)
		return b == "", e
	}
	final, e := a.run("ls-tree", ref, "--", finalPath)
	if e != nil {
		return false, e
	}
	m := strings.Fields(entry)
	b := strings.Fields(before)
	f := strings.Fields(final)
	return len(m) > 0 && len(b) > 0 && len(f) > 0 && m[0] != b[0] && f[0] == m[0], nil
}
func (a *lineAttribution) deletionInEffect(sha, path, ref string, final []blameLine, canceled map[string]bool) (bool, error) {
	h, e := a.canceledHunks(sha, path, false)
	if e != nil {
		return false, e
	}
	var deletions []diffHunk
	for _, x := range h {
		for _, l := range x.removed {
			if !trivialLine(l) {
				deletions = append(deletions, x)
				break
			}
		}
	}
	if len(deletions) == 0 {
		return false, nil
	}
	p, e := a.parent(sha)
	if e != nil || p == "" {
		return false, e
	}
	end, e := a.resolve(ref)
	if e != nil {
		return false, e
	}
	reverse, e := a.blame(path, p+".."+end, true, false)
	if e != nil {
		return false, e
	}
	survivors := map[int]int{}
	original := map[int]bool{}
	for _, l := range reverse {
		if l.commit == end {
			survivors[l.blamedLine] = l.commitLine
			original[l.commitLine] = true
		}
	}
	for _, x := range deletions {
		before, after := x.oldStart-1, x.oldStart+x.oldCount
		for before > 0 && survivors[before] == 0 {
			before--
		}
		for after <= len(reverse) && survivors[after] == 0 {
			after++
		}
		low := survivors[before]
		high := len(final) + 1
		if after <= len(reverse) {
			high = survivors[after]
		}
		var authored []blameLine
		for i := low; i < max(low, high-1) && i < len(final); i++ {
			if !original[final[i].blamedLine] && !trivialLine(final[i].text) {
				authored = append(authored, final[i])
			}
		}
		var removed []string
		for _, l := range x.removed {
			if !trivialLine(l) {
				removed = append(removed, normalizedLine(l))
			}
		}
		idx := 0
		for _, l := range authored {
			if idx < len(removed) && normalizedLine(l.text) == removed[idx] {
				idx++
			}
		}
		if idx == len(removed) {
			continue
		}
		if len(authored) == 0 {
			return true, nil
		}
		for _, l := range authored {
			if canceled[l.commit] {
				return true, nil
			}
		}
	}
	return false, nil
}
