package setup

// upgrade --agent-check / --agent-choice (cli/commands/upgrade.py
// _agent_check_payload, _record_agent_choice; readiness/upgrade_ux.py).

import (
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// isVersionNewer is version_checker.is_version_newer (None/unparseable → false).
func isVersionNewer(latest, installed string) bool {
	if latest == "" {
		return false
	}
	c, ok := versionCmp(latest, installed)
	return ok && c > 0
}

// agentCheckPayload is _agent_check_payload.
func agentCheckPayload() *jsonx.Object {
	now := clock.Now().UTC()
	inv := Invocation{CommandPath: []string{"upgrade"}, RawArgs: []string{"--agent-check", "--json"}, EnvCI: isCIEnv(), StdoutTTY: true}
	p := PlanCompatWith(inv, "", CompatOptions{Now: now})
	st := p.CLI
	var latest any
	if st.latest != "" {
		latest = st.latest
	}
	payload := jsonx.NewObject("schema_version", 1, "action", "none", "installed_version", st.installed, "latest_version", latest,
		"latest_source", st.source, "install_method", "unknown", "upgrade_command", nil, "upgrade_note", unknownInstallNote, "reason", "up_to_date")
	if !loadUpgradeConfig().nagEnabled {
		payload.Set("reason", "nag_disabled")
		return payload
	}
	if !isVersionNewer(st.latest, st.installed) {
		return payload
	}
	if isTruthy(os.Getenv("SPEC_KITTY_UPGRADE_DISABLED")) {
		payload.Set("reason", "upgrade_disabled")
		return payload
	}
	existing := readNagCache()
	seen := ""
	if existing != nil {
		seen = existing.RemoteVersionSeen
	}
	reset := st.latest != "" && seen != st.latest
	var snoozed *time.Time
	neverAsk, always := false, false
	if existing != nil {
		always = existing.AlwaysUpgrade
		if !reset {
			snoozed, neverAsk = existing.SnoozedUntil, existing.NeverAsk
		}
	}
	neverAsk = neverAsk || isTruthy(os.Getenv("SPEC_KITTY_UPGRADE_NEVER_ASK"))
	always = always || isTruthy(os.Getenv("SPEC_KITTY_UPGRADE_AUTO"))
	switch {
	case neverAsk:
		payload.Set("reason", "never_ask")
	case snoozed != nil && now.Before(*snoozed):
		payload.Set("reason", "snoozed")
	case always:
		// An unknown install method is never safe to auto-upgrade and has
		// no upgrade command.
		payload.Set("action", "guidance")
		payload.Set("reason", "always_upgrade")
	default:
		payload.Set("action", "guidance")
		payload.Set("reason", "manual_upgrade_required")
	}
	return payload
}

func runAgentCheck(w io.Writer, jsonOut bool) error {
	payload := agentCheckPayload()
	if jsonOut {
		fmt.Fprintln(w, prettyJSON(payload))
		return nil
	}
	if payload.GetString("action") != "none" {
		latest, installed := payload.GetString("latest_version"), payload.GetString("installed_version")
		if latest == "" {
			latest = "unknown"
		}
		if installed == "" {
			installed = "unknown"
		}
		fmt.Fprintf(w, "Spec Kitty upgrade available: %s -> %s\n", installed, latest)
	}
	return nil
}

var snoozeCadence = map[string]struct {
	next string
	secs int
}{"": {"24h", 24 * 3600}, "24h": {"48h", 48 * 3600}, "48h": {"7d", 7 * 24 * 3600}, "7d": {"7d", 7 * 24 * 3600}}

// recordAgentChoice is _record_agent_choice + upgrade_ux.apply_choice.
func recordAgentChoice(choice, latest string) {
	now := clock.Now().UTC()
	existing := readNagCache()
	reset := existing != nil && latest != "" && existing.RemoteVersionSeen != latest
	r := &nagRecord{CLIVersionKey: app.Version, LatestVersion: latest, LatestSource: "pypi", FetchedAt: now, LastShownAt: &now}
	if existing != nil {
		r.AlwaysUpgrade = existing.AlwaysUpgrade
		if !reset {
			r.RemoteVersionSeen, r.SnoozeStep, r.SnoozedUntil, r.NeverAsk = existing.RemoteVersionSeen, existing.SnoozeStep, existing.SnoozedUntil, existing.NeverAsk
		}
	}
	if latest != "" {
		r.RemoteVersionSeen = latest
	}
	switch choice {
	case "upgrade_now":
		r.SnoozeStep, r.SnoozedUntil = "", nil
	case "always":
		r.AlwaysUpgrade, r.SnoozeStep, r.SnoozedUntil = true, "", nil
	case "not_now":
		c := snoozeCadence[r.SnoozeStep]
		until := now.Add(time.Duration(c.secs) * time.Second)
		r.SnoozeStep, r.SnoozedUntil = c.next, &until
	case "never_ask":
		r.NeverAsk, r.SnoozeStep, r.SnoozedUntil = true, "", nil
	}
	writeNagCache(r)
}
