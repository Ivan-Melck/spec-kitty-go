package agents

import "github.com/spec-kitty/go-kitty3/internal/kittify"

// Register the agent registry with kittify's agents-section validation.
func init() {
	kittify.KnownAgent = IsKnown
	kittify.ValidAgentKeys = func() []string { return append([]string(nil), Keys...) }
}
