package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/helpercmd"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// This is a public-binary witness: every query starts in a fresh process after
// the mutating process and its owner have closed. The kernel-only fixture is
// not a claim that the full upstream mission workflow is implemented.
func TestRealGapDBReadinessPublicationIsObservableFromFreshCLIProcesses(t *testing.T) {
	sidecar := os.Getenv("SPEC_KITTY_WP15_GAPDBD")
	if sidecar == "" {
		t.Skip("set SPEC_KITTY_WP15_GAPDBD to the exact admitted assertion-extension sidecar")
	}
	repository := repositoryRoot(t)
	binary := buildBinary(t, repository, "./cmd/spec-kitty")
	// Honor TMPDIR so qualification can use an admitted local filesystem; /tmp
	// may be tmpfs, which is intentionally outside the production policy.
	parent, err := os.MkdirTemp("", "spk-observe-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(parent) })
	if err := os.Chmod(parent, 0o700); err != nil {
		t.Fatal(err)
	}
	root := filepath.Join(parent, "authority")
	environment := append(os.Environ(),
		"SPEC_KITTY_GAPDBD_PATH="+sidecar,
		"SPEC_KITTY_EVIDENCE_ROOT="+filepath.Join(repository, "docs", "programs", "evidence"),
		"SPEC_KITTY_RUNTIME_ROOT="+filepath.Join(parent, "runtime"),
	)
	run := func(input []byte, args ...string) helpercmd.Envelope {
		t.Helper()
		command := exec.CommandContext(t.Context(), binary, append([]string{"--control-root", root}, args...)...)
		command.Env = environment
		command.Stdin = bytes.NewReader(input)
		var diagnostics bytes.Buffer
		command.Stderr = &diagnostics
		output, err := command.Output()
		if err != nil {
			t.Fatalf("%v: %v stdout=%s stderr=%s", args, err, output, diagnostics.Bytes())
		}
		var envelope helpercmd.Envelope
		if err := json.Unmarshal(output, &envelope); err != nil || envelope.Outcome != helpercmd.OutcomeSuccess {
			t.Fatalf("%v: invalid response=%s err=%v", args, output, err)
		}
		return envelope
	}
	initialized := run(nil, "initialize")
	digest, err := journalv2.ReducerStateDigestV2([]byte(`{"definition":"cli-observation-regression"}`))
	if err != nil {
		t.Fatal(err)
	}
	state, err := mission.NewReadinessAuthorityStateV2(mission.ReadinessAuthorityStateInputV2{
		ProjectID: "project-cli-observe", MissionID: "mission-cli-observe", DefinitionRevision: "definition-cli-observe", DefinitionDigest: digest,
		Version: 1, Lifecycle: mission.MissionOpen, LastOperationID: "operation-cli-observe", ObservedAt: "2026-09-05T12:00:00Z",
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(map[string]any{
		"schema_version": mission.ReadinessAuthorityStateSchemaV2,
		"operation_id":   "operation-cli-observe", "project_id": "project-cli-observe", "mission_id": "mission-cli-observe",
		"actor_id": "actor-cli-observe", "process_id": "process-cli-observe", "runtime_epoch_id": helpercmd.RuntimeEpochCurrent,
		"doctrine_revision": "doctrine-cli-observe", "profile_revision": "profile-cli-observe", "policy_revision": "policy-cli-observe",
		"observed_at": "2026-09-05T12:00:00Z", "event_type": mission.ReadinessEventCreatedV2, "updated": json.RawMessage(state.CanonicalBytes()),
	})
	if err != nil {
		t.Fatal(err)
	}
	request, err := json.Marshal(helpercmd.OperationRequest{
		SchemaVersion: helpercmd.RequestSchemaVersion, Action: string(mission.ActionMissionCreate), Scope: "mission:mission-cli-observe",
		Kind: helpercmd.OperationReadiness, Payload: payload,
	})
	if err != nil {
		t.Fatal(err)
	}
	committed := run(request, "operation", "execute", "--request", "-")
	if committed.Result == nil || committed.Result.Kind != helpercmd.ResultReadiness || committed.ProjectionRevision <= initialized.ProjectionRevision {
		t.Fatalf("fixture did not publish readiness authority: %+v", committed)
	}
	for _, query := range []struct{ project, mission, operation string }{
		{"project-other", "mission-cli-observe", "operation-cli-observe"},
		{"project-cli-observe", "mission-other", "operation-cli-observe"},
		{"project-cli-observe", "mission-cli-observe", "operation-missing"},
	} {
		command := exec.CommandContext(t.Context(), binary, "--control-root", root, "operation", "inspect", "--project", query.project, "--mission", query.mission, "--operation", query.operation)
		command.Env = environment
		output, err := command.Output()
		var envelope helpercmd.Envelope
		exit, exited := err.(*exec.ExitError)
		if !exited || exit.ExitCode() != helpercmd.ExitNotFound || json.Unmarshal(output, &envelope) != nil || envelope.Outcome != helpercmd.OutcomeNotFound || envelope.Item != nil || envelope.Result != nil {
			t.Fatalf("inspection disclosed a missing or differently scoped operation: query=%+v err=%v output=%s", query, err, output)
		}
	}
	var firstDigest string
	for iteration := 0; iteration < 2; iteration++ {
		status := run(nil, "status")
		progress := run(nil, "progress", "--project", "project-cli-observe", "--mission", "mission-cli-observe")
		operation := run(nil, "operation", "inspect", "--project", "project-cli-observe", "--mission", "mission-cli-observe", "--operation", "operation-cli-observe")
		for _, queried := range []helpercmd.Envelope{status, progress, operation} {
			if queried.DatabaseID != committed.DatabaseID || queried.RuntimeEpochID != committed.RuntimeEpochID || queried.ProjectionRevision != committed.ProjectionRevision {
				t.Fatalf("query changed authority coordinate: committed=%+v query=%+v", committed, queried)
			}
		}
		if status.Host == nil || progress.Progress == nil || !progress.Progress.Complete || progress.Progress.Lifecycle != "open" || operation.Item == nil || operation.Item.AggregateID != "operation-cli-observe" {
			t.Fatalf("durable publication missing from queries: status=%+v progress=%+v operation=%+v", status, progress, operation)
		}
		if len(progress.Progress.Operations) != 1 || progress.Progress.Operations[0].AggregateID != operation.Item.AggregateID || !bytes.Equal(progress.Progress.Operations[0].CanonicalState, operation.Item.CanonicalState) {
			t.Fatalf("mission progress omitted or misattributed its committed operation: operations=%+v inspected=%+v", progress.Progress.Operations, operation.Item)
		}
		if iteration == 0 {
			firstDigest = status.Host.ProjectionSHA256
		} else if firstDigest == "" || status.Host.ProjectionSHA256 != firstDigest {
			t.Fatal("fresh read-only processes changed the durable projection digest")
		}
	}
}
