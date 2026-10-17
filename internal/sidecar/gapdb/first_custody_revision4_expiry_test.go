//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

// AUTHORED UNRUN. Returned-error components close their real owned object once;
// none is a failed physical kernel Close, authentic provider or Host witness.
import (
	"bytes"
	"context"
	"errors"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"testing"

	"golang.org/x/sys/unix"
)

func TestRevision4CancellationDuringLifecycleAssociation(t *testing.T) {
	for _, associatedFirst := range []bool{false, true} {
		t.Run(map[bool]string{false:"callback-loaded-nil", true:"callback-loaded-owner"}[associatedFirst], func(t *testing.T) {
			parent, cancelParent := context.WithCancel(t.Context())
			defer cancelParent()
			gateCtx, cancelGate := context.WithCancel(context.Background())
			defer cancelGate()
			entered, release := make(chan struct{}), make(chan struct{})
			r := new(expiryCleanupReport)
			g := &expiryGateState{ctx:gateCtx, report:r}
			// Call-only checkpoint at the existing cancel invocation: callback
			// has already performed its ONE association Load and invalid Store.
			g.cancel = func() { close(entered); <-release; cancelGate() }
			v := &VerifiedExecutable{expiry:verifiedExpiryState{gate:g, report:r}}
			l := &Lifecycle{pidfd:-1}
			if associatedFirst { l.initExpiry(v) }
			g.watch = g.watchContext(parent)
			cancelParent()
			<-entered
			if !associatedFirst { l.initExpiry(v) } // association while callback is active
			if g.lifecycle.Load() != l || !g.invalid.Load() || !l.ownedRevoked.Load() || l.expiry.work == nil || !r.lifecycleBound.Load() { close(release); t.Fatal("association/cutoff not safely published") }
			l.mu.Lock()
			work, err := l.beginExpiryWork() // MUST refuse before held mutex
			l.mu.Unlock()
			if err == nil || work != nil || r.lifecycleRetired.Load() { close(release); t.Fatal("cutoff became admission/retirement") }
			select { case <-gateCtx.Done(): close(release); t.Fatal("checkpoint did not precede notification"); default: }
			close(release)
			<-g.watch.done
			if !l.ownedRevoked.Load() || r.lifecycleRetired.Load() { t.Fatal("callback fabricated lifecycle retirement") }
		})
	}
}

func TestRevision4AtomicAssociationSourceContract(t *testing.T) {
	file, err := parser.ParseFile(token.NewFileSet(), "expiry_qualification_launch.go", nil, 0)
	if err != nil { t.Fatal(err) }
	found := false
	ast.Inspect(file, func(n ast.Node) bool {
		s, ok := n.(*ast.TypeSpec); if !ok || s.Name.Name != "expiryGateState" { return true }
		st, ok := s.Type.(*ast.StructType); if !ok { t.Fatal("gate not a struct") }
		for _, field := range st.Fields.List {
			if len(field.Names) != 1 || field.Names[0].Name != "lifecycle" { continue }
			index, ok := field.Type.(*ast.IndexExpr)
			if !ok || strictEOFExpr(index.X) != "atomic.Pointer" || strictEOFExpr(index.Index) != "Lifecycle" { t.Fatal("callback association is not private atomic pointer") }
			found = true
		}
		return false
	})
	if !found { t.Fatal("atomic association absent") }
	init := strictEOFFunction(t, "expiry_qualification_launch.go", "initExpiry")
	store := strictEOFCall(t, init, "v.expiry.gate.lifecycle.Store", "l")
	initialized := false
	for _, stmt := range init.Body.List {
		if a, ok := stmt.(*ast.AssignStmt); ok && len(a.Lhs) == 1 && strictEOFExpr(a.Lhs[0]) == "l.expiry" && a.Pos() < store.Pos() { initialized = true }
	}
	if !initialized { t.Fatal("association precedes lifecycle initialization") }
	strictEOFCall(t, init, "v.expiry.gate.invalid.Load")
	revoke := strictEOFFunction(t, "expiry_qualification_launch.go", "revoke")
	invalid := strictEOFCall(t, revoke, "g.invalid.Store", "true")
	load := strictEOFCall(t, revoke, "g.lifecycle.Load")
	cutoff := strictEOFCall(t, revoke, "l.ownedRevoked.Store", "true")
	notify := strictEOFCall(t, revoke, "g.cancel")
	if !(invalid.Pos() < load.Pos() && load.Pos() < cutoff.Pos() && cutoff.Pos() < notify.Pos()) { t.Fatal("association handshake/cutoff order differs") }
	for _, forbidden := range []string{"g.operationMu.Lock", "l.mu.Lock", "l.lockCleanup"} {
		if len(strictEOFCalls(revoke, forbidden)) != 0 { t.Fatal("revocation added callback lock", forbidden) }
	}
	for _, name := range []string{"expiryStarted", "checkOriginalLifecycle", "CrashExpiryQualification"} {
		fn := strictEOFFunction(t, "expiry_qualification_launch.go", name)
		loads := len(strictEOFCalls(fn, "g.lifecycle.Load"))+len(strictEOFCalls(fn, "l.expiry.gate.lifecycle.Load"))
		if loads != 1 { t.Fatal("association reader must Load once", name, loads) }
	}
}

func TestRevision4ArtifactActualOwnedCloseConjunctions(t *testing.T) {
	for _, badDigest := range []bool{false, true} {
		path := filepath.Join(t.TempDir(), "artifact")
		raw := []byte("real owned artifact\n")
		if err := os.WriteFile(path, raw, 0600); err != nil { t.Fatal(err) }
		digest := expiryRawSHA256(raw)
		if badDigest { digest = expiryRawSHA256([]byte("different")) }
		r := new(expiryCleanupReport)
		calls := 0
		var actualClose error
		encoded, err := readExpiryArtifactWithClose(path, uint64(len(raw)), digest, r, func(file *os.File) error {
			calls++
			actualClose = file.Close() // real owner; no raw FD manipulation/reclose
			return withStartupCustody(actualClose, unix.EIO)
		})
		if calls != 1 || actualClose != nil || encoded != nil || !errors.Is(err, unix.EIO) || !errors.Is(r.firstError(), unix.EIO) { t.Fatal("artifact bytes/cause escaped final close failure", err, calls) }
		if badDigest && err.Error() != "qualification artifact bytes differ" { t.Fatal("artifact primary text changed", err) }
		if !badDigest && err != unix.EIO { t.Fatal("close-only result not actual returned cause", err) }
	}
}

func TestRevision4VerifierActualArtifactClosePreservesPrimary(t *testing.T) {
	dir := t.TempDir()
	input := filepath.Join(dir, "input")
	image := filepath.Join(dir, "image")
	// Real input length matches; authenticated digest deliberately does not.
	raw := bytes.Repeat([]byte{'x'}, len(expiryInputCanonical)+1)
	if err := os.WriteFile(input, raw, 0600); err != nil { t.Fatal(err) }
	if err := os.WriteFile(image, []byte("never executable-opened"), 0600); err != nil { t.Fatal(err) }
	config := VerifyConfig{ExecutablePath:image, AdmissionRecordPath:input}
	_, primary := verifyExpiryQualificationExecutableWithReport(config, new(expiryCleanupReport))
	if !IsFailure(primary, FailureAdmissionMismatch) { t.Fatal("actual artifact primary not reached", primary) }
	r := new(expiryCleanupReport)
	calls := 0
	var actualClose error
	v, got := verifyExpiryQualificationExecutableWithClose(config, r, func(file *os.File) error {
		calls++
		actualClose = file.Close()
		return withStartupCustody(actualClose, unix.EIO)
	})
	var typed *Error
	if v != nil || calls != 1 || actualClose != nil || !errors.As(got, &typed) || typed.Reason != "qualification input cannot be authenticated" || got.Error() != primary.Error() || !errors.Is(got, unix.EIO) || !errors.Is(r.firstError(), unix.EIO) { t.Fatal("actual verifier/artifact/report primary conjunction lost", got, calls) }
	if r.counts[expirySource].required.Load() != 0 || r.counts[expiryImage].required.Load() != 0 { t.Fatal("invalid input reached executable verifier") }
	// Also retain a real already-returned source-close cause at the actual
	// verifier report boundary without inventing a source/memfd fault witness.
	owned, err := os.Open(image)
	if err != nil { t.Fatal(err) }
	r2 := new(expiryCleanupReport)
	r2.sourceOpened()
	r2.sourceClosed(withStartupCustody(owned.Close(), unix.ENOSPC))
	v, got = verifyExpiryQualificationExecutableWithReport(config, r2)
	if v != nil || got.Error() != primary.Error() || !errors.Is(got, unix.ENOSPC) { t.Fatal("verifier report cause changed primary", got) }
}

func TestRevision4ExpiryInputActualCallChainSource(t *testing.T) {
	wrapper := strictEOFFunction(t, "expiry_qualification_input.go", "verifyExpiryQualificationExecutableWithReport")
	if len(wrapper.Body.List) != 1 { t.Fatal("verifier wrapper is not fixed") }
	strictEOFCall(t, wrapper, "verifyExpiryQualificationExecutableWithClose", "config", "report", "*os.File.Close")
	read := strictEOFFunction(t, "expiry_qualification_input.go", "readExpiryArtifact")
	if len(read.Body.List) != 1 { t.Fatal("artifact wrapper is not fixed") }
	strictEOFCall(t, read, "readExpiryArtifactWithClose", "filename", "expectedBytes", "expectedSHA", "report", "*os.File.Close")
	verify := strictEOFFunction(t, "expiry_qualification_input.go", "verifyExpiryQualificationExecutableWithClose")
	if len(strictEOFCalls(verify, "errors.Join")) != 0 || len(strictEOFCalls(verify, "withStartupCustody")) != 1 { t.Fatal("actual verifier boundary not primary preserving") }
	var previous token.Pos
	for _, name := range []string{"validateAbsolutePath", "validatePathChain", "readExpiryArtifactWithClose", "decodeExpiryInput", "authenticateExpiryArtifactsWithClose", "debug.ReadBuildInfo", "verifyExpiryClient", "verifyExecutableImage"} {
		calls := strictEOFCalls(verify, name)
		if len(calls) != 1 || calls[0].Pos() <= previous { t.Fatal("authentic verification order changed", name) }
		previous = calls[0].Pos()
	}
	auth := strictEOFFunction(t, "expiry_qualification_input.go", "authenticateExpiryArtifactsWithClose")
	if len(strictEOFCalls(auth, "readExpiryArtifactWithClose")) != 1 { t.Fatal("detached ref boundary bypassed") }
	artifact := strictEOFFunction(t, "expiry_qualification_input.go", "readExpiryArtifactWithClose")
	if artifact.Type.Results == nil || len(artifact.Type.Results.List) != 2 || len(artifact.Type.Results.List[0].Names) != 1 || artifact.Type.Results.List[0].Names[0].Name != "encoded" { t.Fatal("artifact bytes are not the named final result") }
	strictEOFCall(t, artifact, "closeCall", "file")
	strictEOFCall(t, artifact, "report.metadataClosed", "err")
	strictEOFCall(t, artifact, "withStartupCustody", "resultErr", "err")
	cleared := false
	ast.Inspect(artifact.Body, func(n ast.Node) bool {
		i, ok := n.(*ast.IfStmt); if !ok { return true }
		b, ok := i.Cond.(*ast.BinaryExpr)
		if !ok || b.Op != token.NEQ || strictEOFExpr(b.X) != "resultErr" || strictEOFExpr(b.Y) != "nil" { return true }
		for _, stmt := range i.Body.List {
			if a, ok := stmt.(*ast.AssignStmt); ok && len(a.Lhs) == 1 && len(a.Rhs) == 1 && strictEOFExpr(a.Lhs[0]) == "encoded" && strictEOFExpr(a.Rhs[0]) == "nil" { cleared = true }
		}
		return true
	})
	if !cleared { t.Fatal("failed final artifact result not cleared") }
}
