package invocation

// Additive (area L): the composition entry points StepContractExecutor uses
// — ProfileInvocationExecutor.invoke with an explicit action_hint, and the
// invocation catalog used for the default-profile availability check.

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/spec-kitty/go-kitty3/internal/charter"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/glossary"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

// InvokeOptions are the keyword arguments of invoke() beyond the request.
type InvokeOptions struct {
	ProfileHint string // required (the composition path always supplies one)
	Actor       string
	Mode        string // "" = task_execution
	ActionHint  string // "" = role-default verb derivation
	MissionID   string
	WPID        string
	// RecordRoot is the checkout that owns the Op records ("" = root).
	RecordRoot string
}

// InvokeWithHint is ProfileInvocationExecutor.invoke(request, profile_hint,
// action_hint=...): explicit-profile routing (router_confidence None), the
// governance context for the hinted action, the started record and the
// glossary observation.
func InvokeWithHint(root, request string, opt InvokeOptions) (*Payload, error) {
	roots := Roots{Project: root, Record: opt.RecordRoot}
	if err := RequireOpWriteAdmitted(roots, "open the Op"); err != nil {
		return nil, err
	}
	record := roots.RecordRoot()
	id := ulid.New()
	reg := NewRegistry(root)
	p, err := reg.Resolve(opt.ProfileHint)
	if err != nil {
		return nil, err
	}
	action := opt.ActionHint
	if action == "" {
		action = roleDefaultVerb(p.Role(), "review")
	}
	rec := Recommend(p, action)
	resolved := dct.LoadProfileCatalog(root).Resolve(p.ID)
	if resolved == nil {
		resolved = p
	}
	ctx, err := charter.BuildContext(root, action, charter.Options{MarkLoaded: false, Profile: resolved})
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256([]byte(ctx.Text))
	hash := hex.EncodeToString(sum[:])[:16]
	available := ctx.Mode != "missing"
	actor := opt.Actor
	if actor == "" {
		actor = "unknown"
	}
	obs := glossary.ScanEvents(root, record, request, id, actor)
	mode := opt.Mode
	if mode == "" {
		mode = ModeTaskExecution
	}
	st := &Started{InvocationID: id, ProfileID: p.ID, Action: action, RequestText: request, Actor: actor,
		ModeOfWork: mode, GovernanceContextHash: hash, GovernanceContextAvailable: available,
		StartedAt: clock.NowUTCISO(), MissionID: opt.MissionID, WPID: opt.WPID}
	if rec != nil {
		if c := rec.CatalogCandidate(); c != nil {
			st.ModelID = c.ModelID
		}
	}
	if _, err := WriteStarted(record, st); err != nil {
		return nil, err
	}
	WriteGlossaryObservation(record, id, obs)
	return &Payload{InvocationID: id, ProfileID: p.ID, ProfileName: p.Name(), Action: action,
		ContextText: ctx.Text, ContextHash: hash, ContextAvailable: available,
		Glossary: obs, ModeOfWork: mode, Recommendation: rec, Alternatives: []Alternative{}}, nil
}

// AvailableProfiles is list_available_profiles (the invocation catalog).
func AvailableProfiles(root string) []*dct.Profile { return NewRegistry(root).List() }
