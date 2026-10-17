package productionruntime

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"
)

// Provider-free MODELs, NOT authentic production Hosts, startup, or workloads.
// Only the existing RestoreV2 failure and Host.Close bodies execute here; the
// private error construction is a model of the source-checked failure branch.
type errorCustodyModelCloser struct{ err error }

func (c errorCustodyModelCloser) Close() error { return c.err }

type errorCustodyModelReader struct {
	journalrestore.ReaderV2 // deliberately unusable: Capture must fail first
	err                     error
}

func (r errorCustodyModelReader) CaptureRevisionV2(context.Context) (journalv2.StoreRevisionV2, error) {
	return journalv2.StoreRevisionV2{}, r.err
}

func errorCustodyModelRestore(t *testing.T) error {
	t.Helper()
	original := &journalv2.Error{Kind: journalv2.ErrorNotFound, Field: "MODEL private reader diagnostic"}
	_, got := journalrestore.RestoreV2(context.Background(), journalrestore.InputV2{
		Reader: errorCustodyModelReader{err: original}, HostAuthorityID: "model-authority", RecoveryAttemptID: "model-attempt",
	})
	if got != original {
		t.Fatalf("RestoreV2 did not return the original reader error: %v", got)
	}
	return got
}

func errorCustodyAssertTree(t *testing.T, err error) {
	t.Helper()
	if err == nil {
		t.Fatal("nil error child/leaf")
	}
	switch e := err.(type) {
	case interface{ Unwrap() []error }:
		children := e.Unwrap()
		if len(children) == 0 {
			t.Fatal("empty multi-unwrap")
		}
		for _, child := range children {
			errorCustodyAssertTree(t, child)
		}
	case interface{ Unwrap() error }:
		errorCustodyAssertTree(t, e.Unwrap())
	}
}

func TestHostErrorCustodyOrdinaryTraversalModel(t *testing.T) {
	for _, failingClose := range []bool{false, true} {
		t.Run(fmt.Sprint(failingClose), func(t *testing.T) {
			var ownerCause error
			if failingClose {
				ownerCause = errors.New("MODEL private owner diagnostic")
			}
			// No lifecycle, adapter, process, monitor, owner claim, or authority.
			o := &productionOwner{closer: errorCustodyModelCloser{ownerCause}}
			h := &Host{owner: o, state: HostReconciling}
			restoreErr := errorCustodyModelRestore(t)
			firstClose := h.Close(context.Background())
			if (firstClose != nil) != failingClose || h.State() != HostClosed || !o.closeOnce {
				t.Fatal("actual Close body did not finish with expected outcome", firstClose)
			}
			failure := &reconciliationRestoreFailure{
				primary:    Error{Kind: ErrorStartup, State: HostDegraded, Field: "reconciliation_restore"},
				restoreErr: restoreErr, closeErr: firstClose,
			}
			const public = "production runtime: kind=STARTUP_REFUSED state=degraded field=reconciliation_restore"
			if failure.Error() != public || failure.Error() != failure.primary.Error() {
				t.Fatal("public diagnostic changed or leaked a child", failure)
			}
			var primary *Error
			if !errors.As(failure, &primary) || primary != &failure.primary || !IsKind(failure, ErrorStartup) {
				t.Fatal("runtime primary classification lost precedence")
			}
			if IsKind(failure, ErrorShutdown) || !errors.Is(failure, restoreErr) {
				t.Fatal("ordinary startup failure traversal lost primary/cause")
			}
			var reader *journalv2.Error
			if !errors.As(failure, &reader) || reader != restoreErr || journalv2.IsKind(failure, journalv2.ErrorNotFound) {
				t.Fatal("standard traversal must work; journalv2.IsKind must NOT be claimed to multi-unwrap")
			}
			children := failure.Unwrap()
			want := 2
			if failingClose {
				want++
				if children[2] != firstClose || !errors.Is(failure, firstClose) {
					t.Fatal("actual first Host.Close error identity lost")
				}
				// Existing Host.Close deliberately classifies rather than unwraps
				// owner errors. This patch must not widen that separate contract.
				if errors.Is(failure, ownerCause) {
					t.Fatal("changed ordinary Host.Close owner-error traversal")
				}
			}
			if len(children) != want || children[0] != &failure.primary || children[1] != restoreErr {
				t.Fatal("wrong fixed child order/count")
			}
			errorCustodyAssertTree(t, failure)
			if h.Close(context.Background()) != nil || !errors.Is(failure, restoreErr) || failingClose && !errors.Is(failure, firstClose) {
				t.Fatal("repeat nil repaired the retained first failure")
			}
		})
	}
}

func TestHostErrorCustodyWrapperOmitsNilChildrenModel(t *testing.T) {
	// Even an uninitialized private model never returns a nil child.
	failure := &reconciliationRestoreFailure{}
	if children := failure.Unwrap(); len(children) != 1 || children[0] != &failure.primary {
		t.Fatal("nil optional child exposed")
	}
	errorCustodyAssertTree(t, failure)
}

func TestHostErrorCustodyStartupSourceDelta(t *testing.T) {
	// Local pre-edit regression check ONLY, not a native/source-compat receipt.
	// Replacing exactly the approved failure block must restore the original
	// whole suffix: success/handoff, all other failures and hooks stay unchanged.
	raw, err := os.ReadFile("composition.go")
	if err != nil {
		t.Fatal(err)
	}
	source := string(raw)
	oldBranch := `		if restoreErr != nil {
			host.degrade("reconciliation_restore")
			_ = host.Close(context.Background())
			return nil, &Error{Kind: ErrorStartup, State: HostDegraded, Field: "reconciliation_restore"}
		}`
	newBranch := `		if restoreErr != nil {
			host.degrade("reconciliation_restore")
			closeErr := host.Close(context.Background())
			return nil, &reconciliationRestoreFailure{
				primary:    Error{Kind: ErrorStartup, State: HostDegraded, Field: "reconciliation_restore"},
				restoreErr: restoreErr, closeErr: closeErr,
			}
		}`
	start, end := strings.Index(source, "func completeHostStartup("), strings.Index(source, "func productionRecoveryBudgetsV2(")
	if start < 0 || end <= start {
		t.Fatal("startup suffix missing")
	}
	suffix := source[start:end]
	if strings.Count(suffix, newBranch) != 1 {
		t.Fatal("failure must capture the existing single Close result, in place, with original restoreErr")
	}
	restored := strings.Replace(suffix, newBranch, oldBranch, 1)
	if fmt.Sprintf("%x", sha256.Sum256([]byte(restored))) != "dba2756f411b6f5ad7447ddde412441d8efbe8c72cd13e313f324562d46bf7bd" {
		t.Fatal("startup changed beyond the approved reconciliation restore failure block")
	}
	start, end = strings.Index(source, "func start("), strings.Index(source, "// preflightHostRoot")
	if start < 0 || end <= start || fmt.Sprintf("%x", sha256.Sum256([]byte(source[start:end]))) != "edc2bc281eae9cba867dc37803fa48d585e8d26d421c4f706e27655351f8b01b" {
		t.Fatal("ordinary admission/root changed")
	}
}
