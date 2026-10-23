package invocation

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/glossary"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

// Payload is InvocationPayload.
type Payload struct {
	InvocationID         string
	ProfileID            string
	ProfileName          string
	Action               string
	ContextText          string
	ContextHash          string
	ContextAvailable     bool
	RouterConfidence     string // "" = None
	Glossary             *glossary.Observation
	ModeOfWork           string
	Recommendation       *Recommendation
	EmptyCharterFallback bool
	Alternatives         []Alternative
}

func altObjects(alts []Alternative, withReason bool) []any {
	out := []any{}
	for _, a := range alts {
		o := jsonx.NewObject("profile_id", a.ProfileID, "action", a.Action, "confidence", a.Confidence, "match_reason", a.MatchReason)
		if !withReason {
			o.Delete("match_reason")
		}
		out = append(out, o)
	}
	return out
}

func (p *Payload) slots(withID bool) *jsonx.Object {
	o := &jsonx.Object{}
	if withID {
		o.Set("invocation_id", p.InvocationID)
	}
	var rc, gl, rec any
	if p.RouterConfidence != "" {
		rc = p.RouterConfidence
	}
	if p.Glossary != nil {
		gl = p.Glossary.ToObject()
	}
	if p.Recommendation != nil {
		rec = p.Recommendation.Object()
	}
	o.Set("profile_id", p.ProfileID)
	o.Set("profile_friendly_name", p.ProfileName)
	o.Set("action", p.Action)
	o.Set("governance_context_text", p.ContextText)
	o.Set("governance_context_hash", p.ContextHash)
	o.Set("governance_context_available", p.ContextAvailable)
	o.Set("router_confidence", rc)
	o.Set("glossary_observations", gl)
	o.Set("recommendation", rec)
	o.Set("empty_charter_fallback", p.EmptyCharterFallback)
	o.Set("alternatives", altObjects(p.Alternatives, true))
	return o
}

// ModePermitsEvidence is mode_permits_evidence (raw recorded value).
func ModePermitsEvidence(raw any, present bool) bool {
	if !present || raw == nil {
		return true
	}
	s, ok := raw.(string)
	if !ok {
		return false
	}
	if s == "" {
		return true
	}
	if !knownModes[s] {
		return false
	}
	return s != ModeAdvisory && s != ModeQuery
}

// CloseContract is build_close_contract.
func CloseContract(id, mode string) *jsonx.Object {
	o := jsonx.NewObject("command", "spec-kitty profile-invocation complete --invocation-id "+id+" --outcome <done|failed|abandoned>",
		"outcomes", []any{"done", "failed", "abandoned"}, "evidence_flag", "--evidence", "artifact_flag", "--artifact", "commit_flag", "--commit")
	if !ModePermitsEvidence(mode, mode != "") {
		o.Delete("evidence_flag")
	}
	return o
}

// Object is InvocationPayload.to_dict.
func (p *Payload) Object() *jsonx.Object {
	o := p.slots(true)
	o.Set("status", "open")
	o.Set("close_contract", CloseContract(p.InvocationID, p.ModeOfWork))
	return o
}

// DryRunObject is to_dry_run_dict.
func (p *Payload) DryRunObject() *jsonx.Object {
	o := p.slots(false)
	o.Set("status", "dry_run")
	return o
}

// AmbiguousDryRunObject is build_ambiguous_dry_run_payload.
func AmbiguousDryRunObject(e *RouterError) *jsonx.Object {
	alts := []any{}
	for _, c := range e.Candidates {
		alts = append(alts, jsonx.NewObject("profile_id", c.ProfileID, "action", c.Action, "confidence", c.Confidence, "match_reason", c.MatchReason))
	}
	return jsonx.NewObject("status", "dry_run", "profile_id", nil, "action", nil, "router_confidence", "ambiguous", "alternatives", alts)
}

// resolution is _RoutingResolution.
type resolution struct {
	profile    *dct.Profile
	action     string
	confidence string
	fallback   bool
	alts       []Alternative
	rec        *Recommendation
	ctx        *charter.Result
	hash       string
	available  bool
	obs        *glossary.Observation
}

func resolveRouting(root, request string, hint *string, actor, invocationID, explicitConfidence string) (*resolution, error) {
	reg := NewRegistry(root)
	// ProfileInvocationExecutor(repo_root, ...) builds its own charter
	// service: another infer_repo_languages (recorded interview read).
	charter.InferRepoLanguages(root, nil, false)
	r := &resolution{alts: []Alternative{}}
	if hint != nil {
		p, err := reg.Resolve(*hint)
		if err != nil {
			return nil, err
		}
		r.profile = p
		r.action = roleDefaultVerb(p.Role(), "review")
		r.confidence = explicitConfidence
	} else {
		dec := GenericFallback(root, request)
		if dec != nil {
			r.fallback = true
		} else {
			var err error
			dec, err = Route(reg, request)
			if err != nil {
				return nil, err
			}
		}
		p, err := reg.Resolve(dec.ProfileID)
		if err != nil {
			return nil, err
		}
		r.profile, r.action, r.confidence, r.alts = p, dec.Action, dec.Confidence, dec.Alternatives
	}
	r.rec = Recommend(r.profile, r.action)
	resolved := dct.LoadProfileCatalog(root).Resolve(r.profile.ID)
	if resolved == nil {
		resolved = r.profile
	}
	ctx, err := charter.BuildContext(root, r.action, charter.Options{MarkLoaded: false, Profile: resolved, SuppressResolver: r.fallback})
	if err != nil {
		return nil, err
	}
	r.ctx = ctx
	sum := sha256.Sum256([]byte(ctx.Text))
	r.hash = hex.EncodeToString(sum[:])[:16]
	r.available = ctx.Mode != "missing"
	r.obs = glossary.Scan(root, request, invocationID, actor)
	return r, nil
}

// Invoke routes the request, writes the started record and returns the
// payload (ProfileInvocationExecutor.invoke).
func Invoke(root, request string, hint *string, actor, mode string) (*Payload, error) {
	id := ulid.New()
	r, err := resolveRouting(root, request, hint, actor, id, "")
	if err != nil {
		return nil, err
	}
	if mode == "" {
		mode = ModeTaskExecution
	}
	st := &Started{InvocationID: id, ProfileID: r.profile.ID, Action: r.action, RequestText: request, Actor: actor,
		ModeOfWork: mode, GovernanceContextHash: r.hash, GovernanceContextAvailable: r.available,
		RouterConfidence: r.confidence, StartedAt: clock.NowUTCISO()}
	if r.rec != nil {
		if c := r.rec.CatalogCandidate(); c != nil {
			st.ModelID = c.ModelID
		}
	}
	if _, err := WriteStarted(root, st); err != nil {
		return nil, err
	}
	WriteGlossaryObservation(root, id, r.obs)
	return &Payload{InvocationID: id, ProfileID: r.profile.ID, ProfileName: r.profile.Name(), Action: r.action,
		ContextText: r.ctx.Text, ContextHash: r.hash, ContextAvailable: r.available, RouterConfidence: r.confidence,
		Glossary: r.obs, ModeOfWork: mode, Recommendation: r.rec, EmptyCharterFallback: r.fallback, Alternatives: r.alts}, nil
}

// DryRun routes and assembles context without writing anything.
func DryRun(root, request string, hint *string, actor string) (*Payload, error) {
	r, err := resolveRouting(root, request, hint, actor, "", "exact")
	if err != nil {
		return nil, err
	}
	return &Payload{ProfileID: r.profile.ID, ProfileName: r.profile.Name(), Action: r.action,
		ContextText: r.ctx.Text, ContextHash: r.hash, ContextAvailable: r.available, RouterConfidence: r.confidence,
		Glossary: r.obs, Recommendation: r.rec, EmptyCharterFallback: r.fallback, Alternatives: r.alts}, nil
}

// InvalidModeForEvidenceError refuses --evidence on advisory/query Ops.
type InvalidModeForEvidenceError struct{ Msg string }

func (e *InvalidModeForEvidenceError) Error() string { return e.Msg }

// CompleteOptions are the optional close links.
type CompleteOptions struct {
	Evidence  *string
	Artifacts []string
	Commit    *string
}

// CommitOutcome is OpCommitOutcome.
type CommitOutcome struct {
	Committed bool
	Warning   string // stderr of a successful commit (logged at WARNING)
	Refusal   string
	Skipped   string
}

func readStarted(root, id string) (*jsonx.Object, error) {
	p, err := RecordPath(root, id)
	if err != nil {
		return nil, err
	}
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, &Error{"Invocation record not found: " + id}
	}
	first, _, _ := strings.Cut(string(b), "\n")
	o, err := decodeLine(first)
	if err != nil || o == nil {
		return nil, &Error{"Invalid invocation record: " + id}
	}
	return o, nil
}

// Complete closes an open Op (complete_invocation with closed_by=agent).
func Complete(root, id, outcome string, opt CompleteOptions) (*Completed, *CommitOutcome, error) {
	first, err := readStarted(root, id)
	if err != nil {
		return nil, nil, err
	}
	rawMode, present := first.Get("mode_of_work")
	if opt.Evidence != nil && !ModePermitsEvidence(rawMode, present) {
		if s, ok := rawMode.(string); ok && knownModes[s] {
			return nil, nil, &InvalidModeForEvidenceError{fmt.Sprintf("Cannot promote evidence on invocation %s: mode is %s; Tier 2 evidence is only allowed on task_execution or mission_step invocations.", id, s)}
		}
		return nil, nil, &InvalidModeForEvidenceError{fmt.Sprintf("Cannot promote evidence on invocation %s: the record's mode_of_work is %s, which is not a recognised mode, so the invocation's evidence eligibility cannot be determined. Repair the kitty-ops record (or run 'spec-kitty upgrade') and retry.", id, reprAny(rawMode))}
	}
	if ClosedIDs(root)[id] {
		return nil, nil, &AlreadyClosedError{id}
	}
	var evidence *string
	if opt.Evidence != nil {
		c, err := evidenceContent(root, id, *opt.Evidence)
		if err != nil {
			return nil, nil, err
		}
		evidence = &c
	}
	c := &Completed{InvocationID: id, CompletedAt: clock.NowUTCISO(), Outcome: outcome, ClosedBy: "agent"}
	if opt.Evidence != nil {
		c.EvidenceRef, c.hasEvidence = *opt.Evidence, true
	}
	if err := WriteCompleted(root, c); err != nil {
		return nil, nil, err
	}
	if evidence != nil {
		dir := filepath.Join(root, ".kittify", "evidence", id)
		os.MkdirAll(dir, 0o755)
		os.WriteFile(filepath.Join(dir, "evidence.md"), []byte(*evidence), 0o644)
		snap := jsonx.NewObject("event", "completed", "invocation_id", id, "completed_at", c.CompletedAt,
			"outcome", c.Outcome, "closed_by", c.ClosedBy, "evidence_ref", c.EvidenceRef)
		os.WriteFile(filepath.Join(dir, "record.json"), []byte(jsonx.Dumps(snap, jsonx.Pretty)), 0o644)
	}
	for _, a := range opt.Artifacts {
		if err := AppendLink(root, id, "artifact", NormaliseRef(a, root), ""); err != nil {
			return nil, nil, err
		}
	}
	if opt.Commit != nil {
		if err := AppendLink(root, id, "", "", *opt.Commit); err != nil {
			return nil, nil, err
		}
	}
	return c, commitOpRecord(root, id, first), nil
}

// UnsupportedEvidenceError is raised for non-UTF-8 evidence.
type UnsupportedEvidenceError struct{ Msg string }

func (e *UnsupportedEvidenceError) Error() string { return e.Msg }

func evidenceContent(root, id, ref string) (string, error) {
	var cand string
	if filepath.IsAbs(ref) {
		cand = ref
	} else {
		rr := resolvePath(root)
		r := resolvePath(filepath.Join(rr, ref))
		if rel, err := filepath.Rel(rr, r); err == nil && rel != ".." && !strings.HasPrefix(rel, "../") {
			cand = r
		}
	}
	if cand != "" {
		if b, err := os.ReadFile(cand); err == nil {
			if !utf8.Valid(b) {
				return "", &UnsupportedEvidenceError{fmt.Sprintf("Cannot use %s as evidence for invocation %s: the file is not UTF-8 text. Tier 2 evidence must be UTF-8 text. No completion was recorded by this call; re-run complete with a UTF-8 text evidence file.", ref, id)}
			}
			return string(b), nil
		}
	}
	return ref, nil
}

// IsNotFound reports whether err is a "record not found" InvocationError.
func IsNotFound(err error) bool {
	var e *Error
	return errors.As(err, &e) && strings.HasPrefix(e.Msg, "Invocation record not found")
}
