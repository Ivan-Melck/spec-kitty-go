package coord

// HatchActive reports whether SPEC_KITTY_ALLOW_PROTECTED_BRANCH_COMMITS is on
// (ProtectionPolicy.operator_hatch_active).
func HatchActive() bool { return hatchActive() }
