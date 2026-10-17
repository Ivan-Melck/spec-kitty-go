//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"crypto/sha256"
	"fmt"
	"os"
	"testing"
)

// FUTURE NORMAL SELECTION ONLY / UNRUN / NOT ACTIVATED.
// Reads actual package-relative live source, never a cache-candidate proxy.
// This intentionally rejects today's approved two-helper live product.
// Overlays MUST replace this file, not add another set of declarations.
// os.ReadFile does not follow compiler overlays. A direct hash alone is NOT
// proof of compiler selection; use the exact group map and input record too.
const expiryCompatSelectedSourcePath = "expiry_qualification.go"
const expiryCompatSelectedSourceSHA256 = "8ec999b5f4fc82f266dda6a2399122917df322bbacf057a1584fa3d57717a4bd"

func expiryCompatReadSelected(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(expiryCompatSelectedSourcePath)
	if err != nil { t.Fatal(err) }
	if got := fmt.Sprintf("%x", sha256.Sum256(raw)); got != expiryCompatSelectedSourceSHA256 {
		t.Fatalf("selected production source drift: %s SHA256=%s want=%s", expiryCompatSelectedSourcePath, got, expiryCompatSelectedSourceSHA256)
	}
	return string(raw)
}
