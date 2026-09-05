package restore

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	journal "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

type deadlineObservation struct {
	method     string
	deadline   time.Time
	observedAt time.Time
	has        bool
}

type deadlineRecordingReader struct {
	ReaderV2
	mu    sync.Mutex
	calls []deadlineObservation
}

func (r *deadlineRecordingReader) observe(method string, ctx context.Context) {
	deadline, has := ctx.Deadline()
	r.mu.Lock()
	r.calls = append(r.calls, deadlineObservation{method: method, deadline: deadline, observedAt: time.Now(), has: has})
	r.mu.Unlock()
}

func (r *deadlineRecordingReader) CaptureRevisionV2(ctx context.Context) (journal.StoreRevisionV2, error) {
	r.observe("capture", ctx)
	return r.ReaderV2.CaptureRevisionV2(ctx)
}

func (r *deadlineRecordingReader) VerifyRevisionV2(ctx context.Context, attempt journal.StoreRevisionV2) error {
	r.observe("verify", ctx)
	return r.ReaderV2.VerifyRevisionV2(ctx, attempt)
}

func (r *deadlineRecordingReader) ReadV2(ctx context.Context, request journal.ReadRequestV2) (journal.RecoveryRecordV2, error) {
	r.observe("read", ctx)
	return r.ReaderV2.ReadV2(ctx, request)
}

func (r *deadlineRecordingReader) ScanV2(ctx context.Context, request journal.ScanRequestV2) (journal.RecoveryPageV2, error) {
	r.observe("scan", ctx)
	return r.ReaderV2.ScanV2(ctx, request)
}

func (r *deadlineRecordingReader) snapshot() []deadlineObservation {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]deadlineObservation(nil), r.calls...)
}

func TestRestoreDeadlineInputSensitiveAndBeforeFirstIO(t *testing.T) {
	durations := []time.Duration{80 * time.Millisecond, 220 * time.Millisecond}
	remaining := make([]time.Duration, len(durations))
	for index, maximum := range durations {
		reader := &deadlineRecordingReader{ReaderV2: &emptyReader{}}
		input := restoreInput(t, reader)
		input.Budgets = DefaultBudgetsV2()
		input.Budgets.MaximumDuration = maximum
		parent, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		result, err := RestoreV2(parent, input)
		cancel()
		if err != nil {
			t.Fatalf("evidence=RESTORE-DEADLINE-DERIVATION duration=%s error=%v", maximum, err)
		}
		calls := reader.snapshot()
		if len(calls) == 0 || calls[0].method != "capture" || !calls[0].has {
			t.Fatalf("evidence=RESTORE-DEADLINE-PLACEMENT duration=%s first-call=%+v", maximum, calls)
		}
		remaining[index] = calls[0].deadline.Sub(calls[0].observedAt)
		if remaining[index] < maximum/2 || remaining[index] > maximum+100*time.Millisecond {
			t.Fatalf("evidence=RESTORE-DEADLINE-DERIVATION configured=%s observed-remaining=%s", maximum, remaining[index])
		}
		if _, err := result.FreshEpochAdmissionRequestV2(); err != nil {
			t.Fatalf("evidence=RESTORE-DEADLINE-DERIVATION valid restore was not sealed: %v", err)
		}
	}
	if remaining[1]-remaining[0] < 70*time.Millisecond {
		t.Fatalf("evidence=RESTORE-DEADLINE-DERIVATION distinct inputs collapsed: %v", remaining)
	}
}

func TestRestoreDeadlinePropagatesExactAttemptDeadline(t *testing.T) {
	input, _, _ := qualificationRestoreFixture(t, 1, 1, false)
	reader := &deadlineRecordingReader{ReaderV2: input.Reader}
	input.Reader = reader
	input.Budgets = DefaultBudgetsV2()
	input.Budgets.MaximumDuration = time.Second
	parent, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	if _, err := RestoreV2(parent, input); err != nil {
		t.Fatalf("evidence=RESTORE-DEADLINE-PROPAGATION restore failed: %v", err)
	}
	calls := reader.snapshot()
	if len(calls) == 0 || calls[0].method != "capture" || !calls[0].has {
		t.Fatalf("evidence=RESTORE-DEADLINE-PROPAGATION missing capture deadline: %+v", calls)
	}
	want := calls[0].deadline
	seen := map[string]bool{}
	for _, call := range calls {
		seen[call.method] = true
		if !call.has || !call.deadline.Equal(want) {
			t.Fatalf("evidence=RESTORE-DEADLINE-PROPAGATION method=%s deadline=%v want=%v", call.method, call.deadline, want)
		}
	}
	for _, method := range []string{"capture", "scan", "read", "verify"} {
		if !seen[method] {
			t.Fatalf("evidence=RESTORE-DEADLINE-PROPAGATION method=%s was not exercised", method)
		}
	}
}

func TestRestoreDeadlineExpirationCancelsAndCannotSeal(t *testing.T) {
	reader := &deadlineRetryReader{blockEveryAttempt: true}
	input := restoreInput(t, reader)
	input.Budgets = DefaultBudgetsV2()
	input.Budgets.MaximumRestarts = 1
	input.Budgets.MaximumDuration = 15 * time.Millisecond
	failsafe, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	started := time.Now()
	result, err := RestoreV2(failsafe, input)
	if elapsed := time.Since(started); elapsed > 500*time.Millisecond {
		t.Fatalf("evidence=RESTORE-DEADLINE-CANCELLATION blocked operation exceeded bound: %s", elapsed)
	}
	var restoreErr *Error
	if !errors.As(err, &restoreErr) || restoreErr.Kind != ErrorUnstableStore {
		t.Fatalf("evidence=RESTORE-DEADLINE-CANCELLATION error=%v", err)
	}
	if _, admissionErr := result.FreshEpochAdmissionRequestV2(); admissionErr == nil {
		t.Fatal("evidence=RESTORE-DEADLINE-CANCELLATION expired attempt admitted a fresh epoch")
	}
}

func TestRestoreDeadlineBudgetBoundariesBeforeIO(t *testing.T) {
	invalid := []struct {
		name     string
		duration time.Duration
	}{
		{name: "negative", duration: -time.Nanosecond},
		{name: "zero", duration: 0},
		{name: "over_maximum", duration: 5*time.Minute + time.Nanosecond},
	}
	for _, test := range invalid {
		t.Run(test.name, func(t *testing.T) {
			reader := &deadlineRecordingReader{ReaderV2: &emptyReader{}}
			input := restoreInput(t, reader)
			input.Budgets = DefaultBudgetsV2()
			input.Budgets.MaximumDuration = test.duration
			_, err := RestoreV2(t.Context(), input)
			var restoreErr *Error
			if !errors.As(err, &restoreErr) || restoreErr.Kind != ErrorBudgetExceeded || len(reader.snapshot()) != 0 {
				t.Fatalf("evidence=RESTORE-DEADLINE-BOUNDS duration=%s error=%v calls=%+v", test.duration, err, reader.snapshot())
			}
		})
	}

	for _, test := range []struct {
		name     string
		duration time.Duration
	}{
		{name: "smallest_positive", duration: time.Nanosecond},
		{name: "exact_maximum", duration: 5 * time.Minute},
	} {
		t.Run(test.name, func(t *testing.T) {
			reader := &deadlineRecordingReader{ReaderV2: &emptyReader{}}
			input := restoreInput(t, reader)
			input.Budgets = DefaultBudgetsV2()
			input.Budgets.MaximumDuration = test.duration
			_, err := RestoreV2(t.Context(), input)
			if len(reader.snapshot()) == 0 || test.duration == 5*time.Minute && err != nil {
				t.Fatalf("evidence=RESTORE-DEADLINE-BOUNDS duration=%s error=%v calls=%+v", test.duration, err, reader.snapshot())
			}
		})
	}
}

type deadlineRetryReader struct {
	mu                sync.Mutex
	captures          int
	deadlines         []time.Time
	blockEveryAttempt bool
	blockFirstAttempt bool
}

func (r *deadlineRetryReader) CaptureRevisionV2(ctx context.Context) (journal.StoreRevisionV2, error) {
	deadline, _ := ctx.Deadline()
	r.mu.Lock()
	r.captures++
	r.deadlines = append(r.deadlines, deadline)
	r.mu.Unlock()
	return journal.NewStoreRevisionV2("database-1", 7, 7)
}

func (r *deadlineRetryReader) VerifyRevisionV2(context.Context, journal.StoreRevisionV2) error {
	return nil
}

func (r *deadlineRetryReader) ReadV2(context.Context, journal.ReadRequestV2) (journal.RecoveryRecordV2, error) {
	panic("empty deadline fixture must not exact-read")
}

func (r *deadlineRetryReader) ScanV2(ctx context.Context, request journal.ScanRequestV2) (journal.RecoveryPageV2, error) {
	r.mu.Lock()
	attempt := r.captures
	block := r.blockEveryAttempt || r.blockFirstAttempt && attempt == 1
	r.mu.Unlock()
	if block {
		<-ctx.Done()
		return journal.RecoveryPageV2{}, ctx.Err()
	}
	return journal.NewRecoveryPageV2(request.AttemptRevision().CurrentRevision(), "2026-09-03T12:00:00Z", nil, nil, false)
}

func (r *deadlineRetryReader) snapshot() (int, []time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.captures, append([]time.Time(nil), r.deadlines...)
}

func TestRestoreDeadlineRetryGetsFreshChildAndLaterParentDoesNotReplaceBudget(t *testing.T) {
	reader := &deadlineRetryReader{blockFirstAttempt: true}
	input := restoreInput(t, reader)
	input.Budgets = DefaultBudgetsV2()
	input.Budgets.MaximumRestarts = 2
	input.Budgets.MaximumDuration = 20 * time.Millisecond
	parent, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	result, err := RestoreV2(parent, input)
	if err != nil {
		t.Fatalf("evidence=RESTORE-DEADLINE-RETRY error=%v", err)
	}
	captures, deadlines := reader.snapshot()
	if captures != 2 || len(deadlines) != 2 || deadlines[1].Sub(deadlines[0]) < 10*time.Millisecond {
		t.Fatalf("evidence=RESTORE-DEADLINE-RETRY captures=%d deadlines=%v", captures, deadlines)
	}
	if _, err := result.FreshEpochAdmissionRequestV2(); err != nil {
		t.Fatalf("evidence=RESTORE-DEADLINE-RETRY replacement attempt did not seal: %v", err)
	}
}

func TestRestoreDeadlineEarlierParentRemainsAuthoritative(t *testing.T) {
	reader := &deadlineRetryReader{blockEveryAttempt: true}
	input := restoreInput(t, reader)
	input.Budgets = DefaultBudgetsV2()
	input.Budgets.MaximumRestarts = 3
	input.Budgets.MaximumDuration = 250 * time.Millisecond
	parent, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	result, err := RestoreV2(parent, input)
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("evidence=RESTORE-DEADLINE-PARENT earlier parent error=%v", err)
	}
	if captures, _ := reader.snapshot(); captures != 1 {
		t.Fatalf("evidence=RESTORE-DEADLINE-PARENT earlier parent caused %d attempts", captures)
	}
	if _, admissionErr := result.FreshEpochAdmissionRequestV2(); admissionErr == nil {
		t.Fatal("evidence=RESTORE-DEADLINE-PARENT canceled parent admitted a fresh epoch")
	}
}

type deadlineSourceMutant struct {
	id, anchor, replacement, killer, evidence string
}

func TestRestoreDeadlineSourceOverlayMutants(t *testing.T) {
	if raceBuild() {
		t.Skip("the bounded source-overlay campaign runs once without -race; every production deadline behavior remains race-tested")
	}
	root := deadlineRepositoryRoot(t)
	sourcePath := filepath.Join(root, "internal/controljournal/v2/restore/restore.go")
	originalBytes, err := os.ReadFile(sourcePath)
	if err != nil {
		t.Fatal(err)
	}
	original := string(originalBytes)
	mutants := []deadlineSourceMutant{
		{
			id:          "M-RESTORE-DEADLINE-REMOVED",
			anchor:      "ctx, cancel := context.WithTimeout(parent, budgets.MaximumDuration)",
			replacement: "ctx, cancel := context.WithCancel(parent)",
			killer:      "TestRestoreDeadlineInputSensitiveAndBeforeFirstIO",
			evidence:    "evidence=RESTORE-DEADLINE-DERIVATION",
		},
		{
			id:          "M-RESTORE-DEADLINE-CONSTANT",
			anchor:      "ctx, cancel := context.WithTimeout(parent, budgets.MaximumDuration)",
			replacement: "ctx, cancel := context.WithTimeout(parent, time.Second)",
			killer:      "TestRestoreDeadlineInputSensitiveAndBeforeFirstIO",
			evidence:    "evidence=RESTORE-DEADLINE-DERIVATION",
		},
		{
			id:          "M-RESTORE-DEADLINE-LATE",
			anchor:      "ctx, cancel := context.WithTimeout(parent, budgets.MaximumDuration)\n\tdefer cancel()\n\tattempt, err := input.Reader.CaptureRevisionV2(ctx)",
			replacement: "attempt, err := input.Reader.CaptureRevisionV2(parent)\n\tctx, cancel := context.WithTimeout(parent, budgets.MaximumDuration)\n\tdefer cancel()",
			killer:      "TestRestoreDeadlineInputSensitiveAndBeforeFirstIO",
			evidence:    "evidence=RESTORE-DEADLINE-DERIVATION",
		},
		{
			id:          "M-RESTORE-DEADLINE-PARTIAL-BYPASS",
			anchor:      "if err := input.Reader.VerifyRevisionV2(ctx, attempt); err != nil {",
			replacement: "if err := input.Reader.VerifyRevisionV2(parent, attempt); err != nil {",
			killer:      "TestRestoreDeadlinePropagatesExactAttemptDeadline",
			evidence:    "evidence=RESTORE-DEADLINE-PROPAGATION",
		},
		{
			id:          "M-RESTORE-DEADLINE-LOWER-BOUND-REMOVED",
			anchor:      "b.MaximumDuration <= 0 || ",
			replacement: "",
			killer:      "TestRestoreDeadlineBudgetBoundariesBeforeIO",
			evidence:    "evidence=RESTORE-DEADLINE-BOUNDS",
		},
		{
			id:          "M-RESTORE-DEADLINE-UPPER-BOUND-REMOVED",
			anchor:      " || b.MaximumDuration > 5*time.Minute",
			replacement: "",
			killer:      "TestRestoreDeadlineBudgetBoundariesBeforeIO",
			evidence:    "evidence=RESTORE-DEADLINE-BOUNDS",
		},
	}
	for _, mutant := range mutants {
		t.Run(mutant.id, func(t *testing.T) {
			mutated := deadlineReplaceExactlyOnce(t, original, mutant.anchor, mutant.replacement)
			overlay := deadlineOverlay(t, sourcePath, mutated)
			ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
			defer cancel()
			command := exec.CommandContext(ctx, "go", "test", "-overlay="+overlay, "./internal/controljournal/v2/restore", "-run", "^"+mutant.killer+"$", "-count=1")
			command.Dir = root
			command.Env = append(os.Environ(), "GOWORK=off")
			output, runErr := command.CombinedOutput()
			if runErr == nil || !strings.Contains(string(output), mutant.evidence) {
				t.Fatalf("mutant=%s killer=%s survived-or-wrong-law err=%v output=%s", mutant.id, mutant.killer, runErr, output)
			}
			t.Logf("mutant=%s killer=%s source_sha256=%s mutant_sha256=%s", mutant.id, mutant.killer, deadlineSHA256(original), deadlineSHA256(mutated))
		})
	}
	t.Run("M-RESTORE-AMBIENT-NOW", func(t *testing.T) {
		anchor := "func restoreAttempt(parent context.Context, input InputV2, budgets BudgetsV2) (ResultV2, error) {\n"
		mutated := deadlineReplaceExactlyOnce(t, original, anchor, anchor+"\t_ = time.Now()\n")
		deadlineRunAmbientArchitectureKiller(t, root, mutated)
		t.Logf("mutant=M-RESTORE-AMBIENT-NOW killer=WP01/RESTORE-DEADLINE-BOUNDARY source_sha256=%s mutant_sha256=%s", deadlineSHA256(original), deadlineSHA256(mutated))
	})
}

func deadlineReplaceExactlyOnce(t *testing.T, source, anchor, replacement string) string {
	t.Helper()
	if count := strings.Count(source, anchor); count != 1 {
		t.Fatalf("source overlay anchor count=%d want=1 anchor=%q", count, anchor)
	}
	mutated := strings.Replace(source, anchor, replacement, 1)
	if mutated == source {
		t.Fatal("source overlay produced no mutation")
	}
	return mutated
}

func deadlineOverlay(t *testing.T, sourcePath, mutated string) string {
	t.Helper()
	temporary := t.TempDir()
	mutatedPath := filepath.Join(temporary, "restore.go")
	if err := os.WriteFile(mutatedPath, []byte(mutated), 0o600); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: map[string]string{sourcePath: mutatedPath}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(temporary, "overlay.json")
	if err := os.WriteFile(overlayPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	return overlayPath
}

func deadlineRunAmbientArchitectureKiller(t *testing.T, root, mutatedRestore string) {
	t.Helper()
	architecturePath := filepath.Join(root, "internal/architecture/m2_dependency_guard_test.go")
	architectureBytes, err := os.ReadFile(architecturePath)
	if err != nil {
		t.Fatal(err)
	}
	appended := fmt.Sprintf(`

func TestM2RestoreDeadlineAmbientOverlay(t *testing.T) {
	directory := t.TempDir()
	if err := os.WriteFile(filepath.Join(directory, "restore.go"), []byte(%q), 0o600); err != nil {
		t.Fatal(err)
	}
	restorePackage := m2ModulePath + "/internal/controljournal/v2/restore"
	violations := m2RestoreDeadlineSourceViolations(Graph{
		ModulePath: m2ModulePath,
		Packages: []Package{{ImportPath: restorePackage, Dir: directory}},
	})
	if len(violations) != 1 || violations[0].Rule != RuleDeferredSurface || violations[0].TargetPackage != "time.Now" {
		t.Fatalf("evidence=RESTORE-DEADLINE-BOUNDARY ambient mutant observed=%%v", violations)
	}
}
`, mutatedRestore)
	temporary := t.TempDir()
	mutatedArchitecturePath := filepath.Join(temporary, "m2_dependency_guard_test.go")
	if err := os.WriteFile(mutatedArchitecturePath, append(architectureBytes, []byte(appended)...), 0o600); err != nil {
		t.Fatal(err)
	}
	encoded, err := json.Marshal(struct {
		Replace map[string]string `json:"Replace"`
	}{Replace: map[string]string{architecturePath: mutatedArchitecturePath}})
	if err != nil {
		t.Fatal(err)
	}
	overlayPath := filepath.Join(temporary, "overlay.json")
	if err := os.WriteFile(overlayPath, encoded, 0o600); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 45*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "-overlay="+overlayPath, "./internal/architecture", "-run", "^TestM2RestoreDeadlineAmbientOverlay$", "-count=1")
	command.Dir = root
	command.Env = append(os.Environ(), "GOWORK=off")
	if output, runErr := command.CombinedOutput(); runErr != nil {
		t.Fatalf("mutant=M-RESTORE-AMBIENT-NOW killer=WP01/RESTORE-DEADLINE-BOUNDARY err=%v output=%s", runErr, output)
	}
}

func deadlineRepositoryRoot(t *testing.T) string {
	t.Helper()
	directory, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(directory, "go.mod")); err == nil {
			return directory
		}
		parent := filepath.Dir(directory)
		if parent == directory {
			t.Fatal("repository root not found")
		}
		directory = parent
	}
}

func deadlineSHA256(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}
