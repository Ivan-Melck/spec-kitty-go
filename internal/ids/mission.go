package ids

// MissionDefinitionID identifies one statically registered mission method.
// It is intentionally distinct from the identity of a mission instance.
type MissionDefinitionID string

// DecisionID identifies one durable mission-scoped decision.
type DecisionID string

// BlockerID identifies one durable mission-scoped blocker.
type BlockerID string

// MissionEventID identifies one durable lifecycle event.
type MissionEventID string

// EvidenceID identifies one bounded, typed gate-evidence reference. The
// evidence body belongs to the capability that produced it.
type EvidenceID string

func (v MissionDefinitionID) String() string { return string(v) }
func (v DecisionID) String() string          { return string(v) }
func (v BlockerID) String() string           { return string(v) }
func (v MissionEventID) String() string      { return string(v) }
func (v EvidenceID) String() string          { return string(v) }

func ParseMissionDefinitionID(v string) (MissionDefinitionID, error) {
	return MissionDefinitionID(v), validate("MissionDefinitionID", v)
}

func ParseDecisionID(v string) (DecisionID, error) {
	return DecisionID(v), validate("DecisionID", v)
}

func ParseBlockerID(v string) (BlockerID, error) {
	return BlockerID(v), validate("BlockerID", v)
}

func ParseMissionEventID(v string) (MissionEventID, error) {
	return MissionEventID(v), validate("MissionEventID", v)
}

func ParseEvidenceID(v string) (EvidenceID, error) {
	return EvidenceID(v), validate("EvidenceID", v)
}
