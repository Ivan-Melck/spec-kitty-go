package hubnot

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestBindingRefusesSelfAdmittedExecutable(t *testing.T) {
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	binary := filepath.Join(dir, "hn")
	if err := os.WriteFile(binary, []byte("#!/bin/sh\ntouch '"+marker+"'\n"), 0700); err != nil {
		t.Fatal(err)
	}
	config := BindingConfig{Schema: bindingSchema, Provider: "hubnot", ProjectID: ids.ProjectID("project-a"), WorkspacePath: dir, ExecutablePath: binary, ExpectedActor: strings.Repeat("a", 64), Identity: QualifiedProviderIdentity()}
	config.Identity.BinarySHA256, _ = fileDigest(binary)
	if _, err := Open(context.Background(), config); err == nil {
		t.Fatal("self-admitted arbitrary executable")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("refused executable ran")
	}
}

func TestBindingStrictDocument(t *testing.T) {
	config := BindingConfig{Schema: bindingSchema, Provider: "hubnot", ProjectID: ids.ProjectID("project-a"), WorkspacePath: "/workspace", ExecutablePath: "/executable", ExpectedActor: strings.Repeat("a", 64), Identity: QualifiedProviderIdentity()}
	good, _ := json.Marshal(config)
	if _, err := DecodeBinding(bytes.NewReader(good)); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{"schema":"old"}`, string(good) + `{}`, strings.Replace(string(good), `"provider":"hubnot"`, `"provider":"hubnot","provider":"hubnot"`, 1), strings.Replace(string(good), `"hubnot"`, `"beads"`, 1), strings.Replace(string(good), `"workspace_path":"/workspace"`, `"workspace_path":"relative"`, 1), strings.Replace(string(good), `"project_id":"project-a"`, `"project_id":"\ud800"`, 1), strings.Replace(string(good), `"provider":"hubnot"`, `"provider":"hubnot","unknown":1`, 1)} {
		if _, err := DecodeBinding(strings.NewReader(bad)); err == nil {
			t.Fatal("accepted invalid binding")
		}
	}
	pin := QualifiedProviderIdentity()
	pin.SourceCommit = "changed"
	if QualifiedProviderIdentity().SourceCommit == pin.SourceCommit {
		t.Fatal("mutable admission")
	}
}

// This is an opt-in probe of the exact admitted executable in disposable state.
// It never reads a private identity record; setup and observations use public CLI.
func TestPinnedPublicCLITransport(t *testing.T) {
	binary := os.Getenv("GO_KITTY_TEST_HUBNOT_BINARY")
	if binary == "" {
		t.Skip("set GO_KITTY_TEST_HUBNOT_BINARY to the admitted public executable")
	}
	dir := t.TempDir()
	fixture := func(executable string, args ...string) []byte {
		t.Helper()
		cmd := exec.Command(executable, args...)
		cmd.Dir = dir
		cmd.Env = processEnvironment()
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("public fixture %s: %v %s", args, err, out)
		}
		return out
	}
	fixture("/usr/bin/git", "init", "-q")
	fixture("/usr/bin/git", "-c", "user.name=Transport Test", "-c", "user.email=transport@test.invalid", "commit", "--allow-empty", "-q", "-m", "fixture")
	fixture(binary, "init", "--name", "Public transport fixture")
	result := runProcess(context.Background(), binary, dir, []string{"identity", "show", "--json"}, nil)
	env, err := decodeEnvelope(result.stdout, identitySchema, result.exitCode)
	if err != nil {
		t.Fatal(err)
	}
	var identity PublicIdentity
	if err := decodeStrict(env.Data, &identity); err != nil {
		t.Fatal(err)
	}
	config := BindingConfig{Schema: bindingSchema, Provider: "hubnot", ProjectID: ids.ProjectID("project-a"), WorkspacePath: dir, ExecutablePath: binary, ExpectedActor: identity.Actor, Identity: QualifiedProviderIdentity()}
	before := string(fixture("/usr/bin/git", "for-each-ref", "--format=%(refname) %(objectname)"))
	b, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	if before != string(fixture("/usr/bin/git", "for-each-ref", "--format=%(refname) %(objectname)")) {
		t.Fatal("Open changed refs")
	}
	if b.PublicIdentity() != identity {
		t.Fatal("public identity differs")
	}
	budget := newObservationBudget()
	defer budget.close()
	s := state()
	opened, outcome, err := b.mutate(context.Background(), budget, mutationRequest{intent: "open", key: "native-open", state: &s})
	if err != nil || outcome != OutcomeApplied {
		t.Fatalf("native open: %v %s", err, outcome)
	}
	rows, _, err := b.listDetails(context.Background(), budget, "")
	if err != nil || len(rows.Items) != 1 || rows.Items[0].ID != opened.IssueID || !reflect.DeepEqual(rows.Items[0].OpeningMetadata, s.Metadata) {
		t.Fatalf("native detailed list: %v", err)
	}
	shown, _, err := b.showIssue(context.Background(), budget, opened.IssueID, "")
	if err != nil || shown.HeadIDs[0] != opened.EventID {
		t.Fatalf("native show: %v", err)
	}
	heads, _, err := b.headsPage(context.Background(), budget, opened.IssueID, "")
	if err != nil || len(heads.Items) != 1 || heads.Items[0].Actor != identity.Actor {
		t.Fatalf("native heads: %v", err)
	}
	history, _, err := b.historyPage(context.Background(), budget, opened.IssueID, "all", "")
	if err != nil || len(history.Items) != 1 {
		t.Fatalf("native history: %v", err)
	}
	graph, _, err := b.graphPage(context.Background(), budget, opened.IssueID, "")
	if err != nil || len(graph.Items) != 0 {
		t.Fatalf("native graph: %v", err)
	}
	op, _, err := b.lookupOperation(context.Background(), budget, "native-open")
	if err != nil || op.EventID != opened.EventID || op.Intent != "open" {
		t.Fatalf("native operation: %v", err)
	}
	comment, _, err := b.mutate(context.Background(), budget, mutationRequest{intent: "comment", id: opened.IssueID, key: "native-comment", body: "exact\nbody"})
	if err != nil {
		t.Fatal(err)
	}
	original, _, err := b.lookupOperation(context.Background(), budget, "native-comment")
	if err != nil || original.EventID != comment.EventID || original.Body == nil || *original.Body != "exact\nbody" {
		t.Fatalf("native comment: %v", err)
	}
	s.Title = "later"
	_, _, err = b.mutate(context.Background(), budget, mutationRequest{intent: "revise", id: opened.IssueID, key: "native-revise", parents: []string{opened.EventID}, state: &s})
	if err != nil {
		t.Fatal(err)
	}
	replay, _, err := b.lookupOperation(context.Background(), budget, "native-open")
	if err != nil || !reflect.DeepEqual(replay, op) {
		t.Fatalf("original lookup changed: %v", err)
	}
	_, _, err = b.lookupOperation(context.Background(), budget, "absent")
	if !tracker.IsKind(err, tracker.ErrorNotFound) {
		t.Fatalf("native absent shape: %v", err)
	}
	config.ReadOnly = true
	readonly, err := Open(context.Background(), config)
	if err != nil {
		t.Fatal(err)
	}
	before = string(fixture("/usr/bin/git", "for-each-ref", "--format=%(refname) %(objectname)"))
	if _, outcome, err := readonly.mutate(context.Background(), budget, mutationRequest{intent: "comment", id: opened.IssueID, key: "readonly", body: "refuse"}); err == nil || outcome != OutcomeUnstarted {
		t.Fatal("readonly mutation accepted")
	}
	if before != string(fixture("/usr/bin/git", "for-each-ref", "--format=%(refname) %(objectname)")) {
		t.Fatal("readonly mutation wrote")
	}
}
