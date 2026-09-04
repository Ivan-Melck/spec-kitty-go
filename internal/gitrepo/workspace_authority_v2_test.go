package gitrepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

func TestWorkspaceAuthorityEffectsUseRealGitAndRepositoryGuardV2(t *testing.T) {
	repository, base := newRepository(t)
	dependency := commitOnBranch(t, repository, "dependency-effects", "dependency.txt", "dependency\n")
	runGit(t, repository, "checkout", "main")
	managed := filepath.Join(t.TempDir(), "workspaces")
	bound, err := Bind(t.Context(), Runner{}, "repository-effects", "project-effects", repository, managed, "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := bound.PrepareManagedRoot(); err != nil {
		t.Fatal(err)
	}
	effects, err := NewWorkspaceAuthorityEffectsV2(bound)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(managed, "workspace-effects")
	claim, _ := workspace.WorkspacePathClaimDigestV2("repository-effects", managed, path)
	binding := workspace.BindingV2{WorkspaceID: "workspace-effects", ProjectID: "project-effects", MissionID: "mission-effects", WorkRevisionID: "revision-effects", WorkID: "work-effects", RunID: "run-effects", LeaseID: "lease-effects", Generation: 1, Fence: strings.Repeat("f", 32), RuntimeEpochID: "epoch-effects", ActorID: "actor-effects", ProcessID: "process-effects", RepositoryID: "repository-effects", LaneID: "lane-effects", ManagedRoot: managed, CanonicalPath: path, PathIdentity: workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}, PathClaimDigest: claim, OwnedBranch: "kitty/effects", PinnedBase: base, Dependencies: []workspace.DependencyV2{{WorkID: "work-dependency", Commit: dependency}}}
	if observation, err := effects.ObserveWorkspace(t.Context(), binding); err != nil || observation.Registered {
		t.Fatalf("initial observation=%+v err=%v", observation, err)
	}
	if err := effects.CreateWorkspace(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	if err := effects.PrepareDependencies(t.Context(), binding); err != nil {
		t.Fatal(err)
	}
	observation, err := effects.ObserveWorkspace(t.Context(), binding)
	if err != nil || !observation.Registered || observation.Head == base || observation.Branch != binding.OwnedBranch || !observation.Clean || observation.GitDirIdentity == "" || observation.IndexIdentity == "" {
		t.Fatalf("observation=%+v err=%v", observation, err)
	}
	if output, err := (Runner{}).Run(t.Context(), binding.CanonicalPath, "merge-base", "--is-ancestor", dependency, observation.Head); err != nil || output != "" {
		t.Fatalf("dependency not incorporated: output=%q err=%v", output, err)
	}
	called := false
	if err := effects.WithRepositoryTargetGuard(context.Background(), func(guard workspace.RepositoryTargetGuardV2) error {
		called = true
		target, targetErr := guard.TargetHead(t.Context())
		inside, observeErr := guard.ObserveWorkspace(t.Context(), binding)
		if targetErr != nil || observeErr != nil || target != base || inside != observation {
			t.Fatalf("target=%s observation=%+v errors=%v/%v", target, inside, targetErr, observeErr)
		}
		return nil
	}); err != nil || !called {
		t.Fatalf("guard called=%v err=%v", called, err)
	}
}

func TestWorkspaceAuthorityEffectsRedactEveryOutboundFailureV2(t *testing.T) {
	repository, base := newRepository(t)
	managed := filepath.Join(t.TempDir(), "workspaces")
	bound, err := Bind(t.Context(), Runner{}, "repository-redaction", "project-redaction", repository, managed, "refs/heads/main", time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := bound.PrepareManagedRoot(); err != nil {
		t.Fatal(err)
	}
	effects, err := NewWorkspaceAuthorityEffectsV2(bound)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(managed, "workspace-redaction")
	claim, _ := workspace.WorkspacePathClaimDigestV2("repository-redaction", managed, path)
	binding := workspace.BindingV2{WorkspaceID: "workspace-redaction", ProjectID: "project-redaction", MissionID: "mission-redaction", WorkRevisionID: "revision-redaction", WorkID: "work-redaction", RunID: "run-redaction", LeaseID: "lease-redaction", Generation: 1, Fence: strings.Repeat("f", 32), RuntimeEpochID: "epoch-redaction", ActorID: "actor-redaction", ProcessID: "process-redaction", RepositoryID: "repository-redaction", LaneID: "lane-redaction", ManagedRoot: managed, CanonicalPath: path, PathIdentity: workspace.PathIdentityV2{Absolute: true, Canonical: true, WithinManagedRoot: true, AliasFree: true, SymlinkFree: true}, PathClaimDigest: claim, OwnedBranch: "kitty/redaction", PinnedBase: base}

	privatePath := filepath.Join(t.TempDir(), "private-provider-path")
	secret := privatePath + strings.Repeat("-private-provider-output", 2048)
	failingExecutable := filepath.Join(t.TempDir(), "private-git-runner")
	if err := os.WriteFile(failingExecutable, []byte("#!/bin/sh\nprintf '%s' '"+secret+"' >&2\nexit 19\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	failRunner := Runner{Executable: failingExecutable}

	tests := []struct {
		name string
		run  func() error
	}{
		{"observe", func() error {
			bound.runner = failRunner
			_, err := effects.ObserveWorkspace(t.Context(), binding)
			return err
		}},
		{"create", func() error { bound.runner = failRunner; return effects.CreateWorkspace(t.Context(), binding) }},
		{"prepare_dependencies", func() error { bound.runner = failRunner; return effects.PrepareDependencies(t.Context(), binding) }},
		{"target_guard_acquire", func() error {
			bound.runner = failRunner
			return effects.WithRepositoryTargetGuard(t.Context(), func(workspace.RepositoryTargetGuardV2) error { return nil })
		}},
		{"target_guard_head", func() error {
			bound.runner = Runner{}
			return effects.WithRepositoryTargetGuard(t.Context(), func(guard workspace.RepositoryTargetGuardV2) error {
				bound.runner = failRunner
				_, err := guard.TargetHead(t.Context())
				return err
			})
		}},
		{"target_guard_observe", func() error {
			bound.runner = Runner{}
			return effects.WithRepositoryTargetGuard(t.Context(), func(guard workspace.RepositoryTargetGuardV2) error {
				bound.runner = failRunner
				_, err := guard.ObserveWorkspace(t.Context(), binding)
				return err
			})
		}},
		{"raw_callback", func() error {
			bound.runner = Runner{}
			return effects.WithRepositoryTargetGuard(t.Context(), func(workspace.RepositoryTargetGuardV2) error { return errors.New(secret) })
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			assertWorkspaceAuthorityAdapterDiagnosticV2(t, test.run(), secret, privatePath, failingExecutable)
		})
	}

	typed := &workspace.Error{Kind: workspace.KindTargetMoved, Message: "repository target moved"}
	bound.runner = Runner{}
	if got := effects.WithRepositoryTargetGuard(t.Context(), func(workspace.RepositoryTargetGuardV2) error { return typed }); got != typed {
		t.Fatalf("typed callback decision was not preserved: %T %v", got, got)
	}
	canceled := workspaceAuthorityAdapterErrorV2("observe workspace", fmt.Errorf("private wrapper: %w", context.Canceled))
	if !errors.Is(canceled, context.Canceled) {
		t.Fatalf("context cancellation classification lost: %v", canceled)
	}
	assertWorkspaceAuthorityAdapterDiagnosticV2(t, canceled, "private wrapper", privatePath, failingExecutable)
}

func assertWorkspaceAuthorityAdapterDiagnosticV2(t *testing.T, err error, secrets ...string) {
	t.Helper()
	var diagnostic *workspace.Error
	if err == nil || !errors.As(err, &diagnostic) || diagnostic.Kind == "" {
		t.Fatalf("missing typed Git diagnostic: %T %v", err, err)
	}
	encoded, marshalErr := json.Marshal(err)
	if marshalErr != nil {
		t.Fatal(marshalErr)
	}
	rendered := err.Error() + "\n" + string(encoded)
	if len(err.Error()) >= 4096 || len(encoded) >= 4096 {
		t.Fatalf("unbounded workspace adapter diagnostic: error=%d json=%d", len(err.Error()), len(encoded))
	}
	for _, secret := range secrets {
		if secret != "" && strings.Contains(rendered, secret) {
			t.Fatalf("workspace adapter diagnostic leaked private data %q", secret)
		}
	}
}
