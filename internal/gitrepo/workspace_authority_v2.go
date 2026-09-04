package gitrepo

import (
	"context"
	"errors"
	"math"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

// WorkspaceAuthorityEffectsV2 adapts the repository's existing, locked Git
// implementation to the v2 staged workspace authority port. It contains no
// journal authority; all filesystem mutation remains downstream of a durable
// preparation intent.
type WorkspaceAuthorityEffectsV2 struct{ repository *Repository }

func NewWorkspaceAuthorityEffectsV2(repository *Repository) (*WorkspaceAuthorityEffectsV2, error) {
	if repository == nil {
		return nil, &workspace.Error{Kind: workspace.KindInvalid, Message: "workspace repository is required"}
	}
	return &WorkspaceAuthorityEffectsV2{repository: repository}, nil
}

func (a *WorkspaceAuthorityEffectsV2) ObserveWorkspace(ctx context.Context, binding workspace.BindingV2) (workspace.ObservationV2, error) {
	record, err := a.record(binding)
	if err != nil {
		return workspace.ObservationV2{}, workspaceAuthorityAdapterErrorV2("validate workspace observation", err)
	}
	probe, err := a.repository.Probe(ctx, record)
	if err != nil {
		return workspace.ObservationV2{}, workspaceAuthorityAdapterErrorV2("observe workspace", err)
	}
	return workspace.ObservationV2{Registered: probe.Registered, Head: probe.Head, Branch: probe.Branch, GitDirIdentity: probe.GitDir, IndexIdentity: probe.IndexPath, Clean: probe.Clean, DependencyHead: probe.Head}, nil
}

func (a *WorkspaceAuthorityEffectsV2) CreateWorkspace(ctx context.Context, binding workspace.BindingV2) error {
	record, err := a.record(binding)
	if err != nil {
		return workspaceAuthorityAdapterErrorV2("validate workspace creation", err)
	}
	return workspaceAuthorityAdapterErrorV2("create workspace", a.repository.Create(ctx, record))
}

func (a *WorkspaceAuthorityEffectsV2) PrepareDependencies(ctx context.Context, binding workspace.BindingV2) error {
	record, err := a.record(binding)
	if err != nil {
		return workspaceAuthorityAdapterErrorV2("validate dependency preparation", err)
	}
	dependencies := make([]workspace.Dependency, len(binding.Dependencies))
	for index, dependency := range binding.Dependencies {
		dependencies[index] = workspace.Dependency{WorkID: dependency.WorkID, Candidate: dependency.Commit}
	}
	_, err = a.repository.PrepareDependencies(ctx, record, dependencies)
	return workspaceAuthorityAdapterErrorV2("prepare workspace dependencies", err)
}

func (a *WorkspaceAuthorityEffectsV2) WithRepositoryTargetGuard(ctx context.Context, fn func(workspace.RepositoryTargetGuardV2) error) error {
	if fn == nil {
		return &workspace.Error{Kind: workspace.KindInvalid, Message: "workspace target guard callback is required"}
	}
	var callback *workspaceTargetGuardCallbackV2
	err := a.repository.WithTargetAuthority(ctx, func(string) error {
		if callbackErr := fn(workspaceTargetGuardV2{a}); callbackErr != nil {
			callback = &workspaceTargetGuardCallbackV2{cause: callbackErr}
			return callback
		}
		return nil
	})
	if err == nil {
		return nil
	}
	if callback != nil && err == callback {
		var decision *workspace.Error
		if errors.As(callback.cause, &decision) {
			return callback.cause
		}
		return workspaceAuthorityAdapterErrorV2("run repository target guard callback", callback.cause)
	}
	return workspaceAuthorityAdapterErrorV2("acquire repository target guard", err)
}

type workspaceTargetGuardV2 struct{ effects *WorkspaceAuthorityEffectsV2 }

func (g workspaceTargetGuardV2) TargetHead(ctx context.Context) (string, error) {
	head, err := g.effects.repository.TargetHead(ctx)
	if err != nil {
		return "", workspaceAuthorityAdapterErrorV2("observe repository target", err)
	}
	return head, nil
}

func (g workspaceTargetGuardV2) ObserveWorkspace(ctx context.Context, binding workspace.BindingV2) (workspace.ObservationV2, error) {
	return g.effects.ObserveWorkspace(ctx, binding)
}

func (a *WorkspaceAuthorityEffectsV2) record(binding workspace.BindingV2) (workspace.Record, error) {
	if a == nil || a.repository == nil {
		return workspace.Record{}, &workspace.Error{Kind: workspace.KindInvalid, Message: "workspace repository is required"}
	}
	if err := binding.Validate(); err != nil {
		return workspace.Record{}, err
	}
	repository := a.repository.Binding()
	expectedPath, err := a.repository.WorkspacePath(binding.WorkspaceID.String())
	if err != nil {
		return workspace.Record{}, err
	}
	if binding.RepositoryID != repository.RepositoryID || binding.ProjectID != repository.ProjectID || binding.ManagedRoot != repository.ManagedRoot || filepath.Clean(binding.CanonicalPath) != expectedPath || binding.Generation > math.MaxInt64 {
		return workspace.Record{}, &workspace.Error{Kind: workspace.KindBindingConflict, Message: "workspace binding does not match repository authority"}
	}
	dependencies := make([]workspace.Dependency, len(binding.Dependencies))
	for index, dependency := range binding.Dependencies {
		dependencies[index] = workspace.Dependency{WorkID: dependency.WorkID, Candidate: dependency.Commit}
	}
	return workspace.Record{Reservation: workspace.Reservation{
		ProjectID: binding.ProjectID, MissionID: binding.MissionID, WorkRevisionID: binding.WorkRevisionID,
		WorkID: binding.WorkID, RunID: binding.RunID, WorkspaceID: binding.WorkspaceID,
		RepositoryID: binding.RepositoryID, LaneID: binding.LaneID,
		Authority:   workspace.Authority{LeaseID: binding.LeaseID, Generation: int64(binding.Generation), Token: binding.Fence},
		OwnedBranch: binding.OwnedBranch, PinnedBase: binding.PinnedBase, Dependencies: dependencies,
	}, CanonicalPath: binding.CanonicalPath}, nil
}

// workspaceAuthorityAdapterErrorV2 terminates private repository and runner
// diagnostics at the workspace capability boundary. Only the closed workspace
// classification and context cancellation identity cross that boundary.
func workspaceAuthorityAdapterErrorV2(action string, err error) error {
	if err == nil {
		return nil
	}
	kind := workspace.KindGitFailure
	var repositoryError *workspace.Error
	if errors.As(err, &repositoryError) {
		kind = repositoryError.Kind
	}
	var cause error
	switch {
	case errors.Is(err, context.Canceled):
		cause = context.Canceled
	case errors.Is(err, context.DeadlineExceeded):
		cause = context.DeadlineExceeded
	}
	return &workspace.Error{Kind: kind, Message: "workspace Git adapter failed to " + action, Cause: cause}
}

type workspaceTargetGuardCallbackV2 struct{ cause error }

func (*workspaceTargetGuardCallbackV2) Error() string {
	return "workspace target guard callback failed"
}

var _ workspace.PreparationEffectPortV2 = (*WorkspaceAuthorityEffectsV2)(nil)
