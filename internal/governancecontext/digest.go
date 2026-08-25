package governancecontext

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"hash"
)

const digestDomain = "go-kitty3/governance-context/v1"

func CommitSHA256(content []byte) (Commitment, error) {
	sum := sha256.Sum256(content)
	return NewCommitment(AlgorithmSHA256, hex.EncodeToString(sum[:]))
}

func ComputeAggregate(schemaVersion uint16, entries []Entry) (Commitment, error) {
	if schemaVersion != SchemaVersion {
		return Commitment{}, &ValidationError{Code: CodeUnsupportedSchema, Field: "schema_version", Message: "unsupported governance context schema version"}
	}
	if len(entries) == 0 {
		return Commitment{}, &ValidationError{Code: CodeEmptyEntries, Field: "entries", Message: "context must contain at least one ordered entry"}
	}
	if err := validateEntries(entries); err != nil {
		return Commitment{}, err
	}
	return computeAggregate(schemaVersion, entries)
}

// computeAggregate is the framing primitive. Callers use ComputeAggregate or
// Context.Validate so invalid values cannot cross a package boundary.
func computeAggregate(schemaVersion uint16, entries []Entry) (Commitment, error) {
	hasher := sha256.New()
	writeFrame(hasher, []byte(digestDomain))
	var schema [2]byte
	binary.BigEndian.PutUint16(schema[:], schemaVersion)
	writeFrame(hasher, schema[:])
	var count [8]byte
	binary.BigEndian.PutUint64(count[:], uint64(len(entries)))
	writeFrame(hasher, count[:])
	for _, entry := range entries {
		writeFrame(hasher, []byte(entry.kind))
		writeFrame(hasher, []byte(entry.ref))
		writeFrame(hasher, []byte(entry.revision))
		writeFrame(hasher, []byte(entry.commitment.algorithm))
		writeFrame(hasher, []byte(entry.commitment.digest))
	}
	return NewCommitment(AlgorithmSHA256, hex.EncodeToString(hasher.Sum(nil)))
}

func writeFrame(destination hash.Hash, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = destination.Write(length[:])
	_, _ = destination.Write(value)
}
