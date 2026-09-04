package gitrepo

import (
	"context"
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
