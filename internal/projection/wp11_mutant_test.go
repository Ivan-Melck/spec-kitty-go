package projection

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type wp11SourceReplacementV2 struct {
	old string
	new string
}

type wp11ProductionMutantV2 struct {
	id           string
	source       string
	pkg          string
	killer       string
	wantFailure  string
	replacements []wp11SourceReplacementV2
	mutantEnv    bool
}

// TestWP11ProductionMutantCampaignV2 alters type-correct production source
// through Go's overlay facility. Every row first proves its killer passes on
// the real tree, then proves the named defect dies for its intended reason.
// Reflection and fixture substitutions elsewhere are useful unit controls;
// this is the sealed evidence that the production path itself is protected.
func TestWP11ProductionMutantCampaignV2(t *testing.T) {
	if testing.Short() {
		t.Skip("production-overlay mutant campaign is excluded from the short PR tier")
	}
	root := wp11RepositoryRootV2(t)
	mutants := []wp11ProductionMutantV2{
		{
			id: "projection_writes_become_authority", source: "internal/projection/query.go", pkg: "./internal/projection", killer: "TestQueryPortCannotMutateAuthorityOrCollapseProgressIntoCeremony", wantFailure: "query method count",
			replacements: []wp11SourceReplacementV2{{"\tProgressBase(string) (ProgressBaseV2, error)\n}", "\tProgressBase(string) (ProgressBaseV2, error)\n\tPublishAcknowledged(journalv2.AcknowledgedPublicationV2) error\n}"}},
		},
		{
			id: "delivery_evidence_changes_mission", source: "internal/controlstore/outbox_model.go", pkg: "./internal/controlstore", killer: "TestOutboxDeliveryEvidenceCannotBecomeProductAuthorityMutant", wantFailure: "delivery-evidence authority mutant populated MissionStatus",
			replacements: []wp11SourceReplacementV2{
				{"\tStage                         OutboxStageV2", "\tMissionStatus                 string                        `json:\"mission_status,omitempty\"`\n\tStage                         OutboxStageV2"},
				{"func (s OutboxDeliveryStateV2) CanonicalBytes() []byte {\n\tif s.Validate() != nil {", "func (s OutboxDeliveryStateV2) CanonicalBytes() []byte {\n\tif s.Observation != nil {\n\t\ts.MissionStatus = s.Observation.Outcome\n\t}\n\tif s.Validate() != nil {"},
			},
		},
		{
			id: "playback_invokes_external_capability", source: "internal/controlstore/outbox_reducer.go", pkg: "./internal/controlstore", killer: "TestOutboxReducerProductionSourceHasNoExternalCapabilityImportsMutant", wantFailure: "pure playback reached external capability import os", mutantEnv: true,
			replacements: []wp11SourceReplacementV2{{"\t\"bytes\"\n", "\t\"bytes\"\n\t\"os\"\n"}, {"func ReduceOutboxEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {\n", "func ReduceOutboxEventV2(prior []byte, event journalv2.EventEnvelopeV2) ([]byte, error) {\n\t_, _ = os.Stat(\".\")\n"}},
		},
		{
			id: "refusal_moves_after_provider_boundary", source: "internal/controlstore/gapdb/outbox_authority.go", pkg: "./internal/controlstore/gapdb", killer: "TestOutboxAdapterAdmitsOnlyExactCapabilityPlanV2", wantFailure: "unminted plan reached provider",
			replacements: []wp11SourceReplacementV2{{"\tif err != nil || !outboxTransportMembershipV2(plan) {\n\t\treturn controlstore.OutboxPublicationAckV2{}, invalidV2Plan(\"outbox authority plan\")\n\t}", "\tif err != nil || !outboxTransportMembershipV2(plan) {\n\t\t_, _ = a.publisher.Publish(ctx, plan)\n\t\treturn controlstore.OutboxPublicationAckV2{}, invalidV2Plan(\"outbox authority plan\")\n\t}"}},
		},
		{
			id: "atomic_batch_member_disappears", source: "internal/controlstore/outbox_command.go", pkg: "./internal/controlstore", killer: "TestOutboxPlanContainsWholeAtomicMembershipMutant", wantFailure: "outbox atomic membership missing",
			replacements: []wp11SourceReplacementV2{{"OperationState: operation, Events: drafts, Heads: heads, Conditions: conditions", "OperationState: operation, Events: drafts[:1], Heads: heads, Conditions: conditions"}},
		},
		{
			id: "list_becomes_unbounded", source: "internal/projection/query.go", pkg: "./internal/projection", killer: "TestProjectionListCursorIsBoundedStableAndRevisionScoped", wantFailure: "over-budget list err",
			replacements: []wp11SourceReplacementV2{{"query.Limit > MaxQueryPageV2 || ", ""}},
		},
		{
			id: "watch_becomes_recovery", source: "internal/projection/watch.go", pkg: "./internal/projection", killer: "TestWatchCannotBecomeRecoveryStreamMutant", wantFailure: "watch recovery mutant populated RecoverySnapshot",
			replacements: []wp11SourceReplacementV2{
				{"\tProjectionDigest string                   `json:\"projection_digest_sha256\"`", "\tProjectionDigest string                   `json:\"projection_digest_sha256\"`\n\tRecoverySnapshot []byte                   `json:\"recovery_snapshot,omitempty\"`"},
				{"notification := NotificationV2{snapshot.databaseID, snapshot.revision, snapshot.epoch, snapshot.projectionDigest.String(), epochChanged || watch.epoch != snapshot.epoch}", "notification := NotificationV2{snapshot.databaseID, snapshot.revision, snapshot.epoch, snapshot.projectionDigest.String(), snapshot.CanonicalBytes(), epochChanged || watch.epoch != snapshot.epoch}"},
			},
		},
		{
			id: "generic_store_leaks_through_query", source: "internal/projection/authority.go", pkg: "./internal/projection", killer: "TestQueryDTOsCannotLeakGenericStoreMutant", wantFailure: "query DTO AggregateViewV2 leaked repository",
			replacements: []wp11SourceReplacementV2{{"type AggregateViewV2 struct {\n", "type AggregateViewV2 struct {\n\tRepository         any                          `json:\"repository,omitempty\"`\n"}},
		},
	}
	type expectedMutantV2 struct {
		source      string
		pkg         string
		killer      string
		wantFailure string
	}
	required := map[string]expectedMutantV2{
		"projection_writes_become_authority":    {"internal/projection/query.go", "./internal/projection", "TestQueryPortCannotMutateAuthorityOrCollapseProgressIntoCeremony", "query method count"},
		"delivery_evidence_changes_mission":     {"internal/controlstore/outbox_model.go", "./internal/controlstore", "TestOutboxDeliveryEvidenceCannotBecomeProductAuthorityMutant", "delivery-evidence authority mutant populated MissionStatus"},
		"playback_invokes_external_capability":  {"internal/controlstore/outbox_reducer.go", "./internal/controlstore", "TestOutboxReducerProductionSourceHasNoExternalCapabilityImportsMutant", "pure playback reached external capability import os"},
		"refusal_moves_after_provider_boundary": {"internal/controlstore/gapdb/outbox_authority.go", "./internal/controlstore/gapdb", "TestOutboxAdapterAdmitsOnlyExactCapabilityPlanV2", "unminted plan reached provider"},
		"atomic_batch_member_disappears":        {"internal/controlstore/outbox_command.go", "./internal/controlstore", "TestOutboxPlanContainsWholeAtomicMembershipMutant", "outbox atomic membership missing"},
		"list_becomes_unbounded":                {"internal/projection/query.go", "./internal/projection", "TestProjectionListCursorIsBoundedStableAndRevisionScoped", "over-budget list err"},
		"watch_becomes_recovery":                {"internal/projection/watch.go", "./internal/projection", "TestWatchCannotBecomeRecoveryStreamMutant", "watch recovery mutant populated RecoverySnapshot"},
		"generic_store_leaks_through_query":     {"internal/projection/authority.go", "./internal/projection", "TestQueryDTOsCannotLeakGenericStoreMutant", "query DTO AggregateViewV2 leaked repository"},
	}
	seen := make(map[string]bool, len(mutants))
	for _, mutant := range mutants {
		mutant := mutant
		t.Run(mutant.id, func(t *testing.T) {
			want, requiredID := required[mutant.id]
			if !requiredID || want.source != mutant.source || want.pkg != mutant.pkg || want.killer != mutant.killer || want.wantFailure != mutant.wantFailure || seen[mutant.id] || len(mutant.replacements) == 0 {
				t.Fatalf("unsealed mutant census row %s", mutant.id)
			}
			seen[mutant.id] = true
			if output, err := wp11RunKillerV2(t, root, mutant, "", ""); err != nil {
				t.Fatalf("positive control failed: %v\n%s", err, output)
			}
			mutated := wp11MutatedSourceV2(t, filepath.Join(root, mutant.source), mutant.replacements)
			overlay := wp11OverlayV2(t, filepath.Join(root, mutant.source), mutated)
			envSource := ""
			if mutant.mutantEnv {
				envSource = mutated
			}
			output, err := wp11RunKillerV2(t, root, mutant, overlay, envSource)
			if err == nil {
				t.Fatalf("production mutant survived\n%s", output)
			}
			if !strings.Contains(output, mutant.wantFailure) {
				t.Fatalf("mutant died for the wrong reason; want %q\n%s", mutant.wantFailure, output)
			}
		})
	}
	if len(seen) != len(required) {
		t.Fatalf("sealed T058 mutant census size=%d want=%d", len(seen), len(required))
	}
	for id := range required {
		if !seen[id] {
			t.Fatalf("sealed T058 mutant census omitted %s", id)
		}
	}
}

func wp11RepositoryRootV2(t *testing.T) string {
	t.Helper()
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate WP11 mutant campaign")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(source), "..", ".."))
}

func wp11MutatedSourceV2(t *testing.T, source string, replacements []wp11SourceReplacementV2) string {
	t.Helper()
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	for _, replacement := range replacements {
		if count := strings.Count(text, replacement.old); count != 1 {
			t.Fatalf("mutant anchor count=%d in %s for %q", count, source, replacement.old)
		}
		text = strings.Replace(text, replacement.old, replacement.new, 1)
	}
	path := filepath.Join(t.TempDir(), filepath.Base(source))
	if err := os.WriteFile(path, []byte(text), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func wp11OverlayV2(t *testing.T, source, replacement string) string {
	t.Helper()
	raw, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{map[string]string{source: replacement}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "overlay.json")
	if err := os.WriteFile(path, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func wp11RunKillerV2(t *testing.T, root string, mutant wp11ProductionMutantV2, overlay, mutantSource string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	arguments := []string{"test"}
	if overlay != "" {
		arguments = append(arguments, "-overlay="+overlay)
	}
	arguments = append(arguments, mutant.pkg, "-run", "^"+mutant.killer+"$", "-count=1")
	command := exec.CommandContext(ctx, "go", arguments...)
	command.Dir = root
	command.Env = os.Environ()
	if mutantSource != "" {
		command.Env = append(command.Env, "SPEC_KITTY_WP11_MUTANT_SOURCE="+mutantSource)
	}
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		return string(output), fmt.Errorf("killer timed out: %w", ctx.Err())
	}
	return string(output), err
}
