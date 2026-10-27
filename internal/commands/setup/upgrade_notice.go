package setup

// Interactive upgrade notices: the compat nag rendered by the root callback
// (cli/helpers._render_nag_if_needed), the "no upgrade available" notifier
// (core/upgrade_notifier + core/upgrade_probe) and the session-presence
// upgrade cache (session_presence/upgrade_check). All of them are
// TTY-only or opted out by SPEC_KITTY_NO_UPGRADE_CHECK.

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/rich"
)

// refreshUpgradeCacheEnv re-enters the binary as session_presence.
// upgrade_check's background refresher (`python -m ...upgrade_check`).
const refreshUpgradeCacheEnv = "SPEC_KITTY_GO_REFRESH_UPGRADE_CACHE"

func init() {
	if os.Getenv(refreshUpgradeCacheEnv) == "1" {
		refreshUpgradeCacheOnce()
		os.Exit(0)
	}
}

// shouldSuppressNag is cli/helpers._should_suppress_nag.
func shouldSuppressNag(argv []string) bool {
	for _, a := range argv {
		switch a {
		case "--no-nag", "--json", "--plan-json", "--quiet", "--help", "-h", "--version", "-v":
			return true
		}
	}
	if isCIEnv() {
		return true
	}
	if v := os.Getenv("SPEC_KITTY_NO_NAG"); v != "" {
		switch strings.ToLower(v) {
		case "0", "false", "no", "off":
		default:
			return true
		}
	}
	return !stdoutIsTTY()
}

// invocationFromArgv is compat.Invocation.from_argv.
func invocationFromArgv(argv []string) Invocation {
	inv := Invocation{EnvCI: isCIEnv(), StdoutTTY: stdoutIsTTY()}
	passed := false
	for _, a := range argv {
		switch a {
		case "--help", "-h":
			inv.IsHelp = true
		case "--no-nag":
			inv.FlagNoNag = true
		}
		if !passed && !strings.HasPrefix(a, "-") {
			inv.CommandPath = append(inv.CommandPath, a)
		} else {
			passed = true
			inv.RawArgs = append(inv.RawArgs, a)
		}
	}
	inv.IsVersion = len(argv) > 0 && (argv[0] == "--version" || argv[0] == "-v")
	return inv
}

// rootUpgradeNotices is the readiness/nag half of helpers.callback, run
// from main_callback's root_callback for ordinary commands.
func rootUpgradeNotices(ctx *cli.Context, argv []string) {
	pos := positionals(argv)
	first := ""
	if len(pos) > 0 {
		first = pos[0]
	}
	switch {
	case first == "upgrade", first == "next", first == "session-start":
		return
	case first == "live-work" && len(pos) > 1 && pos[1] == "hook":
		return
	}
	if shouldSuppressNag(argv) {
		return
	}
	renderNagIfNeeded(ctx, argv)
	name := first
	if name == "" {
		name = "help"
	}
	if name != "init" && name != "upgrade" && name != "version" && name != "help" {
		maybeEmitUpgradeNotice(ctx.Stdout)
	}
}

// renderNagIfNeeded is helpers._render_nag_if_needed.
func renderNagIfNeeded(ctx *cli.Context, argv []string) {
	inv := invocationFromArgv(argv)
	if inv.suppressesNag() {
		return
	}
	p := PlanCompat(inv, locateProject(mustGetwd()))
	if p.Decision != DecisionAllowWithNag {
		return
	}
	// CliConsole(stderr=True, color_system="auto" on a TTY).print(message):
	// markup, repr highlighting and wrapping at the console width.
	if msg := strings.TrimRight(p.Human, " \t\r\n"); msg != "" {
		rich.Print(ctx.Stderr, msg)
	}
	markNagShown(p.CLI)
}

func mustGetwd() string {
	d, _ := os.Getwd()
	return d
}

// ---------------------------------------------------------------------------
// core/upgrade_probe + core/upgrade_notifier
// ---------------------------------------------------------------------------

type probeResult struct {
	Installed string   `json:"installed_version"`
	Latest    *string  `json:"latest_pypi_version"`
	Channel   string   `json:"channel"`
	ProbedAt  string   `json:"probed_at"`
	Error     *string  `json:"error"`
	Releases  []string `json:"releases"`
}

// probePyPI is upgrade_probe.probe_pypi.
func probePyPI(cliVersion string) probeResult {
	now := clock.ISO(clock.Now().UTC())
	unknown := func(e string) probeResult {
		return probeResult{Installed: cliVersion, Channel: "unknown", ProbedAt: now, Error: &e, Releases: []string{}}
	}
	url := "https://pypi.org/pypi/spec-kitty-cli/json"
	if v := os.Getenv(pypiURLEnv); v != "" {
		url = fmt.Sprintf(v, cliPackageName)
	}
	client := &http.Client{Timeout: 2 * time.Second}
	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set("User-Agent", "spec-kitty-cli/"+cliVersion+" (https://github.com/spec-kitty/spec-kitty)")
	resp, err := client.Do(req)
	if err != nil {
		return unknown(err.Error())
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return unknown(fmt.Sprintf("HTTPStatusError: HTTP %d", resp.StatusCode))
	}
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return unknown(err.Error())
	}
	var payload map[string]any
	if err := json.Unmarshal(raw, &payload); err != nil {
		return unknown("JSONDecodeError: " + err.Error())
	}
	info, _ := payload["info"].(map[string]any)
	latest, _ := info["version"].(string)
	if latest == "" {
		return unknown("PyPI response missing info.version")
	}
	rel := map[string]any{}
	if v, present := payload["releases"]; present && v != nil {
		r, ok := v.(map[string]any)
		if !ok {
			return unknown("PyPI response releases is not an object")
		}
		rel = r
	}
	releases := make([]string, 0, len(rel))
	var installable []string
	for k, v := range rel {
		releases = append(releases, k)
		if files, ok := v.([]any); ok {
			for _, f := range files {
				if fo, ok := f.(map[string]any); ok {
					if y, _ := fo["yanked"].(bool); !y {
						installable = append(installable, k)
						break
					}
				}
			}
		}
	}
	chLatest := latest
	if prereleaseEnabled() {
		if h := highestVersion(append([]string{latest}, installable...), true); h != "" {
			chLatest = h
		}
	}
	return probeResult{Installed: cliVersion, Latest: &chLatest, Channel: classifyProbe(cliVersion, chLatest, releases), ProbedAt: now, Releases: releases}
}

func classifyProbe(installed, latest string, releases []string) string {
	if _, ok := parsePEP440(installed); !ok {
		return "unknown"
	}
	if c, ok := versionCmp(installed, latest); ok && c == 0 {
		return "already_current"
	}
	if isVersionNewer(installed, latest) {
		return "ahead_of_pypi"
	}
	for _, r := range releases {
		if r == installed {
			return "upgrade_available"
		}
	}
	return "no_upgrade_path"
}

func upgradeCheckCachePath() string { return filepath.Join(userCacheDir(), "upgrade-check.json") }

// maybeEmitUpgradeNotice is upgrade_notifier.maybe_emit_upgrade_notice.
func maybeEmitUpgradeNotice(w io.Writer) bool {
	if isTruthy(os.Getenv("SPEC_KITTY_NO_UPGRADE_CHECK")) {
		return false
	}
	now := clock.Now().UTC()
	path := upgradeCheckCachePath()
	var cached *probeResult
	fresh := false
	if b, err := os.ReadFile(path); err == nil {
		var d map[string]any
		if json.Unmarshal(b, &d) == nil {
			var pr probeResult
			if json.Unmarshal(b, &pr) == nil && pr.Channel != "" && pr.ProbedAt != "" {
				if t, ok := parseISOUTC(pr.ProbedAt); ok {
					ttl := 24 * 3600
					if v, ok := d["ttl_seconds"].(float64); ok {
						ttl = int(v)
					}
					cached = &pr
					fresh = pr.Installed == app.Version && now.Sub(t) < time.Duration(ttl)*time.Second
				}
			}
		}
	}
	var res probeResult
	if fresh {
		res = *cached
	} else {
		res = probePyPI(app.Version)
		res.ProbedAt = clock.ISO(now)
	}
	suppress := res.Channel == "already_current" && cached != nil && cached.Channel == "already_current" && fresh
	emitted := false
	if !suppress {
		latest := "?"
		if res.Latest != nil && *res.Latest != "" {
			latest = *res.Latest
		}
		switch res.Channel {
		case "already_current":
			out(w, "[dim]spec-kitty-cli "+res.Installed+" — you are on the latest supported version.[/dim]")
			emitted = true
		case "ahead_of_pypi":
			out(w, "[dim]spec-kitty-cli "+res.Installed+" — build is ahead of the latest PyPI release ("+latest+"). No upgrade required.[/dim]")
			emitted = true
		case "no_upgrade_path":
			out(w, "[dim]spec-kitty-cli "+res.Installed+" — installed from a non-PyPI build/channel. No PyPI upgrade path is available.[/dim]")
			emitted = true
		}
	}
	ttl := 24 * 3600
	if res.Channel == "unknown" {
		ttl = 3600
	}
	if res.Releases == nil {
		res.Releases = []string{}
	}
	rel := make([]any, len(res.Releases))
	for i, r := range res.Releases {
		rel[i] = r
	}
	var latestV, errV any
	if res.Latest != nil {
		latestV = *res.Latest
	}
	if res.Error != nil {
		errV = *res.Error
	}
	o := jsonx.NewObject("channel", res.Channel, "error", errV, "installed_version", res.Installed, "latest_pypi_version", latestV,
		"probed_at", res.ProbedAt, "releases", rel, "ttl_seconds", ttl)
	if os.MkdirAll(filepath.Dir(path), 0o755) == nil {
		os.WriteFile(path, []byte(jsonx.Dumps(o, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true})), 0o644)
	}
	return emitted
}

// ---------------------------------------------------------------------------
// session_presence/upgrade_check
// ---------------------------------------------------------------------------

func lastCLICheckPath() string {
	home, _ := os.UserHomeDir()
	return filepath.Join(home, ".kittify", "last-cli-check.json")
}

// upgradeCheckerAvailable is UpgradeChecker.get_available_version.
func upgradeCheckerAvailable() string {
	if isTruthy(os.Getenv("SPEC_KITTY_NO_UPGRADE_CHECK")) {
		return ""
	}
	b, err := os.ReadFile(lastCLICheckPath())
	if err != nil {
		return ""
	}
	var d map[string]any
	if json.Unmarshal(b, &d) != nil {
		return ""
	}
	latest, ok := d["latest_version"].(string)
	if !ok {
		return ""
	}
	pre, _ := d["prerelease"].(bool)
	if pre != prereleaseEnabled() {
		return ""
	}
	ca, ok := d["checked_at"].(string)
	if !ok {
		return ""
	}
	t, ok := parseISOUTC(ca)
	if !ok {
		return ""
	}
	if clock.Now().UTC().Sub(t) > time.Hour {
		upgradeCheckInBackground()
	}
	return latest
}

// upgradeCheckInBackground is UpgradeChecker.check_in_background.
func upgradeCheckInBackground() {
	if isTruthy(os.Getenv("SPEC_KITTY_NO_UPGRADE_CHECK")) {
		return
	}
	os.MkdirAll(filepath.Dir(lastCLICheckPath()), 0o755)
	exe, err := os.Executable()
	if err != nil || strings.HasSuffix(exe, ".test") {
		return // never re-enter a `go test` binary
	}
	cmd := exec.Command(exe, "--version")
	cmd.Env = append(os.Environ(), refreshUpgradeCacheEnv+"=1")
	detachBackgroundCheck(cmd)
	if cmd.Start() == nil {
		cmd.Process.Release()
	}
}

// refreshUpgradeCacheOnce is upgrade_check.refresh_cache_once.
func refreshUpgradeCacheOnce() {
	if isTruthy(os.Getenv("SPEC_KITTY_NO_UPGRADE_CHECK")) {
		return
	}
	pre := prereleaseEnabled()
	var latest any
	if r := pypiGetLatest(cliPackageName, pre); r.version != "" {
		latest = r.version
	}
	p := lastCLICheckPath()
	if os.MkdirAll(filepath.Dir(p), 0o755) != nil {
		return
	}
	o := jsonx.NewObject("checked_at", clock.NowUTCISO(), "latest_version", latest, "prerelease", pre)
	tmp := strings.TrimSuffix(p, ".json") + ".tmp"
	if os.WriteFile(tmp, []byte(jsonx.Dumps(o, jsonx.Options{Indent: -1, EnsureASCII: true})), 0o644) == nil {
		os.Rename(tmp, p)
	}
}
