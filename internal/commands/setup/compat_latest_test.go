package setup_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

// fakePyPI serves the PyPI JSON API for spec-kitty-cli with info.version
// latest (and a yanked 999.0.0rc1 release that must be ignored).
func fakePyPI(t *testing.T, latest string) (*httptest.Server, *int32) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.URL.Path != "/pypi/spec-kitty-cli/json" || !strings.HasPrefix(r.Header.Get("User-Agent"), "spec-kitty-cli/") {
			http.NotFound(w, r)
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"info": map[string]any{"version": latest},
			"releases": map[string]any{
				latest:        []any{map[string]any{"yanked": false}},
				"999.0.0rc1":  []any{map[string]any{"yanked": true}},
				"100.0.0rc1":  []any{map[string]any{"yanked": false}},
				"0.0.1.dev1":  []any{},
				"not-a-thing": []any{map[string]any{}},
			},
		})
	}))
	t.Cleanup(srv.Close)
	return srv, &hits
}

// MAINT-04: the PyPI latest-version lookup (Go-only: upstream's PyPI URL is
// hardcoded, so the oracle cannot be pointed at a fake index).
func TestUpgradeCLIQueriesFakeIndex(t *testing.T) {
	srv, hits := fakePyPI(t, "99.0.0")
	s := testkit.NewSandbox(t)
	s.Env = append(s.Env, "SPEC_KITTY_GO_PYPI_JSON_URL="+srv.URL+"/pypi/%s/json")

	r := s.Run("upgrade", "--cli", "--json")
	if r.Code != 0 {
		t.Fatalf("exit %d: %s%s", r.Code, r.Stdout, r.Stderr)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(r.Stdout), &d); err != nil {
		t.Fatalf("json: %v\n%s", err, r.Stdout)
	}
	cli := d["cli"].(map[string]any)
	if cli["latest_version"] != "99.0.0" || cli["latest_source"] != "pypi" || cli["is_outdated"] != true || cli["fetched_at"] == nil {
		t.Errorf("cli block = %v", cli)
	}
	if d["decision"] != "ALLOW_WITH_NAG" || d["case"] != "install_method_unknown" || d["rendered_human"] != "" {
		t.Errorf("decision/case = %v/%v rendered_human=%q", d["decision"], d["case"], d["rendered_human"])
	}
	// The explicit query is read-only: no nag cache is written.
	if _, err := os.Stat(filepath.Join(s.Home, ".cache", "spec-kitty", "upgrade-nag.json")); err == nil {
		t.Errorf("read-only query wrote the nag cache")
	}

	r = s.Run("upgrade", "--cli")
	want := "Spec Kitty 99.0.0 is available; you have "
	if r.Code != 0 || !strings.HasPrefix(r.Stdout, want) || !strings.Contains(r.Stdout, "Your install method could not be detected automatically.") {
		t.Errorf("human guidance = %q (exit %d)", r.Stdout, r.Code)
	}

	// --no-nag keeps the status but drops the nag decision.
	r = s.Run("upgrade", "--cli", "--json", "--no-nag")
	json.Unmarshal([]byte(r.Stdout), &d)
	if d["decision"] != "ALLOW" || d["cli"].(map[string]any)["is_outdated"] != true {
		t.Errorf("--no-nag: decision %v cli %v", d["decision"], d["cli"])
	}

	// Prerelease channel: the highest installable release wins.
	s.Env = append(s.Env, "SPEC_KITTY_PRERELEASE=1")
	r = s.Run("upgrade", "--cli", "--json")
	json.Unmarshal([]byte(r.Stdout), &d)
	if got := d["cli"].(map[string]any)["latest_version"]; got != "100.0.0rc1" {
		t.Errorf("prerelease latest = %v", got)
	}

	// CI suppresses nothing for an explicit query, but the network is never
	// used by a passive (read-only, non-query) plan.
	before := atomic.LoadInt32(hits)
	s.Env = append(s.Env, "CI=1")
	r = s.Run("upgrade", "--agent-check", "--json")
	if atomic.LoadInt32(hits) != before {
		t.Errorf("agent check under CI hit the index")
	}
	json.Unmarshal([]byte(r.Stdout), &d)
	if d["action"] != "none" || d["reason"] != "up_to_date" {
		t.Errorf("agent check under CI = %v", d)
	}
}

func TestUpgradeAgentCheckAndChoiceUseNagCache(t *testing.T) {
	srv, _ := fakePyPI(t, "99.0.0")
	s := testkit.NewSandbox(t)
	s.Env = append(s.Env, "SPEC_KITTY_GO_PYPI_JSON_URL="+srv.URL+"/pypi/%s/json")

	var d map[string]any
	r := s.Run("upgrade", "--agent-check", "--json")
	json.Unmarshal([]byte(r.Stdout), &d)
	if d["action"] != "guidance" || d["reason"] != "manual_upgrade_required" || d["latest_version"] != "99.0.0" {
		t.Fatalf("agent check = %s", r.Stdout)
	}
	cache := filepath.Join(s.Home, ".cache", "spec-kitty", "upgrade-nag.json")
	st, err := os.Stat(cache)
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("nag cache not written 0600: %v %v", st, err)
	}

	r = s.Run("upgrade", "--agent-choice", "not_now", "--agent-latest", "99.0.0", "--json")
	if r.Code != 0 || !strings.Contains(r.Stdout, `"status": "recorded"`) {
		t.Fatalf("agent choice: %d %s", r.Code, r.Stdout)
	}
	b, _ := os.ReadFile(cache)
	var rec map[string]any
	json.Unmarshal(b, &rec)
	if rec["snooze_step"] != "24h" || rec["remote_version_seen"] != "99.0.0" || rec["snoozed_until"] == nil {
		t.Errorf("snooze not recorded: %s", b)
	}
	r = s.Run("upgrade", "--agent-check", "--json")
	json.Unmarshal([]byte(r.Stdout), &d)
	if d["reason"] != "snoozed" || d["action"] != "none" {
		t.Errorf("after not_now: %s", r.Stdout)
	}
	r = s.Run("upgrade", "--agent-choice", "never_ask", "--agent-latest", "99.0.0")
	r = s.Run("upgrade", "--agent-check", "--json")
	json.Unmarshal([]byte(r.Stdout), &d)
	if d["reason"] != "never_ask" {
		t.Errorf("after never_ask: %s", r.Stdout)
	}
}
