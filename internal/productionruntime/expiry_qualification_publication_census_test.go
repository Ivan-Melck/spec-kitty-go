//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"bytes"
	"reflect"
	"testing"

	journalv2 "github.com/spec-kitty/go-kitty3/internal/controljournal/v2"
)

// This is a pure fixture-contract regression, NOT a provider/Host expiry witness.
// Use the exact native publication helper with an in-memory commit observer. No
// fake ACK constructor, live daemon, admission tuple or old receipt is involved.
func TestExpiryFixtureContinuationPublicationCensus(t *testing.T) {
	for _, restart := range []bool{false, true} {
		name := "same_epoch"
		if restart {
			name = "successor_epoch"
		}
		t.Run(name, func(t *testing.T) {
			registry := journalv2.DefaultEventRegistryV2()
			reducers, err := BuildReducerSetV2(registry)
			if err != nil {
				t.Fatal(err)
			}
			tracker, err := journalv2.NewProjectionTrackerV2(reducers, journalv2.CanonicalCodecVersionV2)
			if err != nil {
				t.Fatal(err)
			}
			fixture := &publicationProofFixture{records: make(map[journalv2.RecordAddressV2]journalv2.RecoveryRecordV2)}
			coordinator, err := journalv2.NewPublicationCoordinatorWithObserverV2(fixture, tracker, fixture)
			if err != nil {
				t.Fatal(err)
			}
			h := &Host{epoch: "epoch-expiry-census-initial"}
			initial := publishLiveProofOperation(t, h, coordinator, nil)
			if restart {
				h.epoch = "epoch-expiry-census-successor"
			}
			continued := publishLiveProofOperation(t, h, coordinator, &initial)
			if len(fixture.observerACKs) != 2 || fixture.commits != 2 {
				t.Fatal("fixture must use two coordinator-created ACKs")
			}

			wantInitial := map[journalv2.SealedRecordRole]int{
				journalv2.RecordOperationBinding: 1, journalv2.RecordJournalEvent: 1,
				journalv2.RecordOperationHead: 1, journalv2.RecordWorkspacePathClaim: 1,
				journalv2.RecordPublicationWitness: 1,
			}
			wantContinued := map[journalv2.SealedRecordRole]int{
				journalv2.RecordJournalEvent: 1, journalv2.RecordOperationHead: 1,
				journalv2.RecordPublicationWitness: 1,
			}
			byRole := func(ack journalv2.AcknowledgedPublicationV2) map[journalv2.SealedRecordRole][]journalv2.SealedRecordV2 {
				rows := make(map[journalv2.SealedRecordRole][]journalv2.SealedRecordV2)
				for _, row := range ack.Plan().Records() {
					rows[row.Role()] = append(rows[row.Role()], row)
				}
				return rows
			}
			first, next := byRole(initial), byRole(continued)
			for i, test := range []struct {
				rows map[journalv2.SealedRecordRole][]journalv2.SealedRecordV2
				want map[journalv2.SealedRecordRole]int
			}{{first, wantInitial}, {next, wantContinued}} {
				got := make(map[journalv2.SealedRecordRole]int)
				for role, rows := range test.rows {
					got[role] = len(rows)
				}
				if !reflect.DeepEqual(got, test.want) {
					t.Fatalf("publication %d roles=%v want=%v", i, got, test.want)
				}
			}
			oldHead, newHead := first[journalv2.RecordOperationHead][0], next[journalv2.RecordOperationHead][0]
			if oldHead.Target() != newHead.Target() || bytes.Equal(oldHead.CanonicalValue(), newHead.CanonicalValue()) {
				t.Fatal("continuation must overwrite exactly the same head with changed state")
			}
			for _, role := range []journalv2.SealedRecordRole{journalv2.RecordJournalEvent, journalv2.RecordPublicationWitness} {
				if first[role][0].Target() == next[role][0].Target() {
					t.Fatalf("%s must retain both immutable historical addresses", role)
				}
			}
			// 5 initial + 3 continuation - 1 overwritten head = 7, not the
			// initial Host total of 7 publication + 6 private metadata rows.
			for _, test := range []struct {
				name string
				acks []journalv2.AcknowledgedPublicationV2
				want uint64
			}{
				{"empty", nil, 0},
				{"initial", []journalv2.AcknowledgedPublicationV2{initial}, 5},
				{"continuation_only", []journalv2.AcknowledgedPublicationV2{continued}, 3},
				{"complete", fixture.observerACKs, 7},
				{"reversed", []journalv2.AcknowledgedPublicationV2{continued, initial}, 7},
				{"duplicate", []journalv2.AcknowledgedPublicationV2{initial, continued, initial, continued}, 7},
			} {
				t.Run(test.name, func(t *testing.T) {
					if got := expiryFixtureFinalPublicationRows(test.acks); got != test.want {
						t.Fatalf("publication rows=%d want=%d", got, test.want)
					}
				})
			}
			if got := expiryFixtureFinalPublicationRows(fixture.observerACKs); got != expiryFixtureContinuationPublicationRows {
				t.Fatalf("native fixture expects %d publication rows, exact role census proves %d (private metadata is separate)", expiryFixtureContinuationPublicationRows, got)
			}
		})
	}
}
