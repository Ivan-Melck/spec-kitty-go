package orchestrator

// contextDigest is design.context.planning_context_digest.
func (d *design) contextDigest(action string) (string, error) {
	return "", derr("DESIGN_CONTEXT_FAILED", "design context is not supported by this build")
}
