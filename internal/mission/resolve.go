package mission

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
)

// Resolved is a uniquely resolved mission (ResolvedMission).
type Resolved struct {
	MissionID string
	Slug      string // directory name under kitty-specs/
	Dir       string // absolute primary mission directory
	Mid8      string
}

// AmbiguousError is the identity resolver's AmbiguousHandleError.
type AmbiguousError struct {
	Handle     string
	Candidates []Resolved
}

func (e *AmbiguousError) Error() string {
	var b strings.Builder
	fmt.Fprintf(&b, "Error: mission handle \"%s\" matches multiple missions.\n\nCandidates:\n", e.Handle)
	for _, c := range e.Candidates {
		fmt.Fprintf(&b, "  %s (mission_id %s, mid8 %s)\n", c.Slug, c.MissionID, c.Mid8)
	}
	b.WriteString("\nRe-run with a more specific handle:\n")
	for _, c := range e.Candidates {
		fmt.Fprintf(&b, "  spec-kitty <command> --mission %s\n", c.Mid8)
		fmt.Fprintf(&b, "  spec-kitty <command> --mission %s\n", c.Slug)
	}
	b.WriteString("\nFor JSON output of all candidates:\n")
	fmt.Fprintf(&b, "  spec-kitty doctor identity --mission %s --json", e.Handle)
	return b.String()
}

// CandidatesJSON renders the to_dict() candidate list.
func (e *AmbiguousError) CandidatesJSON() []any {
	out := []any{}
	for _, c := range e.Candidates {
		out = append(out, jsonx.NewObject("mission_id", c.MissionID, "mid8", c.Mid8, "slug", c.Slug, "feature_dir", c.Dir))
	}
	return out
}

// NotFoundError is MissionNotFoundError.
type NotFoundError struct{ Handle string }

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("No mission found for handle \"%s\". If this is a legacy mission whose meta.json lacks a mission_id, run `spec-kitty migrate backfill-identity` to backfill it.", e.Handle)
}

// HumanMessage is the human-mode rendering used by resolve_mission_handle.
func (e *NotFoundError) HumanMessage() string {
	return fmt.Sprintf("No mission found for handle \"%s\". Check that the handle is correct and that the mission exists in kitty-specs/.", e.Handle)
}

// SelectorAmbiguousError is the read-path MissionSelectorAmbiguous.
type SelectorAmbiguousError struct {
	Handle     string
	Candidates []string
}

// Code is the stable error code.
func (e *SelectorAmbiguousError) Code() string { return "MISSION_AMBIGUOUS_SELECTOR" }

func (e *SelectorAmbiguousError) Error() string {
	return fmt.Sprintf("Mission handle %s matches multiple missions: %s. Re-run with a more specific handle (full slug or full mission_id).",
		PyRepr(e.Handle), strings.Join(e.Candidates, ", "))
}

// PyRepr renders a string like Python repr().
func PyRepr(s string) string {
	q := "'"
	if strings.Contains(s, "'") && !strings.Contains(s, `"`) {
		q = `"`
	}
	var b strings.Builder
	b.WriteString(q)
	for _, r := range s {
		switch {
		case r == '\\':
			b.WriteString(`\\`)
		case string(r) == q:
			b.WriteString(`\` + q)
		case r == '\n':
			b.WriteString(`\n`)
		case r == '\r':
			b.WriteString(`\r`)
		case r == '\t':
			b.WriteString(`\t`)
		case r < 0x20 || r == 0x7f:
			fmt.Fprintf(&b, `\x%02x`, r)
		default:
			b.WriteRune(r)
		}
	}
	b.WriteString(q)
	return b.String()
}

func missionDirs(root string) []string {
	entries, err := os.ReadDir(project.Specs(root))
	if err != nil {
		return nil
	}
	var names []string
	for _, e := range entries {
		if e.IsDir() || isDirLink(filepath.Join(project.Specs(root), e.Name())) {
			names = append(names, e.Name())
		}
	}
	sort.Strings(names)
	return names
}

func isDirLink(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

// Index walks kitty-specs/ and returns identity-bearing missions.
func Index(root string) []Resolved {
	var out []Resolved
	for _, name := range missionDirs(root) {
		dir := project.Specs(root, name)
		meta, err := ReadMeta(dir)
		if err != nil || meta == nil {
			continue
		}
		id, _ := meta.Get("mission_id")
		ids, ok := id.(string)
		if !ok || ids == "" {
			continue
		}
		out = append(out, Resolved{MissionID: ids, Slug: name, Dir: dir, Mid8: ResolveMid8(name, ids)})
	}
	return out
}

func isDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// ResolveIdentity runs the identity resolver ladder (resolve_mission):
// ULID → mid8 prefix → exact dir name → NNN-stripped name → numeric prefix.
func ResolveIdentity(root, handle string) (*Resolved, error) {
	ms := Index(root)
	pick := func(m []Resolved) (*Resolved, error) {
		switch len(m) {
		case 1:
			return &m[0], nil
		case 0:
			return nil, &NotFoundError{Handle: handle}
		}
		return nil, &AmbiguousError{Handle: handle, Candidates: m}
	}
	filter := func(f func(Resolved) bool) []Resolved {
		var out []Resolved
		for _, m := range ms {
			if f(m) {
				out = append(out, m)
			}
		}
		return out
	}
	if resolverULIDRe.MatchString(handle) {
		return pick(filter(func(m Resolved) bool { return m.MissionID == handle }))
	}
	if resolverMid8Re.MatchString(handle) {
		return pick(filter(func(m Resolved) bool { return strings.HasPrefix(m.MissionID, handle) }))
	}
	if m := filter(func(m Resolved) bool { return m.Slug == handle }); len(m) > 0 {
		return pick(m)
	}
	if !isDigits(handle) {
		if m := filter(func(m Resolved) bool { return StripNumericPrefix(m.Slug) == handle }); len(m) > 0 {
			return pick(m)
		}
		return nil, &NotFoundError{Handle: handle}
	}
	return pick(filter(func(m Resolved) bool {
		p := prefix3Re.FindStringSubmatch(m.Slug)
		return p != nil && p[1] == handle
	}))
}

// BareModernDirName maps a bare human slug to its single composed
// <slug>-<mid8> directory (resolve_bare_modern_mission_dir_name).
func BareModernDirName(root, slug string) (string, error) {
	if Mid8FromSlug(slug) != "" {
		return "", nil
	}
	pattern := filepath.Join(project.Specs(root), globEscape(slug)+"-*", MetaFile)
	paths, _ := filepath.Glob(pattern)
	sort.Strings(paths)
	var matches []string
	for _, p := range paths {
		name := filepath.Base(filepath.Dir(p))
		if Mid8FromSlug(name) != "" {
			matches = append(matches, name)
		}
	}
	if len(matches) > 1 {
		sort.Strings(matches)
		return "", &SelectorAmbiguousError{Handle: slug, Candidates: matches}
	}
	if len(matches) == 0 {
		return "", nil
	}
	return matches[0], nil
}

func globEscape(s string) string {
	r := strings.NewReplacer(`[`, `\[`, `*`, `\*`, `?`, `\?`, `\`, `\\`)
	return r.Replace(s)
}

// ResolveHandle resolves a `--mission` handle the way the agent tasks/status
// commands do (_find_mission_slug): literal dir with meta.json, then the
// identity ladder (ambiguity → SelectorAmbiguousError), then the bare-modern
// fold, then the literal dir, else the identity resolver's error
// (*NotFoundError / *AmbiguousError). Returns the mission directory name.
func ResolveHandle(root, handle string) (string, error) {
	if SafePathSegment(handle) {
		if st, err := os.Stat(filepath.Join(project.Specs(root, handle), MetaFile)); err == nil && !st.IsDir() {
			return handle, nil
		}
	}
	r, err := ResolveIdentity(root, handle)
	if err == nil {
		return r.Slug, nil
	}
	if amb, ok := err.(*AmbiguousError); ok {
		var c []string
		for _, x := range amb.Candidates {
			c = append(c, x.Slug)
		}
		return "", &SelectorAmbiguousError{Handle: handle, Candidates: c}
	}
	name, berr := BareModernDirName(root, handle)
	if berr != nil {
		return "", berr
	}
	if name != "" {
		return name, nil
	}
	if SafePathSegment(handle) && isDirLink(project.Specs(root, handle)) {
		return handle, nil
	}
	return "", err
}

// Listing is one row of the selection listing (MissionListing).
type Listing struct {
	Slug         string
	Mid8         string // "" for legacy missions without mission_id
	FriendlyName string
}

// List returns every kitty-specs/ directory carrying spec.md or meta.json.
func List(root string) []Listing {
	var out []Listing
	for _, name := range missionDirs(root) {
		dir := project.Specs(root, name)
		_, e1 := os.Stat(filepath.Join(dir, "spec.md"))
		_, e2 := os.Stat(filepath.Join(dir, MetaFile))
		if e1 != nil && e2 != nil {
			continue
		}
		meta := ReadMetaOrEmpty(dir)
		id, _ := meta.Get("mission_id")
		ids, _ := id.(string)
		fn := name
		if v, ok := meta.Get("friendly_name"); ok && truthy(v) {
			fn = fmt.Sprint(v)
		}
		out = append(out, Listing{Slug: name, Mid8: ResolveMid8(name, ids), FriendlyName: fn})
	}
	return out
}
