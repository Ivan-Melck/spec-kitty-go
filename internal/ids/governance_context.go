package ids

// GovernanceContextID identifies one immutable, run-owned governance context.
// It is intentionally distinct from run, lease, work, and operation identities.
type GovernanceContextID string

func (v GovernanceContextID) String() string { return string(v) }

func ParseGovernanceContextID(v string) (GovernanceContextID, error) {
	return GovernanceContextID(v), validate("GovernanceContextID", v)
}
