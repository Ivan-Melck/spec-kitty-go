// Package charter implements the project charter surface: the charter
// bundle files under .kittify/charter/, first-load context state, the
// action-scoped `charter context` render (text + JSON), and the governance
// summary the compact render shows.
//
// Fidelity: the "missing" mode (no charter.yaml / charter.md) and the
// compact render for an un-chartered project are byte-identical with
// upstream 4.0.0rc8; bootstrap renders for a generated charter carry the
// same sections in simplified form (see docs/parity/doctrine.md).
package charter

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/atomicfile"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// Charter bundle paths (repo-relative).
const (
	Dir          = ".kittify/charter"
	CharterYAML  = ".kittify/charter/charter.yaml"
	CharterMD    = ".kittify/charter/charter.md"
	StateFile    = ".kittify/charter/context-state.json"
	MetadataYAML = ".kittify/charter/metadata.yaml"
	AnswersYAML  = ".kittify/charter/interview/answers.yaml"
)

// SchemaVersion is CONTEXT_SCHEMA_VERSION.
const SchemaVersion = "1.3.0"

// BootstrapActions is the 4-token fast path.
var BootstrapActions = map[string]bool{"specify": true, "plan": true, "implement": true, "review": true}

const minEffectiveDepth = 2

// Path joins a repo-relative charter path.
func Path(root, rel string) string { return filepath.Join(root, filepath.FromSlash(rel)) }

func exists(p string) bool { _, err := os.Stat(p); return err == nil }

// Result is upstream CharterContextResult.
type Result struct {
	Action          string
	Mode            string // missing | compact | bootstrap
	FirstLoad       bool
	Text            string
	ReferencesCount int
	Depth           int
}

// Options tunes BuildContext.
type Options struct {
	MarkLoaded       bool
	MissionType      string
	Profile          *dct.Profile
	SuppressResolver bool
	Depth            *int
}

type state struct {
	path      string
	data      *jsonx.Object
	firstLoad bool
	depth     int
}

func loadState(root string) *jsonx.Object {
	empty := jsonx.NewObject("schema_version", "1.0.0", "actions", &jsonx.Object{})
	b, err := os.ReadFile(Path(root, StateFile))
	if err != nil {
		return empty
	}
	o, err := jsonx.DecodeObject(b)
	if err != nil || o == nil {
		return empty
	}
	if o.GetObject("actions") == nil {
		o.Set("actions", &jsonx.Object{})
	}
	return o
}

func prepareState(root, action string, depth *int) state {
	st := state{path: Path(root, StateFile), data: loadState(root)}
	st.firstLoad = !st.data.GetObject("actions").Has(action)
	switch {
	case depth != nil:
		st.depth = *depth
	case st.firstLoad:
		st.depth = minEffectiveDepth
	default:
		st.depth = 1
	}
	return st
}

// nowStamp is kernel.clock.now_utc_stamp ("%Y-%m-%dT%H:%M:%SZ").
func nowStamp() string {
	if v := os.Getenv("SPEC_KITTY_FAKE_NOW"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t.UTC().Format("2006-01-02T15:04:05Z")
		}
	}
	return time.Now().UTC().Format("2006-01-02T15:04:05Z")
}

func (st state) markLoaded(action string) {
	st.data.GetObject("actions").Set(action, nowStamp())
	os.MkdirAll(filepath.Dir(st.path), 0o755)
	atomicfile.WriteMode(st.path, []byte(jsonx.Dumps(st.data, jsonx.SortedPretty)), 0o644)
}

// MissingText is the missing-charter render.
const MissingText = "Charter Context:\n" +
	"  - Charter file not found at `.kittify/charter/charter.yaml`.\n" +
	"  - Run `spec-kitty charter interview` then `spec-kitty charter generate`."

// Present reports whether either charter.yaml or charter.md exists.
func Present(root string) bool {
	return exists(Path(root, CharterYAML)) || exists(Path(root, CharterMD))
}

// actionNodeDeclared reports whether action:<missionType>/<action> exists in
// the merged DRG.
func actionNodeDeclared(root, missionType, action string) bool {
	if missionType == "" {
		return false
	}
	g, err := dct.LoadGraph(root, dct.OrgRoots(root))
	if err != nil {
		return false
	}
	return g.NodeURNs()["action:"+missionType+"/"+action]
}

// ErrConfig wraps activation config errors surfaced by BuildContext.
var ErrConfig = errors.New("config")

// BuildContext is upstream build_charter_context.
func BuildContext(root, action string, opt Options) (*Result, error) {
	if _, err := dct.LoadActivation(root); err != nil {
		return nil, err
	}
	norm := strings.ToLower(strings.TrimSpace(action))
	st := prepareState(root, norm, opt.Depth)
	if !BootstrapActions[norm] && !actionNodeDeclared(root, opt.MissionType, norm) {
		d := 1
		if opt.Depth != nil {
			d = *opt.Depth
		}
		return &Result{Action: norm, Mode: "compact", FirstLoad: false, Depth: d,
			Text: RenderCompact(root, CompactInput{Action: norm, Profile: opt.Profile, SuppressResolver: opt.SuppressResolver})}, nil
	}
	if !Present(root) {
		return &Result{Action: norm, Mode: "missing", FirstLoad: st.firstLoad, Text: MissingText, Depth: st.depth}, nil
	}
	b := ResolveActionBundle(root, norm, opt.MissionType, st.depth)
	if st.depth < minEffectiveDepth {
		if opt.MarkLoaded && st.firstLoad {
			st.markLoaded(norm)
		}
		in := CompactInput{Action: norm, Profile: opt.Profile, SuppressResolver: opt.SuppressResolver}
		if b != nil {
			in.Directives, in.Tactics, in.Styleguides, in.Toolguides, in.Procedures, in.Assets =
				b.IDs[dct.Directive], b.IDs[dct.Tactic], b.IDs[dct.Styleguide], b.IDs[dct.Toolguide], b.IDs[dct.Procedure], b.IDs[dct.Asset]
		}
		return &Result{Action: norm, Mode: "compact", FirstLoad: st.firstLoad, Depth: st.depth, Text: RenderCompact(root, in)}, nil
	}
	refs := LoadReferences(root)
	text := renderBootstrap(root, norm, b, refs, opt.Profile)
	if opt.MarkLoaded && st.firstLoad {
		st.markLoaded(norm)
	}
	return &Result{Action: norm, Mode: "bootstrap", FirstLoad: st.firstLoad, Text: text, ReferencesCount: len(refs), Depth: st.depth}, nil
}
