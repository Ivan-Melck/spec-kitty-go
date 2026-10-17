package scenario

import (
	"bytes"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"syscall"
	"testing"
	"time"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/gitrepo"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type terminalRepositoryFixtureOptions struct {
	linked, foreignProject, foreignRepository, nestedLayout bool
	declaration                                             string
	generation                                              uint64
}

// Fresh btrfs temp repositories ONLY; never run a crown/portable scenario or a
// provider. Actual Bind/default Runner and actual service/coordinator ACKs use
// the reviewed claim-retaining port. PathIdentity flags here are typed fixture
// declarations, NOT successful filesystem/effect-observation witnesses.
func terminalRepositoryFixture(t *testing.T, opt terminalRepositoryFixtureOptions) (*terminalPublicationHistory, *portableHarness) {
	t.Helper()
	root := t.TempDir()
	var fs syscall.Statfs_t
	if err := syscall.Statfs(root, &fs); err != nil || uint64(fs.Type) != 0x9123683e {
		t.Fatalf("repository fixture requires btrfs t.TempDir: %v type=%x", err, fs.Type)
	}
	primary, managed := filepath.Join(root, "primary"), filepath.Join(root, "workspaces")
	if err := os.Mkdir(primary, 0700); err != nil {
		t.Fatal(err)
	}
	runner := gitrepo.Runner{}
	git := func(dir string, args ...string) string {
		t.Helper()
		s, err := runner.Run(t.Context(), dir, args...)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	git(primary, "init", "-b", "main")
	git(primary, "config", "user.name", "Private repository fixture")
	git(primary, "config", "user.email", "private@example.invalid")
	if err := os.WriteFile(filepath.Join(primary, "base.txt"), []byte("original configured snapshot\n"), 0600); err != nil {
		t.Fatal(err)
	}
	git(primary, "add", "base.txt")
	git(primary, "commit", "-m", "initial")
	if opt.linked {
		linked := filepath.Join(root, "linked-primary")
		git(primary, "worktree", "add", "-b", "linked-primary", linked)
		primary = linked
	}
	project := ids.ProjectID("project-fixture")
	if opt.foreignProject {
		project = "project-other"
	}
	repo, err := gitrepo.Bind(t.Context(), runner, "repository-a", project, primary, managed, "refs/heads/main", time.Date(2026, 9, 5, 12, 0, 0, 123456789, time.UTC))
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.PrepareManagedRoot(); err != nil {
		t.Fatal(err)
	}
	// The harness retains exactly this ORIGINAL repository; no fake binding or
	// passed-owner replacement is manufactured for positive/owner controls.
	driver := &realScenarioGit{repository: repo, primary: primary, managed: managed}
	if opt.declaration == "primary" {
		driver.primary = filepath.Join(root, "declared-primary")
	}
	if opt.declaration == "managed" {
		driver.managed = filepath.Join(root, "declared-managed")
	}
	owner := &portableHarness{root: root, git: driver}
	durable, coordinator := terminalBoundCoordinator(t)
	template := terminalRecoveryFixture(t, false).History.Publications[1].Ack.Plan()
	seedPlan := template
	if opt.generation != 0 {
		binding := *template.BindingCreate()
		input := journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: template.OperationState(), Heads: template.Heads()}
		for _, e := range template.Events() {
			input.Events = append(input.Events, terminalCouplingDraft(e))
		}
		terminalExecutionControlEvent(t, &input, func(e *execution.ExecutionSlotEventV2, _ *journalv2.EventDraftV2) {
			e.State.Generation = opt.generation
		})
		terminalBoundRefresh(t, &input)
		seedPlan, err = journalv2.SealCommitPlanV2(input, journalv2.DefaultEventRegistryV2())
		if err != nil {
			t.Fatal(err)
		}
	}
	seed, err := coordinator.Publish(t.Context(), seedPlan)
	if err != nil {
		t.Fatal(err)
	}
	var before execution.ExecutionSlotStateV2
	var coordinate journalv2.AggregateCoordinate
	for _, e := range seed.Plan().Events() {
		if e.Family() == "execution_slot" {
			event, err := execution.DecodeExecutionSlotEventV2(e.Payload())
			if err != nil {
				t.Fatal(err)
			}
			before = event.State
			coordinate = e.Coordinate()
		}
	}
	path := filepath.Join(driver.managed, "workspace-a")
	if opt.nestedLayout {
		path = filepath.Join(driver.managed, "nested", "workspace-a")
	}
	repository := ids.RepositoryID("repository-a")
	if opt.foreignRepository {
		repository = "repository-other"
	}
	b := workspace.BindingV2{WorkspaceID: "workspace-a", ProjectID: before.ProjectID, MissionID: before.MissionID, WorkRevisionID: before.WorkRevisionID, WorkID: before.WorkID, RunID: before.RunID, LeaseID: before.LeaseID, Generation: before.Generation, Fence: before.Fence, ActorID: before.ActorID, ProcessID: before.ProcessID, RuntimeEpochID: before.RuntimeEpochID, RepositoryID: repository, LaneID: "lane-a", ManagedRoot: driver.managed, CanonicalPath: path, PathIdentity: workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}, OwnedBranch: "kitty/work-a", PinnedBase: repo.Binding().Primary.Head, Dependencies: []workspace.DependencyV2{{WorkID: "work-dependency", Commit: strings.Repeat("d", 40)}}}
	b.PathClaimDigest, err = workspace.WorkspacePathClaimDigestV2(b.RepositoryID, b.ManagedRoot, b.CanonicalPath)
	if err != nil {
		t.Fatal(err)
	}
	wc, err := workspace.WorkspaceCoordinateV2(b)
	if err != nil {
		t.Fatal(err)
	}
	port := &terminalBoundFixturePort{terminalCouplingPort: terminalCouplingPort{executionCoordinate: coordinate, workspaceCoordinate: wc, execution: execution.ExecutionSlotObservationV2{State: &before, RecordRevision: seed.ProviderRevision()}, epoch: before.RuntimeEpochID}, coordinator: coordinator, acks: []journalv2.AcknowledgedPublicationV2{seed}}
	service, err := execution.NewWorkspaceAuthorityServiceV2(port, port, journalv2.DefaultEventRegistryV2())
	if err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 9, 5, 12, 10, 0, 0, time.UTC)
	contribution, err := workspace.NewBindingContributionV2(workspace.BindingContributionInputV2{Binding: b, EffectID: "effect-create-a", EventID: "event-bind-workspace", OccurredAt: at})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Execute(t.Context(), execution.WorkspaceAuthorityRequestV2{Kind: execution.WorkspaceBindV2, OperationID: "operation-bind", OperationEventID: "event-bind-operation", ExecutionEventID: "event-bind-execution", Governance: before.Governance, Contribution: contribution, ObservedAt: at})
	if err != nil || result.Replayed || result.RecordRevision != 2 {
		t.Fatalf("actual bind: %v %+v", err, result)
	}
	port.epoch = "epoch-new"
	result, err = service.RebindRuntimeEpoch(t.Context(), execution.WorkspaceEpochRebindRequestV2{ProjectID: before.ProjectID, MissionID: before.MissionID, WorkRevisionID: before.WorkRevisionID, WorkID: before.WorkID, ActorID: before.ActorID, ProcessID: before.ProcessID, OriginatingRuntimeEpochID: port.epoch, OperationID: "operation-bound-rebind", OperationEventID: "event-bound-rebind-operation", ExecutionEventID: "event-bound-rebind-execution", WorkspaceEventID: "event-bound-rebind-workspace", Governance: before.Governance, ObservedAt: at.Add(10 * time.Minute)})
	if err != nil || result.Replayed || result.RecordRevision != 3 {
		t.Fatalf("actual rebind: %v %+v", err, result)
	}
	return terminalBoundHistory(t, durable, port.acks), owner
}
func terminalRepositoryGraph(t *testing.T, h *terminalPublicationHistory, owner *portableHarness) *terminalCoordinateProof {
	t.Helper()
	p, err := newTerminalCoordinateProof(h, owner, "repository-a", "repository-other")
	if err != nil {
		t.Fatal("must reach configured layer through actual validated history", err)
	}
	return p
}
func terminalRepositoryRef(p *terminalRepositoryProof, s terminalHistoricalState) terminalRepositoryReference {
	return terminalRepositoryReference{p.origin.historySHA, p.origin.capture, p.origin.integrity, s.key, s.digest}
}
func TestTerminalRepositoryOriginalConfiguration(t *testing.T) {
	for _, linked := range []bool{false, true} {
		t.Run(fmt.Sprint(linked), func(t *testing.T) {
			h, owner := terminalRepositoryFixture(t, terminalRepositoryFixtureOptions{linked: linked, generation: math.MaxInt64})
			driver := owner.git.(*realScenarioGit)
			original := driver.repository.Binding()
			p := terminalRepositoryGraph(t, h, owner)
			proof, err := newTerminalRepositoryProof(p)
			if err != nil {
				t.Fatal(err)
			}
			if len(proof.entries) != 2 || proof.origin.binding != original {
				t.Fatal("original configuration or every-version census lost")
			}
			if linked && terminalRepositoryWithin(original.CommonDir, original.PrimaryPath) {
				t.Fatal("fixture did not exercise common outside linked primary")
			}
			old, next := p.states[p.events["event-bind-workspace"]], p.states[p.events["event-bound-rebind-workspace"]]
			if old.key == next.key || old.digest == next.digest {
				t.Fatal("old snapshot aliased to latest")
			}
			for _, s := range []terminalHistoricalState{old, next} {
				got, err := proof.resolve(terminalRepositoryRef(proof, s))
				if err != nil {
					t.Fatal(err)
				}
				state, err := workspace.DecodeStateV2(got.canonical)
				if err != nil {
					t.Fatal(err)
				}
				if got.key != s.key || got.configured != original || !bytes.Equal(got.canonical, s.canonical) || got.claimRevision != 2 || state.Binding.Generation != math.MaxInt64 || state.Binding.PinnedBase != original.Primary.Head || state.Binding.Dependencies[0].Commit != strings.Repeat("d", 40) {
					t.Fatal("snapshot/timestamp/uint64/Git SHA/claim preservation")
				}
				claim := h.Publications[1].Ack.Plan().WorkspacePathClaim()
				if got.claim.ClaimDigest() != claim.ClaimDigest() || !bytes.Equal(got.claim.CanonicalValue(), claim.CanonicalValue()) {
					t.Fatal("permanent claim substituted")
				}
				got.canonical[0] = '!'
				got.configured.Primary.Head = "changed"
				got.configured.CreatedAt = time.Time{}
				claimCopy := got.claim.CanonicalValue()
				claimCopy[0] = '!'
				again, err := proof.resolve(terminalRepositoryRef(proof, s))
				if err != nil || !bytes.Equal(again.canonical, s.canonical) || again.configured != original || !bytes.Equal(again.claim.CanonicalValue(), claim.CanonicalValue()) {
					t.Fatal("lookup alias", err)
				}
			}
			// Delete only the fresh temp repositories after capture: pure proof and
			// retained lookup must neither refresh Binding nor consult filesystem/Git.
			if err := os.RemoveAll(filepath.Join(owner.root, "primary")); err != nil {
				t.Fatal(err)
			}
			if linked {
				if err := os.RemoveAll(original.PrimaryPath); err != nil {
					t.Fatal(err)
				}
			}
			driver.repository = nil
			driver.primary = "changed"
			driver.managed = "changed"
			driver.calls = append(driver.calls, "later unrelated effect")
			if _, err := newTerminalRepositoryProof(p); err != nil {
				t.Fatal("proof performed later IO/Binding refresh", err)
			}
			p.states[old.key] = terminalHistoricalState{}
			p.repositoryCapture = terminalRepositoryCapture{}
			h.Publications = nil
			if _, err := proof.resolve(terminalRepositoryRef(proof, old)); err != nil {
				t.Fatal("owned proof aliases input", err)
			}
		})
	}
}

func TestTerminalRepositoryPublishedConfiguredControls(t *testing.T) {
	cases := map[string]terminalRepositoryFixtureOptions{
		"foreign_project": {foreignProject: true}, "foreign_repository": {foreignRepository: true}, "exact_derived_layout": {nestedLayout: true}, "primary_declaration": {declaration: "primary"}, "managed_declaration": {declaration: "managed"}, "adapter_generation_overflow": {generation: uint64(math.MaxInt64) + 1}, "max_uint64": {generation: math.MaxUint64},
	}
	for name, opt := range cases {
		t.Run(name, func(t *testing.T) {
			h, owner := terminalRepositoryFixture(t, opt)
			p := terminalRepositoryGraph(t, h, owner)
			if opt.generation != 0 {
				for _, w := range p.workspaces {
					for _, key := range w.versions {
						s, err := workspace.DecodeStateV2(p.states[key].canonical)
						if err != nil || s.Binding.Generation != opt.generation {
							t.Fatal("overflow rounded before configured gate", err)
						}
					}
				}
			}
			_, err := newTerminalRepositoryProof(p)
			if err == nil {
				t.Fatal("published owner-invalid configuration accepted")
			}
			t.Logf("actual Bind + service/coordinator + logical closure passed; configured refusal: %v", err)
		})
	}
}

func TestTerminalRepositoryAbsentCaptureLogicalProofIntact(t *testing.T) {
	h, owner := terminalBoundFixture(t, false)
	for _, kind := range []string{"absent", "portable", "nil_real_driver", "nil_real_repository"} {
		t.Run(kind, func(t *testing.T) {
			clone := *owner
			switch kind {
			case "portable":
				clone.git = newAttributableGit()
			case "nil_real_driver":
				var g *realScenarioGit
				clone.git = g
			case "nil_real_repository":
				clone.git = &realScenarioGit{primary: filepath.Join(owner.root, "primary"), managed: filepath.Join(owner.root, "workspaces")}
			}
			p, err := newTerminalCoordinateProof(h, &clone, "repository-a")
			if err != nil {
				t.Fatal("logical proof regressed", err)
			}
			if len(p.boundExecutions) != 2 {
				t.Fatal("logical reference census changed")
			}
			if _, err := newTerminalRepositoryProof(p); err == nil {
				t.Fatal("absent/portable configuration inherited real witness")
			}
		})
	}
}

// Capture mutants are NOT fabricated owner-valid publications. These exercise
// integrity against edits to the construction-time scalar copy, separately
// from the genuinely published configured controls above.
func TestTerminalRepositoryCaptureIntegrityControls(t *testing.T) {
	h, owner := terminalRepositoryFixture(t, terminalRepositoryFixtureOptions{})
	p := terminalRepositoryGraph(t, h, owner)
	mutations := map[string]func(*terminalRepositoryCapture){
		"repository":          func(c *terminalRepositoryCapture) { c.binding.RepositoryID = "repository-other" },
		"project":             func(c *terminalRepositoryCapture) { c.binding.ProjectID = "project-other" },
		"primary":             func(c *terminalRepositoryCapture) { c.binding.PrimaryPath += "/other" },
		"common":              func(c *terminalRepositoryCapture) { c.binding.CommonDir += "/other" },
		"managed":             func(c *terminalRepositoryCapture) { c.binding.ManagedRoot += "/other" },
		"target":              func(c *terminalRepositoryCapture) { c.binding.TargetRef = "refs/heads/other" },
		"head":                func(c *terminalRepositoryCapture) { c.binding.Primary.Head = strings.Repeat("e", 40) },
		"index":               func(c *terminalRepositoryCapture) { c.binding.Primary.IndexDigest = strings.Repeat("e", 64) },
		"tree":                func(c *terminalRepositoryCapture) { c.binding.Primary.TreeDigest = strings.Repeat("e", 64) },
		"created":             func(c *terminalRepositoryCapture) { c.binding.CreatedAt = c.binding.CreatedAt.Add(time.Nanosecond) },
		"verified":            func(c *terminalRepositoryCapture) { c.binding.VerifiedAt = c.binding.VerifiedAt.Add(time.Nanosecond) },
		"primary_declaration": func(c *terminalRepositoryCapture) { c.primaryDeclaration += "/other" },
		"managed_declaration": func(c *terminalRepositoryCapture) { c.managedDeclaration += "/other" },
		"incomplete":          func(c *terminalRepositoryCapture) { c.binding.Primary.IndexDigest = "" },
		"oversized": func(c *terminalRepositoryCapture) {
			c.binding.TargetRef = strings.Repeat("x", workspace.MaxWorkspacePathBytesV2+1)
		},
		"history": func(c *terminalRepositoryCapture) { c.historySHA = strings.Repeat("f", 64) },
		"capture": func(c *terminalRepositoryCapture) {
			c.capture, _ = journalv2.NewStoreRevisionV2("database-other", 3, 3)
		},
		"commitment": func(c *terminalRepositoryCapture) { c.integrity[0] ^= 1 },
	}
	for name, mutate := range mutations {
		t.Run(name, func(t *testing.T) {
			clone := *p
			mutate(&clone.repositoryCapture)
			if _, err := newTerminalRepositoryProof(&clone); err == nil {
				t.Fatal("mutated capture accepted")
			}
		})
	}
	foreignH, foreignOwner := terminalRepositoryFixture(t, terminalRepositoryFixtureOptions{})
	foreign := terminalRepositoryGraph(t, foreignH, foreignOwner)
	clone := *p
	clone.repositoryCapture = foreign.repositoryCapture
	if _, err := newTerminalRepositoryProof(&clone); err == nil {
		t.Fatal("foreign source graph capture accepted")
	}
}

func TestTerminalRepositoryConfiguredRootAncestryControls(t *testing.T) {
	h, owner := terminalRepositoryFixture(t, terminalRepositoryFixtureOptions{})
	p := terminalRepositoryGraph(t, h, owner)
	// Locally resealed invalid configurations isolate the pure separation law;
	// they are NOT credited as original configured-owner provenance witnesses.
	for _, root := range []string{"primary", "common"} {
		for _, direction := range []string{"equal", "managed_inside", "managed_ancestor"} {
			t.Run(root+"_"+direction, func(t *testing.T) {
				clone := *p
				c := &clone.repositoryCapture
				r := c.binding.PrimaryPath
				if root == "common" {
					r = c.binding.CommonDir
				}
				switch direction {
				case "equal":
					c.binding.ManagedRoot = r
				case "managed_inside":
					c.binding.ManagedRoot = filepath.Join(r, "managed")
				case "managed_ancestor":
					c.binding.ManagedRoot = filepath.Dir(r)
				}
				c.managedDeclaration = c.binding.ManagedRoot
				c.integrity, _ = c.commitment()
				_, err := newTerminalRepositoryProof(&clone)
				if err == nil || !strings.Contains(err.Error(), "root overlap") {
					t.Fatal("ancestry refusal not isolated", err)
				}
			})
		}
	}
}

func TestTerminalRepositoryExactLookupAndGraphControls(t *testing.T) {
	h, owner := terminalRepositoryFixture(t, terminalRepositoryFixtureOptions{})
	p := terminalRepositoryGraph(t, h, owner)
	proof, err := newTerminalRepositoryProof(p)
	if err != nil {
		t.Fatal(err)
	}
	old, next := p.states[p.events["event-bind-workspace"]], p.states[p.events["event-bound-rebind-workspace"]]
	cases := map[string]func(*terminalRepositoryReference){
		"database":              func(r *terminalRepositoryReference) { r.key.database = "database-other" },
		"coordinate":            func(r *terminalRepositoryReference) { r.key.coordinate.ID = "workspace-other" },
		"version":               func(r *terminalRepositoryReference) { r.key.version = next.key.version },
		"revision":              func(r *terminalRepositoryReference) { r.key.revision = next.key.revision },
		"event":                 func(r *terminalRepositoryReference) { r.key.event = next.key.event },
		"publication":           func(r *terminalRepositoryReference) { r.key.publication = next.key.publication },
		"latest_digest_for_old": func(r *terminalRepositoryReference) { r.digest = next.digest },
		"history":               func(r *terminalRepositoryReference) { r.historySHA = strings.Repeat("f", 64) },
		"origin":                func(r *terminalRepositoryReference) { r.origin[0] ^= 1 },
		"capture": func(r *terminalRepositoryReference) {
			r.capture, _ = journalv2.NewStoreRevisionV2("database-other", 3, 3)
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			r := terminalRepositoryRef(proof, old)
			mutate(&r)
			if _, err := proof.resolve(r); err == nil {
				t.Fatal("nonexact lookup accepted")
			}
		})
	}
	for _, version := range []terminalHistoricalState{old, next} {
		t.Run(fmt.Sprint(version.key.version), func(t *testing.T) {
			original := p.states[version.key]
			state, err := workspace.DecodeStateV2(original.canonical)
			if err != nil {
				t.Fatal(err)
			}
			state.Binding.PinnedBase = strings.Repeat("e", 40)
			changed := original
			changed.canonical = state.CanonicalBytes()
			changed.digest, _ = journalv2.ReducerStateDigestV2(changed.canonical)
			p.states[version.key] = changed
			if _, err := newTerminalRepositoryProof(p); err == nil {
				t.Fatal("changed historical Git SHA accepted")
			}
			p.states[version.key] = original
		})
	}
	for _, kind := range []string{"claim", "claim_revision", "drop_old_version", "first_binding", "graph_history", "graph_capture", "unknown_business_field"} {
		t.Run(kind, func(t *testing.T) {
			clone := *p
			clone.workspaces = make(map[journalv2.AggregateCoordinate]terminalWorkspaceInventory)
			for c, w := range p.workspaces {
				w.versions = append([]terminalHistoricalKey(nil), w.versions...)
				clone.workspaces[c] = w
			}
			clone.states = make(map[terminalHistoricalKey]terminalHistoricalState)
			for key, s := range p.states {
				clone.states[key] = s
			}
			switch kind {
			case "graph_history":
				clone.historySHA = strings.Repeat("f", 64)
			case "graph_capture":
				clone.capture, _ = journalv2.NewStoreRevisionV2("database-other", 3, 3)
			case "unknown_business_field":
				s := clone.states[old.key]
				s.canonical = append(bytes.Clone(s.canonical[:len(s.canonical)-1]), []byte(`,"unknown_business":18446744073709551615}`)...)
				s.digest, _ = journalv2.ReducerStateDigestV2(s.canonical)
				clone.states[old.key] = s
			default:
				for c, w := range clone.workspaces {
					switch kind {
					case "claim":
						w.claim, _ = journalv2.NewWorkspacePathClaimV2(w.claim.ClaimDigest(), []byte(`{"changed":true}`))
					case "claim_revision":
						w.claimRevision++
					case "drop_old_version":
						w.versions = w.versions[1:]
					case "first_binding":
						w.first.OwnedBranch = "kitty/changed"
					}
					clone.workspaces[c] = w
				}
			}
			if _, err := newTerminalRepositoryProof(&clone); err == nil {
				t.Fatal("graph/capture/source/version/claim mix accepted")
			}
		})
	}
	if !reflect.DeepEqual(proof.origin.binding, owner.git.(*realScenarioGit).repository.Binding()) {
		t.Fatal("configuration copy lost facts")
	}
}

func TestTerminalRepositoryEarlierTypedReplayControls(t *testing.T) {
	h, owner := terminalRepositoryFixture(t, terminalRepositoryFixtureOptions{})
	// Mutants really go through sealing/publication. Refusals at those gates or
	// logical playback are NOT configured proof coverage.
	t.Run("unknown_workspace_field", func(t *testing.T) {
		_, coordinator := terminalBoundCoordinator(t)
		for index, receipt := range h.Publications {
			plan := receipt.Ack.Plan()
			binding := *plan.BindingCreate()
			input := journalv2.CommitPlanInput{OperationBinding: &binding, OperationState: plan.OperationState(), Heads: plan.Heads(), Conditions: plan.Conditions(), EffectIntents: plan.EffectIntents(), WorkspacePathClaim: plan.WorkspacePathClaim()}
			for _, event := range plan.Events() {
				input.Events = append(input.Events, terminalCouplingDraft(event))
			}
			// Rebuild the original unsealed operation/event/head/CAS input first:
			// the retained sealed operation carries LastPublicationID. Never run
			// this strict typed refresh after introducing the unknown field.
			terminalBoundRefresh(t, &input)
			if index == 2 {
				for i := range input.Events {
					draft := &input.Events[i]
					if draft.Family != "workspace" {
						continue
					}
					unknown := append(bytes.Clone(draft.Payload[:len(draft.Payload)-1]), []byte(`,"unknown":true}`)...)
					var err error
					draft.Payload, err = journalv2.CanonicalObject(unknown)
					if err != nil {
						t.Fatal("canonical unknown-field fixture", err)
					}
				}
			}
			sealed, err := journalv2.SealCommitPlanV2(input, journalv2.DefaultEventRegistryV2())
			stage := "seal"
			if err == nil {
				_, err = coordinator.Publish(t.Context(), sealed)
				stage = "publish"
			}
			if index < 2 && err != nil {
				t.Fatal("unaltered prefix", err)
			}
			if index == 2 {
				if stage != "publish" || !workspace.IsKind(err, workspace.KindInvalid) || !strings.Contains(err.Error(), `workspace authority refused: payload: json: unknown field "unknown"`) {
					t.Fatalf("expected precise typed publication unknown-field refusal, got %s: %v", stage, err)
				}
				t.Logf("earlier %s refusal, NOT configured-layer coverage: %v", stage, err)
			}
		}
	})
	for _, kind := range []string{"changed_continuation_sha", "changed_continuation_generation"} {
		t.Run(kind, func(t *testing.T) {
			mutated, stage, err := terminalBoundReseal(t, h, func(index int, input *journalv2.CommitPlanInput) {
				if index != 2 {
					return
				}
				for i := range input.Events {
					e := &input.Events[i]
					if e.Family != "workspace" {
						continue
					}
					payload, decodeErr := workspace.DecodeEventV2(e.Payload)
					if decodeErr != nil {
						t.Fatal(decodeErr)
					}
					if kind == "changed_continuation_sha" {
						payload.State.Binding.PinnedBase = strings.Repeat("e", 40)
					} else {
						payload.State.Binding.Generation++
					}
					e.Payload, decodeErr = journalv2.CanonicalObject(mustTerminalJSON(payload))
					if decodeErr != nil {
						t.Fatal("canonical continuation fixture", decodeErr)
					}
				}
			})
			if err == nil {
				_, err = newTerminalCoordinateProof(mutated, owner, "repository-a")
				stage = "logical"
			}
			if stage != "publish" || !workspace.IsKind(err, workspace.KindStaleAuthority) || !strings.Contains(err.Error(), "workspace authority refused: runtime_epoch_rebind") {
				t.Fatalf("expected typed publication immutable-binding refusal, got %s: %v", stage, err)
			}
			t.Logf("earlier %s refusal, NOT configured-layer coverage: %v", stage, err)
		})
	}
}
