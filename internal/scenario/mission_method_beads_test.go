package scenario

import (
	"crypto/sha256"
	"encoding/hex"
	"io"
	"os"
	"path/filepath"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// This portable campaign is the semantic control for every provider-specific
// qualification. It deliberately requires neither Beads nor provider
// environment variables, so an absent real provider cannot hide regressions.
func TestMissionMethodProviderParityPortableCampaign(t *testing.T) {
	result := runPortableMissionFixture(t, "provider-parity")
	if result.GenerationOne.Generation() != 1 || result.GenerationTwo.Generation() != 2 || result.GenerationTwo.PredecessorID() != result.GenerationOne.ID() {
		t.Fatalf("portable lineage one=%s two=%s predecessor=%s", result.GenerationOne.ID(), result.GenerationTwo.ID(), result.GenerationTwo.PredecessorID())
	}
	if result.MissionOne.TerminalOutcome != mission.OutcomeWorkFinalized || result.MissionTwo.TerminalOutcome != mission.OutcomeWorkFinalized {
		t.Fatalf("provider work changed mission authority: one=%s two=%s", result.MissionOne.TerminalOutcome, result.MissionTwo.TerminalOutcome)
	}
	if result.GenerationOne.GraphDigest() == result.GenerationTwo.GraphDigest() || result.GenerationTwo.Generation() != 2 {
		t.Fatal("semantic provider mutation did not require an explicit successor generation")
	}
	if result.GenerationTwo.WorkItems()[0].WorkID() != "work-a-v2" || result.MissionTwo.CurrentWorkRevisionID != result.GenerationTwo.ID() {
		t.Fatalf("explicit successor was not adopted: mission=%s revision=%s", result.MissionTwo.CurrentWorkRevisionID, result.GenerationTwo.ID())
	}
}

// The executable qualification itself lives beside the bounded Beads adapter,
// where it can reuse the established isolated workspace harness. This scenario
// guard makes its opt-in identity explicit and independently verifies the
// reviewed binary before the test selection is admitted.
func TestMissionMethodBeadsQualificationProfileIdentity(t *testing.T) {
	if os.Getenv("SPEC_KITTY_REAL_BEADS_QUALIFICATION") != "1" {
		t.Skip("real Beads mission-method qualification is explicit and is not portable-test success")
	}
	binary := os.Getenv("BEADS_BIN")
	want := os.Getenv("BEADS_DIGEST_SHA256")
	if !filepath.IsAbs(binary) || filepath.Clean(binary) != binary || len(want) != sha256.Size*2 {
		t.Fatal("qualified Beads profile requires canonical binary and reviewed SHA-256")
	}
	file, err := os.Open(binary)
	if err != nil {
		t.Fatal(err)
	}
	hash := sha256.New()
	_, copyErr := io.Copy(hash, io.LimitReader(file, 1<<30))
	closeErr := file.Close()
	if copyErr != nil || closeErr != nil {
		t.Fatalf("hash qualified Beads binary: copy=%v close=%v", copyErr, closeErr)
	}
	if got := hex.EncodeToString(hash.Sum(nil)); got != want {
		t.Fatalf("qualified Beads digest mismatch: got=%s", got)
	}
}
