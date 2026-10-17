package restore

import (
	"testing"

	journal "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
	"github.com/spec-kitty/go-kitty3/internal/controljournal/v2/playback"
)

func TestRestoreProvesHistoricalProjectionPrefixWithoutChangingLatestSnapshot(t *testing.T) {
	for _, phase := range []string{"before_checkpoint", "checkpoint_boundary", "within_suffix", "latest_revision"} {
		t.Run(phase, func(t *testing.T) {
			input, latest, latestRolling := qualificationRestoreFixture(t, 1, 2, false)
			reader := input.Reader.(*journalReader)
			events, _ := input.EventRegistry.Descriptor()
			reducers, _ := input.Reducers.Descriptor()
			empty, err := playback.RunV2(playback.InputV2{EventRegistry: input.EventRegistry, Reducers: input.Reducers,
				ExpectedEventRegistryDigest: events.Digest(), ExpectedReducerRegistryDigest: reducers.Digest()})
			if err != nil {
				t.Fatal(err)
			}
			revision, digest := uint64(1), empty.CanonicalProjectionDigest()
			switch phase {
			case "before_checkpoint":
				revision = 0
			case "within_suffix":
				_, firstMission, _ := qualificationRestoreFixture(t, 1, 1, false)
				revision, digest = 3, firstMission.CanonicalProjectionDigest()
			case "latest_revision":
				revision, digest = reader.revision, latest.CanonicalProjectionDigest()
			}
			input.ExpectedProjectionPrefix, err = NewProjectionPrefixV2(revision, digest)
			if err != nil {
				t.Fatal(err)
			}
			result, err := RestoreV2(t.Context(), input)
			if err != nil {
				t.Fatal(err)
			}
			if result.StoreRevision().CurrentRevision() != reader.revision || result.Playback().CanonicalProjectionDigest() != latest.CanonicalProjectionDigest() {
				t.Fatal("prefix validation replaced the latest snapshot with historical state")
			}
			if phase != "before_checkpoint" && result.RollingPrefixDigest() != latestRolling {
				t.Fatal("prefix validation changed the latest rolling journal proof")
			}
			if _, err := result.FreshEpochAdmissionRequestV2(); err != nil {
				t.Fatalf("validated latest restore lost its seal: %v", err)
			}
		})
	}
}

func TestRestoreHistoricalProjectionPrefixRefusesWrongDigestAndFutureRevision(t *testing.T) {
	for _, phase := range []string{"wrong_digest", "future_revision"} {
		t.Run(phase, func(t *testing.T) {
			input, latest, _ := qualificationRestoreFixture(t, 1, 2, false)
			revision, digest := uint64(1), restoreDigest("wrong-admission-prefix")
			if phase == "future_revision" {
				revision, digest = input.Reader.(*journalReader).revision+1, latest.CanonicalProjectionDigest()
			}
			var err error
			input.ExpectedProjectionPrefix, err = NewProjectionPrefixV2(revision, digest)
			if err != nil {
				t.Fatal(err)
			}
			result, err := RestoreV2(t.Context(), input)
			if err == nil {
				t.Fatal("unproven historical projection became a sealed restore")
			}
			if _, err := result.FreshEpochAdmissionRequestV2(); err == nil {
				t.Fatal("failed prefix proof could admit an epoch")
			}
		})
	}
	if _, err := NewProjectionPrefixV2(0, journal.Digest{}); err == nil {
		t.Fatal("empty expected digest disabled a requested proof silently")
	}
}
