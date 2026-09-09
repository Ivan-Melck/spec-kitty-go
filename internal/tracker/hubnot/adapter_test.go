package hubnot

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const testID = "sha256:aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
const testSnapshot = "sha256:bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

// This constructor exists only in tests. Public Open can never admit this child.
func testBound(t *testing.T, binary string) *BoundAdapter {
	t.Helper()
	dir := t.TempDir()
	digest, err := fileDigest(binary)
	if err != nil {
		t.Fatal(err)
	}
	ei, _ := os.Stat(binary)
	wi, _ := os.Stat(dir)
	key := make([]byte, 32)
	actor := sha256.Sum256(key)
	name := hex.EncodeToString(actor[:])
	return &BoundAdapter{config: BindingConfig{ExecutablePath: binary, WorkspacePath: dir, ExpectedActor: name}, binding: tracker.TrackerBinding{ExecutablePath: binary, WorkspacePath: dir, ExecutableDigest: digest, ProjectID: ids.ProjectID("project-a")}, identity: PublicIdentity{Actor: name, Name: "Test", PublicKey: base64.RawStdEncoding.EncodeToString(key)}, executableInfo: ei, workspaceInfo: wi}
}
func scenario(t *testing.T, b *BoundAdapter, output string, exit int) {
	t.Helper()
	encoded, _ := json.Marshal(struct {
		Output, Stderr []byte
		Exit           int
	}{[]byte(output), []byte("bounded diagnostic"), exit})
	if err := os.WriteFile(filepath.Join(b.binding.WorkspacePath, "scenario.json"), encoded, 0600); err != nil {
		t.Fatal(err)
	}
}
func success(data any, snapshot string) string {
	raw, _ := json.Marshal(map[string]any{"schema": issueSchema, "ok": true, "data": data, "snapshot": snapshot})
	return string(raw)
}
func calls(t *testing.T, b *BoundAdapter) int {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(b.binding.WorkspacePath, "calls"))
	if os.IsNotExist(err) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return strings.Count(string(raw), "call\n")
}
func state() issueState {
	return issueState{Title: "Full work", Body: "exact body", Status: "open", Criteria: []issueCriterion{}, Labels: []string{}, Assignees: []string{}, Relations: []issueRelation{}, Metadata: map[string]string{"foreign/value": "preserved"}}
}

func TestAdapterOutcomeAccounting(t *testing.T) {
	child := testChild(t)
	for _, tc := range []struct {
		name, output string
		exit         int
		want         MutationOutcome
		code         string
	}{
		{"applied", success(issueMutation{IssueID: testID, EventID: testID, HeadCount: 1}, ""), 0, OutcomeApplied, ""},
		{"lost response", "", 0, OutcomeAmbiguous, ""},
		{"malformed success", `{"schema":"hn.issue/1","ok":true,"data":{}}`, 0, OutcomeAmbiguous, ""},
		{"typed pin refusal", `{"schema":"hn.issue/1","ok":false,"error":{"code":"actor_mismatch","message":"repair binding"}}`, 1, OutcomeNotApplied, "actor_mismatch"},
		{"post append failure", `{"schema":"hn.issue/1","ok":false,"error":{"code":"repository_error","message":"failed after write"}}`, 1, OutcomeAmbiguous, "repository_error"},
		{"post append invalid history", `{"schema":"hn.issue/1","ok":false,"error":{"code":"invalid_history","message":"failed after write"}}`, 1, OutcomeAmbiguous, "invalid_history"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			b := testBound(t, child)
			scenario(t, b, tc.output, tc.exit)
			budget := newObservationBudget()
			defer budget.close()
			s := state()
			_, outcome, err := b.mutate(context.Background(), budget, mutationRequest{intent: "open", key: "stable", state: &s})
			if outcome != tc.want {
				t.Fatalf("outcome %s want %s err %v", outcome, tc.want, err)
			}
			if outcome == OutcomeApplied && err != nil {
				t.Fatal(err)
			}
			if outcome != OutcomeApplied {
				var ce *CommandError
				if !errors.As(err, &ce) || ce.ProviderCode != tc.code || ce.Diagnostic != "bounded diagnostic" {
					t.Fatalf("missing failure evidence: %v", err)
				}
			}
			if calls(t, b) != 1 {
				t.Fatal("unexpected process count")
			}
		})
	}
}
func TestAdapterMutationPinAndLimits(t *testing.T) {
	child := testChild(t)
	b := testBound(t, child)
	scenario(t, b, success(issueMutation{IssueID: testID, EventID: testID, HeadCount: 1}, ""), 0)
	budget := newObservationBudget()
	defer budget.close()
	s := state()
	s.Title = "--hostile literal"
	if _, _, err := b.mutate(context.Background(), budget, mutationRequest{intent: "open", key: "stable", state: &s}); err != nil {
		t.Fatal(err)
	}
	args, _ := os.ReadFile(filepath.Join(b.binding.WorkspacePath, "argv.json"))
	var argv []string
	_ = json.Unmarshal(args, &argv)
	want := []string{"issue", "open", "--json", "--actor", b.config.ExpectedActor, "--operation", "stable", "--input", "-"}
	if !reflect.DeepEqual(argv, want) {
		t.Fatalf("argv %v", argv)
	}
	input, _ := os.ReadFile(filepath.Join(b.binding.WorkspacePath, "input.json"))
	var got issueState
	if err := decodeStrict(input, &got); err != nil || !reflect.DeepEqual(got, s) {
		t.Fatalf("stdin changed: %v", err)
	}
	before := calls(t, b)
	b.binding.ReadOnly = true
	if _, outcome, err := b.mutate(context.Background(), budget, mutationRequest{intent: "open", key: "other", state: &s}); err == nil || outcome != OutcomeUnstarted {
		t.Fatal("read-only mutation dispatched")
	}
	b.binding.ReadOnly = false
	s.Metadata["foreign/huge"] = strings.Repeat("x", 4097)
	if _, outcome, err := b.mutate(context.Background(), budget, mutationRequest{intent: "open", key: "other", state: &s}); err == nil || outcome != OutcomeUnstarted {
		t.Fatal("native limit mutation dispatched")
	}
	if err := b.checkProject(ids.ProjectID("wrong-project")); err == nil {
		t.Fatal("wrong project accepted")
	}
	if calls(t, b) != before {
		t.Fatal("refusal started child")
	}
}
func TestAdapterBindingResourceDrift(t *testing.T) {
	original := testChild(t)
	for _, which := range []string{"binary_bytes", "binary_symlink", "workspace_symlink"} {
		t.Run(which, func(t *testing.T) {
			binary := filepath.Join(t.TempDir(), "child")
			raw, _ := os.ReadFile(original)
			if err := os.WriteFile(binary, raw, 0700); err != nil {
				t.Fatal(err)
			}
			b := testBound(t, binary)
			switch which {
			case "binary_bytes":
				if err := os.WriteFile(binary, []byte("changed"), 0700); err != nil {
					t.Fatal(err)
				}
			case "binary_symlink":
				link := filepath.Join(t.TempDir(), "link")
				if err := os.Symlink(binary, link); err != nil {
					t.Fatal(err)
				}
				b.config.ExecutablePath = link
				_ = os.Remove(link)
				_ = os.Symlink(original, link)
			case "workspace_symlink":
				link := filepath.Join(t.TempDir(), "link")
				_ = os.Symlink(b.binding.WorkspacePath, link)
				b.config.WorkspacePath = link
				_ = os.Remove(link)
				_ = os.Symlink(t.TempDir(), link)
			}
			budget := newObservationBudget()
			defer budget.close()
			_, err := b.execute(context.Background(), budget, command{argv: []string{"identity", "show", "--json"}, schema: identitySchema})
			var ce *CommandError
			if !errors.As(err, &ce) || ce.Outcome != OutcomeUnstarted || calls(t, b) != 0 {
				t.Fatalf("drift dispatched child: %v", err)
			}
		})
	}
}
func TestAdapterPublicReadShapes(t *testing.T) {
	b := testBound(t, testChild(t))
	budget := newObservationBudget()
	defer budget.close()
	s := state()
	rows := page[issueDetail]{Items: []issueDetail{{ID: testID, Creator: b.config.ExpectedActor, State: &s, OpeningMetadata: map[string]string{"original/value": "immutable"}, HeadCount: 1}}, Total: 1}
	scenario(t, b, success(rows, testSnapshot), 0)
	page, env, err := b.listDetails(context.Background(), budget, "")
	if err != nil || env.Snapshot != testSnapshot || !reflect.DeepEqual(page, rows) {
		t.Fatalf("detail shape %v", err)
	}
	body := "original\n"
	op := issueOperation{IssueID: testID, EventID: testID, Actor: b.config.ExpectedActor, Operation: "key", Intent: "comment", Kind: "issue.comment", Timestamp: time.Now().UTC().Format(time.RFC3339Nano), Parents: []string{}, Body: &body, Request: testID}
	scenario(t, b, success(op, testSnapshot), 0)
	result, _, err := b.lookupOperation(context.Background(), budget, "key")
	if err != nil || !reflect.DeepEqual(result, op) {
		t.Fatalf("original operation changed: %v", err)
	}
	scenario(t, b, `{"schema":"hn.issue/1","ok":false,"error":{"code":"not_found","message":"absent in verified history"}}`, 1)
	_, _, err = b.lookupOperation(context.Background(), budget, "absent")
	if !tracker.IsKind(err, tracker.ErrorNotFound) {
		t.Fatal(err)
	}
}

func TestAdapterFailedResponseStillConsumesBudget(t *testing.T) {
	b := testBound(t, testChild(t))
	scenario(t, b, "malformed returned bytes", 0)
	budget := newObservationBudget()
	defer budget.close()
	if err := budget.charge(maxObservationBytes-1, 0); err != nil {
		t.Fatal(err)
	}
	_, err := b.execute(context.Background(), budget, command{argv: []string{"issue", "list", "--json", "--details"}, schema: issueSchema})
	if err == nil {
		t.Fatal("oversized logical read accepted")
	}
	before := calls(t, b)
	if err := budget.restart(); err == nil {
		t.Fatal("failed attempt bytes forgotten")
	}
	_, err = b.execute(context.Background(), budget, command{argv: []string{"issue", "list", "--json", "--details"}, schema: issueSchema})
	if err == nil || calls(t, b) != before {
		t.Fatal("exhausted budget dispatched again")
	}
}
func TestQueryGrammarAndNativeMutationBoundaries(t *testing.T) {
	actor := strings.Repeat("a", 64)
	s := state()
	for _, intent := range []string{"open", "revise", "resolve", "close", "reopen", "comment"} {
		r := mutationRequest{intent: intent, id: testID, key: "key"}
		switch intent {
		case "open":
			r.id = ""
			r.state = &s
		case "revise":
			r.parents = []string{testID}
			r.state = &s
		case "resolve":
			r.parents = []string{testID, testSnapshot}
			r.state = &s
			r.partial = true
			r.snapshot = testSnapshot
		case "close", "reopen":
			r.parents = []string{testID}
		case "comment":
			r.body = "--literal body\n"
		}
		c, err := r.command(actor, "name")
		if err != nil {
			t.Fatalf("%s: %v", intent, err)
		}
		if !strings.Contains(strings.Join(c.argv, "\n"), "--actor\n"+actor) || !strings.Contains(strings.Join(c.argv, "\n"), "--operation\nkey") {
			t.Fatal("mutation missing pins")
		}
	}
	for _, q := range []query{{kind: "list", id: testID}, {kind: "graph", id: "short"}, {kind: "history", id: testID, historyKind: "wrong"}, {kind: "heads", id: testID, limit: 201}, {kind: "show", id: testID, cursor: strings.Repeat("x", 4097)}} {
		if _, err := q.command(); err == nil {
			t.Fatal("bad query accepted")
		}
	}
	s.Body = strings.Repeat("\x01", 65536)
	if _, err := (mutationRequest{intent: "open", key: "key", state: &s}).command(actor, "name"); err == nil {
		t.Fatal("escaped native payload exceeds signed bound")
	}
}

func TestAdapterLogicalCaptureStopsAtRemainingByte(t *testing.T) {
	b := testBound(t, testChild(t))
	scenario(t, b, strings.Repeat("x", 1<<20), 0)
	budget := newObservationBudget()
	defer budget.close()
	if err := budget.charge(maxObservationBytes-1, 0); err != nil {
		t.Fatal(err)
	}
	started := time.Now()
	_, err := b.execute(context.Background(), budget, command{argv: []string{"issue", "list", "--json", "--details"}, schema: issueSchema})
	if err == nil {
		t.Fatal("logical overflow accepted")
	}
	if budget.retained > maxObservationBytes {
		t.Fatalf("retained cumulative bytes exceeded cap by %d", budget.retained-maxObservationBytes)
	}
	if budget.bytes <= maxObservationBytes || budget.bytes > maxObservationBytes+(64<<10) {
		t.Fatalf("observed pipe overage missing or unbounded: %d", budget.bytes-maxObservationBytes)
	}
	if time.Since(started) > 3*time.Second {
		t.Fatal("logical overflow did not cancel promptly")
	}
}
func TestAdapterOriginalOperationRejectsForbiddenNullFields(t *testing.T) {
	child := testChild(t)
	for _, intent := range []string{"comment", "open", "revise", "resolve", "close", "reopen"} {
		t.Run(intent, func(t *testing.T) {
			b := testBound(t, child)
			budget := newObservationBudget()
			defer budget.close()
			s := state()
			body := "exact comment"
			op := issueOperation{IssueID: testID, EventID: testID, Actor: b.config.ExpectedActor, Operation: "key", Intent: intent, Timestamp: "2026-09-09T00:00:00Z", Parents: []string{}, Request: testID}
			forbidden := "body"
			switch intent {
			case "comment":
				op.Kind = "issue.comment"
				op.Body = &body
				forbidden = "state"
			case "open":
				op.Kind = "issue.open"
				op.State = &s
			default:
				op.Kind = "issue.revise"
				op.State = &s
				op.Parents = []string{testID}
				if intent == "resolve" {
					op.Parents = append(op.Parents, testSnapshot)
				}
			}
			raw := success(op, testSnapshot)
			scenario(t, b, raw, 0)
			if _, _, err := b.lookupOperation(context.Background(), budget, "key"); err != nil {
				t.Fatal("valid original operation rejected", err)
			}
			raw = strings.Replace(raw, `"data":{`, `"data":{"`+forbidden+`":null,`, 1)
			scenario(t, b, raw, 0)
			if _, _, err := b.lookupOperation(context.Background(), budget, "key"); err == nil {
				t.Fatal("forbidden null field accepted")
			}
		})
	}
}
