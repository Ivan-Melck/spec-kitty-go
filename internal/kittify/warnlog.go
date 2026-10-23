package kittify

// WarnLog receives the logger.warning records of this package's upstream
// counterparts (load_agent_config's "Config file not found: <path>"). The
// CLI logging bootstrap installs a stderr sink in human mode; nil (--json,
// tests) discards them.
var WarnLog func(msg string)

// quietAgentConfig > 0 suppresses LoadAgentConfig's warning (loads with no
// upstream load_agent_config counterpart).
var quietAgentConfig int

// QuietAgentConfig runs fn with LoadAgentConfig's missing-config warning
// suppressed.
func QuietAgentConfig(fn func()) {
	quietAgentConfig++
	defer func() { quietAgentConfig-- }()
	fn()
}

// AgentConfigMissingWarning logs n extra "Config file not found" records for
// upstream load_agent_config calls the Go command folds into fewer loads.
func AgentConfigMissingWarning(root string, n int) {
	if WarnLog == nil || quietAgentConfig > 0 || ConfigExists(root) {
		return
	}
	for i := 0; i < n; i++ {
		WarnLog("Config file not found: " + ConfigPath(root))
	}
}
