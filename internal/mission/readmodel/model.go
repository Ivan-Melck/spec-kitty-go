// Package readmodel exposes immutable mission facts to presentation-only code.
package readmodel

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/ids"
)

const maxStepLength = 128

// SnapshotParams contains the canonical mission facts needed by projections.
type SnapshotParams struct {
	MissionID             ids.MissionID
	StateVersion          uint64
	CurrentStep           string
	CurrentWorkRevisionID ids.WorkRevisionID
}

// Snapshot is an immutable, authority-free view of one mission state version.
type Snapshot struct {
	missionID             ids.MissionID
	stateVersion          uint64
	currentStep           string
	currentWorkRevisionID ids.WorkRevisionID
}

// NewSnapshot validates and copies canonical mission facts.
func NewSnapshot(params SnapshotParams) (Snapshot, error) {
	if _, err := ids.ParseMissionID(params.MissionID.String()); err != nil {
		return Snapshot{}, fmt.Errorf("mission read model: invalid mission identity")
	}
	if params.StateVersion == 0 {
		return Snapshot{}, fmt.Errorf("mission read model: state version must be positive")
	}
	if !safeLabel(params.CurrentStep, maxStepLength) {
		return Snapshot{}, fmt.Errorf("mission read model: invalid current step")
	}
	if params.CurrentWorkRevisionID != "" {
		if _, err := ids.ParseWorkRevisionID(params.CurrentWorkRevisionID.String()); err != nil {
			return Snapshot{}, fmt.Errorf("mission read model: invalid work revision identity")
		}
	}
	return Snapshot{
		missionID:             params.MissionID,
		stateVersion:          params.StateVersion,
		currentStep:           params.CurrentStep,
		currentWorkRevisionID: params.CurrentWorkRevisionID,
	}, nil
}

func (snapshot Snapshot) MissionID() ids.MissionID { return snapshot.missionID }
func (snapshot Snapshot) StateVersion() uint64     { return snapshot.stateVersion }
func (snapshot Snapshot) CurrentStep() string      { return snapshot.currentStep }
func (snapshot Snapshot) CurrentWorkRevisionID() ids.WorkRevisionID {
	return snapshot.currentWorkRevisionID
}

// Validate rejects zero values and values restored without the constructor.
func (snapshot Snapshot) Validate() error {
	_, err := NewSnapshot(SnapshotParams{
		MissionID:             snapshot.missionID,
		StateVersion:          snapshot.stateVersion,
		CurrentStep:           snapshot.currentStep,
		CurrentWorkRevisionID: snapshot.currentWorkRevisionID,
	})
	return err
}

func safeLabel(value string, limit int) bool {
	if value == "" || len(value) > limit || !utf8.ValidString(value) || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}
