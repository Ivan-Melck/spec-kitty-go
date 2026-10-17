//go:build linux

package gapdb

// UNRUN source contracts; never substitutes for exercising local-close faults,
// authentic admission, actual exchange rundown, or genuine Host workloads.
import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"strings"
	"testing"
)
func revision3Source(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil { t.Fatal(err) }
	return string(raw)
}
func TestRevision3RequiredPreflightAndLocalCloseSource(t *testing.T) {
	provenance := revision3Source(t, "provenance.go")
	adoption := revision3Source(t, "adoption.go")
	if !strings.Contains(adoption, "readExactNoFollow(") || !strings.Contains(provenance, "readExactRecordFile(os.NewFile(uintptr(fd), \"verified-record\"), maximum, (*os.File).Close)") { t.Fatal("genuine preflight edge not closed") }
	for _, required := range []string{"resultErr = withStartupCustody(resultErr, closeFile(file))", "if resultErr != nil { encoded, digestResult = nil, \"\" }", "resultErr = withStartupCustody(resultErr, cleanupErr)", "cleanupErr = joinStartupCustody(cleanupErr, err)", "cleanupErr = joinStartupCustody(cleanupErr, closeErr)", "v.closeOnce.Do(", "return v.closeErr"} {
		if !strings.Contains(provenance, required) { t.Error("verifier/record actual close loss", required) }
	}
	life := revision3Source(t, "lifecycle.go")
	for _, required := range []string{"resultErr = withStartupCustody(resultErr, report.closeLocalFD(fd))", "resultErr = withStartupCustody(resultErr, report.closeLocalFile(file))", "result, digestResult = fileIdentity{}, \"\"", "if resultErr != nil { result = fileIdentity{} }", "resultErr = withStartupCustody(resultErr, connection.Close())", "withStartupCustody(err, closeErr)", "l.finishExitedShutdown(ctx, \"cleanup_stale\")", "l.finishExitedShutdown(ctx, \"restart_unclean\")", "l.shutdownTailCustody(ctx, resultErr)"} {
		if !strings.Contains(life, required) { t.Error("shared helper/shutdown missing custody", required) }
	}
	if strings.Contains(life, "os.Remove(runtimeDirectory)") { t.Fatal("blind pre-child runtime path removal reintroduced") }
	_, attach, ok := strings.Cut(life, "func Attach(")
	if !ok { t.Fatal("Attach absent") }
	if strings.Index(attach, "expiryAttachAllowed(recorded)") > strings.Index(attach, "normalizeLifecycleConfig(&config)") { t.Fatal("marked Attach no longer refuses first") }
	if strings.Index(attach, "verifiedCloseErr != nil") > strings.Index(attach, "acquireRecordedProcess(config.processes") { t.Fatal("close-only Attach reached pidfd") }
	// The original readiness startpoint remains after immediate verified close;
	// no startup phase deadline is passed to the cleanup ownership seam.
	_, start, _ := strings.Cut(life, "func startVerified(")
	start, _, _ = strings.Cut(start, "func Attach(")
	if strings.Index(start, "deadline := intersectCleanupDeadline(ctx, time.Now().Add(config.StartTimeout))") < strings.Index(start, "closeErr := verified.Close()") || strings.Contains(start, "abortStartBefore(ctx, deadline)") { t.Fatal("spent readiness reused or readiness window moved") }
}
func TestRevision3ApprovedTransportNoSuccessfulOwnerHistoryTripwire(t *testing.T) {
	// Observe approved T bytes without changing its fields or lifetime behavior.
	parsed, err := parser.ParseFile(token.NewFileSet(), "../../controlstore/gapdb/retained_transport.go", nil, 0)
	if err != nil { t.Fatal(err) }
	expected := strings.Fields("lifetime endpoint database ownerStarted ownerPID timeout requests mu closed active dials dialStop closeOnce closeErr connectionCloseErr connectionCloseAttempts")
	seen := map[string]bool{}
	for _, declaration := range parsed.Decls {
		gen, ok := declaration.(*ast.GenDecl)
		if !ok { continue }
		for _, specification := range gen.Specs {
			typ, ok := specification.(*ast.TypeSpec)
			if !ok || typ.Name.Name != "retainedTransport" { continue }
			structure, ok := typ.Type.(*ast.StructType)
			if !ok { t.Fatal("transport not structure") }
			for _, field := range structure.Fields.List { for _, name := range field.Names { seen[name.Name] = true } }
		}
	}
	if len(seen) != len(expected) { t.Fatal("transport roots changed; independently re-review reachability", seen) }
	for _, name := range expected { if !seen[name] { t.Error("approved transport field absent", name) } }
	// Structural tripwire only. Active/reservation/callback/exchange tails still
	// retain actual owners; nonnil failure causes may grow. No heap bound claim.
}
