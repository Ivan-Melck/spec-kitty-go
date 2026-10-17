package scenario

// Foundation only: these private proofs do not replace the restart oracle and
// do not authorize recursive normalization, Git SHA substitution, or IO.
import (
	"bytes"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/productionruntime"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type terminalHistoricalKey struct {
	database    journalv2.DatabaseID
	coordinate  journalv2.AggregateCoordinate
	version     journalv2.LogicalVersion
	revision    uint64
	event       journalv2.EventID
	publication journalv2.PublicationID
}
type terminalHistoricalState struct {
	key       terminalHistoricalKey
	canonical []byte
	digest    journalv2.Digest
	envelope  journalv2.EventEnvelopeV2
}
type terminalWorkspaceInventory struct {
	coordinate    journalv2.AggregateCoordinate
	versions      []terminalHistoricalKey
	first         workspace.BindingV2
	relative      string
	claim         journalv2.WorkspacePathClaimV2
	claimRevision uint64
}
type terminalCoordinateProof struct {
	historySHA        string
	capture           journalv2.StoreRevisionV2
	scope             journalv2.Scope
	states            map[terminalHistoricalKey]terminalHistoricalState
	events            map[journalv2.EventID]terminalHistoricalKey
	workspaces        map[journalv2.AggregateCoordinate]terminalWorkspaceInventory
	boundExecutions   map[terminalHistoricalKey]terminalHistoricalKey
	repositoryCapture terminalRepositoryCapture // Optional original real-Git configuration; never logical authority.
}

// No arbitrary root lookup: declarations are derived from the same harness
// fields used by runWorkspaceToReady. Repository identities are mission-scoped.
func newTerminalCoordinateProof(h *terminalPublicationHistory, owner *portableHarness, repositories ...ids.RepositoryID) (*terminalCoordinateProof, error) {
	if _, err := validateTerminalHistory(h); err != nil {
		return nil, err
	}
	if owner == nil || owner.root == "" || !filepath.IsAbs(owner.root) || filepath.Clean(owner.root) != owner.root || owner.root == "/" {
		return nil, fmt.Errorf("coordinate proof: missing harness-owned private root")
	}
	root := filepath.Join(owner.root, "workspaces")
	if terminalRepositoryDriverPresent(owner) && owner.git.managedRoot() != "" {
		root = owner.git.managedRoot()
	}
	if !filepath.IsAbs(root) || filepath.Clean(root) != root || root == "/" {
		return nil, fmt.Errorf("coordinate proof: noncanonical managed root")
	}
	declared := make(map[ids.RepositoryID]bool)
	for _, id := range repositories {
		if _, err := ids.ParseRepositoryID(id.String()); err != nil || declared[id] {
			return nil, fmt.Errorf("coordinate proof: invalid/duplicate repository declaration")
		}
		declared[id] = true
	}
	p := &terminalCoordinateProof{historySHA: h.HistorySHA256, capture: h.Publications[0].Proof.CapturedCoordinate, scope: journalv2.Scope{Kind: journalv2.ScopeMission, ProjectID: h.Progress.ProjectID, MissionID: h.Progress.MissionID}, states: make(map[terminalHistoricalKey]terminalHistoricalState), events: make(map[journalv2.EventID]terminalHistoricalKey), workspaces: make(map[journalv2.AggregateCoordinate]terminalWorkspaceInventory)}
	reducers, err := productionruntime.BuildReducerSetV2(journalv2.DefaultEventRegistryV2())
	if err != nil {
		return nil, err
	}
	type indexed struct {
		event   journalv2.EventEnvelopeV2
		receipt terminalPublicationReceipt
		head    journalv2.HeadTransitionV2
	}
	var all []indexed
	for _, r := range h.Publications {
		events := r.Ack.Plan().Events()
		// Qualify the entire publication before admitting any generic event,
		// irrespective of its event index. An optional generic claim is not
		// itself a workspace ownership proof.
		if err := qualifyTerminalPublication(r, p.scope, root, declared); err != nil {
			return nil, err
		}
		for i, record := range r.Proof.JournalRecords {
			event, err := journalv2.DecodeEventEnvelopeRecordV2(record, journalv2.DefaultEventRegistryV2())
			if err != nil {
				return nil, err
			}
			actual, err := journalv2.EncodeEventEnvelopeV2(event)
			if err != nil {
				return nil, err
			}
			expected, err := journalv2.EncodeEventEnvelopeV2(events[i])
			if err != nil || !bytes.Equal(actual, expected) {
				return nil, fmt.Errorf("coordinate proof: journal/ACK event mismatch")
			}
			var heads []journalv2.HeadTransitionV2
			for _, head := range r.Ack.Plan().Heads() {
				if head.Coordinate == event.Coordinate() && head.EventID == event.EventID() && head.NextLogicalVersion == event.LogicalVersion() {
					heads = append(heads, head)
				}
			}
			if len(heads) != 1 {
				return nil, fmt.Errorf("coordinate proof: missing/ambiguous owned head")
			}
			all = append(all, indexed{event, r, heads[0]})
		}
	}
	sort.Slice(all, func(i, j int) bool {
		a, b := all[i], all[j]
		if a.receipt.Ack.ProviderRevision() != b.receipt.Ack.ProviderRevision() {
			return a.receipt.Ack.ProviderRevision() < b.receipt.Ack.ProviderRevision()
		}
		return a.event.EventIndex() < b.event.EventIndex()
	})
	latest := make(map[journalv2.AggregateCoordinate]terminalHistoricalState)
	claims := make(map[journalv2.Digest]bool)
	for _, entry := range all {
		e, r, head := entry.event, entry.receipt, entry.head
		c := e.Coordinate()
		previous, exists := latest[c]
		if _, duplicate := p.events[e.EventID()]; duplicate {
			return nil, fmt.Errorf("coordinate proof: duplicate global event ID")
		}
		if e.LogicalVersion() != previous.key.version+1 || head.ExpectedLogicalVersion != previous.key.version || head.ExpectedRecordRevision != previous.key.revision {
			return nil, fmt.Errorf("coordinate proof: noncontiguous full coordinate %v", c)
		}
		if exists && r.Ack.ProviderRevision() <= previous.key.revision {
			return nil, fmt.Errorf("coordinate proof: reused historical coordinate")
		}
		state, digest, err := reducers.ApplyPlaybackV2(previous.canonical, e)
		if err != nil {
			return nil, fmt.Errorf("coordinate proof: playback %s: %w", e.Family(), err)
		}
		if e.Family() == "operation" {
			// Operation heads digest the event payload, not the reconstructed
			// state with its last publication. The only other known summary
			// preimage is the service's closed compact status object.
			operation, err := journalv2.ReconstructOperationStateFromEventV2(e)
			if err != nil || !journalv2.EqualOperationStatesV2(operation, r.Ack.Plan().OperationState()) {
				return nil, fmt.Errorf("coordinate proof: operation reconstruction")
			}
			encoded, err := journalv2.EncodeOperationStateV2(operation)
			if err != nil || !bytes.Equal(encoded, state) {
				return nil, fmt.Errorf("coordinate proof: typed operation replay")
			}
			compact := mustTerminalJSON(struct {
				Status string `json:"status"`
			}{string(operation.Status())})
			payloadDigest, err := journalv2.ReducerStateDigestV2(e.Payload())
			if err != nil || head.CanonicalStateDigest != payloadDigest || (!bytes.Equal(head.AuthoritySummary, e.Payload()) && !bytes.Equal(head.AuthoritySummary, compact)) {
				return nil, fmt.Errorf("coordinate proof: operation head domain")
			}
		} else if digest != head.CanonicalStateDigest || !bytes.Equal(state, head.AuthoritySummary) {
			return nil, fmt.Errorf("coordinate proof: unsupported/nonexact head digest domain %s", e.Family())
		}
		key := terminalHistoricalKey{p.capture.DatabaseID(), c, e.LogicalVersion(), r.Ack.ProviderRevision(), e.EventID(), e.PublicationID()}
		owned := terminalHistoricalState{key, append([]byte(nil), state...), digest, e}
		p.states[key] = owned
		p.events[e.EventID()] = key
		latest[c] = owned
		if e.Family() == "execution_slot" {
			event, err := execution.DecodeExecutionSlotEventV2(e.Payload())
			if err != nil {
				return nil, err
			}
			decoded, err := execution.DecodeExecutionSlotStateV2(state)
			if err != nil {
				return nil, err
			}
			replay, err := execution.ReduceExecutionSlotEventV2(previous.canonical, e)
			if err != nil || !bytes.Equal(replay, state) || !bytes.Equal(decoded.CanonicalBytes(), state) || !bytes.Equal(event.State.CanonicalBytes(), state) {
				return nil, fmt.Errorf("coordinate proof: typed execution replay")
			}
		}
		if e.Family() != "workspace" {
			continue
		}
		event, err := workspace.DecodeEventV2(e.Payload())
		if err != nil {
			return nil, err
		}
		decoded, err := workspace.DecodeStateV2(state)
		if err != nil {
			return nil, err
		}
		workspaceReplay, err := workspace.ReduceEventV2(previous.canonical, e)
		if err != nil || !bytes.Equal(workspaceReplay, state) || !bytes.Equal(event.State.CanonicalBytes(), state) {
			return nil, fmt.Errorf("coordinate proof: typed workspace replay")
		}
		b := decoded.Binding
		if b.Validate() != nil || c.Scope != (journalv2.Scope{Kind: journalv2.ScopeHost}) || !declared[b.RepositoryID] || b.ProjectID != ids.ProjectID(p.scope.ProjectID) || b.MissionID != ids.MissionID(p.scope.MissionID) || b.ManagedRoot != root {
			return nil, fmt.Errorf("coordinate proof: foreign/undeclared workspace owner")
		}
		relative, err := filepath.Rel(root, b.CanonicalPath)
		if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, "../") || filepath.IsAbs(relative) || filepath.Join(root, relative) != b.CanonicalPath {
			return nil, fmt.Errorf("coordinate proof: aliased workspace path")
		}
		inventory := p.workspaces[c]
		if !exists {
			claim := r.Ack.Plan().WorkspacePathClaim()
			observed := r.Proof.WorkspacePathClaim
			if claim == nil || observed == nil || r.Proof.ClaimRecordRevision != r.Ack.ProviderRevision() || claim.ClaimDigest() != b.PathClaimDigest || observed.ClaimDigest() != claim.ClaimDigest() || !bytes.Equal(claim.CanonicalValue(), observed.CanonicalValue()) || claims[b.PathClaimDigest] {
				return nil, fmt.Errorf("coordinate proof: missing/reused permanent claim ACK")
			}
			// The capability-owned constructor closes the ownership JSON schema; it
			// neither invents a new digest nor publishes a replacement durable claim.
			occurredAt, err := time.Parse(time.RFC3339Nano, decoded.OccurredAt)
			if err != nil {
				return nil, err
			}
			contribution, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: b, EffectID: decoded.EffectID, EventID: e.EventID(), OccurredAt: occurredAt})
			if err != nil {
				return nil, err
			}
			input, err := workspace.AppendPlanContributionV2(journalv2.CommitPlanInput{}, contribution)
			if err != nil {
				return nil, err
			}
			if input.WorkspacePathClaim == nil || !bytes.Equal(input.WorkspacePathClaim.CanonicalValue(), claim.CanonicalValue()) {
				return nil, fmt.Errorf("coordinate proof: permanent claim ownership JSON")
			}
			copied, err := journalv2.NewWorkspacePathClaimV2(observed.ClaimDigest(), observed.CanonicalValue())
			if err != nil {
				return nil, err
			}
			inventory = terminalWorkspaceInventory{coordinate: c, first: b, relative: relative, claim: copied, claimRevision: r.Proof.ClaimRecordRevision}
			claims[b.PathClaimDigest] = true
		} else {
			// Production workspace replay already proves immutable binding identity
			// and allows epoch changes only in the typed runtime_epoch_rebound event.
			if relative != inventory.relative || r.Ack.Plan().WorkspacePathClaim() != nil {
				return nil, fmt.Errorf("coordinate proof: replacement claim/generation")
			}
		}
		inventory.versions = append(inventory.versions, key)
		p.workspaces[c] = inventory
	}
	if len(claims) != len(p.workspaces) {
		return nil, fmt.Errorf("coordinate proof: permanent claim inventory census")
	}
	if err := p.closeBoundExecutionReferences(h); err != nil {
		return nil, err
	}
	p.repositoryCapture = captureTerminalRepository(p, owner)
	return p, nil
}

// This foundation admits only schemas with closed owner evidence. In
// particular integration_target v1 has no repository root or project/mission
// owner, and runtime admission v2 needs a later immutable host-config/status
// proof. Neither host-name prefixes nor mission-bound operation coordinates
// supply that missing evidence.
func qualifyTerminalPublication(r terminalPublicationReceipt, scope journalv2.Scope, root string, repositories map[ids.RepositoryID]bool) error {
	unsupported := func(e journalv2.EventEnvelopeV2, why string) error {
		return fmt.Errorf("coordinate proof: UNSUPPORTED family=%s event_schema=%d reducer=%d type=%s: %s", e.Family(), e.EventSchemaVersion(), e.ReducerVersion(), e.EventType(), why)
	}
	bindingScope := r.Proof.Binding.Scope()
	matchedScope := func(s journalv2.Scope) bool {
		return (s.Kind == journalv2.ScopeMission || s.Kind == journalv2.ScopeWork) && s.ProjectID == scope.ProjectID && s.MissionID == scope.MissionID
	}
	if bindingScope.Kind != journalv2.ScopeHost && !matchedScope(bindingScope) {
		return fmt.Errorf("coordinate proof: unsupported binding owner scope")
	}
	claim := r.Ack.Plan().WorkspacePathClaim()
	workspaceCount, creations := 0, 0
	for _, e := range r.Ack.Plan().Events() {
		if e.Coordinate().Scope.Kind != journalv2.ScopeHost && !matchedScope(e.Coordinate().Scope) {
			return unsupported(e, "foreign or unsupported event scope")
		}
		if e.Family() != "workspace" {
			continue
		}
		if e.EventSchemaVersion() != workspace.WorkspaceEventSchemaV2 || e.ReducerVersion() != workspace.WorkspaceReducerV2 {
			return unsupported(e, "unclosed workspace schema")
		}
		event, err := workspace.DecodeEventV2(e.Payload())
		if err != nil {
			return err
		}
		b := event.State.Binding
		coordinate, err := workspace.WorkspaceCoordinateV2(b)
		if err != nil || coordinate != e.Coordinate() || !repositories[b.RepositoryID] || b.ProjectID != ids.ProjectID(scope.ProjectID) || b.MissionID != ids.MissionID(scope.MissionID) || b.ManagedRoot != root {
			return unsupported(e, "foreign/undeclared typed workspace owner")
		}
		workspaceCount++
		if e.LogicalVersion() != 1 {
			if claim != nil || r.Proof.WorkspacePathClaim != nil || r.Proof.ClaimRecordRevision != 0 {
				return unsupported(e, "continuation carries permanent claim")
			}
			continue
		}
		creations++
		if claim == nil || e.EventType() != "workspace.preparation_requested" || event.Kind != "preparation_requested" || event.State.LogicalVersion != 1 || event.State.Stage != workspace.StageCreationRequestedV2 || claim.ClaimDigest() != b.PathClaimDigest {
			return unsupported(e, "creation without exact declared claim")
		}
		at, err := time.Parse(time.RFC3339Nano, event.State.OccurredAt)
		if err != nil {
			return err
		}
		contribution, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: b, EffectID: event.State.EffectID, EventID: e.EventID(), OccurredAt: at})
		if err != nil {
			return err
		}
		input, err := workspace.AppendPlanContributionV2(journalv2.CommitPlanInput{}, contribution)
		if err != nil || input.WorkspacePathClaim == nil || !bytes.Equal(input.WorkspacePathClaim.CanonicalValue(), claim.CanonicalValue()) || len(input.Events) != 1 || !bytes.Equal(input.Events[0].Payload, e.Payload()) {
			return unsupported(e, "permanent claim ownership schema/creation binding")
		}
	}
	if (claim != nil && creations != 1) || (claim == nil && creations != 0) {
		return fmt.Errorf("coordinate proof: UNSUPPORTED declared workspace claim/creation census: %d", creations)
	}
	for _, e := range r.Ack.Plan().Events() {
		switch e.Family() {
		case "workspace": // Every workspace was typed and qualified above.
		case "execution_slot":
			if e.EventSchemaVersion() != execution.ExecutionEventSchemaV2 || e.ReducerVersion() != execution.ExecutionReducerV2 {
				return unsupported(e, "unclosed execution schema")
			}
			event, err := execution.DecodeExecutionSlotEventV2(e.Payload())
			if err != nil {
				return err
			}
			s := event.State
			coordinate, err := execution.ExecutionSlotCoordinateV2(s.ProjectID, s.MissionID, s.WorkRevisionID, s.WorkID)
			if err != nil || coordinate != e.Coordinate() || s.ProjectID != ids.ProjectID(scope.ProjectID) || s.MissionID != ids.MissionID(scope.MissionID) || s.LogicalVersion != uint64(e.LogicalVersion()) {
				return unsupported(e, "foreign/nonexact typed execution owner")
			}
			// Bound references are admitted only after the second atomic,
			// publication-grouped pass closes the fully replayed typed owners.
			// Exact envelope authority and transition/predecessor laws are
			// enforced by typed playback over the contiguous full history.
		case "operation":
			if e.EventSchemaVersion() != 2 || e.ReducerVersion() != 2 || (bindingScope.Kind == journalv2.ScopeHost && workspaceCount == 0) {
				return unsupported(e, "host root lacks closed typed owner facts")
			}
		default:
			// Extend only with a closed typed owner proof, not a family whitelist.
			return unsupported(e, "no closed typed owner proof in this foundation")
		}
	}
	return nil
}

// Reference closure is exact and historical, never a latest-state fallback or
// an arbitrary digest map. Capture and history identity cannot cross snapshots.
type terminalStateReference struct {
	historySHA string
	capture    journalv2.StoreRevisionV2
	evidence   integration.AcceptanceEvidenceV2
}

func (p *terminalCoordinateProof) resolveAcceptanceReference(ref terminalStateReference) (terminalHistoricalState, error) {
	if p == nil || ref.historySHA != p.historySHA || ref.capture != p.capture {
		return terminalHistoricalState{}, fmt.Errorf("coordinate reference: cross snapshot")
	}
	e := ref.evidence
	if e.Disposition != integration.EvidenceSatisfiedV2 || e.LogicalVersion == 0 || e.RecordRevision == 0 || e.StateDigestSHA256 == "" {
		return terminalHistoricalState{}, fmt.Errorf("coordinate reference: unsupported/nonexact evidence")
	}
	if e.Coordinate.Scope != p.scope {
		s := e.Coordinate.Scope
		missionExecution := e.Coordinate.Kind == "execution_slot" && s.Kind == journalv2.ScopeWork && s.ProjectID == p.scope.ProjectID && s.MissionID == p.scope.MissionID
		_, workspaceOwner := p.workspaces[e.Coordinate]
		if !missionExecution && (!workspaceOwner || s != (journalv2.Scope{Kind: journalv2.ScopeHost})) {
			return terminalHistoricalState{}, fmt.Errorf("coordinate reference: foreign scope")
		}
	}
	var matches []terminalHistoricalState
	for key, state := range p.states {
		if key.coordinate == e.Coordinate && key.version == e.LogicalVersion && key.revision == e.RecordRevision {
			matches = append(matches, state)
		}
	}
	if len(matches) != 1 {
		return terminalHistoricalState{}, fmt.Errorf("coordinate reference: unresolved exact historical coordinate")
	}
	state := matches[0]
	raw, err := journalv2.ReducerStateDigestV2(state.canonical)
	if err != nil || raw != state.digest || raw.String() != e.StateDigestSHA256 {
		return terminalHistoricalState{}, fmt.Errorf("coordinate reference: unverified raw digest preimage")
	}
	state.canonical = append([]byte(nil), state.canonical...)
	return state, nil
}

// Only workspace-binding comparison facts are supported here. This does NOT
// compare full business states or normalize any consumer's embedded references.
type terminalWorkspacePair struct {
	left, right     journalv2.AggregateCoordinate
	comparisonFacts string
}

func pairTerminalWorkspaceBindings(left, right *terminalCoordinateProof) ([]terminalWorkspacePair, error) {
	if left == nil || right == nil || left.scope != right.scope || len(left.workspaces) != len(right.workspaces) {
		return nil, fmt.Errorf("workspace correspondence: census/scope")
	}
	facts := func(w terminalWorkspaceInventory) string {
		// Retain every binding fact except proven physical root/claim coordinates
		// and serving epoch. No replacement durable claim digest is minted.
		b := w.first
		b.ManagedRoot = ""
		b.CanonicalPath = ""
		b.PathClaimDigest = journalv2.Digest{}
		b.RuntimeEpochID = ""
		return string(mustTerminalJSON(struct {
			Binding  workspace.BindingV2
			Relative string
		}{b, w.relative}))
	}
	index := make(map[string]journalv2.AggregateCoordinate)
	for c, w := range right.workspaces {
		f := facts(w)
		if _, ok := index[f]; ok {
			return nil, fmt.Errorf("workspace correspondence: noninjective right")
		}
		index[f] = c
	}
	var pairs []terminalWorkspacePair
	for c, w := range left.workspaces {
		f := facts(w)
		other, ok := index[f]
		if !ok {
			return nil, fmt.Errorf("workspace correspondence: unmatched exact binding facts")
		}
		delete(index, f)
		pairs = append(pairs, terminalWorkspacePair{c, other, f})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].comparisonFacts < pairs[j].comparisonFacts })
	return pairs, nil
}
