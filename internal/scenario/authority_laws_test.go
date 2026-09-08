package scenario

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/testcampaign/m2/iterationreceipt"
)

func TestZombieWorkspaceRemainsWritableButAuthorityIsFenced(t *testing.T) {
	h := newPortableHarness(t, ProductionConfig{ExecutablePath: productionExecutable(t), Seed: t.Name()})
	h.createMission("project-zombie", "mission-zombie", "operation-zombie-create")
	staleDirectory := filepath.Join(h.root, "stale-workspace")
	if err := os.Mkdir(staleDirectory, 0o700); err != nil {
		t.Fatal(err)
	}
	staleFile := filepath.Join(staleDirectory, "candidate.txt")
	if err := os.WriteFile(staleFile, []byte("before\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	semanticBefore := semanticProgressDigest(t, h, "project-zombie", "mission-zombie")
	staleEpoch, _ := h.restart()
	file, err := os.OpenFile(staleFile, os.O_APPEND|os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := file.WriteString("zombie-write\n"); err != nil {
		_ = file.Close()
		t.Fatal(err)
	}
	if err := file.Close(); err != nil {
		t.Fatal(err)
	}
	assertStaleEpochRefusesBeforeRepository(t, h, staleEpoch)
	if got := semanticProgressDigest(t, h, "project-zombie", "mission-zombie"); got != semanticBefore {
		t.Fatalf("stale filesystem writer changed authority: before=%s after=%s", semanticBefore, got)
	}
	raw, err := os.ReadFile(staleFile)
	if err != nil || !strings.Contains(string(raw), "zombie-write") {
		t.Fatalf("test did not prove the stale directory remained writable: %q err=%v", raw, err)
	}
	assertUniversalInvariants(t, h, "project-zombie", "mission-zombie", "zombie-resumed")
}

func TestZombieOwnerReplacementCampaign100(t *testing.T) {
	requireFullCampaign(t)
	h := newPortableHarness(t, ProductionConfig{ExecutablePath: productionExecutable(t), Seed: t.Name()})
	h.createMission("project-replacement", "mission-replacement", "operation-replacement-create")
	want := semanticProgressDigest(t, h, "project-replacement", "mission-replacement")
	seen := map[string]struct{}{string(h.host.RuntimeEpochID()): {}}
	for trial := range 100 {
		started := time.Now()
		stale, current := h.restart()
		if _, duplicate := seen[string(current)]; duplicate {
			t.Fatalf("trial=%d reused owner epoch %s", trial, current)
		}
		seen[string(current)] = struct{}{}
		assertStaleEpochRefusesBeforeRepository(t, h, stale)
		if got := semanticProgressDigest(t, h, "project-replacement", "mission-replacement"); got != want {
			t.Fatalf("trial=%d replacement changed authority: want=%s got=%s", trial, want, got)
		}
		invariants := assertUniversalInvariants(t, h, "project-replacement", "mission-replacement", fmt.Sprintf("replacement-%03d", trial))
		receipt, err := iterationreceipt.New(
			iterationreceipt.DefinitionID("behavior/zombie-workspace-isolation"), trial, fmt.Sprintf("scenario-%s-%d", shortDigest(h.config.Seed), h.starts), "runtime-epoch-replacement",
			"stale-refused-authority-unchanged", time.Since(started),
			map[string]any{"prior_epoch": stale, "current_epoch": current, "authority_digest": want}, json.RawMessage(invariants),
		)
		if err != nil {
			t.Fatal(err)
		}
		iterationreceipt.Emit(t, receipt)
	}
	t.Logf("campaign seed=%s replacements=100 distinct_epochs=%d outcome=pass", t.Name(), len(seen))
}

func TestStaleEpochRefusesBeforeRepositoryFence(t *testing.T) {
	h := newPortableHarness(t, ProductionConfig{ExecutablePath: productionExecutable(t), Seed: t.Name()})
	h.createMission("project-guard", "mission-guard", "operation-guard-create")
	stale, _ := h.restart()
	assertStaleEpochRefusesBeforeRepository(t, h, stale)
	if h.git.count() != 0 {
		t.Fatalf("refusal acquired repository authority: calls=%d", h.git.count())
	}
}
