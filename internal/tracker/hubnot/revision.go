package hubnot

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"time"
)

func observationProof(externalID, snapshot string, value any) (tracker.ProviderObservation, error) {
	raw, err := json.Marshal(value)
	if err != nil {
		return tracker.ProviderObservation{}, protocolFailure(err)
	}
	raw, err = canonicalPayloadJSON(raw)
	if err != nil {
		return tracker.ProviderObservation{}, protocolFailure(err)
	}
	digest := sha256.Sum256(raw)
	revision := tracker.Revision(binary.BigEndian.Uint64(digest[:8]))
	if revision == 0 {
		revision = 1
	}
	return tracker.NewProviderObservation(externalID, raw, revision, hex.EncodeToString(digest[:]), snapshot, time.Now().UTC())
}
