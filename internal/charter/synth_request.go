package charter

import (
	"crypto/rand"
	"encoding/hex"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// SynthesisRequest is synthesizer.request.SynthesisRequest as built by
// _build_synthesis_request.
type SynthesisRequest struct {
	InterviewSections []string
	DRGNodes          []string // drg_snapshot node URNs (directive:<id>)
	InterviewSnapshot *jsonx.Object
	RunID             string
	Evidence          *SynthEvidence
	Placeholder       SynthTarget
}

// DoctrineSnapshot is the request's (empty) doctrine snapshot.
func (r *SynthesisRequest) DoctrineSnapshot() *jsonx.Object {
	return jsonx.NewObject("directives", &jsonx.Object{}, "tactics", &jsonx.Object{}, "styleguides", &jsonx.Object{})
}

// DRGSnapshot is the request's drg_snapshot mapping.
func (r *SynthesisRequest) DRGSnapshot() *jsonx.Object {
	nodes := []any{}
	for _, u := range r.DRGNodes {
		nodes = append(nodes, jsonx.NewObject("urn", u, "kind", "directive"))
	}
	return jsonx.NewObject("nodes", nodes, "edges", []any{}, "schema_version", "1")
}

// RequestError is the TaskCliError raised by _build_synthesis_request.
type RequestError struct{ Msg string }

func (e *RequestError) Error() string { return e.Msg }

// BuildSynthesisRequest is _build_synthesis_request.
func BuildSynthesisRequest(root, adapter string) (*SynthesisRequest, error) {
	iv := ReadInterview(InterviewPath(root))
	if iv == nil {
		return nil, &RequestError{"No interview answers found. Run 'spec-kitty charter interview' first."}
	}
	cr, err := resolveConfigRoots(root, nil)
	if err != nil {
		return nil, err
	}
	req := &SynthesisRequest{RunID: newRunID()}
	directives := []string{}
	if t, err := LoadWriteTarget(root); err == nil {
		if v, ok := t.Data.Get("activated_directives"); ok && v != nil {
			directives = append(directives, cr.byKind[dct.Directive]...)
		}
	}
	for _, d := range directives {
		req.DRGNodes = append(req.DRGNodes, "directive:"+d)
	}
	snap := jsonx.NewObject("mission_id", iv.Mission, "selected_directives", strs(directives), "selected_paradigms", strs(nz(cr.byKind[dct.Paradigm])))
	for _, kv := range iv.Answers.Pairs() {
		snap.Set(kv.Key, kv.Value)
	}
	req.InterviewSnapshot = snap
	sections := map[string]bool{}
	for _, kv := range snap.Pairs() {
		sections[kv.Key] = true
	}
	req.InterviewSections = dct.SortedKeys(sections)
	ss := "mission_type"
	req.Placeholder = SynthTarget{Kind: "directive", Slug: "synthesize-placeholder", Title: "Synthesize Placeholder", ArtifactID: "PROJECT_000", SourceSection: &ss}
	switch adapter {
	case "generated", "fixture":
	default:
		return nil, &RequestError{"Unknown adapter '" + adapter + "'. Supported adapters are '--adapter generated' and '--adapter fixture'. Doctrine generation is performed by the LLM harness (Claude Code, Codex, Cursor, etc.) via the spk-charter-governance skill. spec-kitty never calls an LLM itself."}
	}
	return req, nil
}

// newRunID is str(uuid.uuid4()).replace("-", "").upper()[:26].
func newRunID() string {
	var b [16]byte
	rand.Read(b[:])
	b[6] = b[6]&0x0f | 0x40
	b[8] = b[8]&0x3f | 0x80
	return strings.ToUpper(hex.EncodeToString(b[:]))[:26]
}
