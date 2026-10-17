//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/handoff"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/integration"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// Pure refusal controls: no TEST/R/SDK access, provider, fake metadata or Native.
func TestExpiryInitialDiagnosticRejectsInputsBeforeRootOrConstructor(t *testing.T) {
	for _, mode := range []string{"nil-context", "cancelled-context", "missing-review", "relative-path", "nonprivate-parent", "git-parent"} {
		t.Run(mode, func(t *testing.T) {
			parent := t.TempDir()
			ctx := t.Context()
			in := ExpiryHostInitialDiagnosticInputs{Image: "/no-image-is-read", InputRecord: "/no-input-is-read", RunParent: parent, M1AdoptionRecord: "/no-adoption-is-read", SourceReviewSHA256: strings.Repeat("a", 64)}
			switch mode {
			case "nil-context":
				ctx = nil
			case "cancelled-context":
				c, cancel := context.WithCancel(ctx)
				cancel()
				ctx = c
			case "missing-review":
				in.SourceReviewSHA256 = ""
			case "relative-path":
				in.Image = "relative"
			case "nonprivate-parent":
				if err := os.Chmod(parent, 0755); err != nil {
					t.Fatal(err)
				}
			case "git-parent":
				if err := os.Mkdir(filepath.Join(parent, ".git"), 0700); err != nil {
					t.Fatal(err)
				}
			}
			before, err := os.ReadDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			report, first := RunExpiryHostInitialDiagnostic(ctx, in)
			after, err := os.ReadDir(parent)
			if err != nil {
				t.Fatal(err)
			}
			if first == nil || report.Root != "" || len(report.Stages) != 0 || report.FirstCleanupAttempted || report.FirstCleanupSucceeded || report.DiagnosticPassed || report.Qualified || report.PrimaryError == "" || !reflect.DeepEqual(before, after) {
				t.Fatal("rejected input created work/root/cleanup/success", first, report)
			}
			if mode == "cancelled-context" && !errors.Is(first, context.Canceled) {
				t.Fatal("actual caller cause lost", first)
			}
		})
	}
}

func TestExpiryDiagnosticCapabilityGuardsNeverInventEvidence(t *testing.T) {
	ctx := t.Context()
	check := func(err error, outputs ...any) {
		t.Helper()
		if err != errExpiryDiagnosticCapabilityUnavailable {
			t.Fatal("unsupported capability did not refuse", err)
		}
		for _, output := range outputs {
			if !reflect.ValueOf(output).IsZero() {
				t.Fatal("unsupported capability invented output", output)
			}
		}
	}
	r, e := (expiryDiagnosticReadiness{}).ObserveCapabilityReadiness(ctx, mission.CapabilityReadinessQueryV2{})
	check(e, r)
	d := expiryDiagnosticDecomposition{}
	check(d.RequireCapabilities(ctx, nil))
	o, found, e := d.ObserveEffect(ctx, "")
	check(e, o, found)
	m, e := d.MutateEffect(ctx, mission.DecompositionProviderMutationV2{})
	check(e, m)
	check(d.CompensateEffect(ctx, mission.DecompositionProviderMutationV2{}, mission.DecompositionProviderObservationV2{}))
	v := expiryDiagnosticVerifier{}
	candidate, e := v.Verify(ctx, handoff.AuthorityScope{}, "")
	check(e, candidate)
	check(v.RevalidateRefusal(ctx, handoff.Refusal{}))
	g := expiryDiagnosticGit{}
	head, e := g.TargetHead(ctx, ids.RepositoryID(""), "")
	check(e, head)
	fence, e := g.InstallFence(ctx, integration.IntegrationFenceIntentV2{})
	check(e, fence)
	fence, found, e = g.ObserveFence(ctx, integration.IntegrationFenceIntentV2{})
	check(e, fence, found)
	workspace, e := g.EnsureWorkspace(ctx, integration.IntegrationWorkspaceIntentV2{})
	check(e, workspace)
	merge, conflict, e := g.MergeCandidate(ctx, integration.IntegrationMergeIntentV2{}, integration.WorkspaceFacts{})
	check(e, merge, conflict)
	publication, e := g.CompareAndSwapTarget(ctx, integration.IntegrationPublishIntentV2{})
	check(e, publication)
	publication, found, e = g.ObserveTargetPublication(ctx, integration.IntegrationPublishIntentV2{})
	check(e, publication, found)
	cleanup, e := g.CleanupWorkspace(ctx, integration.IntegrationCleanupIntentV2{})
	check(e, cleanup)
	cleanup, found, e = g.ObserveCleanup(ctx, integration.IntegrationCleanupIntentV2{})
	check(e, cleanup, found)
	ancestor, e := g.IsAncestor(ctx, ids.RepositoryID(""), "", "")
	check(e, ancestor)
}
