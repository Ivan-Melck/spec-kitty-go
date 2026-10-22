// Package toolsurface ports upstream's tool-surface contract
// (specify_cli.tool_surface) and the owner assessments that the upgrade
// finalizer, `upgrade --plan-json`, `doctor tool-surfaces` and plugin bundle
// staging consume: every executable owner (global assets, managed charter
// skills, command skills, provisioning, agent profiles, session presence,
// native config, plugin bundles) prepares PhysicalEffects and Dispositions
// against disk before anything is written.
package toolsurface

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"path"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Phases is operations.PHASES (effect sort order).
var Phases = []string{"global_bootstrap", "migrations", "metadata", "provisioning", "manifest_repair", "surface_repair"}

func phaseIndex(p string) int {
	for i, x := range Phases {
		if x == p {
			return i
		}
	}
	return len(Phases)
}

// FileState is operations.FileState. Mode < 0 means None (absent nodes);
// mtime is never part of a serialized state and is not tracked.
type FileState struct {
	Kind   string // absent, file, directory, symlink
	SHA256 string
	Target string
	Mode   int
}

// Absent is FileState("absent").
func Absent() FileState { return FileState{Kind: "absent", Mode: -1} }

// FileOf is FileState("file", sha256=digest, mode=mode).
func FileOf(digest string, mode int) FileState {
	return FileState{Kind: "file", SHA256: digest, Mode: mode}
}

// DirOf is FileState("directory", mode=mode).
func DirOf(mode int) FileState { return FileState{Kind: "directory", Mode: mode} }

// JSON is upgrade._state_json.
func (s FileState) JSON() *jsonx.Object {
	var mode any
	if s.Mode >= 0 {
		mode = s.Mode
	}
	return jsonx.NewObject("kind", s.Kind, "sha256", nullable(s.SHA256), "target", nullable(s.Target), "mode", mode)
}

func nullable(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// Digest is the hex sha256 of b.
func Digest(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// Root is operations.OperationRoot.
type Root struct {
	ID    string
	Scope string // global, project, worktree
	Path  string
}

// Proof is operations.OwnershipProof.
type Proof struct{ Kind, Reference string }

// Action is operations._action (empty = no change).
func Action(before, after FileState) string {
	switch {
	case before.Kind == "absent":
		if after.Kind != "absent" {
			return "create"
		}
		return ""
	case after.Kind == "absent":
		return "delete"
	case before.Kind != after.Kind:
		return "replace"
	case before.SHA256 != after.SHA256:
		return "update"
	case before.Target != after.Target:
		return "retarget"
	case before.Mode != after.Mode:
		return "chmod"
	}
	return ""
}

// Effect is operations.PhysicalEffect.
type Effect struct {
	Owner         string
	Phase         string
	Root          Root
	Path          string // root-relative POSIX
	Action        string
	Before, After FileState
	Reason        string
	Ownership     []Proof
	LogicalOwners []string
	SurfaceIDs    []string
}

// NewEffect builds a normalized effect (sorted unique owners/ids/proofs).
func NewEffect(owner, phase string, root Root, rel, action string, before, after FileState, reason string, proofs []Proof, logical, surfaceIDs []string) Effect {
	e := Effect{Owner: owner, Phase: phase, Root: root, Path: rel, Action: action, Before: before, After: after, Reason: reason,
		Ownership: proofs, LogicalOwners: logical, SurfaceIDs: surfaceIDs}
	e.normalize()
	return e
}

func uniqueSorted(in []string) []string {
	seen := map[string]bool{}
	out := []string{}
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

func (e *Effect) normalize() {
	e.LogicalOwners = uniqueSorted(e.LogicalOwners)
	e.SurfaceIDs = uniqueSorted(e.SurfaceIDs)
	seen := map[Proof]bool{}
	var ps []Proof
	for _, p := range e.Ownership {
		if !seen[p] {
			seen[p] = true
			ps = append(ps, p)
		}
	}
	sort.Slice(ps, func(i, j int) bool {
		if ps[i].Kind != ps[j].Kind {
			return ps[i].Kind < ps[j].Kind
		}
		return ps[i].Reference < ps[j].Reference
	})
	e.Ownership = ps
}

// ID is PhysicalEffect.id: sha256 of the compact JSON identity tuple.
func (e Effect) ID() string {
	identity := []any{e.Phase, e.Owner, e.Root.ID, e.Path, e.Action}
	return Digest([]byte(jsonx.Dumps(identity, jsonx.Options{Indent: -1, EnsureASCII: true, Compact: true})))
}

// Destination is the lexical physical destination.
func (e Effect) Destination() string {
	return filepath.Join(e.Root.Path, filepath.FromSlash(e.Path))
}

func (e Effect) sortKey(o Effect) bool {
	pi, pj := phaseIndex(e.Phase), phaseIndex(o.Phase)
	if pi != pj {
		return pi < pj
	}
	if e.Owner != o.Owner {
		return e.Owner < o.Owner
	}
	if e.Root.ID != o.Root.ID {
		return e.Root.ID < o.Root.ID
	}
	return e.Path < o.Path
}

func sameState(a, b FileState) bool { return a == b }

// Coalesce is operations.coalesce_effects: deduplicate equivalent intent at
// one destination (merging logical claims), refusing cross-owner conflicts.
func Coalesce(effects []Effect) ([]Effect, error) {
	sorted := append([]Effect(nil), effects...)
	sort.SliceStable(sorted, func(i, j int) bool { return sorted[i].sortKey(sorted[j]) })
	roots := map[string]string{}
	byDest := map[string]int{}
	var out []Effect
	for _, e := range sorted {
		if p, ok := roots[e.Root.ID]; ok && p != e.Root.Path {
			return nil, fmt.Errorf("Root identity conflict: one root ID names distinct resolved paths")
		}
		roots[e.Root.ID] = e.Root.Path
		dest := e.Destination()
		i, ok := byDest[dest]
		if !ok {
			byDest[dest] = len(out)
			out = append(out, e)
			continue
		}
		prev := out[i]
		var differing []string
		if prev.Owner != e.Owner {
			differing = append(differing, "owner")
		}
		if prev.Phase != e.Phase {
			differing = append(differing, "phase")
		}
		if prev.Action != e.Action {
			differing = append(differing, "action")
		}
		if !sameState(prev.Before, e.Before) {
			differing = append(differing, "before")
		}
		if !sameState(prev.After, e.After) {
			differing = append(differing, "after")
		}
		sharedDir := prev.Action == "create" && e.Action == "create" && prev.Phase == e.Phase && sameState(prev.Before, e.Before) &&
			prev.Before.Kind == "absent" && sameState(prev.After, e.After) && prev.After.Kind == "directory"
		if len(differing) > 0 && !sharedDir {
			return nil, fmt.Errorf("Owner effect conflict at %s: %s vs %s (differs in: %s)", dest, prev.Owner, e.Owner, strings.Join(differing, ", "))
		}
		merged := prev
		merged.LogicalOwners = append(append([]string(nil), prev.LogicalOwners...), e.LogicalOwners...)
		merged.SurfaceIDs = append(append([]string(nil), prev.SurfaceIDs...), e.SurfaceIDs...)
		merged.Ownership = append(append([]Proof(nil), prev.Ownership...), e.Ownership...)
		if e.Reason < prev.Reason {
			merged.Reason = e.Reason
		}
		merged.normalize()
		out[i] = merged
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].sortKey(out[j]) })
	return out, nil
}

// MustCoalesce coalesces effects that are known to be conflict-free.
func MustCoalesce(effects []Effect) []Effect {
	out, err := Coalesce(effects)
	if err != nil {
		panic(err)
	}
	return out
}

// Disposition is operations.Disposition (RootID/Path "" = None).
type Disposition struct {
	Owner            string
	RootID           string
	Path             string
	HasRoot, HasPath bool
	State            string // unchanged, preserve, consent_required, not_applicable
	Reason           string
}

// NewDisposition builds a disposition with a root id and optional path.
func NewDisposition(owner, rootID, rel string, hasPath bool, state, reason string) Disposition {
	return Disposition{Owner: owner, RootID: rootID, HasRoot: true, Path: rel, HasPath: hasPath, State: state, Reason: reason}
}

// JSON is the plan-json disposition record.
func (d Disposition) JSON() *jsonx.Object {
	var root, p any
	if d.HasRoot {
		root = d.RootID
	}
	if d.HasPath {
		p = d.Path
	}
	return jsonx.NewObject("owner", d.Owner, "root_id", root, "path", p, "state", d.State, "reason", d.Reason)
}

// Diagnostic is operations.Diagnostic (Owner "" with HasOwner=false = None).
type Diagnostic struct {
	Code     string
	Owner    string
	HasOwner bool
	Severity string
	Message  string
}

// Diag builds an owned diagnostic.
func Diag(code, owner, severity, message string) Diagnostic {
	return Diagnostic{Code: code, Owner: owner, HasOwner: true, Severity: severity, Message: message}
}

// JSON is the plan-json diagnostic record.
func (d Diagnostic) JSON() *jsonx.Object {
	var owner any
	if d.HasOwner {
		owner = d.Owner
	}
	return jsonx.NewObject("code", d.Code, "owner", owner, "severity", d.Severity, "message", d.Message)
}

// Artifact is one bounded apply-only temporary path pattern.
type Artifact struct{ Directory, NamePattern, Purpose string }

// Assessment is operations.OwnerAssessment.
type Assessment struct {
	OwnerKey     string
	Root         Root
	Effects      []Effect
	Dispositions []Disposition
	Diagnostics  []Diagnostic
	Complete     bool
	Artifacts    []Artifact
	// Prepared is the owner's retained payload (owner-specific type).
	Prepared any
}

// Incomplete builds a failed assessment carrying one error diagnostic.
func Incomplete(owner string, root Root, code, msg string) *Assessment {
	return &Assessment{OwnerKey: owner, Root: root, Complete: false, Diagnostics: []Diagnostic{Diag(code, owner, "error", msg)}}
}

// HasErrors reports an error-severity diagnostic.
func (a *Assessment) HasErrors() bool {
	for _, d := range a.Diagnostics {
		if d.Severity == "error" {
			return true
		}
	}
	return false
}

// EffectIDs lists the effect ids in effect order.
func (a *Assessment) EffectIDs() []string {
	out := make([]string, 0, len(a.Effects))
	for _, e := range a.Effects {
		out = append(out, e.ID())
	}
	return out
}

// relPosix returns root-relative POSIX path of p.
func relPosix(root, p string) string {
	r, err := filepath.Rel(root, p)
	if err != nil {
		return filepath.ToSlash(p)
	}
	return filepath.ToSlash(r)
}

// parentsOf lists the strict ancestors of a relative POSIX path, outermost
// first ("a", "a/b" for "a/b/c").
func parentsOf(rel string) []string {
	var out []string
	d := path.Dir(rel)
	for d != "." && d != "/" && d != "" {
		out = append([]string{d}, out...)
		d = path.Dir(d)
	}
	return out
}
