package workspace

import "context"

type PreparationActionV2 string

const (
	PreparationObserveCreatedV2 PreparationActionV2 = "observe_created"
	PreparationVerifyBaseV2     PreparationActionV2 = "verify_base"
	PreparationRequestDepsV2    PreparationActionV2 = "request_dependencies"
	PreparationObserveDepsV2    PreparationActionV2 = "observe_dependencies"
	PreparationVerifyOwnerV2    PreparationActionV2 = "verify_ownership"
	PreparationPublishReadyV2   PreparationActionV2 = "publish_ready"
	PreparationReconcileV2      PreparationActionV2 = "reconciliation_required"
)

type PreparationCommandV2 struct {
	Action      PreparationActionV2
	Prior       StateV2
	Observation ObservationV2
	PreTarget   string
	PostTarget  string
}

type PreparationJournalV2 interface {
	ObservePreparation(context.Context, BindingV2) (StateV2, error)
	PublishPreparation(context.Context, PreparationCommandV2) (StateV2, error)
}

type RepositoryTargetGuardV2 interface {
	TargetHead(context.Context) (string, error)
	ObserveWorkspace(context.Context, BindingV2) (ObservationV2, error)
}

type PreparationEffectPortV2 interface {
	ObserveWorkspace(context.Context, BindingV2) (ObservationV2, error)
	CreateWorkspace(context.Context, BindingV2) error
	PrepareDependencies(context.Context, BindingV2) error
	WithRepositoryTargetGuard(context.Context, func(RepositoryTargetGuardV2) error) error
}

type PreparationServiceV2 struct {
	journal PreparationJournalV2
	effects PreparationEffectPortV2
}

func NewPreparationServiceV2(journal PreparationJournalV2, effects PreparationEffectPortV2) (*PreparationServiceV2, error) {
	if journal == nil || effects == nil {
		return nil, workspaceV2Error(KindInvalid, "preparation_dependencies", nil)
	}
	return &PreparationServiceV2{journal: journal, effects: effects}, nil
}

func (s *PreparationServiceV2) EnsureReady(ctx context.Context, binding BindingV2) (StateV2, error) {
	if err := binding.Validate(); err != nil {
		return StateV2{}, err
	}
	state, err := s.journal.ObservePreparation(ctx, binding)
	if err != nil {
		return StateV2{}, err
	}
	if !bindingEqualV2(state.Binding, binding) {
		return StateV2{}, workspaceV2Error(KindWorkspaceConflict, "binding", nil)
	}
	for steps := 0; steps < 16; steps++ {
		switch state.Stage {
		case StageCreationRequestedV2:
			observation, observeErr := s.effects.ObserveWorkspace(ctx, binding)
			if observeErr != nil {
				return StateV2{}, observeErr
			}
			if !observation.Registered {
				if err := s.effects.CreateWorkspace(ctx, binding); err != nil {
					return StateV2{}, err
				}
				observation, observeErr = s.effects.ObserveWorkspace(ctx, binding)
				if observeErr != nil {
					return StateV2{}, observeErr
				}
			}
			if !observation.Registered {
				return StateV2{}, workspaceV2Error(KindWorkspaceNotReady, "worktree_registration", nil)
			}
			state, err = s.journal.PublishPreparation(ctx, PreparationCommandV2{Action: PreparationObserveCreatedV2, Prior: state, Observation: observation})
		case StageWorktreeObservedV2:
			if !state.Observation.Registered || state.Observation.Head != binding.PinnedBase {
				return StateV2{}, workspaceV2Error(KindTargetMoved, "pinned_base", nil)
			}
			state, err = s.journal.PublishPreparation(ctx, PreparationCommandV2{Action: PreparationVerifyBaseV2, Prior: state, Observation: state.Observation})
		case StageBaseVerifiedV2:
			state, err = s.journal.PublishPreparation(ctx, PreparationCommandV2{Action: PreparationRequestDepsV2, Prior: state, Observation: state.Observation})
		case StageDependenciesRequestedV2:
			observation, observeErr := s.effects.ObserveWorkspace(ctx, binding)
			if observeErr != nil {
				return StateV2{}, observeErr
			}
			if !dependenciesPreparedV2(binding, observation) {
				if err := s.effects.PrepareDependencies(ctx, binding); err != nil {
					return StateV2{}, err
				}
				observation, observeErr = s.effects.ObserveWorkspace(ctx, binding)
				if observeErr != nil {
					return StateV2{}, observeErr
				}
			}
			if !dependenciesPreparedV2(binding, observation) {
				return StateV2{}, workspaceV2Error(KindDependencyConflict, "dependency_head", nil)
			}
			state, err = s.journal.PublishPreparation(ctx, PreparationCommandV2{Action: PreparationObserveDepsV2, Prior: state, Observation: observation})
		case StageDependenciesObservedV2:
			if !ownedWorkspaceObservationV2(binding, state.Observation) {
				return StateV2{}, workspaceV2Error(KindWorkspaceNotReady, "ownership", nil)
			}
			state, err = s.journal.PublishPreparation(ctx, PreparationCommandV2{Action: PreparationVerifyOwnerV2, Prior: state, Observation: state.Observation})
		case StageOwnershipVerifiedV2:
			err = s.effects.WithRepositoryTargetGuard(ctx, func(guard RepositoryTargetGuardV2) error {
				preTarget, e := guard.TargetHead(ctx)
				if e != nil {
					return e
				}
				pre, e := guard.ObserveWorkspace(ctx, binding)
				if e != nil {
					return e
				}
				post, e := guard.ObserveWorkspace(ctx, binding)
				if e != nil {
					return e
				}
				postTarget, e := guard.TargetHead(ctx)
				if e != nil {
					return e
				}
				if preTarget != binding.PinnedBase || postTarget != preTarget || !observationsEqualV2(pre, post) || !ownedWorkspaceObservationV2(binding, post) {
					return workspaceV2Error(KindTargetMoved, "ready_guard", nil)
				}
				state, e = s.journal.PublishPreparation(ctx, PreparationCommandV2{Action: PreparationPublishReadyV2, Prior: state, Observation: post, PreTarget: preTarget, PostTarget: postTarget})
				return e
			})
		case StageReadyV2:
			return state, nil
		case StageReconciliationV2:
			return StateV2{}, workspaceV2Error(KindWorkspaceNotReady, "reconciliation_required", nil)
		case StageQuarantinedV2:
			return StateV2{}, workspaceV2Error(KindStaleAuthority, "workspace_quarantined", nil)
		default:
			return StateV2{}, workspaceV2Error(KindWorkspaceNotReady, "workspace_stage", nil)
		}
		if err != nil {
			return StateV2{}, err
		}
	}
	return StateV2{}, workspaceV2Error(KindWorkspaceNotReady, "preparation_budget", nil)
}

func dependenciesPreparedV2(binding BindingV2, o ObservationV2) bool {
	return o.Registered && o.Clean && o.Head != "" && o.DependencyHead == o.Head
}
func ownedWorkspaceObservationV2(binding BindingV2, o ObservationV2) bool {
	return dependenciesPreparedV2(binding, o) && o.Branch == binding.OwnedBranch && o.GitDirIdentity != "" && o.IndexIdentity != ""
}
func observationsEqualV2(a, b ObservationV2) bool { return a == b }
