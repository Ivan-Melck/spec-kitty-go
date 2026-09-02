package controlroot

import (
	"bytes"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

const SQLiteHeader = "SQLite format 3\x00"

var historicalSignatureNames = []string{
	"control.db",
	"control.db-journal",
	"control.db-shm",
	"control.db-wal",
}

const (
	gapIdentityName = "IDENTITY"
	gapManifestName = "CURRENT"
	gapLockName     = "LOCK"
	gapAuditName    = "audit.jsonl"
	gapIdentitySize = 64
	gapHeaderSize   = 64
	gapVersion      = uint16(1)
)

var castagnoli = crc32.MakeTable(crc32.Castagnoli)

// HistoricalSignatureNames returns the exact closed legacy layout census.
func HistoricalSignatureNames() []string {
	result := make([]string, len(historicalSignatureNames))
	copy(result, historicalSignatureNames)
	return result
}

func hasSQLiteHeader(value []byte) bool {
	return len(value) >= len(SQLiteHeader) && bytes.Equal(value[:len(SQLiteHeader)], []byte(SQLiteHeader))
}

func hasSQLiteWALHeader(value []byte) bool {
	if len(value) < 4 {
		return false
	}
	magic := binary.BigEndian.Uint32(value[:4])
	return magic == 0x377f0682 || magic == 0x377f0683
}

func historicalName(relativePath string) (base bool, companion bool) {
	name := filepath.Base(relativePath)
	switch name {
	case historicalSignatureNames[0]:
		return true, false
	case historicalSignatureNames[1], historicalSignatureNames[2], historicalSignatureNames[3]:
		return false, true
	default:
		return false, false
	}
}

func isCurrentMarkerName(name string) bool {
	if name == gapIdentityName || name == gapManifestName || name == gapLockName || name == gapAuditName || strings.HasPrefix(name, gapAuditName+".") || strings.HasSuffix(name, ".sock") {
		return true
	}
	_, snapshot := parseRevisionFilename(name, "snapshot-")
	_, wal := parseRevisionFilename(name, "wal-")
	return snapshot || wal
}

func parseRevisionFilename(name, prefix string) (uint64, bool) {
	if len(name) != len(prefix)+20+len(".gdb") || !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".gdb") {
		return 0, false
	}
	digits := name[len(prefix) : len(prefix)+20]
	for _, c := range digits {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	value, err := strconv.ParseUint(digits, 10, 64)
	return value, err == nil
}

type gapIdentity struct {
	DatabaseID [16]byte
	Generation uint64
}

func decodeGapIdentity(value []byte) (gapIdentity, error) {
	if len(value) != gapIdentitySize {
		return gapIdentity{}, fmt.Errorf("identity size=%d", len(value))
	}
	if string(value[:8]) != "GAPID001" || binary.BigEndian.Uint16(value[8:10]) != gapVersion || binary.BigEndian.Uint16(value[10:12]) != gapIdentitySize {
		return gapIdentity{}, errors.New("identity header is invalid")
	}
	if !allZero(value[44:60]) || binary.BigEndian.Uint32(value[60:64]) != crc32.Checksum(value[:60], castagnoli) {
		return gapIdentity{}, errors.New("identity reserved bytes or checksum are invalid")
	}
	var identity gapIdentity
	copy(identity.DatabaseID[:], value[12:28])
	identity.Generation = binary.BigEndian.Uint64(value[36:44])
	if allZero(identity.DatabaseID[:]) || identity.Generation == 0 {
		return gapIdentity{}, errors.New("identity authority fields are zero")
	}
	return identity, nil
}

type gapManifest struct {
	DatabaseID       string `json:"database_id"`
	Generation       uint64 `json:"generation"`
	SnapshotFile     string `json:"snapshot_file"`
	SnapshotRevision uint64 `json:"snapshot_revision"`
	SnapshotSHA256   string `json:"snapshot_sha256"`
	WALFile          string `json:"wal_file"`
	WALStartRevision uint64 `json:"wal_start_revision"`
}

func decodeGapManifest(value []byte) (gapManifest, error) {
	const header = 16
	const trailer = 4
	if len(value) < header+trailer || string(value[:8]) != "GAPCUR01" || binary.BigEndian.Uint16(value[8:10]) != gapVersion || binary.BigEndian.Uint16(value[10:12]) != 0 {
		return gapManifest{}, errors.New("manifest header is invalid")
	}
	payloadLength := int(binary.BigEndian.Uint32(value[12:16]))
	if payloadLength > 4096 || payloadLength+header+trailer != len(value) {
		return gapManifest{}, errors.New("manifest payload length is invalid")
	}
	if binary.BigEndian.Uint32(value[len(value)-trailer:]) != crc32.Checksum(value[:len(value)-trailer], castagnoli) {
		return gapManifest{}, errors.New("manifest checksum is invalid")
	}
	var manifest gapManifest
	decoder := json.NewDecoder(bytes.NewReader(value[header : len(value)-trailer]))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&manifest); err != nil {
		return gapManifest{}, errors.New("manifest payload is invalid")
	}
	if err := ensureJSONEOF(decoder); err != nil {
		return gapManifest{}, err
	}
	if len(manifest.DatabaseID) != 32 || strings.ToLower(manifest.DatabaseID) != manifest.DatabaseID || !isLowerHex(manifest.DatabaseID) || manifest.Generation == 0 || !isLowerHexDigest(manifest.SnapshotSHA256) {
		return gapManifest{}, errors.New("manifest authority fields are invalid")
	}
	snapshotRevision, snapshotOK := parseRevisionFilename(manifest.SnapshotFile, "snapshot-")
	walRevision, walOK := parseRevisionFilename(manifest.WALFile, "wal-")
	if !snapshotOK || !walOK || snapshotRevision != manifest.SnapshotRevision || walRevision != manifest.WALStartRevision || manifest.WALStartRevision == 0 {
		return gapManifest{}, errors.New("manifest filenames and revisions disagree")
	}
	return manifest, nil
}

func ensureJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		return errors.New("manifest contains trailing data")
	}
	return nil
}

func validateGapSnapshotHeader(value []byte, identity [16]byte, revision uint64) error {
	if len(value) < gapHeaderSize || string(value[:8]) != "GAPSNAP1" || binary.BigEndian.Uint16(value[8:10]) != gapVersion || binary.BigEndian.Uint16(value[10:12]) != gapHeaderSize {
		return errors.New("snapshot header is invalid")
	}
	if !bytes.Equal(value[12:28], identity[:]) || binary.BigEndian.Uint64(value[28:36]) != revision || !allZero(value[52:64]) {
		return errors.New("snapshot authority fields disagree")
	}
	return nil
}

func validateGapWALHeader(value []byte, identity [16]byte, firstRevision uint64, generation uint64) error {
	if len(value) < gapHeaderSize || string(value[:8]) != "GAPWAL01" || binary.BigEndian.Uint16(value[8:10]) != gapVersion || binary.BigEndian.Uint16(value[10:12]) != gapHeaderSize {
		return errors.New("WAL header is invalid")
	}
	if !bytes.Equal(value[12:28], identity[:]) || binary.BigEndian.Uint64(value[28:36]) != firstRevision || binary.BigEndian.Uint64(value[36:44]) != generation || !allZero(value[44:60]) || binary.BigEndian.Uint32(value[60:64]) != crc32.Checksum(value[:60], castagnoli) {
		return errors.New("WAL authority fields disagree")
	}
	return nil
}

func coherentCurrentGroup(files map[string]fileObservation) (string, error) {
	identityFile, identityOK := files[gapIdentityName]
	manifestFile, manifestOK := files[gapManifestName]
	if !identityOK || !manifestOK {
		return "", errors.New("identity and manifest must both exist")
	}
	identity, err := decodeGapIdentity(identityFile.Prefix)
	if err != nil {
		return "", err
	}
	manifest, err := decodeGapManifest(manifestFile.Prefix)
	if err != nil {
		return "", err
	}
	if hex.EncodeToString(identity.DatabaseID[:]) != manifest.DatabaseID || identity.Generation != manifest.Generation {
		return "", errors.New("identity and manifest authority disagree")
	}
	snapshot, snapshotOK := files[manifest.SnapshotFile]
	wal, walOK := files[manifest.WALFile]
	if !snapshotOK || !walOK {
		return "", errors.New("manifest-referenced snapshot and WAL must exist")
	}
	if err := validateGapSnapshotHeader(snapshot.Prefix, identity.DatabaseID, manifest.SnapshotRevision); err != nil {
		return "", err
	}
	if err := validateGapWALHeader(wal.Prefix, identity.DatabaseID, manifest.WALStartRevision, manifest.Generation); err != nil {
		return "", err
	}
	return manifest.DatabaseID, nil
}

func allZero(value []byte) bool {
	for _, b := range value {
		if b != 0 {
			return false
		}
	}
	return true
}

func isLowerHex(value string) bool {
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return value != ""
}
