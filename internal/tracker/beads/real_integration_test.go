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
	"sync"
	"testing"
	"time"

	controlsqlite "github.com/spec-kitty/go-kitty3/internal/controlstore/sqlite"
	"github.com/spec-kitty/go-kitty3/internal/execution"
	"github.com/spec-kitty/go-kitty3/internal/governancecontext"
	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/workgraph"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type realBeadsFixture struct {
	root, repository, workspace, database, backup, restore, worktrees string
	binary, digest, sourceStatus                                      string
	projectID                                                         ids.ProjectID
	server                                                            bool
}

func newRealBeadsFixture(t *testing.T) *realBeadsFixture {
	return newRealBeadsFixtureMode(t, false)
}

func newRealBeadsServerFixture(t *testing.T) *realBeadsFixture {
	return newRealBeadsFixtureMode(t, true)
}

func newRealBeadsFixtureMode(t *testing.T, server bool) *realBeadsFixture {
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
	workspace := filepath.Join(root, "beads-workspace")
	if server {
		workspace = filepath.Join(root, "repository", ".beads")
	}
	fixture := &realBeadsFixture{
		root: root, repository: filepath.Join(root, "repository"), workspace: workspace,
		backup: filepath.Join(root, "native-backup"), restore: filepath.Join(root, "restore-workspace"),
		worktrees: filepath.Join(root, "worktrees"), binary: binary, digest: digest, projectID: ids.ProjectID("wp09-real-provider"),
		server: server,
	}
	paths := []string{fixture.repository, fixture.backup, fixture.restore, fixture.worktrees, filepath.Join(root, "home")}
	if !server {
		paths = append(paths, fixture.workspace)
	}
	for _, path := range paths {
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
	initArgs := []string{"init", "--stealth", "--skip-agents", "--skip-hooks", "--non-interactive", "--prefix", "skt"}
	initEnvironment := fixture.environment()
	if server {
		hash := sha256.Sum256([]byte(root))
		initArgs[len(initArgs)-1] = fmt.Sprintf("skq%x", hash[:4])
		initArgs = append(initArgs, "--shared-server", "--external", "--server-host", os.Getenv("DOLT_SERVER_HOST"), "--server-port", os.Getenv("DOLT_SERVER_PORT"), "--server-user", "root")
		initEnvironment = withoutEnvironmentKeys(initEnvironment, "BEADS_DIR")
	}
	if _, _, err := fixture.runWithEnvironment(t, fixture.repository, initEnvironment, fixture.binary, initArgs...); err != nil {
		t.Fatalf("initialize disposable Beads workspace: %v", err)
	}
	if server {
		if _, _, err := fixture.runBD(t, "config", "set", "dolt.auto-start", "false"); err != nil {
			t.Fatalf("pin external server lifecycle configuration: %v", err)
		}
	}
	wherePayload, _, err := fixture.runBD(t, "where", "--json")
	if err != nil {
		t.Fatalf("inspect initialized Beads workspace: %v", err)
	}
	var where struct {
		Data struct {
			DatabasePath string `json:"database_path"`
		} `json:"data"`
	}
	if decodeRealEnvelope(wherePayload, &where) != nil || !filepath.IsAbs(where.Data.DatabasePath) {
		t.Fatal("initialized Beads workspace did not report an absolute database path")
	}
	if server {
		showPayload, _, showErr := fixture.runBD(t, "dolt", "show", "--json")
		if showErr != nil {
			t.Fatalf("inspect initialized Beads server binding: %v", showErr)
		}
		var show struct {
			Data struct {
				Database string `json:"database"`
			} `json:"data"`
		}
		if decodeRealEnvelope(showPayload, &show) != nil || !validServerDatabase(show.Data.Database) {
			t.Fatal("initialized Beads server did not report a logical database identity")
		}
		fixture.database = show.Data.Database
	} else {
		fixture.database = filepath.Clean(where.Data.DatabasePath)
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
	mode := tracker.ConcurrencyEmbeddedSerialized
	if fixture.server {
		mode = tracker.ConcurrencyServerMultiwriter
	}
	bound, err := Open(fixture.bindingConfig(mode))
	if err != nil {
		t.Fatal(err)
	}
	return bound
}

func (fixture *realBeadsFixture) bindingConfig(mode tracker.ConcurrencyMode) BindingConfig {
	config := BindingConfig{
		ProjectID: fixture.projectID, ExecutablePath: fixture.binary, WorkspacePath: fixture.workspace,
		ProviderDatabaseIdentity: fixture.database, ProtocolSchema: SupportedProtocolSchema,
		ConcurrencyMode: mode, ExpectedExecutableDigest: fixture.digest,
	}
	if mode == tracker.ConcurrencyServerMultiwriter {
		config.ServerIdentity = os.Getenv("BEADS_SERVER_ID")
		config.ServerEndpoint = os.Getenv("BEADS_SERVER_ENDPOINT")
		config.Credentials = map[string]string{"DOLT_SERVER_HOST": os.Getenv("DOLT_SERVER_HOST"), "DOLT_SERVER_PORT": os.Getenv("DOLT_SERVER_PORT")}
	}
	return config
}

func (fixture *realBeadsFixture) runBD(t *testing.T, args ...string) ([]byte, []byte, error) {
	t.Helper()
	return fixture.run(t, fixture.binary, args...)
}

func (fixture *realBeadsFixture) run(t *testing.T, executable string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	return fixture.runWithEnvironment(t, fixture.repository, fixture.environment(), executable, args...)
}

func (fixture *realBeadsFixture) runWithEnvironment(t *testing.T, directory string, environment []string, executable string, args ...string) ([]byte, []byte, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = directory
	command.Env = environment
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if stdout.Len() > 4<<20 || stderr.Len() > 1<<20 {
		return nil, nil, errors.New("real provider output exceeded the qualification bound")
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

func (fixture *realBeadsFixture) environment() []string {
	result := realBeadsEnvironment(fixture.root, fixture.workspace)
	if fixture.server {
		result = append(result,
			"BEADS_DOLT_SERVER_HOST="+os.Getenv("DOLT_SERVER_HOST"),
			"BEADS_DOLT_SERVER_PORT="+os.Getenv("DOLT_SERVER_PORT"),
			"BEADS_DOLT_AUTO_START=0",
		)
	}
	return result
}

func withoutEnvironmentKeys(environment []string, excluded ...string) []string {
	blocked := make(map[string]struct{}, len(excluded))
	for _, key := range excluded {
		blocked[key] = struct{}{}
	}
	result := make([]string, 0, len(environment))
	for _, item := range environment {
		key, _, _ := strings.Cut(item, "=")
		if _, skip := blocked[key]; !skip {
			result = append(result, item)
		}
	}
	return result
}

func realBeadsEnvironment(root, workspace string) []string {
	allowed := map[string]bool{"PATH": true, "TMPDIR": true, "LANG": true, "LC_ALL": true, "TZ": true}
	result := make([]string, 0, 12)
	for _, item := range os.Environ() {
		key, _, ok := strings.Cut(item, "=")
		if ok && allowed[key] {
			result = append(result, item)
		}
	}
	return append(result,
		"BD_JSON_ENVELOPE=1", "BD_NON_INTERACTIVE=1", "BEADS_DIR="+workspace,
		"BD_DISABLE_METRICS=1", "BD_DISABLE_EVENT_FLUSH=1", "HOME="+filepath.Join(root, "home"),
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
		entries, discussionObservation, err := bound.observeDiscussion(context.Background(), blocked.ExternalRef)
		if err != nil {
			t.Fatal(err)
		}
		observation := discussionObservation.Revision
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
		spy := &realCommandCensusRunner{delegate: bound.adapter.runner}
		bound.adapter.runner = spy
		const repetitions = 10
		census := map[string]int{"attempted": repetitions * 2}
		var audited []tracker.ExternalWorkRef
		compensationRefusalsWithoutMutation := 0
		for index := 0; index < repetitions; index++ {
			record := createRealWork(t, bound, fixture.projectID, 1100+index, nil).Record
			operation := ids.OperationID(fmt.Sprintf("op-assign-human-%02d", index))
			projected, err := bound.ProjectAssignment(context.Background(), tracker.ProjectAssignmentRequest{
				ExternalRef: record.ExternalRef, OperationID: operation, Assignee: "mapped-actor",
				ExpectedObservation: record.Observation.Revision, RequiredGuarantee: tracker.AssignmentUnguarded,
			})
			if err != nil {
				t.Fatal(err)
			}
			if _, _, err := fixture.runBD(t, "update", record.ExternalRef.ExternalID, "--assignee", "human-actor", "--json"); err != nil {
				t.Fatal(err)
			}
			beforeCompensation := spy.mutations()
			_, err = bound.CompensateAssignment(context.Background(), tracker.CompensateAssignmentRequest{
				ExternalRef: record.ExternalRef, OperationID: ids.OperationID(fmt.Sprintf("op-compensate-human-%02d", index)),
				ExpectedObservation: projected.Observation.Observation.Revision, ExpectedAssignee: "mapped-actor",
				ExpectedMarker: operation.String(), RestoreAssignee: "",
			})
			if !tracker.IsKind(err, tracker.ErrorAttributionLost) || spy.mutations() != beforeCompensation {
				t.Fatalf("human mutation was not refused before provider update: err=%v mutations=%d/%d", err, beforeCompensation, spy.mutations())
			}
			compensationRefusalsWithoutMutation++
			actual, observeErr := bound.ObserveAssignment(context.Background(), record.ExternalRef)
			if observeErr != nil || actual.Assignee != "human-actor" {
				t.Fatalf("human assignment did not survive: %+v err=%v", actual, observeErr)
			}
			census["terminal"]++
			census["preserved"]++
			census["attribution_lost"]++
			audited = append(audited, record.ExternalRef)
		}
		for index := 0; index < repetitions; index++ {
			record := createRealWork(t, bound, fixture.projectID, 1200+index, nil).Record
			operation := ids.OperationID(fmt.Sprintf("op-assign-matching-%02d", index))
			projected, err := bound.ProjectAssignment(context.Background(), tracker.ProjectAssignmentRequest{
				ExternalRef: record.ExternalRef, OperationID: operation, Assignee: "mapped-actor",
				ExpectedObservation: record.Observation.Revision, RequiredGuarantee: tracker.AssignmentUnguarded,
			})
			if err != nil {
				t.Fatal(err)
			}
			beforeCompensation := spy.mutations()
			_, err = bound.CompensateAssignment(context.Background(), tracker.CompensateAssignmentRequest{
				ExternalRef: record.ExternalRef, OperationID: ids.OperationID(fmt.Sprintf("op-compensate-matching-%02d", index)),
				ExpectedObservation: projected.Observation.Observation.Revision, ExpectedAssignee: "mapped-actor",
				ExpectedMarker: operation.String(), RestoreAssignee: "",
			})
			if !tracker.IsKind(err, tracker.ErrorSafeCompensationUnsupported) || spy.mutations() != beforeCompensation {
				t.Fatalf("matching state did not refuse unsupported compensation before provider update: err=%v mutations=%d/%d", err, beforeCompensation, spy.mutations())
			}
			compensationRefusalsWithoutMutation++
			actual, observeErr := bound.ObserveAssignment(context.Background(), record.ExternalRef)
			if observeErr != nil || actual.Assignee != "mapped-actor" || actual.OperationMarker != operation.String() {
				t.Fatalf("matching assignment changed during refusal: %+v err=%v", actual, observeErr)
			}
			census["terminal"]++
			census["preserved"]++
			census["safe_compensation_unsupported"]++
			audited = append(audited, record.ExternalRef)
		}
		census["failed"] = census["attempted"] - census["preserved"]
		if census["attempted"] != 20 || census["terminal"] != 20 || census["preserved"] != 20 || census["failed"] != 0 || census["attribution_lost"] != 10 || census["safe_compensation_unsupported"] != 10 {
			t.Fatalf("assignment preservation census is incomplete: %+v", census)
		}
		assignmentPayload, _ := json.Marshal(census)
		t.Logf("WP09_ASSIGNMENT_PRESERVATION %s", assignmentPayload)

		audits, failures := auditRealAuthorityIsolation(t, bound, audited, compensationRefusalsWithoutMutation)
		authorityPayload, _ := json.Marshal(map[string]int{"audits": audits, "failures": failures})
		t.Logf("WP09_AUTHORITY_AUDIT %s", authorityPayload)
		if failures != 0 {
			t.Fatalf("production-relevant authority audit failures=%d", failures)
		}
		stale := runRealStaleAuthorityProbe(t)
		stalePayload, _ := json.Marshal(stale)
		t.Logf("WP10_STALE_AUTHORITY %s", stalePayload)
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

type realStaleAuthorityEvidence struct {
	Attempted                     int `json:"attempted"`
	Refused                       int `json:"refused"`
	ReplacementWorkspacePreserved int `json:"replacement_workspace_preserved"`
}

func TestRealStaleAuthorityProbeRejectsExpiredWorkspaceWriter(t *testing.T) {
	if got := runRealStaleAuthorityProbe(t); got != (realStaleAuthorityEvidence{Attempted: 1, Refused: 1, ReplacementWorkspacePreserved: 1}) {
		t.Fatalf("stale authority census=%+v", got)
	}
}

func runRealStaleAuthorityProbe(t *testing.T) realStaleAuthorityEvidence {
	t.Helper()
	ctx := context.Background()
	store, err := controlsqlite.Open(ctx, filepath.Join(t.TempDir(), "authority.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	now := time.Date(2026, 8, 26, 18, 0, 0, 0, time.UTC)
	draft := workgraph.NewDraft("project-wp10", "mission-wp10")
	if err := draft.AddLane(workgraph.Lane{ID: "lane-wp10"}); err != nil {
		t.Fatal(err)
	}
	if err := draft.AddWork(workgraph.WorkItem{ID: "work-wp10", Title: "stale authority probe", LaneID: "lane-wp10", Criteria: []workgraph.Criterion{{ID: "criterion-wp10", Text: "stale writer refused"}}, SourceRevisions: []workgraph.SourceRevision{{Provider: "beads", Revision: "1"}}}); err != nil {
		t.Fatal(err)
	}
	revision, err := draft.Finalize(workgraph.FinalizeMetadata{ID: "revision-wp10", Number: 1, ActorID: "actor-owner", FinalizedAt: now})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.FinalizeWorkRevision(ctx, controlsqlite.FinalizeWorkRevisionRequest{SchemaVersion: 1, CorrelationID: "finalize-wp10", OperationID: "operation-finalize-wp10", ActorID: "actor-owner", ProcessID: "process-owner", OccurredAt: now, Revision: revision}); err != nil {
		t.Fatal(err)
	}
	governance := realQualificationGovernance(t, now)
	claim := func(operation ids.OperationID, process ids.ProcessID, at time.Time) execution.RunCapsule {
		request := execution.StartRequest{CorrelationID: operation.String(), OperationID: operation, ProjectID: "project-wp10", MissionID: "mission-wp10", WorkRevisionID: "revision-wp10", WorkID: "work-wp10", ActorID: "actor-worker", ProcessID: process, Now: at, LeaseTTL: time.Second, GovernanceContext: governance}
		capsule, claimErr := store.Claim(ctx, request, ids.RunID("run-"+operation.String()), ids.LeaseID("lease-"+operation.String()))
		if claimErr != nil {
			t.Fatal(claimErr)
		}
		return capsule
	}
	first := claim("operation-first", "process-first", now)
	binding := workspace.RepositoryBinding{RepositoryID: "repository-wp10", ProjectID: "project-wp10", CommonDir: "/repo/.git", PrimaryPath: "/repo", ManagedRoot: "/runtime/workspaces", TargetRef: "refs/heads/main", Primary: workspace.PrimarySnapshot{Head: strings.Repeat("a", 40), IndexDigest: "index", TreeDigest: "tree"}, CreatedAt: now, VerifiedAt: now}
	if err := store.BindRepository(ctx, binding); err != nil {
		t.Fatal(err)
	}
	reserve := func(capsule execution.RunCapsule, workspaceID ids.WorkspaceID, operation ids.OperationID, at time.Time) workspace.Record {
		reservation := workspace.Reservation{OperationID: operation, ProjectID: "project-wp10", MissionID: "mission-wp10", WorkRevisionID: "revision-wp10", WorkID: "work-wp10", RunID: capsule.RunID, WorkspaceID: workspaceID, RepositoryID: "repository-wp10", LaneID: "lane-wp10", Authority: workspace.Authority{LeaseID: capsule.LeaseID, Generation: capsule.Generation, Token: capsule.Token}, OwnedBranch: "runs/" + workspaceID.String(), PinnedBase: strings.Repeat("a", 40), Now: at}
		record, reserveErr := store.ReserveWorkspace(ctx, reservation, filepath.Join("/runtime/workspaces", workspaceID.String()))
		if reserveErr != nil {
			t.Fatal(reserveErr)
		}
		return record
	}
	oldWorkspace := reserve(first, "workspace-first", "operation-first", now)
	expiredAt := now.Add(2 * time.Second)
	if err := store.Expire(ctx, execution.ExpireRequest{SchemaVersion: 1, CorrelationID: "expire-first", OperationID: "operation-expire-first", ActorID: "actor-worker", ProcessID: "process-first", LeaseID: first.LeaseID, Generation: first.Generation, Token: first.Token, Now: expiredAt}); err != nil {
		t.Fatal(err)
	}
	second := claim("operation-second", "process-second", expiredAt.Add(time.Second))
	replacementBefore := reserve(second, "workspace-second", "operation-second", expiredAt.Add(time.Second))
	_, staleErr := store.AdvanceWorkspace(ctx, oldWorkspace.WorkspaceID, oldWorkspace.Authority, workspace.StageReserved, workspace.StageWorktreeCreated, workspace.Probe{}, "", expiredAt.Add(2*time.Second))
	if !workspace.IsKind(staleErr, workspace.KindStaleAuthority) {
		t.Fatalf("expired writer mutation error=%v", staleErr)
	}
	replacementAfter, err := store.Workspace(ctx, replacementBefore.WorkspaceID)
	if err != nil {
		t.Fatal(err)
	}
	preserved := replacementAfter.Status == replacementBefore.Status && replacementAfter.Stage == replacementBefore.Stage && replacementAfter.Authority == replacementBefore.Authority && replacementAfter.CanonicalPath == replacementBefore.CanonicalPath
	if !preserved {
		t.Fatalf("stale writer changed replacement workspace: before=%+v after=%+v", replacementBefore, replacementAfter)
	}
	return realStaleAuthorityEvidence{Attempted: 1, Refused: 1, ReplacementWorkspacePreserved: 1}
}

func realQualificationGovernance(t *testing.T, now time.Time) governancecontext.Context {
	t.Helper()
	commitment, err := governancecontext.CommitSHA256([]byte("wp10-real-authority-profile"))
	if err != nil {
		t.Fatal(err)
	}
	entry, err := governancecontext.NewEntry(governancecontext.KindProfile, "implementer-ivan", "revision-wp10", commitment)
	if err != nil {
		t.Fatal(err)
	}
	value, err := governancecontext.NewContext("context-wp10", governancecontext.SchemaVersion, []governancecontext.Entry{entry}, now)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

type realCommandCensusRunner struct {
	delegate      commandRunner
	mu            sync.Mutex
	totalCount    int
	mutatingCount int
}

func (runner *realCommandCensusRunner) run(ctx context.Context, request command) commandResult {
	runner.mu.Lock()
	runner.totalCount++
	if request.mutating {
		runner.mutatingCount++
	}
	runner.mu.Unlock()
	return runner.delegate.run(ctx, request)
}

func (runner *realCommandCensusRunner) mutations() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.mutatingCount
}

func (runner *realCommandCensusRunner) commands() int {
	runner.mu.Lock()
	defer runner.mu.Unlock()
	return runner.totalCount
}

func auditRealAuthorityIsolation(t *testing.T, bound *BoundAdapter, references []tracker.ExternalWorkRef, compensationRefusalsWithoutMutation int) (int, int) {
	t.Helper()
	audits, failures := 0, 0
	audit := func(passed bool) {
		audits++
		if !passed {
			failures++
		}
	}
	beforeIdentity := bound.binding.WorkspaceIdentity
	readiness, err := bound.Preflight(context.Background())
	audit(err == nil && readiness.binding.WorkspaceIdentity == beforeIdentity && bound.binding.WorkspaceIdentity == beforeIdentity)
	capabilities := implementedCapabilities(bound.binding.ConcurrencyMode)
	audit(capabilities.SafeCompensation == tracker.CompensationUnsupported)
	audit(compensationRefusalsWithoutMutation == 20)
	for _, reference := range references {
		issue, err := bound.readIssue(context.Background(), reference.ExternalID)
		forbidden := false
		if err == nil {
			for _, key := range []string{"run_id", "lease_id", "workspace_id", "fencing_token", "review_verdict", "acceptance_result", "integration_result"} {
				_, inRoot := issue.document.root[key]
				_, inNamespace := issue.document.namespace[key]
				forbidden = forbidden || inRoot || inNamespace
			}
		}
		audit(err == nil && !forbidden)
	}
	return audits, failures
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
