package beads

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type realBeadsFixture struct {
	root, repository, workspace, backup, restore, worktrees string
	binary, digest, sourceStatus                            string
	projectID                                               ids.ProjectID
}

func newRealBeadsFixture(t *testing.T) *realBeadsFixture {
	t.Helper()
	if os.Getenv("SPEC_KITTY_REAL_BEADS_QUALIFICATION") != "1" {
		t.Skip("real Beads qualification requires SPEC_KITTY_REAL_BEADS_QUALIFICATION=1")
	}
	binary := os.Getenv("BEADS_BIN")
	digest := os.Getenv("BEADS_DIGEST_SHA256")
	if !filepath.IsAbs(binary) || filepath.Clean(binary) != binary || len(digest) != sha256.Size*2 {
		t.Skip("real Beads qualification requires an absolute BEADS_BIN and reviewed BEADS_DIGEST_SHA256")
	}
	file, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	if _, err := io.Copy(hash, io.LimitReader(file, 1<<30)); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != digest {
		t.Fatal("BEADS_BIN digest differs from the explicit qualification digest")
	}
	root := t.TempDir()
	fixture := &realBeadsFixture{
		root: root, repository: filepath.Join(root, "repository"), workspace: filepath.Join(root, "beads-workspace"),
		backup: filepath.Join(root, "native-backup"), restore: filepath.Join(root, "restore-workspace"),
		worktrees: filepath.Join(root, "worktrees"), binary: binary, digest: digest, projectID: ids.ProjectID("wp09-real-provider"),
	}
	for _, path := range []string{fixture.repository, fixture.workspace, fixture.backup, fixture.restore, fixture.worktrees} {
		if err := os.Mkdir(path, 0o700); err != nil {
			t.Fatal(err)
		}
	}
	fixture.sourceStatus = repositoryStatus(t)
	t.Cleanup(func() {
		if after := repositoryStatus(t); after != fixture.sourceStatus {
			t.Errorf("qualification mutated the checked-out source repository")
		}
	})
	if _, _, err := fixture.run(t, "git", "init", "--quiet"); err != nil {
		t.Fatalf("initialize disposable git repository: %v", err)
	}
	if _, _, err := fixture.runBD(t, "init", "--stealth", "--skip-agents", "--skip-hooks", "--non-interactive", "--prefix", "skt"); err != nil {
		t.Fatalf("initialize disposable Beads workspace: %v", err)
	}
	for _, name := range []string{"AGENTS.md", "CLAUDE.md", ".cursorrules"} {
		for _, parent := range []string{fixture.repository, fixture.workspace} {
			if _, err := os.Stat(filepath.Join(parent, name)); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("provider initialization created agent instruction file %s", name)
			}
		}
	}
	hooks, err := os.ReadDir(filepath.Join(fixture.repository, ".git", "hooks"))
	if err != nil {
		t.Fatal(err)
	}
	for _, hook := range hooks {
		if !hook.IsDir() && !strings.HasSuffix(hook.Name(), ".sample") {
			t.Fatalf("provider initialization installed Git hook %s", hook.Name())
		}
	}
	stdout, _, err := fixture.runBD(t, "version", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var identity struct {
		Schema int `json:"schema_version"`
		Data   struct {
			Version string `json:"version"`
		} `json:"data"`
	}
	if decodeRealEnvelope(stdout, &identity) != nil || identity.Schema != 1 || identity.Data.Version != SupportedBeadsVersion {
		t.Fatal("real executable is outside the qualified version/schema tuple")
	}
	return fixture
}

func (fixture *realBeadsFixture) adapter(t *testing.T) *BoundAdapter {
	t.Helper()
	bound, err := Open(BindingConfig{
		ProjectID: fixture.projectID, ExecutablePath: fixture.binary, WorkspacePath: fixture.workspace,
		ProtocolSchema: SupportedProtocolSchema, ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized,
		ExpectedExecutableDigest: fixture.digest,
	})
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func (fixture *realBeadsFixture) runBD(t *testing.T, args ...string) ([]byte, []byte, error) {
	t.Helper()
	return fixture.run(t, fixture.binary, args...)
}

func (fixture *realBeadsFixture) run(t *testing.T, executable string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = fixture.repository
	command.Env = realBeadsEnvironment(fixture.root, fixture.workspace)
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if stdout.Len() > 4<<20 || stderr.Len() > 1<<20 {
		return nil, nil, errors.New("real provider output exceeded the qualification bound")
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

func realBeadsEnvironment(root, workspace string) []string {
	allowed := map[string]bool{"PATH": true, "TMPDIR": true, "LANG": true, "LC_ALL": true, "TZ": true, "HOME": true}
	result := make([]string, 0, 12)
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok && allowed[key] {
			result = append(result, item)
		}
	}
	return append(result,
		"BD_JSON_ENVELOPE=1", "BD_NON_INTERACTIVE=1", "BEADS_DIR="+workspace,
		"XDG_CONFIG_HOME="+filepath.Join(root, "xdg-config"), "XDG_DATA_HOME="+filepath.Join(root, "xdg-data"),
		"XDG_CACHE_HOME="+filepath.Join(root, "xdg-cache"), "GIT_CONFIG_GLOBAL="+filepath.Join(root, "gitconfig"),
		"GIT_CONFIG_NOSYSTEM=1",
	)
}

func repositoryStatus(t *testing.T) string {
	t.Helper()
	command := exec.Command("git", "status", "--porcelain=v1", "--untracked-files=normal")
	output, err := command.Output()
	if err != nil {
		t.Fatal(err)
	}
	return string(output)
}

func decodeRealEnvelope(payload []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(payload))
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing json.RawMessage
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("multiple JSON values")
	}
	return nil
}

// TestPinnedBeadsRealityProbe locks the actual v1.2.2 behavior that triggered
// the WP09 non-admissible result. It is deliberately not one of the required
// passing scenarios: it proves the failure is a real protocol mismatch rather
// than an unavailable binary or an invented test fixture.
func TestPinnedBeadsRealityProbe(t *testing.T) {
	fixture := newRealBeadsFixture(t)
	stdout, _, err := fixture.runBD(t, "where", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var where struct {
		Schema int `json:"schema_version"`
		Data   struct {
			Path         string `json:"path"`
			DatabasePath string `json:"database_path"`
		} `json:"data"`
	}
	if decodeRealEnvelope(stdout, &where) != nil || where.Schema != 1 || where.Data.Path != fixture.workspace || where.Data.DatabasePath == "" {
		t.Fatalf("unexpected where envelope")
	}
	info, _, infoErr := fixture.runBD(t, "info", "--schema", "--json")
	if infoErr != nil {
		t.Fatal(infoErr)
	}
	if json.Valid(info) {
		t.Fatal("v1.2.2 info unexpectedly became a documented JSON envelope; re-qualify the contract")
	}
	doctor, _, doctorErr := fixture.runBD(t, "doctor", "--agent", "--json")
	if doctorErr != nil {
		t.Fatal(doctorErr)
	}
	if json.Valid(doctor) {
		t.Fatal("v1.2.2 embedded doctor unexpectedly became a documented JSON envelope; re-qualify the contract")
	}

	createdPayload, _, err := fixture.runBD(t, "create", "--title", "shape probe", "--acceptance", "criterion", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var created struct {
		Data struct {
			ID                 string `json:"id"`
			AcceptanceCriteria string `json:"acceptance_criteria"`
		} `json:"data"`
	}
	if decodeRealEnvelope(createdPayload, &created) != nil || created.Data.ID == "" || created.Data.AcceptanceCriteria != "criterion" {
		t.Fatal("v1.2.2 create shape changed")
	}
	showPayload, _, err := fixture.runBD(t, "show", created.Data.ID, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var shown struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if decodeRealEnvelope(showPayload, &shown) != nil || len(shown.Data) != 1 || shown.Data[0].ID != created.Data.ID {
		t.Fatal("v1.2.2 show is no longer a one-element array")
	}
	updatedPayload, _, err := fixture.runBD(t, "update", created.Data.ID, "--assignee", "mapped-actor", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var updated struct {
		Data []struct {
			ID       string `json:"id"`
			Assignee string `json:"assignee"`
		} `json:"data"`
	}
	if decodeRealEnvelope(updatedPayload, &updated) != nil || len(updated.Data) != 1 || updated.Data[0].ID != created.Data.ID || updated.Data[0].Assignee != "mapped-actor" {
		t.Fatal("v1.2.2 update is no longer a one-element array")
	}
	blockerPayload, _, err := fixture.runBD(t, "create", "--title", "blocker probe", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var blocker struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if decodeRealEnvelope(blockerPayload, &blocker) != nil || blocker.Data.ID == "" {
		t.Fatal("v1.2.2 blocker create shape changed")
	}
	claimedPayload, _, err := fixture.runBD(t, "update", blocker.Data.ID, "--claim", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var claimed struct {
		Data []struct {
			ID       string `json:"id"`
			Assignee string `json:"assignee"`
		} `json:"data"`
	}
	if decodeRealEnvelope(claimedPayload, &claimed) != nil || len(claimed.Data) != 1 || claimed.Data[0].ID != blocker.Data.ID || claimed.Data[0].Assignee == "" {
		t.Fatal("v1.2.2 atomic claim shape changed")
	}
	if _, _, err := fixture.runBD(t, "dep", "add", created.Data.ID, blocker.Data.ID, "--type", "blocks", "--json"); err != nil {
		t.Fatal(err)
	}
	dependencyPayload, _, err := fixture.runBD(t, "dep", "list", created.Data.ID, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var dependencies struct {
		Data []struct {
			ID   string `json:"id"`
			Kind string `json:"dependency_type"`
		} `json:"data"`
	}
	if decodeRealEnvelope(dependencyPayload, &dependencies) != nil || len(dependencies.Data) != 1 || dependencies.Data[0].ID != blocker.Data.ID || dependencies.Data[0].Kind != "blocks" {
		t.Fatal("v1.2.2 dependency-list shape changed")
	}
	if _, _, err := fixture.runBD(t, "comments", "add", created.Data.ID, "shape comment", "--json"); err != nil {
		t.Fatal(err)
	}
	commentPayload, _, err := fixture.runBD(t, "comments", created.Data.ID, "--json")
	if err != nil {
		t.Fatal(err)
	}
	var comments struct {
		Data []struct {
			Text string `json:"text"`
		} `json:"data"`
	}
	if decodeRealEnvelope(commentPayload, &comments) != nil || len(comments.Data) != 1 || comments.Data[0].Text != "shape comment" {
		t.Fatal("v1.2.2 comment shape changed")
	}

	missingStdout, missingStderr, missingErr := fixture.runBD(t, "show", "skt-missing", "--json")
	if missingErr == nil || len(bytes.TrimSpace(missingStderr)) == 0 {
		t.Fatal("v1.2.2 missing issue error transport changed")
	}
	var missing struct {
		Data struct {
			Error string `json:"error"`
		} `json:"data"`
	}
	if decodeRealEnvelope(missingStdout, &missing) != nil || missing.Data.Error == "" {
		t.Fatal("v1.2.2 missing issue no longer reports an enveloped stdout error with prose stderr")
	}

	configPayload, _, err := fixture.runBD(t, "config", "show", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var config struct {
		Data []struct {
			Key    string `json:"key"`
			Value  string `json:"value"`
			Source string `json:"source"`
		} `json:"data"`
	}
	if decodeRealEnvelope(configPayload, &config) != nil {
		t.Fatal("v1.2.2 config show shape changed")
	}
	foundMode, foundDatabase := false, false
	for _, value := range config.Data {
		foundMode = foundMode || value.Key == "dolt_mode" && value.Value == "embedded" && value.Source == "metadata"
		foundDatabase = foundDatabase || value.Key == "dolt_database" && value.Value != "" && value.Source == "metadata"
	}
	if !foundMode || !foundDatabase {
		t.Fatal("v1.2.2 config show does not expose metadata-sourced embedded identity")
	}
	statusPayload, _, err := fixture.runBD(t, "status", "--no-activity", "--json")
	if err != nil {
		t.Fatal(err)
	}
	var status struct {
		Schema int `json:"schema_version"`
		Data   struct {
			Summary map[string]json.RawMessage `json:"summary"`
		} `json:"data"`
	}
	if decodeRealEnvelope(statusPayload, &status) != nil || status.Schema != 1 || len(status.Data.Summary) == 0 {
		t.Fatal("v1.2.2 status health envelope changed")
	}
	backupStatus, _, err := fixture.runBD(t, "backup", "status")
	if err != nil {
		t.Fatal(err)
	}
	if len(bytes.TrimSpace(backupStatus)) == 0 || json.Valid(backupStatus) {
		t.Fatal("v1.2.2 native backup status transport changed")
	}
	exportPayload, _, err := fixture.runBD(t, "export", "--json")
	if err != nil {
		t.Fatal(err)
	}
	firstLine, _, _ := bytes.Cut(exportPayload, []byte("\n"))
	var exported struct {
		Type string `json:"_type"`
		ID   string `json:"id"`
	}
	if json.Unmarshal(firstLine, &exported) != nil || exported.Type != "issue" || exported.ID == "" {
		t.Fatal("v1.2.2 export is no longer raw bounded JSONL interchange")
	}
}

func TestRealBeadsQualification(t *testing.T) {
	fixture := newRealBeadsFixture(t)
	bound := fixture.adapter(t)

	t.Run("inspection", func(t *testing.T) {
		inspection, err := bound.Inspect(context.Background(), tracker.InspectRequest{ProjectID: fixture.projectID})
		if err != nil {
			t.Fatalf("real v1.2.2 preflight is non-admissible: %v", err)
		}
		if inspection.Binding.ExecutableVersion != SupportedBeadsVersion || inspection.Binding.ProtocolSchema != 1 || inspection.Health != tracker.HealthHealthy {
			t.Fatalf("incomplete inspection: %+v", inspection)
		}
	})

	t.Run("catalog_round_trip", func(t *testing.T) {
		criteria := []tracker.Criterion{{ID: "criterion-1", Text: "observable result", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "go test ./..."}}
		for index := 0; index < 100; index++ {
			mutation := createRealWork(t, bound, fixture.projectID, index, criteria)
			record, err := bound.ReadWork(context.Background(), mutation.Record.ExternalRef)
			if err != nil {
				t.Fatal(err)
			}
			if record.ExternalRef.WorkID != ids.WorkID(fmt.Sprintf("work-%03d", index)) || !reflect.DeepEqual(record.Criteria(), criteria) {
				t.Fatalf("catalog round trip lost stable work or criterion identity")
			}
		}
	})

	t.Run("content_drift", func(t *testing.T) {
		mutation := createRealWork(t, bound, fixture.projectID, 101, nil)
		before := mutation.Record.Observation.Revision
		if _, _, err := fixture.runBD(t, "update", mutation.Record.ExternalRef.ExternalID, "--title", "human edit", "--json"); err != nil {
			t.Fatal(err)
		}
		after, err := bound.ReadWork(context.Background(), mutation.Record.ExternalRef)
		if err != nil {
			t.Fatal(err)
		}
		if after.Observation.Revision == before || after.Title != "human edit" {
			t.Fatal("canonical observation did not detect provider content drift")
		}
	})

	t.Run("graph_and_discussion", func(t *testing.T) {
		blocked := createRealWork(t, bound, fixture.projectID, 102, nil).Record
		blocker := createRealWork(t, bound, fixture.projectID, 103, nil).Record
		dependency := tracker.DependencyRef{BlockedWorkID: blocked.ExternalRef.WorkID, BlockingWorkID: blocker.ExternalRef.WorkID, Kind: tracker.DependencyBlocks}
		if _, err := bound.PutBlockingDependency(context.Background(), tracker.DependencyRequest{ProjectID: fixture.projectID, OperationID: ids.OperationID("op-dependency"), Dependency: dependency}); err != nil {
			t.Fatal(err)
		}
		edges, err := bound.ListDependencies(context.Background(), blocked.ExternalRef)
		if err != nil || len(edges) != 1 || edges[0].BlockingWorkID != blocker.ExternalRef.WorkID {
			t.Fatalf("blocking direction did not survive round trip: %+v err=%v", edges, err)
		}
		entries, err := bound.ListDiscussion(context.Background(), blocked.ExternalRef)
		if err != nil {
			t.Fatal(err)
		}
		observation := blocked.Observation.Revision
		if len(entries) != 0 {
			observation = entries[len(entries)-1].Observation
		}
		appendResult, err := bound.AppendDiscussion(context.Background(), tracker.AppendDiscussionRequest{
			ExternalRef: blocked.ExternalRef, OperationID: ids.OperationID("op-comment"), Author: "qualified-actor",
			Body: "qualified durable discussion", ExpectedObservation: observation,
		})
		if err != nil || appendResult.Entry.OperationMarker != "op-comment" {
			t.Fatalf("discussion round trip failed: %+v err=%v", appendResult, err)
		}
	})

	t.Run("assignment_safe_refusal", func(t *testing.T) {
		record := createRealWork(t, bound, fixture.projectID, 104, nil).Record
		projected, err := bound.ProjectAssignment(context.Background(), tracker.ProjectAssignmentRequest{
			ExternalRef: record.ExternalRef, OperationID: ids.OperationID("op-assign"), Assignee: "mapped-actor",
			ExpectedObservation: record.Observation.Revision, RequiredGuarantee: tracker.AssignmentUnguarded,
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, _, err := fixture.runBD(t, "update", record.ExternalRef.ExternalID, "--assignee", "human-actor", "--json"); err != nil {
			t.Fatal(err)
		}
		_, err = bound.CompensateAssignment(context.Background(), tracker.CompensateAssignmentRequest{
			ExternalRef: record.ExternalRef, OperationID: ids.OperationID("op-compensate"),
			ExpectedObservation: projected.Observation.Observation.Revision, ExpectedAssignee: "mapped-actor",
			ExpectedMarker: "op-assign", RestoreAssignee: "",
		})
		if !tracker.IsKind(err, tracker.ErrorAttributionLost) {
			t.Fatalf("human mutation was not preserved as attribution loss: %v", err)
		}
		actual, observeErr := bound.ObserveAssignment(context.Background(), record.ExternalRef)
		if observeErr != nil || actual.Assignee != "human-actor" {
			t.Fatalf("human assignment did not survive: %+v err=%v", actual, observeErr)
		}
	})

	t.Run("latency", func(t *testing.T) {
		preflight := make([]time.Duration, 100)
		for index := range preflight {
			started := time.Now()
			if _, err := bound.Preflight(context.Background()); err != nil {
				t.Fatal(err)
			}
			preflight[index] = time.Since(started)
		}
		emitLatency(t, "preflight", preflight, time.Second)
		work := createRealWork(t, bound, fixture.projectID, 105, nil).Record
		reads := make([]time.Duration, 200)
		for index := range reads {
			started := time.Now()
			if _, err := bound.ReadWork(context.Background(), work.ExternalRef); err != nil {
				t.Fatal(err)
			}
			reads[index] = time.Since(started)
		}
		emitLatency(t, "warm_single_item_read", reads, 500*time.Millisecond)
	})
}

func createRealWork(t *testing.T, bound *BoundAdapter, projectID ids.ProjectID, index int, criteria []tracker.Criterion) tracker.WorkMutation {
	t.Helper()
	mutation, err := bound.CreateWork(context.Background(), tracker.CreateWorkRequest{
		ProjectID: projectID, WorkID: ids.WorkID(fmt.Sprintf("work-%03d", index)), OperationID: ids.OperationID(fmt.Sprintf("op-create-%03d", index)),
		Title: fmt.Sprintf("qualified work %03d", index), Description: "provider qualification", DesignContext: "real Beads v1.2.2",
		Criteria: criteria, MetadataSchema: 1,
	})
	if err != nil {
		t.Fatal(err)
	}
	return mutation
}

func emitLatency(t *testing.T, operation string, samples []time.Duration, maximumP95 time.Duration) {
	t.Helper()
	sort.Slice(samples, func(i, j int) bool { return samples[i] < samples[j] })
	p50, p95, maximum := samples[len(samples)/2], samples[(len(samples)*95-1)/100], samples[len(samples)-1]
	payload, _ := json.Marshal(map[string]any{
		"operation": operation, "samples": len(samples),
		"p50_ms": float64(p50.Microseconds()) / 1000, "p95_ms": float64(p95.Microseconds()) / 1000, "max_ms": float64(maximum.Microseconds()) / 1000,
	})
	t.Logf("WP09_LATENCY %s", payload)
	if p95 > maximumP95 {
		t.Fatalf("%s p95 %s exceeds %s", operation, p95, maximumP95)
	}
}
