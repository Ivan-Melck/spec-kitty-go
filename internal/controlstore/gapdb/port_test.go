package gapdb

import (
	"context"
	"errors"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	gapdbapi "github.com/spec-kitty/gapdb/gapdb"
)

type fakeProvider struct {
	getFn      func(context.Context, string) (gapdbapi.Record, error)
	readManyFn func(context.Context, []string) (gapdbapi.ReadManyResult, error)
	putFn      func(context.Context, string, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error)
	absentFn   func(context.Context, string, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error)
	casFn      func(context.Context, string, gapdbapi.Revision, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error)
	deleteFn   func(context.Context, string, gapdbapi.Revision, gapdbapi.AckMode) (gapdbapi.MutationResult, error)
	batchFn    func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error)
	scanFn     func(context.Context, string, int, string) (gapdbapi.ScanPage, error)
	watchFn    func(context.Context, string, gapdbapi.Revision) (providerWatch, error)
	statusFn   func(context.Context) (gapdbapi.StatusResult, error)
	healthFn   func(context.Context) (gapdbapi.HealthResult, error)
	configFn   func(context.Context) (gapdbapi.ConfigResult, error)
}

var _ provider = (*fakeProvider)(nil)

func (f *fakeProvider) Get(ctx context.Context, key string) (gapdbapi.Record, error) {
	if f.getFn != nil {
		return f.getFn(ctx, key)
	}
	return gapdbapi.Record{}, errors.New("unexpected Get")
}
func (f *fakeProvider) ReadMany(ctx context.Context, keys []string) (gapdbapi.ReadManyResult, error) {
	if f.readManyFn != nil {
		return f.readManyFn(ctx, keys)
	}
	if f.getFn == nil || f.statusFn == nil {
		return gapdbapi.ReadManyResult{}, errors.New("unexpected ReadMany")
	}
	status, err := f.statusFn(ctx)
	if err != nil {
		return gapdbapi.ReadManyResult{}, err
	}
	result := gapdbapi.ReadManyResult{ObservedRevision: status.CurrentRevision, AsOf: time.Now().UTC(), Entries: make([]gapdbapi.ReadManyEntry, len(keys))}
	for index, key := range keys {
		record, readErr := f.getFn(ctx, key)
		if readErr != nil {
			var providerErr *gapdbapi.Error
			if errors.As(readErr, &providerErr) && providerErr.Code == gapdbapi.CodeNotFound {
				result.Entries[index] = gapdbapi.ReadManyEntry{Key: key}
				continue
			}
			return gapdbapi.ReadManyResult{}, readErr
		}
		result.Entries[index] = gapdbapi.ReadManyEntry{Key: key, Found: true, Record: &record}
	}
	return result, nil
}
func (f *fakeProvider) Put(ctx context.Context, key string, value []byte, expiry *time.Time, ack gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
	if f.putFn != nil {
		return f.putFn(ctx, key, value, expiry, ack)
	}
	return gapdbapi.MutationResult{}, errors.New("unexpected Put")
}
func (f *fakeProvider) PutIfAbsent(ctx context.Context, key string, value []byte, expiry *time.Time, ack gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
	if f.absentFn != nil {
		return f.absentFn(ctx, key, value, expiry, ack)
	}
	return gapdbapi.MutationResult{}, errors.New("unexpected PutIfAbsent")
}
func (f *fakeProvider) CompareAndSwap(ctx context.Context, key string, revision gapdbapi.Revision, value []byte, expiry *time.Time, ack gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
	if f.casFn != nil {
		return f.casFn(ctx, key, revision, value, expiry, ack)
	}
	return gapdbapi.MutationResult{}, errors.New("unexpected CompareAndSwap")
}
func (f *fakeProvider) DeleteIfRevision(ctx context.Context, key string, revision gapdbapi.Revision, ack gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
	if f.deleteFn != nil {
		return f.deleteFn(ctx, key, revision, ack)
	}
	return gapdbapi.MutationResult{}, errors.New("unexpected DeleteIfRevision")
}
func (f *fakeProvider) AtomicBatch(ctx context.Context, batch gapdbapi.Batch) (gapdbapi.MutationResult, error) {
	if f.batchFn != nil {
		return f.batchFn(ctx, batch)
	}
	return gapdbapi.MutationResult{}, errors.New("unexpected AtomicBatch")
}
func (f *fakeProvider) ScanPrefix(ctx context.Context, prefix string, limit int, cursor string) (gapdbapi.ScanPage, error) {
	if f.scanFn != nil {
		return f.scanFn(ctx, prefix, limit, cursor)
	}
	return gapdbapi.ScanPage{}, errors.New("unexpected ScanPrefix")
}
func (f *fakeProvider) Watch(ctx context.Context, prefix string, revision gapdbapi.Revision) (providerWatch, error) {
	if f.watchFn != nil {
		return f.watchFn(ctx, prefix, revision)
	}
	return nil, errors.New("unexpected Watch")
}
func (f *fakeProvider) Status(ctx context.Context) (gapdbapi.StatusResult, error) {
	if f.statusFn != nil {
		return f.statusFn(ctx)
	}
	return gapdbapi.StatusResult{}, errors.New("unexpected Status")
}
func (f *fakeProvider) Health(ctx context.Context) (gapdbapi.HealthResult, error) {
	if f.healthFn != nil {
		return f.healthFn(ctx)
	}
	return gapdbapi.HealthResult{}, errors.New("unexpected Health")
}
func (f *fakeProvider) DescribeConfig(ctx context.Context) (gapdbapi.ConfigResult, error) {
	if f.configFn != nil {
		return f.configFn(ctx)
	}
	return gapdbapi.ConfigResult{}, errors.New("unexpected DescribeConfig")
}

func TestPortBindsOnlyAdmittedPublicClient(t *testing.T) {
	var client publicClient = (*gapdbapi.Client)(nil)
	_ = client // compile-time binding to the admitted public method set
	if AdmittedModulePath != "github.com/spec-kitty/gapdb" || AdmittedModuleVersion != "v0.0.0-20260824033134-ff3dd478f87c" {
		t.Fatal("admitted public tuple drifted")
	}
}

func TestPortCapabilityAbsenceIsTyped(t *testing.T) {
	_, err := New(nil, "/explicit/socket")
	if !IsKind(err, OutcomeCapabilityAbsent) {
		t.Fatalf("nil public client error=%v", err)
	}
}

func TestPortContextCancellationRemainsDistinct(t *testing.T) {
	provider := &fakeProvider{getFn: func(ctx context.Context, _ string) (gapdbapi.Record, error) {
		<-ctx.Done()
		return gapdbapi.Record{}, ctx.Err()
	}}
	adapter, err := newAdapter(provider, "/sensitive/socket", time.Second)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err = adapter.Get(ctx, "record")
	if !IsKind(err, OutcomeCanceled) || !errors.Is(err, context.Canceled) {
		t.Fatalf("cancellation error=%v", err)
	}
}

func TestPortDeadlineIsBoundedAndDistinct(t *testing.T) {
	provider := &fakeProvider{getFn: func(ctx context.Context, _ string) (gapdbapi.Record, error) {
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 100*time.Millisecond {
			t.Fatalf("adapter failed to impose bound: %v %v", deadline, ok)
		}
		<-ctx.Done()
		return gapdbapi.Record{}, ctx.Err()
	}}
	adapter, err := newAdapter(provider, "/explicit/socket", 20*time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	_, err = adapter.Get(context.Background(), "record")
	if !IsKind(err, OutcomeDeadlineExceeded) || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("deadline error=%v", err)
	}
}

func TestPortMalformedRecordIsNotAvailability(t *testing.T) {
	provider := &fakeProvider{getFn: func(context.Context, string) (gapdbapi.Record, error) {
		return gapdbapi.NewRecord("dishonest-key", []byte("value"), 9, nil), nil
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
	_, err := adapter.Get(context.Background(), "expected-key")
	if !IsKind(err, OutcomeMalformedResponse) || IsKind(err, OutcomeUnavailable) {
		t.Fatalf("malformed record error=%v", err)
	}
}

func TestPortTypedProviderErrors(t *testing.T) {
	cases := []struct {
		name string
		err  error
		kind OutcomeKind
	}{
		{"not-found", &gapdbapi.Error{Code: gapdbapi.CodeNotFound}, OutcomeNotFound},
		{"conflict", &gapdbapi.Error{Code: gapdbapi.CodeRevisionMismatch}, OutcomeConflict},
		{"skew", &gapdbapi.Error{Code: gapdbapi.CodeUnsupportedVersion}, OutcomeUnsupportedVersion},
		{"unavailable", &gapdbapi.Error{Code: gapdbapi.CodeServerUnavailable}, OutcomeUnavailable},
		{"before-send", &gapdbapi.TransportError{Operation: "get", Ambiguous: false, Cause: errors.New("dial")}, OutcomeUnavailable},
		{"read-response-loss", &gapdbapi.TransportError{Operation: "get", Ambiguous: true, Cause: errors.New("eof")}, OutcomeUnavailable},
	}
	for _, test := range cases {
		t.Run(test.name, func(t *testing.T) {
			provider := &fakeProvider{getFn: func(context.Context, string) (gapdbapi.Record, error) { return gapdbapi.Record{}, test.err }}
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			_, err := adapter.Get(context.Background(), "key")
			if !IsKind(err, test.kind) {
				t.Fatalf("error=%v want kind=%s", err, test.kind)
			}
		})
	}
}

func TestPortDiagnosticIsBoundedAndRedacted(t *testing.T) {
	secret := "credential=top-secret"
	path := "/home/user/private/socket.sock"
	provider := &fakeProvider{putFn: func(context.Context, string, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
		return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "put", SocketPath: path, Ambiguous: true, Cause: errors.New(secret + strings.Repeat("x", MaxDiagnosticBytes*2))}
	}}
	adapter, _ := newAdapter(provider, path, time.Second)
	operationID := "operation-123"
	_, err := adapter.ConditionalMutate(context.Background(), operationID, put("key", []byte("value"), ConditionAny, 0), AckDurable)
	text := err.Error()
	if len(text) > MaxDiagnosticBytes || strings.Contains(text, secret) || strings.Contains(text, path) {
		t.Fatalf("unsafe diagnostic (%d bytes): %s", len(text), text)
	}
	var typed *Error
	if !errors.As(err, &typed) || typed.OperationID != operationID || typed.Recovery != RecoveryReconcile {
		t.Fatalf("ambiguous recovery diagnostic=%+v", typed)
	}
}

func TestMutatingTransportFailuresNeverRetryWithANewOperation(t *testing.T) {
	routes := []struct {
		name     string
		mutation Mutation
	}{
		{"put", put("key", nil, ConditionAny, 0)},
		{"put-if-absent", put("key", nil, ConditionAbsent, 0)},
		{"compare-and-swap", put("key", nil, ConditionRevision, 7)},
		{"delete", Mutation{Kind: MutationDelete, Key: "key", Condition: Condition{Kind: ConditionRevision, ExpectedRevision: 7}}},
	}
	for _, route := range routes {
		for _, ambiguous := range []bool{false, true} {
			label := "before-send"
			recovery := RecoveryRetrySameOperation
			kind := OutcomeUnavailable
			if ambiguous {
				label = "after-send"
				recovery = RecoveryReconcile
				kind = OutcomeAmbiguous
			}
			t.Run(route.name+"/"+label, func(t *testing.T) {
				operationID := "original-operation"
				var calls atomic.Int32
				provider := mutationErrorProvider(&calls, &gapdbapi.TransportError{Operation: route.name, Ambiguous: ambiguous, Cause: errors.New(label)})
				adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
				_, err := adapter.ConditionalMutate(context.Background(), operationID, route.mutation, AckDurable)
				var typed *Error
				if !IsKind(err, kind) || !errors.As(err, &typed) || typed.OperationID != operationID || typed.Recovery != recovery || calls.Load() != 1 {
					t.Fatalf("error=%v typed=%+v calls=%d", err, typed, calls.Load())
				}
			})
		}
	}

	for _, ambiguous := range []bool{false, true} {
		t.Run("atomic-batch/"+map[bool]string{false: "before-send", true: "after-send"}[ambiguous], func(t *testing.T) {
			var calls atomic.Int32
			provider := &fakeProvider{batchFn: func(context.Context, gapdbapi.Batch) (gapdbapi.MutationResult, error) {
				calls.Add(1)
				return gapdbapi.MutationResult{}, &gapdbapi.TransportError{Operation: "atomic_batch", Ambiguous: ambiguous, Cause: errors.New("transport")}
			}}
			adapter, _ := newAdapter(provider, "/explicit/socket", time.Second)
			_, err := adapter.AtomicBatch(context.Background(), minimalBatch())
			var typed *Error
			wantKind, wantRecovery := OutcomeUnavailable, RecoveryRetrySameOperation
			if ambiguous {
				wantKind, wantRecovery = OutcomeAmbiguous, RecoveryReconcile
			}
			if !IsKind(err, wantKind) || !errors.As(err, &typed) || typed.OperationID != "operation" || typed.Recovery != wantRecovery || calls.Load() != 1 {
				t.Fatalf("error=%v typed=%+v calls=%d", err, typed, calls.Load())
			}
		})
	}
}

func TestMutatingDeadlineIsBoundedAndReconciledWithOriginalOperation(t *testing.T) {
	var calls atomic.Int32
	provider := &fakeProvider{putFn: func(ctx context.Context, _ string, _ []byte, _ *time.Time, _ gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
		calls.Add(1)
		<-ctx.Done()
		return gapdbapi.MutationResult{}, ctx.Err()
	}}
	adapter, _ := newAdapter(provider, "/explicit/socket", 10*time.Millisecond)
	_, err := adapter.ConditionalMutate(context.Background(), "deadline-operation", put("key", nil, ConditionAny, 0), AckDurable)
	var typed *Error
	if !IsKind(err, OutcomeDeadlineExceeded) || !errors.As(err, &typed) || typed.OperationID != "deadline-operation" || typed.Recovery != RecoveryReconcile || calls.Load() != 1 {
		t.Fatalf("error=%v typed=%+v calls=%d", err, typed, calls.Load())
	}
}

func mutationErrorProvider(calls *atomic.Int32, failure error) *fakeProvider {
	respond := func() (gapdbapi.MutationResult, error) {
		calls.Add(1)
		return gapdbapi.MutationResult{}, failure
	}
	return &fakeProvider{
		putFn: func(context.Context, string, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
			return respond()
		},
		absentFn: func(context.Context, string, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
			return respond()
		},
		casFn: func(context.Context, string, gapdbapi.Revision, []byte, *time.Time, gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
			return respond()
		},
		deleteFn: func(context.Context, string, gapdbapi.Revision, gapdbapi.AckMode) (gapdbapi.MutationResult, error) {
			return respond()
		},
	}
}

func put(key string, value []byte, condition ConditionKind, revision uint64) Mutation {
	return Mutation{Kind: MutationPut, Key: key, Value: value, Condition: Condition{Kind: condition, ExpectedRevision: revision}}
}
