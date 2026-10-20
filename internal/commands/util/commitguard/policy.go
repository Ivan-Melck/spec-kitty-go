// Package commitguard implements the hidden `spec-kitty commit-guard-hook`
// pre-commit entrypoint (upstream policy/commit_guard_hook.py,
// policy/commit_guard.py, policy/config.py and the active-WP resolver of
// workspace/context.py).
package commitguard

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/commands/util/uconsole"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Config is policy.config.CommitGuardConfig.
type Config struct {
	Enabled           bool
	Mode              string // warn | block | off
	BlockMissionSpecs bool
	EnforceOwnership  bool
}

// DefaultConfig is CommitGuardConfig().
func DefaultConfig() Config {
	return Config{Enabled: true, Mode: "warn", BlockMissionSpecs: true, EnforceOwnership: true}
}

var validModes = map[string]bool{"warn": true, "block": true, "off": true}

// resolveMode is _resolve_mode: absent/null -> warn; a case/whitespace
// tolerant valid mode -> it; anything else fails closed to block with a
// logged warning (written to warn).
func resolveMode(section string, raw any, present bool, warn io.Writer) string {
	if !present || raw == nil {
		return "warn"
	}
	cand := strings.ToLower(pyStrip(pyjson.Str(raw)))
	if validModes[cand] {
		return cand
	}
	if warn != nil {
		uconsole.LogWarning(warn, fmt.Sprintf("policy.%s.mode %s is not one of block, off, warn; using 'block' so the gate fails closed", section, pyjson.Repr(raw)))
	}
	return "block"
}

func pyStrip(s string) string {
	return strings.TrimFunc(s, func(r rune) bool {
		return r == ' ' || (r >= '\t' && r <= '\r') || (r >= 0x1c && r <= 0x1f) || r == 0x85 || r == 0xa0 || r == 0x1680 ||
			(r >= 0x2000 && r <= 0x200a) || r == 0x2028 || r == 0x2029 || r == 0x202f || r == 0x205f || r == 0x3000
	})
}

func boolOf(o *jsonx.Object, key string, def any) bool {
	v, ok := o.Get(key)
	if !ok {
		v = def
	}
	return pyjson.Truthy(v)
}

// LoadConfig is load_policy_config(repo_root).commit_guard: the policy
// section of .kittify/config.yaml (all defaults when absent/unreadable).
// Warnings logged by upstream (unreadable config, invalid mode) go to warn.
func LoadConfig(repoRoot string, warn io.Writer) Config {
	path := filepath.Join(repoRoot, ".kittify", "config.yaml")
	b, err := os.ReadFile(path)
	if err != nil {
		return DefaultConfig()
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		if warn != nil {
			uconsole.LogWarning(warn, fmt.Sprintf("Failed to read config.yaml for policy: %v", err))
		}
		return DefaultConfig()
	}
	data, ok := v.(*jsonx.Object)
	if !ok {
		return DefaultConfig()
	}
	policy, ok := mustGet(data, "policy").(*jsonx.Object)
	if !ok {
		return DefaultConfig()
	}
	// _parse_risk runs first and can only log a mode warning.
	if risk, ok := mustGet(policy, "risk").(*jsonx.Object); ok {
		m, present := risk.Get("mode")
		if !present {
			m, present = "warn", true
		}
		resolveMode("risk", m, present, warn)
	}
	cg, ok := mustGet(policy, "commit_guard").(*jsonx.Object)
	cfg := DefaultConfig()
	if ok {
		cfg.Enabled = boolOf(cg, "enabled", true)
		m, present := cg.Get("mode")
		if !present {
			m, present = "warn", true
		}
		cfg.Mode = resolveMode("commit_guard", m, present, warn)
		if bv, ok := cg.Get("block_mission_specs"); ok {
			cfg.BlockMissionSpecs = pyjson.Truthy(bv)
		} else {
			cfg.BlockMissionSpecs = boolOf(cg, "block_kitty_specs", true)
		}
		cfg.EnforceOwnership = boolOf(cg, "enforce_ownership", true)
	}
	if mg, ok := mustGet(policy, "merge_gates").(*jsonx.Object); ok {
		m, present := mg.Get("mode")
		if !present {
			m, present = "warn", true
		}
		resolveMode("merge_gates", m, present, warn)
	}
	return cfg
}

func mustGet(o *jsonx.Object, k string) any {
	v, _ := o.Get(k)
	return v
}

// --- guard -----------------------------------------------------------------

// Scope is OwnershipScope: the active WP context at guard time.
type Scope struct {
	OwnedFiles        []string
	ActiveWPID        string
	LaneID            string
	ContextSource     string // "absent" by default
	DiagnosticCode    string
	DiagnosticMessage string
	Warnings          []string
}

// Result is CommitGuardResult.
type Result struct {
	Allowed    bool
	Violations []string
	Warnings   []string
}

var laneBranchRe = regexp.MustCompile(`^kitty/mission-(.+)-(lane-[a-z]+)\n?$`)

// IsImplementationBranch is is_lane_branch (legacy, plain-legacy or new
// lane grammar; all three are subsumed by the plain-legacy pattern).
func IsImplementationBranch(branch string) bool { return laneBranchRe.MatchString(branch) }

// IsOccurrenceMapPath is core.constants.is_occurrence_map_path.
func IsOccurrenceMapPath(p string) bool {
	return strings.HasPrefix(p, "kitty-specs/") && strings.HasSuffix(p, "/occurrence_map.yaml") && strings.Count(p, "/") == 2
}

func orUnknown(s string) string {
	if s == "" {
		return "unknown"
	}
	return s
}

// ValidateStagedFiles is validate_staged_files.
func ValidateStagedFiles(staged, owned []string, branch string, cfg Config, scope *Scope) Result {
	if !cfg.Enabled || cfg.Mode == "off" || !IsImplementationBranch(branch) {
		return Result{Allowed: true}
	}
	var violations, warnings []string
	effective := owned
	if scope != nil {
		warnings = append(warnings, scope.Warnings...)
		effective = scope.OwnedFiles
	}
	hasDiag := scope != nil && scope.DiagnosticCode != ""
	if cfg.EnforceOwnership && hasDiag {
		msg := scope.DiagnosticMessage
		if msg == "" {
			msg = fmt.Sprintf("%s: Cannot prove active WP for ownership guard; active_wp=%s; lane_id=%s; context_source=%s",
				scope.DiagnosticCode, orUnknown(scope.ActiveWPID), orUnknown(scope.LaneID), scope.ContextSource)
		}
		violations = append(violations, msg)
	}
	if cfg.BlockMissionSpecs {
		for _, f := range staged {
			if strings.HasPrefix(f, "kitty-specs/") && !IsOccurrenceMapPath(f) {
				violations = append(violations, "Protected path: "+f+" — implementation branches must not modify kitty-specs/")
			}
		}
	}
	if cfg.EnforceOwnership && scope != nil && scope.ActiveWPID != "" && len(effective) == 0 && !hasDiag {
		violations = append(violations, fmt.Sprintf("ACTIVE_WP_OWNERSHIP_MISSING: active_wp=%s has no owned_files; lane_id=%s; context_source=%s",
			scope.ActiveWPID, orUnknown(scope.LaneID), scope.ContextSource))
	}
	if cfg.EnforceOwnership && len(effective) > 0 && !hasDiag {
		for _, f := range staged {
			if strings.HasPrefix(f, "kitty-specs/") {
				continue
			}
			if !matchesAnyGlob(f, effective) {
				violations = append(violations, scopeViolation(f, effective, scope))
			}
		}
	}
	if len(violations) == 0 {
		return Result{Allowed: true, Warnings: warnings}
	}
	if cfg.Mode == "warn" {
		return Result{Allowed: true, Warnings: append(warnings, violations...)}
	}
	return Result{Allowed: false, Violations: violations, Warnings: warnings}
}

func scopeViolation(f string, owned []string, scope *Scope) string {
	if scope != nil && scope.ActiveWPID != "" {
		return fmt.Sprintf("ACTIVE_WP_SCOPE_VIOLATION: %s is outside active_wp=%s owned_files %s; lane_id=%s; context_source=%s",
			f, scope.ActiveWPID, pyjson.ReprStrings(owned), orUnknown(scope.LaneID), scope.ContextSource)
	}
	return fmt.Sprintf("Out of scope: %s — not matched by owned_files %s", f, pyjson.ReprStrings(owned))
}

func matchesAnyGlob(path string, patterns []string) bool {
	for _, p := range patterns {
		if Fnmatch(path, p) {
			return true
		}
		prefix := strings.ReplaceAll(p, "/**", "")
		prefix = strings.ReplaceAll(prefix, "/*", "")
		prefix = strings.TrimRight(prefix, "/")
		if prefix != "" && strings.HasPrefix(path, prefix+"/") {
			return true
		}
	}
	return false
}

// Fnmatch is Python fnmatch.fnmatch on POSIX (case-sensitive; '*' also
// matches '/').
func Fnmatch(name, pattern string) bool {
	re, err := regexp.Compile(translate(pattern))
	if err != nil {
		return false
	}
	return re.MatchString(name)
}

// translate is fnmatch.translate.
func translate(pat string) string {
	rs := []rune(pat)
	var b strings.Builder
	b.WriteString(`(?s:\A`)
	for i := 0; i < len(rs); {
		c := rs[i]
		i++
		switch c {
		case '*':
			for i < len(rs) && rs[i] == '*' {
				i++
			}
			b.WriteString(`.*`)
		case '?':
			b.WriteString(`.`)
		case '[':
			j := i
			if j < len(rs) && rs[j] == '!' {
				j++
			}
			if j < len(rs) && rs[j] == ']' {
				j++
			}
			for j < len(rs) && rs[j] != ']' {
				j++
			}
			if j >= len(rs) {
				b.WriteString(`\[`)
				continue
			}
			stuff := string(rs[i:j])
			i = j + 1
			neg := strings.HasPrefix(stuff, "!")
			if neg {
				stuff = stuff[1:]
			}
			if stuff == "" {
				if neg {
					b.WriteString(`.`)
				} else {
					b.WriteString(`[^\x{0}-\x{10FFFF}]`)
				}
				continue
			}
			var cls strings.Builder
			for _, r := range stuff {
				if r == '\\' || r == ']' || r == '[' || r == '^' {
					cls.WriteByte('\\')
				}
				cls.WriteRune(r)
			}
			if neg {
				b.WriteString("[^" + cls.String() + "]")
			} else {
				b.WriteString("[" + cls.String() + "]")
			}
		default:
			b.WriteString(regexp.QuoteMeta(string(c)))
		}
	}
	b.WriteString(`\z)`)
	return b.String()
}
