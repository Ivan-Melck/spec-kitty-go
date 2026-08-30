package architecture

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

var scenarioIDs = []string{
	"data/get-put", "conditional/cas-delete", "batch/atomic",
	"scan/sorted-prefix", "watch/registered-order",
	"expiry/release-authority", "ack/memory-visible",
	"ack/durable-restart", "inspect/authority", "authority/lease",
	"authority/review", "authority/workflow", "authority/integration",
	"response-loss/reconcile",
}

var faultIDs = []string{"GJ-01", "GJ-02", "GJ-03", "GJ-04", "GJ-05"}

var faultCuts = []string{
	"before_request_publication",
	"after_durable_acceptance_before_client_response",
	"after_workspace_creation_before_durable_binding",
	"after_durable_binding_before_run_capsule_response",
	"after_effect_intent_before_observation",
}

var focusedIDs = []string{
	"atomic-publication-nfr001",
	"lost-response-idempotency-nfr002",
	"concurrent-claims-nfr003",
	"competing-integrations-nfr004",
	"restore-playback-nfr005",
	"restart-epoch-nfr007",
	"zombie-isolation-nfr008",
	"attribution-reassignment-nfr015",
}

var mutantIDs = []string{
	"split-batch", "pre-ack-publish", "retry-new-operation-id",
	"revision-fill-in", "server-revision-in-value-or-key",
	"invalid-event-order", "current-batch-checkpoint",
	"global-authority-cas", "shared-projection-head",
	"runtime-coordinate-conflation", "workspace-reuse",
	"same-contributor-review", "invalid-checkpoint",
	"playback-adapter-or-write", "watch-as-journal",
	"misattributed-public-fields", "fake-timeout",
	"gapdb-import-allowlist-or-internal-server", "incomplete-signature",
}

func TestM1EvidencePassPredicateRejectsVacuousPasses(t *testing.T) {
	compiled := compileEvidenceSchema(t)
	valid := passingEvidenceFixture()
	assertSchemaValid(t, compiled, valid)
	if err := validateEvidencePass(valid); err != nil {
		t.Fatalf("positive fixture rejected: %v", err)
	}

	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"scenario failed", func(m map[string]any) { item(m, "scenario_results", 0)["outcome"] = "fail" }},
		{"scenario not started", func(m map[string]any) { item(m, "scenario_results", 0)["started"] = false }},
		{"fault failed", func(m map[string]any) { item(m, "fault_results", 0)["outcome"] = "fail" }},
		{"fault nonterminal", func(m map[string]any) { item(m, "fault_results", 0)["terminal"] = false }},
		{"mutant survived", func(m map[string]any) { item(m, "mutant_results", 0)["outcome"] = "survived" }},
		{"independent review failed", func(m map[string]any) { object(m, "independent_review")["verdict"] = "fail" }},
		{"ordered trial failed", func(m map[string]any) { object(m, "ordered_trial")["outcome"] = "fail" }},
		{"ordered trial nonterminal", func(m map[string]any) { object(m, "ordered_trial")["terminal"] = false }},
		{"FR044 false", func(m map[string]any) { object(m, "ordered_trial")["fr_044_satisfied"] = false }},
		{"SC013 false", func(m map[string]any) { object(m, "ordered_trial")["sc_013_satisfied"] = false }},
		{"universal invariant false", func(m map[string]any) {
			object(m, "invariant_summary")["journal_complete"] = false
		}},
		{"focused summary absent", func(m map[string]any) {
			m["focused_subtrial_summaries"] = array(m, "focused_subtrial_summaries")[:7]
		}},
		{"focused repetitions below 100", func(m map[string]any) {
			item(m, "focused_subtrial_summaries", 0)["repetitions_passed"] = 99
		}},
		{"focused census unequal", func(m map[string]any) {
			item(m, "focused_subtrial_summaries", 0)["repetitions_terminal"] = 101
		}},
		{"child RunID differs", func(m map[string]any) { item(m, "scenario_results", 0)["run_id"] = "other-run" }},
		{"runtime epoch reused", func(m map[string]any) {
			object(object(m, "ordered_trial"), "owner_restart")["after_runtime_epoch_id"] = "epoch-before"
		}},
		{"new runtime epoch not admitted", func(m map[string]any) {
			object(object(m, "ordered_trial"), "owner_restart")["new_epoch_admitted"] = false
		}},
		{"old capsule not refused", func(m map[string]any) {
			object(object(m, "ordered_trial"), "old_capsule")["refused"] = false
		}},
		{"restore digest differs", func(m map[string]any) {
			object(object(m, "ordered_trial"), "restore_playback")["playback_digest"] = strings.Repeat("b", 64)
		}},
		{"playback called adapter", func(m map[string]any) {
			object(object(m, "ordered_trial"), "restore_playback")["playback_adapter_calls"] = 1
		}},
		{"playback wrote durably", func(m map[string]any) {
			object(object(m, "ordered_trial"), "restore_playback")["playback_durable_writes"] = 1
		}},
		{"workspace isolation false", func(m map[string]any) {
			object(object(m, "ordered_trial"), "workspace_isolation")["w1_quarantined"] = false
		}},
		{"replacement reuses W1", func(m map[string]any) {
			object(object(object(m, "ordered_trial"), "identities"), "replacement_c")["workspace_id"] = "W1"
		}},
		{"review not actor independent", func(m map[string]any) {
			item(object(m, "ordered_trial"), "reviews", 0)["actor_independent"] = false
		}},
		{"review rejected", func(m map[string]any) {
			item(object(m, "ordered_trial"), "reviews", 0)["verdict"] = "rejected"
		}},
		{"reviewer contributed", func(m map[string]any) {
			item(object(m, "ordered_trial"), "reviews", 0)["reviewer_actor_id"] = "A"
		}},
		{"integration not serialized", func(m map[string]any) {
			object(object(m, "ordered_trial"), "integration")["serialized"] = false
		}},
		{"integration candidate mismatch", func(m map[string]any) {
			object(object(m, "ordered_trial"), "integration")["candidate_ids"] = []any{"candidate-a", "candidate-x"}
		}},
		{"only one publication", func(m map[string]any) {
			object(object(m, "ordered_trial"), "integration")["successful_publications"] = 1
		}},
		{"required artifact absent", func(m map[string]any) { m["artifacts"] = []any{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := cloneMap(t, valid)
			test.mutate(fixture)
			schemaErr := compiled.Validate(fixture)
			semanticErr := validateEvidencePass(fixture)
			if schemaErr == nil && semanticErr == nil {
				t.Fatal("vacuous pass was accepted by schema and semantic predicate")
			}
		})
	}
}

func TestM1EvidenceSchemaConditionalRejectsVacuousPasses(t *testing.T) {
	compiled := compileEvidenceSchema(t)
	valid := passingEvidenceFixture()
	tests := []struct {
		name   string
		mutate func(map[string]any)
	}{
		{"scenario", func(m map[string]any) { item(m, "scenario_results", 0)["outcome"] = "fail" }},
		{"fault", func(m map[string]any) { item(m, "fault_results", 0)["terminal"] = false }},
		{"mutant", func(m map[string]any) { item(m, "mutant_results", 0)["outcome"] = "survived" }},
		{"review", func(m map[string]any) { object(m, "independent_review")["verdict"] = "fail" }},
		{"ordered trial", func(m map[string]any) { object(m, "ordered_trial")["fr_044_satisfied"] = false }},
		{"invariant", func(m map[string]any) { object(m, "invariant_summary")["journal_complete"] = false }},
		{"focused absent", func(m map[string]any) { m["focused_subtrial_summaries"] = []any{} }},
		{"focused short", func(m map[string]any) { item(m, "focused_subtrial_summaries", 0)["repetitions_passed"] = 99 }},
		{"artifacts", func(m map[string]any) { m["artifacts"] = []any{} }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			fixture := cloneMap(t, valid)
			test.mutate(fixture)
			if err := compiled.Validate(fixture); err == nil {
				t.Fatal("schema conditional accepted a vacuous pass")
			}
		})
	}
}

func TestM1EvidenceSchemaRetainsHonestFailureAndIncomplete(t *testing.T) {
	compiled := compileEvidenceSchema(t)
	for _, outcome := range []string{"fail", "incomplete"} {
		t.Run(outcome, func(t *testing.T) {
			fixture := passingEvidenceFixture()
			fixture["outcome"] = outcome
			for key := range object(fixture, "invariant_summary") {
				object(fixture, "invariant_summary")[key] = false
			}
			fixture["independent_review"] = map[string]any{"verdict": outcome}
			if outcome == "fail" {
				item(fixture, "scenario_results", 0)["outcome"] = "fail"
				item(fixture, "fault_results", 0)["outcome"] = "fail"
				item(fixture, "mutant_results", 0)["outcome"] = "survived"
				item(fixture, "focused_subtrial_summaries", 0)["repetitions_passed"] = 99
				trial := object(fixture, "ordered_trial")
				trial["outcome"] = "fail"
				trial["fr_044_satisfied"] = false
				trial["sc_013_satisfied"] = false
				object(trial, "workspace_isolation")["w1_quarantined"] = false
				object(trial, "owner_restart")["new_epoch_admitted"] = false
				object(trial, "old_capsule")["refused"] = false
				object(trial, "restore_playback")["digests_equal"] = false
				object(trial, "restore_playback")["playback_adapter_calls"] = 1
				item(trial, "reviews", 0)["actor_independent"] = false
				item(trial, "reviews", 0)["verdict"] = "rejected"
				object(trial, "integration")["serialized"] = false
				object(trial, "integration")["successful_publications"] = 0
				object(trial, "integration")["false_completions"] = 1
			} else {
				for _, raw := range array(fixture, "scenario_results") {
					child := raw.(map[string]any)
					child["started"] = false
					child["terminal"] = false
					child["outcome"] = "incomplete"
				}
				for _, raw := range array(fixture, "fault_results") {
					child := raw.(map[string]any)
					child["started"] = false
					child["terminal"] = false
					child["outcome"] = "incomplete"
				}
				fixture["focused_subtrial_summaries"] = []any{}
				for _, raw := range array(fixture, "mutant_results") {
					raw.(map[string]any)["outcome"] = "not_run"
				}
				fixture["artifacts"] = []any{}
				fixture["ordered_trial"] = map[string]any{
					"schema_version":   1,
					"run_id":           "run-1",
					"trial_id":         "m1-ordered-torture-trial",
					"started":          false,
					"terminal":         false,
					"outcome":          "incomplete",
					"fr_044_satisfied": false,
					"sc_013_satisfied": false,
				}
			}
			assertSchemaValid(t, compiled, fixture)
			if validateEvidencePass(fixture) == nil {
				t.Fatal("honest non-pass evidence was admitted as a pass")
			}
		})
	}
}

func validateEvidencePass(m map[string]any) error {
	var reasons []string
	check := func(ok bool, message string) {
		if !ok {
			reasons = append(reasons, message)
		}
	}
	runID, _ := m["run_id"].(string)
	check(m["outcome"] == "pass", "manifest outcome")
	checkResults := func(key string, ids []string, idKey, pass string) {
		values := array(m, key)
		check(len(values) == len(ids), key+" census")
		for index, expected := range ids {
			if index >= len(values) {
				continue
			}
			value := values[index].(map[string]any)
			check(value[idKey] == expected, key+" identity")
			check(value["run_id"] == runID, key+" RunID")
			if idKey != "id" || key != "mutant_results" {
				check(value["started"] == true, key+" started")
				check(value["terminal"] == true, key+" terminal")
			}
			check(value["outcome"] == pass, key+" outcome")
		}
	}
	checkResults("scenario_results", scenarioIDs, "scenario_id", "pass")
	checkResults("fault_results", faultIDs, "fault_id", "pass")
	checkResults("mutant_results", mutantIDs, "id", "killed")

	summaries := array(m, "focused_subtrial_summaries")
	check(len(summaries) == len(focusedIDs), "focused summary census")
	for index, expected := range focusedIDs {
		if index >= len(summaries) {
			continue
		}
		summary := summaries[index].(map[string]any)
		started := integer(summary["repetitions_started"])
		terminal := integer(summary["repetitions_terminal"])
		passed := integer(summary["repetitions_passed"])
		check(summary["id"] == expected, "focused summary identity")
		check(summary["run_id"] == runID, "focused summary RunID")
		check(started >= 100 && started == terminal && terminal == passed, "focused summary census")
	}
	for key, value := range object(m, "invariant_summary") {
		check(value == true, "universal invariant "+key)
	}
	check(object(m, "independent_review")["verdict"] == "pass", "independent review")
	check(len(array(m, "artifacts")) > 0, "artifacts")
	validateOrderedTrial(object(m, "ordered_trial"), runID, check)
	if len(reasons) != 0 {
		return fmt.Errorf("%s", strings.Join(reasons, "; "))
	}
	return nil
}

func validateOrderedTrial(trial map[string]any, runID string, check func(bool, string)) {
	check(trial["run_id"] == runID, "ordered trial RunID")
	check(trial["started"] == true && trial["terminal"] == true && trial["outcome"] == "pass", "ordered trial terminal pass")
	check(trial["fr_044_satisfied"] == true, "FR-044")
	check(trial["sc_013_satisfied"] == true, "SC-013")
	identities := object(trial, "identities")
	for name, expected := range map[string][]string{
		"implementation_a": {"A", "A", "W1"},
		"implementation_b": {"B", "B", "W2"},
		"replacement_c":    {"A", "C", "W3"},
	} {
		value := object(identities, name)
		check(value["actor_id"] == expected[0] && value["process_id"] == expected[1] && value["workspace_id"] == expected[2], name)
	}
	workspace := object(trial, "workspace_isolation")
	check(workspace["w1_quarantined"] == true && workspace["w2_preserved"] == true && workspace["w3_fresh"] == true && integer(workspace["zombie_authority_successes"]) == 0, "workspace isolation")
	restart := object(trial, "owner_restart")
	check(restart["same_umbrella_run"] == true && restart["new_epoch_admitted"] == true, "owner restart")
	check(restart["before_runtime_epoch_id"] != restart["after_runtime_epoch_id"], "new RuntimeEpochID")
	capsule := object(trial, "old_capsule")
	check(capsule["predecessor_runtime_epoch_id"] == restart["before_runtime_epoch_id"] && capsule["refused"] == true, "old capsule")
	playback := object(trial, "restore_playback")
	check(playback["restore_digest"] == playback["playback_digest"] && playback["digests_equal"] == true, "restore/playback equality")
	check(integer(playback["playback_adapter_calls"]) == 0 && integer(playback["playback_durable_writes"]) == 0, "zero-call playback")
	reviews := array(trial, "reviews")
	var reviewed []string
	check(len(reviews) == 2, "review census")
	for _, raw := range reviews {
		review := raw.(map[string]any)
		reviewer := review["reviewer_actor_id"].(string)
		contributors := stringsOf(review["contributor_actor_ids"])
		check(review["actor_independent"] == true && review["verdict"] == "approved" && !slices.Contains(contributors, reviewer), "review independence")
		reviewed = append(reviewed, review["candidate_id"].(string))
	}
	integration := object(trial, "integration")
	candidates := stringsOf(integration["candidate_ids"])
	order := stringsOf(integration["publication_order"])
	slices.Sort(reviewed)
	slices.Sort(candidates)
	slices.Sort(order)
	check(slices.Equal(reviewed, candidates) && slices.Equal(candidates, order), "integration candidates")
	check(integration["serialized"] == true && integer(integration["successful_publications"]) == 2 && integer(integration["false_completions"]) == 0, "serialized integration")
}

func passingEvidenceFixture() map[string]any {
	hash := strings.Repeat("a", 64)
	runID := "run-1"
	scenarios := make([]any, len(scenarioIDs))
	for index, id := range scenarioIDs {
		scenarios[index] = map[string]any{"scenario_id": id, "run_id": runID, "seed": index, "started": true, "terminal": true, "outcome": "pass", "artifact_sha256": hash}
	}
	faults := make([]any, len(faultIDs))
	for index, id := range faultIDs {
		faults[index] = map[string]any{"fault_id": id, "cut": faultCuts[index], "run_id": runID, "seed": index, "started": true, "terminal": true, "outcome": "pass", "artifact_sha256": hash}
	}
	summaries := make([]any, len(focusedIDs))
	for index, id := range focusedIDs {
		summaries[index] = map[string]any{"id": id, "run_id": runID, "repetitions_started": 100, "repetitions_terminal": 100, "repetitions_passed": 100, "artifact_sha256": hash}
	}
	mutants := make([]any, len(mutantIDs))
	for index, id := range mutantIDs {
		mutants[index] = map[string]any{"id": id, "run_id": runID, "outcome": "killed", "artifact_sha256": hash}
	}
	trial := map[string]any{
		"schema_version": 1, "run_id": runID, "trial_id": "m1-ordered-torture-trial",
		"started": true, "terminal": true, "outcome": "pass", "fr_044_satisfied": true, "sc_013_satisfied": true,
		"identities": map[string]any{
			"implementation_a": map[string]any{"actor_id": "A", "process_id": "A", "workspace_id": "W1"},
			"implementation_b": map[string]any{"actor_id": "B", "process_id": "B", "workspace_id": "W2"},
			"replacement_c":    map[string]any{"actor_id": "A", "process_id": "C", "workspace_id": "W3"},
		},
		"workspace_isolation": map[string]any{"w1_quarantined": true, "w2_preserved": true, "w3_fresh": true, "zombie_authority_successes": 0},
		"owner_restart":       map[string]any{"same_umbrella_run": true, "before_runtime_epoch_id": "epoch-before", "after_runtime_epoch_id": "epoch-after", "new_epoch_admitted": true},
		"old_capsule":         map[string]any{"predecessor_runtime_epoch_id": "epoch-before", "refused": true, "reason_kind": "runtime_epoch_conflict"},
		"restore_playback":    map[string]any{"restore_digest": hash, "playback_digest": hash, "digests_equal": true, "playback_adapter_calls": 0, "playback_durable_writes": 0},
		"reviews": []any{
			map[string]any{"candidate_id": "candidate-a", "contributor_actor_ids": []any{"A"}, "reviewer_actor_id": "reviewer-1", "actor_independent": true, "verdict": "approved"},
			map[string]any{"candidate_id": "candidate-b", "contributor_actor_ids": []any{"B"}, "reviewer_actor_id": "reviewer-2", "actor_independent": true, "verdict": "approved"},
		},
		"integration":     map[string]any{"target_ref": "refs/heads/main", "candidate_ids": []any{"candidate-a", "candidate-b"}, "publication_order": []any{"candidate-a", "candidate-b"}, "competing_attempts": 4, "serialized": true, "successful_publications": 2, "false_completions": 0},
		"artifact_sha256": hash,
	}
	return map[string]any{
		"schema_version": 1, "program_id": "production-runtime", "mission_id": "M1",
		"run_id": runID, "source_commit": strings.Repeat("a", 40), "source_tree": strings.Repeat("b", 40),
		"admitted_tuple_sha256":     "4ddf904a48c83fe343ea9589bdf6ef7108af71b4b8140d76da68abdaf9d5824d",
		"sidecar_provenance_sha256": hash, "scenario_results": scenarios, "fault_results": faults,
		"ordered_trial": trial, "focused_subtrial_summaries": summaries, "mutant_results": mutants,
		"invariant_summary": map[string]any{
			"one_valid_authority_per_work_revision": true, "workspace_binding_immutable": true,
			"stale_authority_refused": true, "primary_isolation": true,
			"reviewer_actor_independence": true, "serialized_target_authority": true,
			"effect_attribution": true, "journal_complete": true,
		},
		"independent_review": map[string]any{"reviewer_actor_id": "reviewer", "reviewer_process_id": "review-process", "verdict": "pass", "artifact_sha256": hash},
		"artifacts":          []any{map[string]any{"kind": "campaign", "path": "evidence/campaign.json", "sha256": hash, "size_bytes": 1}},
		"outcome":            "pass",
	}
}

func compileEvidenceSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	compiler := jsonschema.NewCompiler()
	compiler.AssertFormat()
	paths, err := filepath.Glob(filepath.Join(m1ContractsDir(t), "*.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	for _, path := range paths {
		payload, readErr := os.ReadFile(path)
		if readErr != nil {
			t.Fatal(readErr)
		}
		var document any
		if decodeErr := json.Unmarshal(payload, &document); decodeErr != nil {
			t.Fatal(decodeErr)
		}
		id := document.(map[string]any)["$id"].(string)
		if addErr := compiler.AddResource(id, document); addErr != nil {
			t.Fatal(addErr)
		}
	}
	compiled, err := compiler.Compile("https://spec-kitty.local/contracts/evidence-manifest-v1.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	return compiled
}

func assertSchemaValid(t *testing.T, schema *jsonschema.Schema, value any) {
	t.Helper()
	if err := schema.Validate(value); err != nil {
		t.Fatalf("fixture violates schema: %v", err)
	}
}

func cloneMap(t *testing.T, value map[string]any) map[string]any {
	t.Helper()
	payload, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	var clone map[string]any
	if err := json.Unmarshal(payload, &clone); err != nil {
		t.Fatal(err)
	}
	return clone
}

func object(parent map[string]any, key string) map[string]any {
	value, _ := parent[key].(map[string]any)
	return value
}

func array(parent map[string]any, key string) []any {
	value, _ := parent[key].([]any)
	return value
}

func item(parent map[string]any, key string, index int) map[string]any {
	return array(parent, key)[index].(map[string]any)
}

func integer(value any) int {
	switch number := value.(type) {
	case int:
		return number
	case float64:
		return int(number)
	default:
		return -1
	}
}

func stringsOf(value any) []string {
	raw, _ := value.([]any)
	result := make([]string, 0, len(raw))
	for _, item := range raw {
		result = append(result, item.(string))
	}
	return result
}
