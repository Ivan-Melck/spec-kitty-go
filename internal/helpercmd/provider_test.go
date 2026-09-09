package helpercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/hubnot"
)

func TestTrackerOuterMutationFactsAndSafeErrors(t *testing.T) {
	for _, test := range []struct {
		outcome        hubnot.MutationOutcome
		prior, applied bool
		want           Outcome
	}{
		{hubnot.OutcomeUnstarted, false, false, OutcomeUnavailable},
		{hubnot.OutcomeApplied, false, true, OutcomeUnavailable},
		{hubnot.OutcomeApplied, true, false, OutcomeUnavailable},
		{hubnot.OutcomeAmbiguous, true, true, OutcomeAmbiguous},
	} {
		cause := &hubnot.CommandError{Outcome: hubnot.OutcomeUnstarted, Diagnostic: "secret stderr", Cause: &tracker.Error{Kind: tracker.ErrorUnavailable, Message: "secret provider message"}}
		err := &hubnot.OperationError{Outcome: test.outcome, PriorMutation: test.prior, AppliedThisCall: test.applied, Cause: cause}
		value := trackerFailure("define", err)
		var out bytes.Buffer
		code := renderTracker(&out, &bytes.Buffer{}, "json", value)
		if value.Outcome != test.want || code != exitForOutcome(test.want) || value.Mutation == nil || value.Mutation.Outcome != test.outcome || value.Mutation.PriorMutation != test.prior || value.Mutation.AppliedThisCall != test.applied {
			t.Fatalf("outer facts lost: %+v", value)
		}
		if strings.Contains(out.String(), "secret") {
			t.Fatal("raw error leaked")
		}
		var e *tracker.Error
		if !errors.As(err, &e) {
			t.Fatal("fixture must wrap tracker cause")
		}
	}
}
func TestTrackerProofBytesAndPrivateRecordFields(t *testing.T) {
	raw := []byte(`{"kind":"known-work-observation/1","exact":9007199254740993}`)
	p, err := tracker.NewProviderObservation("native", raw, 23, "digest", "token", time.Unix(1, 0))
	if err != nil {
		t.Fatal(err)
	}
	dto := proofDTO(p)
	encoded, _ := json.Marshal(dto)
	var restored trackerProofDTO
	if err = json.Unmarshal(encoded, &restored); err != nil || !bytes.Equal(restored.CanonicalFields, raw) || restored.Kind != "known-work-observation/1" || restored.Revision != 23 {
		t.Fatalf("proof lost: %s %v", encoded, err)
	}
	// Direct neutral marshaling excludes canonical bytes; the explicit DTO must not.
	direct, _ := json.Marshal(p)
	if bytes.Contains(direct, []byte("canonical_fields_base64")) {
		t.Fatal("fixture assumption changed")
	}
}

func TestTrackerTypedRefusalsAndCanceledOutput(t *testing.T) {
	for _, test := range []struct {
		kind tracker.ErrorKind
		want Outcome
	}{
		{tracker.ErrorInvalidRequest, OutcomeInvalid}, {tracker.ErrorNotFound, OutcomeNotFound},
		{tracker.ErrorRevisionConflict, OutcomeConflict}, {tracker.ErrorAttributionLost, OutcomeAmbiguous},
		{tracker.ErrorAmbiguous, OutcomeAmbiguous}, {tracker.ErrorUnavailable, OutcomeUnavailable},
		{tracker.ErrorMalformedProtocol, OutcomeUnavailable}, {tracker.ErrorVersionSkew, OutcomeRefused},
		{tracker.ErrorGuardedUpdateUnsupported, OutcomeRefused}, {tracker.ErrorCapabilityUnsupported, OutcomeRefused},
	} {
		v := trackerFailure("read", &tracker.Error{Kind: test.kind, Message: "private raw error"})
		if v.Outcome != test.want || v.Diagnostic.Kind != string(test.kind) {
			t.Fatalf("wrong typed disposition: %+v", v)
		}
	}
	v := trackerFailure("comment", context.Canceled)
	if v.Outcome != OutcomeCanceled {
		t.Fatal("cancellation lost")
	}
}

func TestTrackerSharedDiscussionProofSerializesOnce(t *testing.T) {
	now := time.Unix(1, 0)
	raw := []byte(`{"kind":"discussion-observation/1","rows":"` + strings.Repeat("x", 32768) + `"}`)
	proof, err := tracker.NewProviderObservation("native", raw, 9, "common-digest", "snapshot", now)
	if err != nil {
		t.Fatal(err)
	}
	current, err := tracker.NewProviderObservation("native", []byte(`{"kind":"known-work-observation/1"}`), 8, "current-digest", "snapshot", now)
	if err != nil {
		t.Fatal(err)
	}
	entries := make([]tracker.AttributedDiscussionEntry, 0, 8)
	for i := 0; i < 8; i++ {
		e, err := tracker.NewAttributedDiscussionEntry(tracker.DiscussionEntry{ExternalCommentID: fmt.Sprint(i), Author: "author", Body: "body", OperationMarker: "marker", CreatedAt: now, Observation: 9}, "signer", proof)
		if err != nil {
			t.Fatal(err)
		}
		entries = append(entries, e)
	}
	discussion, err := tracker.NewDiscussionObservation(entries, proof, current)
	if err != nil {
		t.Fatal(err)
	}
	result, err := invokeTracker(t.Context(), discussionConsumer{value: discussion}, "discussion", decodedProviderRequest{})
	if err != nil {
		t.Fatal(err)
	}
	rawResult, err := json.Marshal(result)
	if err != nil {
		t.Fatal(err)
	}
	if len(rawResult) > len(raw)*2 {
		t.Fatalf("shared proof repeated: %d bytes for %d-byte proof", len(rawResult), len(raw))
	}
	var out struct {
		Entries []struct {
			ProofRef struct {
				Kind, Digest string
				Revision     tracker.Revision
			} `json:"proof_ref"`
		} `json:"entries"`
		Common trackerProofDTO `json:"common_proof"`
	}
	if err = json.Unmarshal(rawResult, &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Common.CanonicalFields, raw) || len(out.Entries) != 8 {
		t.Fatal("common bytes/entries lost")
	}
	for _, entry := range out.Entries {
		if entry.ProofRef.Kind != out.Common.Kind || entry.ProofRef.Digest != out.Common.Digest || entry.ProofRef.Revision != out.Common.Revision {
			t.Fatal("entry proof reference does not resolve")
		}
	}
}

type discussionConsumer struct {
	trackerConsumer
	value tracker.DiscussionObservation
}

func (p discussionConsumer) ObserveDiscussion(context.Context, tracker.ExternalWorkRef) (tracker.DiscussionObservation, error) {
	return p.value, nil
}

func TestTrackerHumanOutputKeepsUsefulIdentityAndRecovery(t *testing.T) {
	proof, _ := tracker.NewProviderObservation("native", []byte(`{"kind":"original-operation/1","large":"evidence"}`), 23, "digest", "token", time.Unix(1, 0))
	var out, diagnostic bytes.Buffer
	env := trackerEnvelope{Schema: trackerResultSchema, Action: "create", Outcome: OutcomeSuccess, Result: map[string]any{"work_id": "readable-work", "proof": proofDTO(proof), "provider_metadata_json": `{"number":9007199254740993}`}}
	if code := renderTracker(&out, &diagnostic, "human", env); code != 0 || !strings.Contains(out.String(), "readable-work") || !strings.Contains(out.String(), "original-operation/1") || !strings.Contains(out.String(), "9007199254740993") || strings.Contains(out.String(), "canonical_fields_base64") {
		t.Fatalf("unhelpful human output: %d %s", code, out.Bytes())
	}
	out.Reset()
	err := &hubnot.OperationError{Outcome: hubnot.OutcomeAmbiguous, PriorMutation: true, Cause: &tracker.Error{Kind: tracker.ErrorUnavailable}}
	if code := renderTracker(&out, &diagnostic, "human", trackerFailure("define", err)); code != ExitAmbiguous || !strings.Contains(diagnostic.String(), "operation ID") || !strings.Contains(diagnostic.String(), "prior mutation: true") {
		t.Fatalf("recovery lost: %s", diagnostic.Bytes())
	}
}

type unavailableReadyConsumer struct{ trackerConsumer }

func (unavailableReadyConsumer) ReadyProjection(context.Context, tracker.ReadyRequest) (tracker.ReadyProjection, error) {
	return tracker.ReadyProjection{ProviderReadyIDs: []ids.WorkID{}, Diagnostics: []tracker.Diagnostic{{Kind: tracker.ErrorAttributionLost, RecoveryCode: "reconcile_mapping", Message: "current graph unavailable"}}}, nil
}
func TestTrackerUnavailableReadyHasExplicitProofAbsence(t *testing.T) {
	value, err := invokeTracker(t.Context(), unavailableReadyConsumer{}, "ready", decodedProviderRequest{})
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(raw, &result)
	if result["availability"] != "unavailable" || result["proof"] != nil || len(result["diagnostics"].([]any)) != 1 || result["authority"] != "diagnostic_only" {
		t.Fatalf("invented readiness proof: %s", raw)
	}
}

type trackerFailedWriter struct{ short bool }

func (w trackerFailedWriter) Write(p []byte) (int, error) {
	if w.short {
		return len(p) - 1, nil
	}
	return 0, errors.New("injected writer failure")
}
func TestTrackerOutputFailuresAndShortWritesAreUnavailable(t *testing.T) {
	success := trackerEnvelope{Schema: trackerResultSchema, Action: "create", Outcome: OutcomeSuccess, Result: map[string]any{"work_id": "work"}}
	failure := trackerFailure("define", &hubnot.OperationError{Outcome: hubnot.OutcomeApplied, AppliedThisCall: true, Cause: &tracker.Error{Kind: tracker.ErrorUnavailable}})
	for _, writer := range []io.Writer{trackerFailedWriter{}, trackerFailedWriter{short: true}} {
		for _, mode := range []string{"json", "human"} {
			if code := renderTracker(writer, io.Discard, mode, success); code != ExitUnavailable {
				t.Fatalf("%s writer failure: %d", mode, code)
			}
		}
		if code := renderTracker(io.Discard, writer, "human", failure); code != ExitUnavailable {
			t.Fatalf("diagnostic write failure: %d", code)
		}
		for _, args := range [][]string{{"--help"}, {"create", "--help"}} {
			if code := runTracker(t.Context(), globalOptions{command: "tracker", args: args, output: "human"}, strings.NewReader(""), writer, io.Discard); code != ExitUnavailable {
				t.Fatalf("help write failure: %d", code)
			}
		}
	}
}

func TestTrackerRegularInputFileBoundary(t *testing.T) {
	dir := t.TempDir()
	fifo := filepath.Join(dir, "fifo")
	if err := syscall.Mkfifo(fifo, 0600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{fifo, dir} {
		if file, err := openTrackerRegularFile(path); err == nil {
			file.Close()
			t.Fatalf("accepted nonregular file %s", path)
		}
	}
	file := filepath.Join(dir, "request.json")
	if err := os.WriteFile(file, []byte("{}"), 0600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "request-link")
	if err := os.Symlink(file, link); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{file, link} {
		f, err := openTrackerRegularFile(path)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := io.ReadAll(f)
		f.Close()
		if err != nil || string(raw) != "{}" {
			t.Fatal("regular file refused")
		}
	}
}

func TestTrackerOutputBackpressureCancellation(t *testing.T) {
	for _, mode := range []string{"json", "human"} {
		reader, writer := io.Pipe()
		ctx, cancel := context.WithCancel(t.Context())
		result := make(chan int, 1)
		go func() {
			result <- renderTrackerContext(ctx, writer, io.Discard, mode, trackerEnvelope{Schema: trackerResultSchema, Action: "create", Outcome: OutcomeSuccess, Result: map[string]any{"work_id": "applied"}})
		}()
		one := make([]byte, 1)
		if _, err := reader.Read(one); err != nil {
			t.Fatal(err)
		}
		cancel()
		select {
		case code := <-result:
			if code != ExitCanceled {
				t.Fatalf("%s cancellation exit=%d", mode, code)
			}
		case <-time.After(time.Second):
			t.Fatal("output write remained blocked")
		}
		reader.Close()
		writer.Close()
	}
	reader, writer := io.Pipe()
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	result := make(chan int, 1)
	failure := trackerFailure("define", &hubnot.OperationError{Outcome: hubnot.OutcomeApplied, PriorMutation: true, AppliedThisCall: true, Cause: &tracker.Error{Kind: tracker.ErrorUnavailable}})
	go func() { result <- renderTrackerContext(ctx, io.Discard, writer, "human", failure) }()
	one := make([]byte, 1)
	if _, err := reader.Read(one); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case code := <-result:
		if code != ExitCanceled {
			t.Fatalf("stderr cancellation exit=%d", code)
		}
	case <-time.After(time.Second):
		t.Fatal("stderr remained blocked")
	}
	reader.Close()
	writer.Close()
}

func TestTrackerCanceledInitialDiagnosticHasBoundedDelivery(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	failure := trackerFailure("define", &hubnot.OperationError{Outcome: hubnot.OutcomeApplied, PriorMutation: true, AppliedThisCall: true, Cause: context.Canceled})
	var out bytes.Buffer
	if code := renderTrackerContext(ctx, &out, io.Discard, "json", failure); code != ExitCanceled {
		t.Fatalf("canceled diagnostic code=%d", code)
	}
	var delivered trackerEnvelope
	if err := json.Unmarshal(out.Bytes(), &delivered); err != nil || delivered.Mutation == nil || !delivered.Mutation.PriorMutation || !delivered.Mutation.AppliedThisCall || delivered.Mutation.Outcome != hubnot.OutcomeApplied {
		t.Fatalf("initial diagnostic lost facts: %s %v", out.Bytes(), err)
	}
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	started := time.Now()
	if code := renderTrackerContext(ctx, writer, io.Discard, "json", failure); code != ExitCanceled {
		t.Fatalf("blocked canceled diagnostic exit=%d", code)
	}
	if elapsed := time.Since(started); elapsed < 200*time.Millisecond || elapsed > time.Second {
		t.Fatalf("initial diagnostic grace not bounded: %s", elapsed)
	}
}
func TestTrackerBorrowedOutputDescriptorsRestoreAcrossRepeats(t *testing.T) {
	warmR, warmW, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	warmR.Close()
	warmW.Close()
	baseline := countRequestDescriptors(t)
	for _, nonblock := range []bool{false, true} {
		for _, cancelWrite := range []bool{false, true} {
			for repeat := 0; repeat < 3; repeat++ {
				reader, writer, err := os.Pipe()
				if err != nil {
					t.Fatal(err)
				}
				raw, err := writer.SyscallConn()
				if err != nil {
					t.Fatal(err)
				}
				var setErr error
				if err = raw.Control(func(fd uintptr) { setErr = syscall.SetNonblock(int(fd), nonblock) }); err != nil || setErr != nil {
					t.Fatalf("fixture flags: %v %v", err, setErr)
				}
				before := requestFileFlags(t, writer)
				ctx, cancel := context.WithCancel(t.Context())
				done := make(chan error, 1)
				payload := []byte("complete-output")
				if cancelWrite {
					payload = bytes.Repeat([]byte("x"), 256<<10)
				}
				go func() { done <- writeTrackerBytesContext(ctx, writer, payload) }()
				if cancelWrite {
					if err = reader.SetReadDeadline(time.Now().Add(time.Second)); err != nil {
						t.Fatal(err)
					}
					one := make([]byte, 1)
					if _, err = reader.Read(one); err != nil {
						t.Fatal(err)
					}
					cancel()
				} else {
					observed := make([]byte, len(payload))
					if _, err = io.ReadFull(reader, observed); err != nil || !bytes.Equal(payload, observed) {
						t.Fatal("successful output lost")
					}
				}
				select {
				case err = <-done:
				case <-time.After(time.Second):
					cancel()
					reader.Close()
					writer.Close()
					t.Fatal("output did not finish")
				}
				if cancelWrite && !errors.Is(err, context.Canceled) || !cancelWrite && err != nil {
					t.Fatalf("output outcome: %v", err)
				}
				cancel()
				if requestFileFlags(t, writer) != before {
					t.Fatal("output flags were not restored")
				}
				drained := make(chan []byte, 1)
				go func() { data, _ := io.ReadAll(reader); drained <- data }()
				if _, err = writer.Write([]byte("still usable")); err != nil {
					t.Fatal(err)
				}
				writer.Close()
				select {
				case data := <-drained:
					if !bytes.HasSuffix(data, []byte("still usable")) {
						t.Fatal("original output descriptor unusable")
					}
				case <-time.After(time.Second):
					reader.Close()
					t.Fatal("output drain did not finish")
				}
				reader.Close()
				if got := countRequestDescriptors(t); got != baseline {
					t.Fatalf("output descriptor leak: before=%d after=%d", baseline, got)
				}
			}
		}
	}
}

type trackerCompletedWrite struct{ cancel context.CancelFunc }

func (w trackerCompletedWrite) Write(data []byte) (int, error) { w.cancel(); return len(data), nil }
func TestTrackerCompletedOutputKeepsDeliveredOutcome(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	value := trackerEnvelope{Schema: trackerResultSchema, Action: "create", Outcome: OutcomeSuccess, Result: map[string]any{"work_id": "delivered"}}
	if code := renderTrackerContext(ctx, trackerCompletedWrite{cancel}, io.Discard, "json", value); code != ExitSuccess {
		t.Fatalf("complete successful output contradicted by exit %d", code)
	}
}
