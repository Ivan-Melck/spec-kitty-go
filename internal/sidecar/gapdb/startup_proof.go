package gapdb

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"sync"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/controljournal"
	journalcheckpoint "github.com/spec-kitty/go-kitty3/internal/controljournal/checkpoint"
)

// ProductRestoreProofV1 is mission-specific evidence that checkpoint.RestoreV1
// completed against this admitted database and owner lifetime.
type ProductRestoreProofV1 struct {
	facts journalcheckpoint.RestoreAdmissionFactsV1
	seal  [32]byte
}

func newProductRestoreProofV1(facts journalcheckpoint.RestoreAdmissionFactsV1) ProductRestoreProofV1 {
	return ProductRestoreProofV1{facts: facts, seal: productRestoreSealV1(facts)}
}

// Facts returns a copy only after revalidating the sidecar binding seal.
func (proof ProductRestoreProofV1) Facts() (journalcheckpoint.RestoreAdmissionFactsV1, error) {
	if proof.seal == [32]byte{} || proof.seal != productRestoreSealV1(proof.facts) {
		return journalcheckpoint.RestoreAdmissionFactsV1{}, failure(FailureOwnership, "product_restore_proof", "product restore proof is invalid", nil)
	}
	return proof.facts, nil
}

// OwnerStartupFactsV1 is a copy-only description of the exact supervisor and
// admitted GapDB instance that constitute one product-host lifetime.
type OwnerStartupFactsV1 struct {
	RuntimeEpochID       controljournal.RuntimeEpochID
	HostInstanceID       string
	SupervisorRunID      string
	Supervisor           ProcessIdentity
	Daemon               ProcessIdentity
	ExecutablePath       string
	ExecutableSHA256     string
	DatabaseID           string
	DaemonOwnerStartedAt string
	AdmittedAt           string
}

type ownerStartupKeyV1 struct {
	supervisor ProcessIdentity
	daemon     ProcessIdentity
	databaseID string
}

type ownerStartupStateV1 struct {
	mu    sync.Mutex
	facts OwnerStartupFactsV1
	epoch controljournal.RuntimeEpochID
}

var ownerStartupRegistryV1 = struct {
	sync.Mutex
	states map[ownerStartupKeyV1]*ownerStartupStateV1
}{states: make(map[ownerStartupKeyV1]*ownerStartupStateV1)}

// OwnerStartupProofV1 is constructor-inaccessible authority for the one
// RuntimeEpochID bound to a product-host lifetime. The same proof may be used
// by many mission partitions; it cannot be rebound to another epoch.
type OwnerStartupProofV1 struct {
	state *ownerStartupStateV1
	seal  [32]byte
}

// BindOwnerStartupEpochV1 binds the current Start/Attach lifetime to one opaque
// epoch. Repeating the same binding is idempotent; a different epoch is
// refused. Ordinary client reconnects have no path to this capability.
func (l *Lifecycle) BindOwnerStartupEpochV1(epoch controljournal.RuntimeEpochID) (OwnerStartupProofV1, error) {
	if _, err := controljournal.NewRuntimeEpochID(string(epoch)); err != nil {
		return OwnerStartupProofV1{}, failure(FailureInvalidConfiguration, "bind_owner_epoch", "runtime epoch identity is invalid", err)
	}
	if l == nil {
		return OwnerStartupProofV1{}, failure(FailureOwnership, "bind_owner_epoch", "lifecycle authority is absent", nil)
	}
	l.mu.Lock()
	if l.stopped || !l.provenance.Readiness.Admitted || !l.provenance.Readiness.ProductRecovered ||
		!l.provenance.Readiness.ReadyForProductRecovery || l.provenance.SchemaVersion != 1 {
		l.mu.Unlock()
		return OwnerStartupProofV1{}, failure(FailureOwnership, "bind_owner_epoch", "lifecycle is not an admitted live owner", nil)
	}
	provenance := l.provenance
	l.mu.Unlock()

	supervisor, err := currentExecutableIdentity()
	if err != nil {
		return OwnerStartupProofV1{}, err
	}
	if err := validateStartupProvenanceV1(provenance, supervisor); err != nil {
		return OwnerStartupProofV1{}, err
	}
	key := ownerStartupKeyV1{supervisor: supervisor, daemon: provenance.Process, databaseID: provenance.DatabaseID}
	ownerStartupRegistryV1.Lock()
	state := ownerStartupRegistryV1.states[key]
	if state == nil {
		facts := startupFactsV1(provenance, supervisor)
		state = &ownerStartupStateV1{facts: facts}
		ownerStartupRegistryV1.states[key] = state
	}
	ownerStartupRegistryV1.Unlock()

	state.mu.Lock()
	defer state.mu.Unlock()
	if state.epoch != "" && state.epoch != epoch {
		return OwnerStartupProofV1{}, failure(FailureOwnership, "bind_owner_epoch", "host lifetime is already bound to another runtime epoch", nil)
	}
	state.epoch = epoch
	state.facts.RuntimeEpochID = epoch
	return OwnerStartupProofV1{state: state, seal: ownerStartupSealV1(state.facts)}, nil
}

func (p OwnerStartupProofV1) snapshot() (OwnerStartupFactsV1, error) {
	if p.state == nil || p.seal == [32]byte{} {
		return OwnerStartupFactsV1{}, failure(FailureOwnership, "owner_epoch_proof", "startup proof is absent", nil)
	}
	p.state.mu.Lock()
	defer p.state.mu.Unlock()
	if p.state.epoch == "" || p.state.facts.RuntimeEpochID != p.state.epoch || ownerStartupSealV1(p.state.facts) != p.seal {
		return OwnerStartupFactsV1{}, failure(FailureOwnership, "owner_epoch_proof", "startup proof is invalid", nil)
	}
	return p.state.facts, nil
}

// Facts returns a value copy only after revalidating the capability seal.
func (p OwnerStartupProofV1) Facts() (OwnerStartupFactsV1, error) { return p.snapshot() }

func validateStartupProvenanceV1(provenance SidecarProvenance, supervisor ProcessIdentity) error {
	if provenance.SupervisorRunID == "" || provenance.DatabaseID == "" || provenance.Executable.ExecutablePath == "" ||
		provenance.Executable.SHA256 == "" || provenance.Process.PID <= 0 || provenance.Process.StartTicks == 0 ||
		provenance.Observation.Status.OwnerStartedAt == "" || provenance.AdmittedAt.IsZero() ||
		supervisor.PID <= 0 || supervisor.StartTicks == 0 {
		return failure(FailureIdentityUnprovable, "bind_owner_epoch", "startup provenance is incomplete", nil)
	}
	return nil
}

func startupFactsV1(provenance SidecarProvenance, supervisor ProcessIdentity) OwnerStartupFactsV1 {
	return OwnerStartupFactsV1{
		HostInstanceID:       hostInstanceIDV1(supervisor, provenance.Process, provenance.DatabaseID),
		SupervisorRunID:      provenance.SupervisorRunID,
		Supervisor:           supervisor,
		Daemon:               provenance.Process,
		ExecutablePath:       provenance.Executable.ExecutablePath,
		ExecutableSHA256:     provenance.Executable.SHA256,
		DatabaseID:           provenance.DatabaseID,
		DaemonOwnerStartedAt: provenance.Observation.Status.OwnerStartedAt,
		AdmittedAt:           provenance.AdmittedAt.UTC().Format(time.RFC3339Nano),
	}
}

func hostInstanceIDV1(supervisor, daemon ProcessIdentity, databaseID string) string {
	h := sha256.New()
	writeStartupIdentityV1(h, supervisor)
	writeStartupIdentityV1(h, daemon)
	writeStartupPartV1(h, []byte(databaseID))
	return "host-" + hex.EncodeToString(h.Sum(nil)[:16])
}

func ownerStartupSealV1(facts OwnerStartupFactsV1) [32]byte {
	h := sha256.New()
	writeStartupPartV1(h, []byte("owner-startup-proof-v1"))
	writeStartupPartV1(h, []byte(facts.RuntimeEpochID))
	writeStartupPartV1(h, []byte(facts.HostInstanceID))
	writeStartupPartV1(h, []byte(facts.SupervisorRunID))
	writeStartupIdentityV1(h, facts.Supervisor)
	writeStartupIdentityV1(h, facts.Daemon)
	writeStartupPartV1(h, []byte(facts.ExecutablePath))
	writeStartupPartV1(h, []byte(facts.ExecutableSHA256))
	writeStartupPartV1(h, []byte(facts.DatabaseID))
	writeStartupPartV1(h, []byte(facts.DaemonOwnerStartedAt))
	writeStartupPartV1(h, []byte(facts.AdmittedAt))
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

func productRestoreSealV1(facts journalcheckpoint.RestoreAdmissionFactsV1) [32]byte {
	h := sha256.New()
	writeStartupPartV1(h, []byte("product-restore-proof-v1"))
	writeStartupPartV1(h, []byte(facts.Source.ProjectID))
	writeStartupPartV1(h, []byte(facts.Source.MissionID))
	writeStartupPartV1(h, []byte(facts.Source.RunID))
	writeStartupPartV1(h, facts.Source.ReducerSetDigest[:])
	writeStartupPartV1(h, facts.Source.EventSchemaSetDigest[:])
	writeStartupPartV1(h, []byte(facts.Binding.DatabaseID))
	writeStartupPartV1(h, []byte(facts.Binding.HostInstanceID))
	var numeric [8]byte
	binary.BigEndian.PutUint64(numeric[:], facts.ObservedStoreRevision)
	writeStartupPartV1(h, numeric[:])
	binary.BigEndian.PutUint64(numeric[:], facts.TerminalWatermark.ThroughRecordRevision)
	writeStartupPartV1(h, numeric[:])
	writeStartupPartV1(h, []byte(facts.TerminalWatermark.ThroughEventID))
	binary.BigEndian.PutUint64(numeric[:], facts.TerminalWatermark.EventCount)
	writeStartupPartV1(h, numeric[:])
	writeStartupPartV1(h, facts.TerminalWatermark.EventPrefixDigest[:])
	if facts.Pristine {
		writeStartupPartV1(h, []byte{1})
	} else {
		writeStartupPartV1(h, []byte{0})
	}
	writeStartupPartV1(h, facts.CanonicalProjectionDigest[:])
	writeStartupPartV1(h, []byte(facts.SelectedCheckpointID))
	writeStartupPartV1(h, facts.SelectedCheckpointDigest[:])
	var result [32]byte
	copy(result[:], h.Sum(nil))
	return result
}

func writeStartupIdentityV1(h interface{ Write([]byte) (int, error) }, identity ProcessIdentity) {
	var numeric [8]byte
	binary.BigEndian.PutUint64(numeric[:], uint64(identity.PID))
	_, _ = h.Write(numeric[:])
	binary.BigEndian.PutUint64(numeric[:], identity.StartTicks)
	_, _ = h.Write(numeric[:])
	binary.BigEndian.PutUint64(numeric[:], identity.Device)
	_, _ = h.Write(numeric[:])
	binary.BigEndian.PutUint64(numeric[:], identity.Inode)
	_, _ = h.Write(numeric[:])
	binary.BigEndian.PutUint64(numeric[:], uint64(identity.Size))
	_, _ = h.Write(numeric[:])
	writeStartupPartV1(h, []byte(identity.SHA256))
}

func writeStartupPartV1(h interface{ Write([]byte) (int, error) }, value []byte) {
	var length [8]byte
	binary.BigEndian.PutUint64(length[:], uint64(len(value)))
	_, _ = h.Write(length[:])
	_, _ = h.Write(value)
}
