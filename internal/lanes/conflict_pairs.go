package lanes

// Pairs returns the (wp_id, recorded_lane) pairs a conflict names (used to
// word refusals; not part of its JSON form). Added for finalize-tasks'
// LANE_MEMBERSHIP_FROZEN console rendering (planning area, additive).
func (c *MembershipConflict) Pairs() [][2]string { return c.pairs }

// IsEmpty is FrozenLaneMembership.is_empty: no bindings, no retired work
// packages and no reserved lane ids.
func (f *FrozenMembership) IsEmpty() bool {
	return f == nil || (len(f.Bindings) == 0 && len(f.Retired) == 0 && len(f.ReservedIDs) == 0)
}
