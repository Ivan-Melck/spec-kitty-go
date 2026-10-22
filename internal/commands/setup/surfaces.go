package setup

import (
	"errors"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/charter"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
)

// RegeneratedSurfaceLine is tool_surface.repair.REGENERATED_SURFACE_LINE.
func RegeneratedSurfaceLine(path string) string {
	return "Regenerated a generated command skill that differed from the current version: " + path
}

func init() { agents.ProfileAdmit = admitProfile }

// activationProvisioningPending is _mission_type_activation_provisioning_pending.
func activationProvisioningPending(root string) bool {
	t, err := charter.LoadWriteTarget(root)
	if err != nil {
		var ce *dct.ConfigError
		if errors.As(err, &ce) {
			return true
		}
		return false
	}
	return !t.Data.Has("mission_type_activations")
}

// admitProfile is the active charter service's profile gate: the activation
// list (activated_agent_profiles) when present, then the language scope
// (applies_to_languages against infer_repo_languages; no signal admits all).
func admitProfile(root string, p agents.Profile) bool {
	if lists, present, err := charter.ListActivated(root); err == nil && present["agent-profile"] {
		found := false
		for _, id := range lists["agent-profile"] {
			found = found || id == p.ID
		}
		if !found {
			return false
		}
	}
	if len(p.AppliesToLanguages) == 0 {
		return true
	}
	langs, known := charter.InferRepoLanguages(root, nil, false)
	if !known {
		return true
	}
	have := map[string]bool{}
	for _, l := range charter.NormalizeLanguages(langs) {
		have[l] = true
	}
	for _, l := range charter.NormalizeLanguages(p.AppliesToLanguages) {
		if have[l] {
			return true
		}
	}
	return false
}

// presenceContent builds the orientation content for root.
func presenceContent(root string) agents.Presence {
	slug := kittify.LoadIdentity(root).Slug
	if slug == "" {
		slug = "unknown"
	}
	// SessionPresenceManager._build_content: the cached available version
	// (refreshed in the background) and the compat health.
	avail := upgradeCheckerAvailable()
	if avail == "" {
		upgradeCheckInBackground()
	}
	c := agents.Presence{Version: app.Version, ProjectSlug: slug, Health: "healthy", AvailableVersion: avail}
	inv := Invocation{CommandPath: []string{"session-start"}, RawArgs: []string{"session-start"}, FlagNoNag: true}
	switch {
	case PlanCompat(inv, root).Decision == DecisionBlockMigration:
		c.Health = "migration-required"
	case isVersionNewer(avail, app.Version):
		c.Health = "upgrade-available"
	}
	return c
}

// InstallPresence is SessionPresenceManager.install: writes orientation for
// configured agents lacking it; returns the change lines.
func InstallPresence(root string, keys []string) []string {
	c := presenceContent(root)
	var changes []string
	for _, k := range keys {
		w := agents.WriterFor(k)
		if w.CanWrite(root) && !w.HasPresence(root) {
			if err := w.WriteOrientation(root, c); err == nil {
				changes = append(changes, "Wrote orientation for "+k)
			}
		}
	}
	return changes
}
