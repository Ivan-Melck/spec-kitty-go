//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

// CODE ONLY / UNRUN. Strict helper-aware AST contract, never a legacy OR
// fallback. Assertions and mutants read the actual selected production bytes
// through the explicit test-only source binding. No Native/placement credit.
import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"reflect"
	"strings"
	"testing"
)

// Compare AST shape, operators, identifiers, literal values, field/list order,
// signatures, receivers, full bodies and every call. Ignore only source offsets
// and comments. SkipObjectResolution prevents parser symbol backreferences.
// Whole-function equality also forbids ANY inserted RPC/hash/factory/other call,
// rather than relying on a finite blacklist or presence of helper-name tokens.
func expiryCompatASTEqual(a, b reflect.Value) bool {
	if !a.IsValid() || !b.IsValid() { return a.IsValid() == b.IsValid() }
	if a.Type() != b.Type() { return false }
	// Preserve absent/present position sentinels (notably CallExpr.Ellipsis).
	if a.Type() == reflect.TypeOf(token.Pos(0)) { return (a.Int() == 0) == (b.Int() == 0) }
	if a.Type() == reflect.TypeOf((*ast.CommentGroup)(nil)) { return true }
	switch a.Kind() {
	case reflect.Interface, reflect.Pointer:
		if a.IsNil() || b.IsNil() { return a.IsNil() == b.IsNil() }
		return expiryCompatASTEqual(a.Elem(), b.Elem())
	case reflect.Struct:
		for i := 0; i < a.NumField(); i++ { if !expiryCompatASTEqual(a.Field(i), b.Field(i)) { return false } }
		return true
	case reflect.Slice, reflect.Array:
		if a.Len() != b.Len() { return false }
		for i := 0; i < a.Len(); i++ { if !expiryCompatASTEqual(a.Index(i), b.Index(i)) { return false } }
		return true
	default:
		return reflect.DeepEqual(a.Interface(), b.Interface())
	}
}

func expiryCompatParse(source string) (*ast.File, error) {
	return parser.ParseFile(token.NewFileSet(), "expiry_qualification.go", source, parser.SkipObjectResolution)
}

func expiryCompatFunction(file *ast.File, name string) (*ast.FuncDecl, error) {
	var result *ast.FuncDecl
	for _, declaration := range file.Decls {
		if fn, ok := declaration.(*ast.FuncDecl); ok && fn.Name.Name == name {
			if result != nil { return nil, fmt.Errorf("duplicate function %s", name) }
			result = fn
		}
	}
	if result == nil { return nil, fmt.Errorf("missing function %s", name) }
	return result, nil
}

func expiryCompatRequire(file *ast.File, name, expected string) error {
	wantFile, err := expiryCompatParse("package productionruntime\n" + expected)
	if err != nil { return fmt.Errorf("invalid literal contract %s: %w", name, err) }
	want, err := expiryCompatFunction(wantFile, name)
	if err != nil { return err }
	got, err := expiryCompatFunction(file, name)
	if err != nil { return err }
	if !expiryCompatASTEqual(reflect.ValueOf(got), reflect.ValueOf(want)) {
		return fmt.Errorf("strict AST contract changed: %s", name)
	}
	return nil
}

// Full combined startGeneration with exactly four permitted extractions: this
// binds the single helper call to the SAME !initial block, AFTER the predecessor
// nil/composition/owner refusal, predecessor Provenance and readCurrentEpoch,
// BEFORE checkPathsForControl/cleanup/quiescence/handoff. It also preserves the
// original expiry_restart_freshness classification and its exact return shape.
func expiryCompatFreshness(source string) error {
	file, err := expiryCompatParse(source)
	if err != nil { return err }
	if err := expiryCompatRequire(file, "startGeneration", expiryCompatStartGeneration); err != nil { return err }
	return expiryCompatFourHelpers(file)
}

// Full original restart with exactly the b4 union extraction. The single
// actual r.host.Close(cleanup) follows optional original owned crash; the helper
// immediately follows that Close and precedes residual paths/context/successor.
// The helper's sole ReturnStmt is the original four-argument errors.Join.
func expiryCompatCleanup(source string) error {
	file, err := expiryCompatParse(source)
	if err != nil { return err }
	if err := expiryCompatRequire(file, "restart", expiryCompatRestart); err != nil { return err }
	return expiryCompatFourHelpers(file)
}

// No inline/legacy OR alternative: every strict function contract requires all
// four full signatures and bodies, once each. Whole-file order is checked too.
func expiryCompatFourHelpers(file *ast.File) error {
	for _, contract := range []struct{name, literal string}{
		{"expiryBootstrapRefused", expiryCompatBootstrapHelper},
		{"expiryBootstrapNotZero", expiryCompatBootstrapZeroHelper},
		{"expiryRestartFreshnessRefused", expiryCompatFreshnessHelper},
		{"expiryRestartCleanupUnion", expiryCompatCleanupHelper},
	} {
		if err := expiryCompatRequire(file, contract.name, contract.literal); err != nil { return err }
	}
	return nil
}

const expiryCompatBootstrapHelper = `func expiryBootstrapRefused(p sidecargapdb.SidecarProvenance, controlRoot, supervisor string, identity sidecargapdb.ExpiryTechnicalIdentity) bool {
	return p.DatabaseID == "" || p.DataDirectory != controlRoot || p.SupervisorRunID != supervisor || p.ExecutedImage.SHA256 != identity.ExecutableSHA256 ||
		p.Observation.Status.Lifecycle != gapdbapi.LifecycleReady || p.Observation.Status.OwnerPID != p.Process.PID || p.Observation.Status.DatabaseID != p.DatabaseID
}`

const expiryCompatBootstrapZeroHelper = `func expiryBootstrapNotZero(initial bool, p sidecargapdb.SidecarProvenance) bool {
	return initial && (p.Observation.Status.CurrentRevision != 0 || p.Observation.Status.DurableThroughRevision != 0 || p.Observation.Status.RecordCount != 0)
}`

const expiryCompatFreshnessHelper = `func expiryRestartFreshnessRefused(readErr error, current runtimeCurrentV2, predecessorEpoch journalv2.RuntimeEpochID,
	p, predecessor sidecargapdb.SidecarProvenance, identity, predecessorIdentity sidecargapdb.ExpiryTechnicalIdentity) bool {
	return readErr != nil || current.PredecessorEpochID != predecessorEpoch || current.RuntimeEpochID == predecessorEpoch ||
		p.DatabaseID != predecessor.DatabaseID || p.RuntimeDirectory == predecessor.RuntimeDirectory || p.ConfiguredSocket == predecessor.ConfiguredSocket ||
		p.Process == predecessor.Process || p.Process.PID == predecessor.Process.PID || p.Process.StartTicks == predecessor.Process.StartTicks || identity != predecessorIdentity
}`

const expiryCompatCleanupHelper = `func expiryRestartCleanupUnion(crashErr, hostErr, outcomesErr, contextErr error) error {
	return errors.Join(crashErr, hostErr, outcomesErr, contextErr)
}`

const expiryCompatStartGeneration = `func (l *expiryHostLease) startGeneration(c *expiryLeaseControl, ctx context.Context, supervisor string) (_ *Host, _ *expiryHostRun, resultErr error) {
	if ctx == nil || ctx.Err() != nil || l.parent.Err() != nil {
		return nil, nil, expiryFailure("expiry_start_context")
	}
	if err := l.checkPathsForControl(c); err != nil {
		return nil, nil, err
	}
	l.mu.Lock()
	if !l.controlLocked(c, expiryControlChecking) || l.failed || c.kind == expiryControlFinal || c.candidate != nil {
		l.failLocked()
		l.mu.Unlock()
		return nil, nil, expiryFailure("expiry_generation_lease")
	}
	initial := c.kind == expiryControlFresh
	predecessorRun := c.predecessor
	config := l.config
	config.Sidecar.SupervisorRunID = supervisor
	generationContext, cancel := context.WithCancel(l.parent)
	r := &expiryHostRun{ctx: generationContext, cancel: cancel, lease: l, window: newExpiryWorkWindow()}
	c.candidate, c.stage = r, expiryControlConstructing
	l.mu.Unlock()
	decision, err := preflightHostRoot(ctx, config)
	if err != nil {
		return nil, nil, err
	}
	if initial && decision.Action != controlroot.Initialize || !initial && decision.Action != controlroot.Recover {
		return nil, nil, expiryFailure("expiry_generation_classification")
	}
	l.mu.Lock()
	if !l.controlLocked(c, expiryControlConstructing) {
		l.failLocked()
		l.mu.Unlock()
		return nil, nil, expiryFailure("expiry_start_controller")
	}
	c.opaqueStartup = true
	l.mu.Unlock()
	lifecycle, gate, err := sidecargapdb.StartExpiryQualification(generationContext, config.Sidecar)
	if err != nil {
		return nil, nil, err
	}
	l.mu.Lock()
	c.opaqueStartup = false
	l.mu.Unlock()
	r.gate, r.identity = gate, gate.Identity()
	r.owner = &productionOwner{expiry: ownerExpiryState{run: r}, lifecycle: lifecycle}
	if !expiryIdentityValid(r.identity) {
		return nil, nil, expiryFailure("expiry_identity")
	}
	p := lifecycle.Provenance()
	if expiryBootstrapRefused(p, l.controlRoot, supervisor, r.identity) {
		return nil, nil, expiryFailure("expiry_bootstrap")
	}
	if expiryBootstrapNotZero(initial, p) {
		return nil, nil, expiryFailure("expiry_bootstrap_not_zero")
	}
	adapter, closer, err := lifecycle.DialOwned(ctx, storegapdb.DefaultRequestTimeout)
	if err != nil {
		return nil, nil, err
	}
	database := journalv2.DatabaseID(p.DatabaseID)
	r.owner.adapter, r.owner.closer, r.owner.database = adapter, closer, database
	if initial {
		for _, key := range []string{authorityBindingKey, runtimeCurrentKey} {
			if _, err := adapter.Get(ctx, key); !storegapdb.IsKind(err, storegapdb.OutcomeNotFound) {
				return nil, nil, expiryFailure("expiry_bootstrap_records")
			}
		}
	} else if database != l.database {
		return nil, nil, expiryFailure("expiry_restart_database")
	}
	binding := AuthorityBindingV2{qualification: authorityQualificationBinding{expiryInputSHA, expiryTupleSHA, expiryInventorySHA, expiryRecoveryContract},
		SchemaVersion: 1, HostAuthorityID: config.HostAuthorityID, DatabaseID: database, SidecarSHA: r.identity.ExecutableSHA256,
		WireProtocol: 1, StorageProtocol: 1, CreatedOperationID: config.AuthorityBindingOperationID}
	if !initial && binding != l.binding {
		return nil, nil, expiryFailure("expiry_restart_binding")
	}
	if err := ensureAuthorityBinding(ctx, adapter, binding, initial); err != nil {
		return nil, nil, err
	}
	r.owner.binding = binding
	if err := l.moveControl(c, expiryControlValidating, expiryControlConstructing); err != nil {
		return nil, nil, err
	}
	if err := l.checkPathsForControl(c); err != nil {
		return nil, nil, err
	}
	host, err := completeHostStartup(ctx, config, r.owner, expiryRecoveryReaderV2{ReaderV2: adapter})
	if err != nil {
		return nil, nil, err
	}
	r.host, r.composition = host, host.retained
	host.expiry = hostExpiryState{run: r}
	if _, ok := host.retainedCapture(r.composition); !ok {
		return nil, nil, expiryFailure("expiry_composition")
	}
	if !initial {
		if predecessorRun == nil || predecessorRun.composition == nil || predecessorRun.owner == nil {
			return nil, nil, expiryFailure("expiry_predecessor")
		}
		predecessor := predecessorRun.owner.lifecycle.Provenance()
		current, _, readErr := readCurrentEpoch(ctx, adapter)
		if expiryRestartFreshnessRefused(readErr, current, predecessorRun.composition.epoch, p, predecessor, r.identity, predecessorRun.identity) {
			return nil, nil, expiryFailure("expiry_restart_freshness")
		}
	}
	if err := l.checkPathsForControl(c); err != nil {
		return nil, nil, err
	}
	if predecessorRun != nil {
		if err := predecessorRun.cleanupOutcomes(); err != nil {
			return nil, nil, err
		}
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if !l.controlLocked(c, expiryControlValidating) || c.candidate != r || c.metadata != nil || !l.quiescentLocked(predecessorRun) ||
		ctx.Err() != nil || generationContext.Err() != nil || l.parent.Err() != nil || l.ctx.Err() != nil || l.failed || !expiryOriginalDoneOpen(r) ||
		l.cleanup.outcome(false) != nil || r.window.retired {
		return nil, nil, expiryFailure("expiry_root_return")
	}
	if err := r.gate.RecordedCleanupError(); err != nil {
		return nil, nil, err
	}
	if initial {
		l.database, l.binding = database, binding
	}
	l.current, l.phase, c.stage, l.control = r, expiryLeaseRunning, expiryControlHandedOff, nil
	return host, r, nil
}`

const expiryCompatRestart = `func (r *expiryHostRun) restart(ctx context.Context, supervisor string, unclean bool) (_ *Host, _ *expiryHostRun, resultErr error) {
	if r == nil || r.lease == nil {
		return nil, nil, expiryFailure("expiry_generation")
	}
	if err := r.checkCurrent(ctx); err != nil {
		return nil, nil, r.fail(err)
	}
	if _, err := journalv2.NewOperationID(supervisor); err != nil || supervisor == r.owner.lifecycle.Provenance().SupervisorRunID ||
		r.owner.lifecycle.InspectOwnership().State != sidecargapdb.OwnershipOwnedLive {
		return nil, nil, r.fail(expiryFailure("expiry_supervisor"))
	}
	l := r.lease
	kind := expiryControlRestartClean
	if unclean {
		kind = expiryControlRestartUnclean
	}
	c, err := l.reserveControl(r, kind, ctx)
	if err != nil {
		return nil, nil, r.fail(err)
	}
	defer func() {
		if resultErr != nil {
			resultErr = l.rollbackControl(c, r.fail(resultErr))
		}
	}()
	cleanup, cancel := expiryCleanupContext()
	defer cancel()
	r.invalidate()
	if err := l.drainControl(cleanup, c); err != nil {
		return nil, nil, err
	}
	if err := l.moveControl(c, expiryControlChecking, expiryControlDrained); err != nil {
		return nil, nil, err
	}
	if err := l.checkPathsForControl(c); err != nil {
		return nil, nil, err
	}
	predecessor := r.owner.lifecycle.Provenance()
	var crashErr error
	if unclean {
		crashErr = r.owner.lifecycle.CrashExpiryQualification(cleanup)
	}
	hostErr := r.host.Close(cleanup)
	if err := expiryRestartCleanupUnion(crashErr, hostErr, r.cleanupOutcomes(), cleanup.Err()); err != nil {
		return nil, nil, l.residual(c, err)
	}
	for _, path := range []string{predecessor.ConfiguredSocket, predecessor.RuntimeDirectory} {
		if _, err := os.Lstat(path); !errors.Is(err, os.ErrNotExist) {
			return nil, nil, expiryFailure("expiry_predecessor_residual")
		}
	}
	if ctx.Err() != nil || l.parent.Err() != nil {
		return nil, nil, expiryFailure("expiry_restart_context")
	}
	return l.startGeneration(c, ctx, supervisor)
}`

// All mutants below are CODE ONLY, never parsed/executed during this handoff.
// Mutations start from actual selected source, checked first against the
// independently literal contract. Failures must be valid Go syntax rejected
// by the same strict source assertion used by the W5 tops.
func expiryCompatReplaceOnce(t *testing.T, source, old, next string) string {
	t.Helper()
	if strings.Count(source, old) != 1 { t.Fatalf("mutant anchor not unique: %q", old) }
	return strings.Replace(source, old, next, 1)
}

func expiryCompatMutantRejected(t *testing.T, source string, check func(string) error) {
	t.Helper()
	if _, err := expiryCompatParse(source); err != nil { t.Fatalf("mutant must be syntactically valid, not a parse-error escape: %v", err) }
	if err := check(source); err == nil { t.Fatal("mutant escaped strict candidate AST contract") }
}

func TestExpiryProposalRestartCompatibilityFreshnessMutants(t *testing.T) {
	literal := "package productionruntime\n" + expiryCompatStartGeneration + "\n" + expiryCompatBootstrapHelper + "\n" + expiryCompatBootstrapZeroHelper + "\n" + expiryCompatFreshnessHelper + "\n" + expiryCompatCleanupHelper
	if err := expiryCompatFreshness(literal); err != nil { t.Fatal("literal positive control", err) }
	base := expiryCompatReadSelected(t)
	if err := expiryCompatFreshness(base); err != nil { t.Fatal("actual selected source control", err) }
	call := "expiryRestartFreshnessRefused(readErr, current, predecessorRun.composition.epoch, p, predecessor, r.identity, predecessorRun.identity)"
	inline := "readErr != nil || current.PredecessorEpochID != predecessorRun.composition.epoch || current.RuntimeEpochID == predecessorRun.composition.epoch || p.DatabaseID != predecessor.DatabaseID || p.RuntimeDirectory == predecessor.RuntimeDirectory || p.ConfiguredSocket == predecessor.ConfiguredSocket || p.Process == predecessor.Process || p.Process.PID == predecessor.Process.PID || p.Process.StartTicks == predecessor.Process.StartTicks || r.identity != predecessorRun.identity"
	t.Run("original_inline_candidate", func(t *testing.T) {
		old := expiryCompatReplaceOnce(t, expiryCompatReplaceOnce(t, base, expiryCompatFreshnessHelper, ""), call, inline)
		expiryCompatMutantRejected(t, old, expiryCompatFreshness)
	})
	t.Run("inline_even_with_unused_helper", func(t *testing.T) { expiryCompatMutantRejected(t, expiryCompatReplaceOnce(t, base, call, inline), expiryCompatFreshness) })
	t.Run("missing_helper", func(t *testing.T) { expiryCompatMutantRejected(t, expiryCompatReplaceOnce(t, base, expiryCompatFreshnessHelper, ""), expiryCompatFreshness) })
	for _, atom := range []string{
		"readErr != nil", "current.PredecessorEpochID != predecessorEpoch", "current.RuntimeEpochID == predecessorEpoch",
		"p.DatabaseID != predecessor.DatabaseID", "p.RuntimeDirectory == predecessor.RuntimeDirectory", "p.ConfiguredSocket == predecessor.ConfiguredSocket",
		"p.Process == predecessor.Process", "p.Process.PID == predecessor.Process.PID", "p.Process.StartTicks == predecessor.Process.StartTicks", "identity != predecessorIdentity",
	} {
		t.Run("omit/"+atom, func(t *testing.T) {
			expiryCompatMutantRejected(t, expiryCompatReplaceOnce(t, base, atom, "false"), expiryCompatFreshness)
		})
		t.Run("drift/"+atom, func(t *testing.T) {
			drift := strings.Replace(atom, " != ", " == ", 1)
			if drift == atom { drift = strings.Replace(atom, " == ", " != ", 1) }
			expiryCompatMutantRejected(t, expiryCompatReplaceOnce(t, base, atom, drift), expiryCompatFreshness)
		})
	}
	// Ordered OR is a contract, not merely set membership. Mutate the actual
	// selected helper's adjacent atoms without changing any surrounding code.
	atoms := []string{"readErr != nil", "current.PredecessorEpochID != predecessorEpoch", "current.RuntimeEpochID == predecessorEpoch",
		"p.DatabaseID != predecessor.DatabaseID", "p.RuntimeDirectory == predecessor.RuntimeDirectory", "p.ConfiguredSocket == predecessor.ConfiguredSocket",
		"p.Process == predecessor.Process", "p.Process.PID == predecessor.Process.PID", "p.Process.StartTicks == predecessor.Process.StartTicks", "identity != predecessorIdentity"}
	for i := 0; i+1 < len(atoms); i++ {
		t.Run(fmt.Sprintf("OR_order_swap_%d", i), func(t *testing.T) {
			changed := expiryCompatReplaceOnce(t, base, atoms[i], "expiryCompatTemporaryAtom")
			changed = expiryCompatReplaceOnce(t, changed, atoms[i+1], atoms[i])
			changed = expiryCompatReplaceOnce(t, changed, "expiryCompatTemporaryAtom", atoms[i+1])
			expiryCompatMutantRejected(t, changed, expiryCompatFreshness)
		})
	}
	for _, change := range []struct{name, old, next string}{
		{"duplicate_call", "if "+call+" {", "if "+call+" || "+call+" {"},
		{"wrong_stage", "current, _, readErr := readCurrentEpoch(ctx, adapter)\n\t\tif "+call+" {\n\t\t\treturn nil, nil, expiryFailure(\"expiry_restart_freshness\")\n\t\t}", "if "+call+" { return nil, nil, expiryFailure(\"expiry_restart_freshness\") }; current, _, readErr := readCurrentEpoch(ctx, adapter)"},
		{"stage_scope", "if !initial {\n\t\tif predecessorRun == nil", "if initial {\n\t\tif predecessorRun == nil"},
		{"reordered_arguments", call, "expiryRestartFreshnessRefused(readErr, current, predecessorRun.composition.epoch, predecessor, p, r.identity, predecessorRun.identity)"},
		{"return_classification", "expiryFailure(\"expiry_restart_freshness\")", "expiryFailure(\"expiry_bootstrap\")"},
		{"extra_RPC", "current, _, readErr := readCurrentEpoch(ctx, adapter)", "current, _, readErr := readCurrentEpoch(ctx, adapter); adapter.Get(ctx, runtimeCurrentKey)"},
		{"extra_hash", "return readErr != nil ||", "digestFile(); return readErr != nil ||"},
		{"extra_factory", "return readErr != nil ||", "providerFactory(); return readErr != nil ||"},
		{"duplicate_helper_declaration", expiryCompatFreshnessHelper, expiryCompatFreshnessHelper+"\n"+expiryCompatFreshnessHelper},
		{"OR_to_AND", "return readErr != nil ||", "return readErr != nil &&"},
		{"extra_disjunct", "return readErr != nil ||", "return true || readErr != nil ||"},
	} {
		t.Run(change.name, func(t *testing.T) { expiryCompatMutantRejected(t, expiryCompatReplaceOnce(t, base, change.old, change.next), expiryCompatFreshness) })
	}
	// Every adjacent argument transposition is individually refused, including
	// otherwise type-incompatible ones: AST source controls are not type checks.
	args := []string{"readErr", "current", "predecessorRun.composition.epoch", "p", "predecessor", "r.identity", "predecessorRun.identity"}
	for i := 0; i+1 < len(args); i++ {
		t.Run(fmt.Sprintf("argument_swap_%d", i), func(t *testing.T) {
			changed := append([]string(nil), args...); changed[i], changed[i+1] = changed[i+1], changed[i]
			expiryCompatMutantRejected(t, expiryCompatReplaceOnce(t, base, call, "expiryRestartFreshnessRefused("+strings.Join(changed, ", ")+")"), expiryCompatFreshness)
		})
	}
}

func TestExpiryProposalRestartCompatibilityCleanupMutants(t *testing.T) {
	literal := "package productionruntime\n" + expiryCompatRestart + "\n" + expiryCompatBootstrapHelper + "\n" + expiryCompatBootstrapZeroHelper + "\n" + expiryCompatFreshnessHelper + "\n" + expiryCompatCleanupHelper
	if err := expiryCompatCleanup(literal); err != nil { t.Fatal("literal positive control", err) }
	base := expiryCompatReadSelected(t)
	if err := expiryCompatCleanup(base); err != nil { t.Fatal("actual selected source control", err) }
	call := "expiryRestartCleanupUnion(crashErr, hostErr, r.cleanupOutcomes(), cleanup.Err())"
	join := "errors.Join(crashErr, hostErr, outcomesErr, contextErr)"
	for _, change := range []struct{name, old, next string}{
		{"legacy_inline", call, "errors.Join(crashErr, hostErr, r.cleanupOutcomes(), cleanup.Err())"},
		{"missing_helper", expiryCompatCleanupHelper, ""},
		{"duplicate_call", "if err := "+call, "_ = "+call+"; if err := "+call},
		{"wrong_stage_before_Close", "hostErr := r.host.Close(cleanup)\n\tif err := "+call+"; err != nil {\n\t\treturn nil, nil, l.residual(c, err)\n\t}", "if err := "+call+"; err != nil { return nil, nil, l.residual(c, err) }; hostErr := r.host.Close(cleanup)"},
		{"Close_removed", "hostErr := r.host.Close(cleanup)", "var hostErr error"},
		{"Close_duplicated", "hostErr := r.host.Close(cleanup)", "hostErr := r.host.Close(cleanup); r.host.Close(cleanup)"},
		{"Close_wrong_context", "hostErr := r.host.Close(cleanup)", "hostErr := r.host.Close(ctx)"},
		{"wrong_residual", "l.residual(c, err)", "l.residual(c, hostErr)"},
		{"extra_RPC", "hostErr := r.host.Close(cleanup)", "r.owner.adapter.Get(ctx, runtimeCurrentKey); hostErr := r.host.Close(cleanup)"},
		{"extra_hash", "return "+join, "digestFile(); return "+join},
		{"extra_factory", "return "+join, "providerFactory(); return "+join},
		{"extra_Join", "return "+join, "return errors.Join("+join+")"},
		{"variadic_Join", join, "errors.Join(crashErr, hostErr, outcomesErr, contextErr...)"},
		{"duplicate_helper_declaration", expiryCompatCleanupHelper, expiryCompatCleanupHelper+"\n"+expiryCompatCleanupHelper},
	} {
		t.Run(change.name, func(t *testing.T) { expiryCompatMutantRejected(t, expiryCompatReplaceOnce(t, base, change.old, change.next), expiryCompatCleanup) })
	}
	for _, operands := range []struct{name, call string; args []string}{
		{"callsite", call, []string{"crashErr", "hostErr", "r.cleanupOutcomes()", "cleanup.Err()"}},
		{"Join4", join, []string{"crashErr", "hostErr", "outcomesErr", "contextErr"}},
	} {
		for i := range operands.args {
			t.Run(fmt.Sprintf("%s/omit_%d", operands.name, i), func(t *testing.T) {
				args := append([]string(nil), operands.args...); args = append(args[:i], args[i+1:]...)
				fun := strings.SplitN(operands.call, "(", 2)[0]
				expiryCompatMutantRejected(t, expiryCompatReplaceOnce(t, base, operands.call, fun+"("+strings.Join(args, ", ")+")"), expiryCompatCleanup)
			})
			if i+1 < len(operands.args) {
				t.Run(fmt.Sprintf("%s/swap_%d", operands.name, i), func(t *testing.T) {
					args := append([]string(nil), operands.args...); args[i], args[i+1] = args[i+1], args[i]
					fun := strings.SplitN(operands.call, "(", 2)[0]
					expiryCompatMutantRejected(t, expiryCompatReplaceOnce(t, base, operands.call, fun+"("+strings.Join(args, ", ")+")"), expiryCompatCleanup)
				})
			}
		}
	}
}
