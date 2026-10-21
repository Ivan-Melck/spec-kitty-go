package missionstate

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// ResolveInvocation is core.checkout_identity._resolve_invocation: the
// (invoking_root, canonical_target) pair from a direct parse of the nearest
// .git entry above cwd.
func ResolveInvocation(cwd string) (invoking, canonical string) {
	resolved := absResolved(cwd)
	for c := resolved; ; {
		g := filepath.Join(c, ".git")
		if st, err := os.Stat(g); err == nil {
			if st.IsDir() {
				return c, c
			}
			b, err := os.ReadFile(g)
			if err != nil {
				return c, c
			}
			content := strings.TrimSpace(string(b))
			if !strings.HasPrefix(content, "gitdir:") {
				return c, c
			}
			gitdir := strings.TrimSpace(strings.SplitN(content, ":", 2)[1])
			parent := filepath.Dir(gitdir)
			if filepath.Base(parent) != "worktrees" || !strings.HasSuffix(filepath.Base(filepath.Dir(parent)), ".git") {
				return c, c
			}
			return c, absResolved(filepath.Dir(filepath.Dir(parent)))
		}
		next := filepath.Dir(c)
		if next == c {
			return resolved, resolved
		}
		c = next
	}
}

// WriteRefusal is enforce_primary_write_ownership: the fail-closed refusal
// message when a foreign lane worktree would canonicalize the primary, or "".
func WriteRefusal(cwd, root string) string {
	invoking, canonical := ResolveInvocation(cwd)
	if canonical != absResolved(root) || invoking == canonical {
		return ""
	}
	return "Refusing to write: this invocation does not own the canonical target checkout " + canonical +
		". Run the command from that checkout, or target a checkout this invocation owns."
}

// Disagreement is CheckoutDisagreement.
type Disagreement struct {
	Slug, Artifact          string
	InvokingSHA, PrimarySHA *string
}

// ToObject is CheckoutDisagreement.to_dict.
func (d Disagreement) ToObject() *jsonx.Object {
	return jsonx.NewObject("mission_slug", d.Slug, "artifact", d.Artifact, "invoking_sha256", optS(d.InvokingSHA), "primary_sha256", optS(d.PrimarySHA))
}

// AuditDisagreements is audit_invocation_disagreement: status.json /
// status.events.jsonl mismatches between the invoking checkout and the
// primary root (empty when the invocation is the primary).
func AuditDisagreements(cwd, root string, mission *string) []Disagreement {
	invoking, canonical := ResolveInvocation(cwd)
	root = absResolved(root)
	if invoking == root || canonical != root {
		return nil
	}
	slugs := map[string]bool{}
	for _, base := range []string{invoking, root} {
		entries, err := os.ReadDir(filepath.Join(base, "kitty-specs"))
		if err != nil {
			continue
		}
		for _, e := range entries {
			if st, err := os.Stat(filepath.Join(base, "kitty-specs", e.Name())); err == nil && st.IsDir() {
				slugs[e.Name()] = true
			}
		}
	}
	var names []string
	for s := range slugs {
		if mission == nil || *mission == s {
			names = append(names, s)
		}
	}
	sort.Strings(names)
	var out []Disagreement
	for _, s := range names {
		for _, a := range []string{StatusFile, EventsFile} {
			i := fileFingerprint(filepath.Join(invoking, "kitty-specs", s, a)).sha
			p := fileFingerprint(filepath.Join(root, "kitty-specs", s, a)).sha
			if !eqStrPtr(i, p) {
				out = append(out, Disagreement{s, a, i, p})
			}
		}
	}
	return out
}
