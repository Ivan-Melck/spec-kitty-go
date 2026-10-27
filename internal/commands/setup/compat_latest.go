package setup

// Latest-version resolution of the compat planner (compat/provider.py,
// compat/cache.py, compat/config.py and planner._resolve_latest_version):
// the PyPI JSON provider, the user-cache nag record and the nag
// configuration. Network access follows upstream exactly: suppressed for
// CI, a non-TTY stdout or --no-nag, except for an explicit upgrade query
// (query_latest).

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/app"
	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

const (
	cliPackageName      = "spec-kitty-cli"
	pypiURLTemplate     = "https://pypi.org/pypi/%s/json"
	maxPyPIResponse     = 1_048_576
	maxNagCacheBytes    = 65_536
	nagCacheFile        = "upgrade-nag.json"
	defaultNagThrottle  = 86_400
	minNagThrottle      = 60
	maxNagThrottle      = 31_536_000
	prereleaseCacheMark = "#prerelease"
)

// pypiURLEnv is a Go-only test seam (no upstream equivalent: the PyPI URL
// is hardcoded there): a URL template with one %s for the package name,
// pointed at a local fake index by the test suites so they never reach
// pypi.org.
const pypiURLEnv = "SPEC_KITTY_GO_PYPI_JSON_URL"

var pypiVersionRe = regexp.MustCompile(`^[A-Za-z0-9.\-+]{1,64}$`)

type latestResult struct {
	version string // "" = None
	source  string // pypi | simple_index | none
	err     string
}

// isTruthy is core.env.is_truthy.
func isTruthy(v string) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// isCIEnv is compat.planner.is_ci_env.
func isCIEnv() bool {
	raw := os.Getenv("CI")
	if raw == "" {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "0", "false", "no", "off", "":
		return false
	}
	return true
}

func prereleaseEnabled() bool { return isTruthy(os.Getenv("SPEC_KITTY_PRERELEASE")) }

// pypiGetLatest is PyPIProvider.get_latest.
func pypiGetLatest(pkg string, prerelease bool) latestResult {
	tmpl := pypiURLTemplate
	if v := os.Getenv(pypiURLEnv); v != "" {
		tmpl = v
	}
	url := fmt.Sprintf(tmpl, pkg)
	client := &http.Client{
		Timeout:       2 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return latestResult{source: "none", err: "http_error"}
	}
	req.Header.Set("User-Agent", cliPackageName+"/"+app.Version+" compat-planner")
	resp, err := client.Do(req)
	if err != nil {
		var ne interface{ Timeout() bool }
		if errors.As(err, &ne) && ne.Timeout() {
			return latestResult{source: "none", err: "timeout"}
		}
		return latestResult{source: "none", err: "http_error"}
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxPyPIResponse+1))
	if err != nil {
		return latestResult{source: "none", err: "http_error"}
	}
	if len(raw) > maxPyPIResponse {
		return latestResult{source: "none", err: "oversized"}
	}
	if resp.StatusCode >= 400 {
		return latestResult{source: "none", err: "http_error"}
	}
	var payload map[string]any
	if json.Unmarshal(raw, &payload) != nil {
		return latestResult{source: "none", err: "parse_error"}
	}
	info, _ := payload["info"].(map[string]any)
	ver, ok := info["version"].(string)
	if info == nil || !ok || !pypiVersionRe.MatchString(ver) {
		return latestResult{source: "none", err: "parse_error"}
	}
	if !prerelease {
		return latestResult{version: ver, source: "pypi"}
	}
	cands := []string{ver}
	if rel, ok := payload["releases"].(map[string]any); ok {
		keys := make([]string, 0, len(rel))
		for k := range rel {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			files, ok := rel[k].([]any)
			if !ok {
				continue
			}
			for _, f := range files {
				if fo, ok := f.(map[string]any); ok {
					if y, _ := fo["yanked"].(bool); !y {
						cands = append(cands, k)
						break
					}
				}
			}
		}
	}
	chosen := highestVersion(cands, true)
	if chosen == "" {
		chosen = ver
	}
	if !pypiVersionRe.MatchString(chosen) {
		return latestResult{source: "none", err: "parse_error"}
	}
	return latestResult{version: chosen, source: "pypi"}
}

// highestVersion is distribution.simple_index._highest_version.
func highestVersion(versions []string, includePre bool) string {
	var bestAny, bestStable string
	var anyV, stableV pepVersion
	for _, raw := range versions {
		if !pypiVersionRe.MatchString(raw) {
			continue
		}
		v, ok := parsePEP440(raw)
		if !ok {
			continue
		}
		if bestAny == "" || anyV.Compare(v) < 0 {
			bestAny, anyV = raw, v
		}
		if v.PreKind == 3 && v.Dev == -1 && (bestStable == "" || stableV.Compare(v) < 0) {
			bestStable, stableV = raw, v
		}
	}
	if includePre || bestStable == "" {
		return bestAny
	}
	return bestStable
}

// ---------------------------------------------------------------------------
// Nag cache (compat/cache.py)
// ---------------------------------------------------------------------------

type nagRecord struct {
	CLIVersionKey     string
	LatestVersion     string // "" = None
	LatestSource      string
	FetchedAt         time.Time
	LastShownAt       *time.Time
	RemoteVersionSeen string // "" = None
	SnoozeStep        string // "" = None
	SnoozedUntil      *time.Time
	AlwaysUpgrade     bool
	NeverAsk          bool
}

// userCacheDir is platformdirs.user_cache_dir("spec-kitty") on POSIX.
func userCacheDir() string {
	base := os.Getenv("XDG_CACHE_HOME")
	if strings.TrimSpace(base) == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".cache")
	}
	return filepath.Join(base, "spec-kitty")
}

// userConfigDir is platformdirs.user_config_dir("spec-kitty") on POSIX.
func userConfigDir() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if strings.TrimSpace(base) == "" {
		home, _ := os.UserHomeDir()
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "spec-kitty")
}

func nagCachePath() string { return filepath.Join(userCacheDir(), nagCacheFile) }

func parseISOUTC(s string) (time.Time, bool) {
	t, err := clock.ParseISO(s)
	if err != nil {
		return time.Time{}, false
	}
	return t.UTC(), true
}

// readNagCache is NagCache.read (nil on any refusal).
func readNagCache() *nagRecord {
	p := nagCachePath()
	pst, err := os.Lstat(filepath.Dir(p))
	if err != nil || pst.Mode()&os.ModeSymlink != 0 {
		return nil
	}
	st, err := os.Lstat(p)
	if err != nil || st.Mode()&os.ModeSymlink != 0 || st.Size() > maxNagCacheBytes {
		return nil
	}
	if !nagCacheOwnedPrivately(st) {
		return nil
	}
	b, err := os.ReadFile(p)
	if err != nil || len(b) > maxNagCacheBytes {
		return nil
	}
	var d map[string]any
	if json.Unmarshal(b, &d) != nil || d == nil {
		return nil
	}
	reqStr := func(k string) (string, bool) { s, ok := d[k].(string); return s, ok }
	optStr := func(k string) (string, bool) {
		v, present := d[k]
		if !present || v == nil {
			return "", true
		}
		s, ok := v.(string)
		return s, ok
	}
	r := &nagRecord{}
	var ok bool
	if r.CLIVersionKey, ok = reqStr("cli_version_key"); !ok {
		return nil
	}
	if r.LatestVersion, ok = optStr("latest_version"); !ok {
		return nil
	}
	if r.LatestSource, ok = reqStr("latest_source"); !ok || (r.LatestSource != "pypi" && r.LatestSource != "simple_index" && r.LatestSource != "none") {
		return nil
	}
	fa, ok := reqStr("fetched_at")
	if !ok {
		return nil
	}
	if r.FetchedAt, ok = parseISOUTC(fa); !ok {
		return nil
	}
	ls, ok := optStr("last_shown_at")
	if !ok {
		return nil
	}
	if ls != "" {
		t, ok := parseISOUTC(ls)
		if !ok {
			return nil
		}
		r.LastShownAt = &t
	}
	if v, ok := optStr("remote_version_seen"); !ok {
		return nil
	} else if v != "" {
		r.RemoteVersionSeen = v
	}
	switch v := d["snooze_step"].(type) {
	case nil:
	case string:
		if v != "24h" && v != "48h" && v != "7d" {
			return nil
		}
		r.SnoozeStep = v
	default:
		return nil
	}
	if v, ok := optStr("snoozed_until"); !ok {
		return nil
	} else if v != "" {
		t, ok := parseISOUTC(v)
		if !ok {
			return nil
		}
		r.SnoozedUntil = &t
	}
	for _, k := range []string{"always_upgrade", "never_ask"} {
		v, present := d[k]
		if !present {
			continue
		}
		bv, ok := v.(bool)
		if !ok {
			return nil
		}
		if k == "always_upgrade" {
			r.AlwaysUpgrade = bv
		} else {
			r.NeverAsk = bv
		}
	}
	return r
}

// writeNagCache is NagCache.write (best effort; refuses symlinks).
func writeNagCache(r *nagRecord) {
	p := nagCachePath()
	parent := filepath.Dir(p)
	if err := os.MkdirAll(parent, 0o700); err != nil {
		return
	}
	if st, err := os.Lstat(parent); err != nil || st.Mode()&os.ModeSymlink != 0 {
		return
	}
	if st, err := os.Lstat(p); err == nil && st.Mode()&os.ModeSymlink != 0 {
		return
	}
	optS := func(v string) any {
		if v == "" {
			return nil
		}
		return v
	}
	optT := func(t *time.Time) any {
		if t == nil {
			return nil
		}
		return clock.ISO(*t)
	}
	o := jsonx.NewObject("always_upgrade", r.AlwaysUpgrade, "cli_version_key", r.CLIVersionKey, "fetched_at", clock.ISO(r.FetchedAt),
		"last_shown_at", optT(r.LastShownAt), "latest_source", r.LatestSource, "latest_version", optS(r.LatestVersion), "never_ask", r.NeverAsk,
		"remote_version_seen", optS(r.RemoteVersionSeen), "snooze_step", optS(r.SnoozeStep), "snoozed_until", optT(r.SnoozedUntil))
	payload := jsonx.Dumps(o, jsonx.Options{Indent: -1, SortKeys: true, EnsureASCII: true})
	f, err := os.CreateTemp(parent, ".upgrade-nag-*.tmp")
	if err != nil {
		return
	}
	tmp := f.Name()
	f.Chmod(0o600)
	_, werr := f.WriteString(payload)
	if cerr := f.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil || os.Rename(tmp, p) != nil {
		os.Remove(tmp)
	}
}

func (r *nagRecord) hasFreshData(throttle int, now time.Time, key string) bool {
	if r == nil || r.CLIVersionKey != key {
		return false
	}
	d := now.Sub(r.FetchedAt).Seconds()
	return d >= 0 && d < float64(throttle)
}

func (r *nagRecord) isFresh(throttle int, now time.Time, key string) bool {
	if r == nil || r.CLIVersionKey != key || r.LastShownAt == nil {
		return false
	}
	d := now.Sub(*r.LastShownAt).Seconds()
	return d >= 0 && d < float64(throttle)
}

// ---------------------------------------------------------------------------
// UpgradeConfig (compat/config.py)
// ---------------------------------------------------------------------------

type upgradeConfig struct {
	throttle   int
	nagEnabled bool
}

func parseThrottle(v any) (int, bool) {
	s := strings.TrimSpace(fmt.Sprint(v))
	n, err := strconv.Atoi(s)
	if err != nil || n < minNagThrottle || n > maxNagThrottle {
		return 0, false
	}
	return n, true
}

func loadUpgradeConfig() upgradeConfig {
	var nag *jsonx.Object
	if b, err := os.ReadFile(filepath.Join(userConfigDir(), "upgrade.yaml")); err == nil {
		if v, err := yamlx.Parse(b); err == nil {
			if o, ok := v.(*jsonx.Object); ok {
				nag = o.GetObject("nag")
			}
		}
	}
	c := upgradeConfig{throttle: defaultNagThrottle, nagEnabled: true}
	if v, ok := os.LookupEnv("SPEC_KITTY_NAG_THROTTLE_SECONDS"); ok {
		if n, ok := parseThrottle(v); ok {
			c.throttle = n
			goto enabled
		}
	}
	if nag != nil {
		if v, ok := nag.Get("throttle_seconds"); ok && v != nil {
			if n, ok := parseThrottle(v); ok {
				c.throttle = n
			}
		}
	}
enabled:
	if isTruthy(os.Getenv("SPEC_KITTY_NO_NAG")) {
		c.nagEnabled = false
		return c
	}
	if nag != nil {
		if v, ok := nag.Get("enabled"); ok && v != nil {
			c.nagEnabled = pyjsonTruthy(v)
		}
	}
	return c
}

// ---------------------------------------------------------------------------
// CLI status (planner._resolve_latest_version and friends)
// ---------------------------------------------------------------------------

type cliStatus struct {
	installed string
	latest    string // "" = None
	source    string
	outdated  bool
	fetchedAt *time.Time
}

func (c cliStatus) json() *jsonx.Object {
	var latest, fetched any
	if c.latest != "" {
		latest = c.latest
	}
	if c.fetchedAt != nil {
		fetched = clock.ISO(*c.fetchedAt)
	}
	return jsonx.NewObject("installed_version", c.installed, "latest_version", latest, "latest_source", c.source, "is_outdated", c.outdated, "fetched_at", fetched)
}

// resolveCLIStatus is the CLI-status half of planner._plan_impl.
func resolveCLIStatus(inv Invocation, opt CompatOptions) cliStatus {
	now := opt.Now
	if now.IsZero() {
		now = clock.Now().UTC()
	}
	cfg := loadUpgradeConfig()
	installed := app.Version
	pre := prereleaseEnabled()
	key := installed
	if pre {
		key += prereleaseCacheMark
	}
	record := readNagCache()
	preference := record
	if record != nil && record.CLIVersionKey != key {
		record = nil
	}
	dataFresh := record.hasFreshData(cfg.throttle, now, key)
	shownFresh := record.isFresh(cfg.throttle, now, key)

	st := cliStatus{installed: installed, source: "none"}
	switch {
	case opt.ReadOnly && !opt.QueryLatest:
		if record != nil && record.LatestSource == "pypi" {
			st.latest, st.source = record.LatestVersion, "pypi"
			t := record.FetchedAt
			st.fetchedAt = &t
		}
	case dataFresh:
		st.latest, st.source = record.LatestVersion, record.LatestSource
	default:
		res := latestResult{source: "none"}
		if !(inv.suppressesNetwork() && !opt.QueryLatest) {
			res = pypiGetLatest(cliPackageName, pre)
		}
		cacheable := res.source == "pypi" || res.source == "simple_index"
		st.latest = res.version
		if cacheable {
			t := now
			st.fetchedAt = &t
		}
		if cacheable && res.version != "" && !opt.ReadOnly {
			nr := &nagRecord{CLIVersionKey: key, LatestVersion: res.version, LatestSource: res.source, FetchedAt: now}
			if preference != nil {
				nr.LastShownAt = preference.LastShownAt
				nr.RemoteVersionSeen, nr.SnoozeStep, nr.SnoozedUntil = preference.RemoteVersionSeen, preference.SnoozeStep, preference.SnoozedUntil
				nr.AlwaysUpgrade, nr.NeverAsk = preference.AlwaysUpgrade, preference.NeverAsk
			}
			writeNagCache(nr)
		} else if record != nil && record.LatestVersion != "" {
			st.latest = record.LatestVersion
			st.fetchedAt = nil
		}
		switch {
		case cacheable:
			st.source = res.source
		case record != nil:
			st.source = record.LatestSource
		}
	}
	if st.latest != "" {
		if c, ok := versionCmp(st.latest, installed); ok && c > 0 {
			st.outdated = true
		}
	}
	if !cfg.nagEnabled && !opt.QueryLatest {
		st.outdated = false
	}
	if shownFresh && !opt.QueryLatest {
		st.outdated = false
	}
	return st
}

// markNagShown is helpers._render_nag_if_needed's cache update.
func markNagShown(st cliStatus) {
	now := clock.Now().UTC()
	r := readNagCache()
	if r == nil {
		r = &nagRecord{CLIVersionKey: st.installed, LatestVersion: st.latest, LatestSource: st.source, FetchedAt: now}
	}
	r.LastShownAt = &now
	writeNagCache(r)
}
