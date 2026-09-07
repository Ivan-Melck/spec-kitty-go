package productionruntime

import journalrestore "github.com/spec-kitty/go-kitty3/internal/controljournal/v2/restore"

// legacyRecoveryReaderV2 deliberately exposes only the provider-neutral
// recovery contract supported by the adopted GapDB authority. Embedding the
// interface, rather than the concrete adapter, prevents RestoreV2 from
// discovering candidate-only wire accelerators through a dynamic type check.
type legacyRecoveryReaderV2 struct {
	journalrestore.ReaderV2
}

// recoveryReaderForModeV2 selects the wire contract before any recovery read
// or mutation. qualificationCandidate is true only after the M2 candidate
// admission record has been verified by the composition root. There is no
// error-based downgrade: a selected contract that fails remains a failure.
func recoveryReaderForModeV2(reader journalrestore.ReaderV2, qualificationCandidate bool) journalrestore.ReaderV2 {
	if qualificationCandidate {
		return reader
	}
	return legacyRecoveryReaderV2{ReaderV2: reader}
}

var _ journalrestore.ReaderV2 = legacyRecoveryReaderV2{}
