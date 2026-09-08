package beads

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

// runPortableAuthorityProof executes one named production-host scenario in a
// separate test process. Beads tests own provider semantics; the scenario
// package owns GapDB lifecycle, restart, projection, and stale-writer laws.
// Joining the two executable proofs here prevents either half of the mission
// method from being replaced by a test-only persistence implementation.
func runPortableAuthorityProof(t *testing.T, selector string) {
	t.Helper()
	executable := os.Getenv("SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE")
	if executable == "" {
		executable = os.Getenv("SPEC_KITTY_WP13_GAPDBD")
	}
	if executable == "" {
		t.Skip("set SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE to run the GapDB production-authority proof")
	}
	_, source, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("locate Beads production-authority proof source")
	}
	repository := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", ".."))
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	command := exec.CommandContext(ctx, "go", "test", "./internal/scenario", "-run", "^"+selector+"$", "-count=1", "-timeout=7m", "-v")
	command.Dir = repository
	command.Env = append(os.Environ(), "SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE="+executable)
	output, err := command.CombinedOutput()
	if ctx.Err() != nil {
		t.Fatalf("GapDB production-authority proof %s timed out: %v", selector, ctx.Err())
	}
	if err != nil {
		t.Fatalf("GapDB production-authority proof %s failed: %v\n%s", selector, err, output)
	}
	if !strings.Contains(string(output), "--- PASS: "+selector) {
		t.Fatalf("GapDB production-authority proof %s did not execute to a pass:\n%s", selector, output)
	}
}
