// Package branchctx is the deterministic branch contract planning commands
// inject into their JSON payloads (mission_branch_context._inject_branch_contract)
// plus the primary-branch and feature target-branch resolvers.
package branchctx

import (
	"fmt"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/gitx"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
)

// UTCStamp is now_utc_stamp() (%Y-%m-%dT%H:%M:%SZ).
func UTCStamp() string { return clock.Now().UTC().Format("2006-01-02T15:04:05Z") }

// Inject is _inject_branch_contract(payload, target_branch, current_branch,
// expected_checkout_branch) without the primary-branch recommendation.
func Inject(payload *jsonx.Object, target, current, matchTarget, expectedCheckout string) *jsonx.Object {
	e := payload.Clone()
	rv := e.GetObject("runtime_vars")
	if rv == nil {
		rv = &jsonx.Object{}
	} else {
		rv = rv.Clone()
	}
	now := rv.GetString("now_utc_iso")
	if now == "" {
		now = UTCStamp()
	}
	cur := strings.TrimSpace(current)
	if cur == "" {
		cur = target
	}
	expected := expectedCheckout
	if expected == "" {
		expected = target
	}
	ref := expected
	if matchTarget != "" {
		ref = matchTarget
	}
	matches := cur == ref
	summary := fmt.Sprintf("Current branch at workflow start: %s. Planning/base branch for this mission: %s. Completed changes must merge into %s.", cur, target, target)
	rv.Set("now_utc_iso", now)
	rv.Set("current_branch", cur)
	rv.Set("target_branch", target)
	rv.Set("base_branch", target)
	rv.Set("planning_base_branch", target)
	rv.Set("merge_target_branch", target)
	rv.Set("branch_matches_target", matches)
	rv.Set("branch_strategy_summary", summary)
	bc := jsonx.NewObject("current_branch", cur, "target_branch", target, "base_branch", target, "planning_base_branch", target,
		"merge_target_branch", target, "expected_checkout_branch", expected, "matches_target", matches, "branch_strategy_summary", summary)
	for _, kv := range []jsonx.KV{{Key: "current_branch", Value: cur}, {Key: "CURRENT_BRANCH", Value: cur}, {Key: "target_branch", Value: target},
		{Key: "base_branch", Value: target}, {Key: "TARGET_BRANCH", Value: target}, {Key: "BASE_BRANCH", Value: target},
		{Key: "planning_base_branch", Value: target}, {Key: "PLANNING_BASE_BRANCH", Value: target}, {Key: "merge_target_branch", Value: target},
		{Key: "MERGE_TARGET_BRANCH", Value: target}, {Key: "EXPECTED_TARGET_BRANCH", Value: target}, {Key: "EXPECTED_BASE_BRANCH", Value: target},
		{Key: "branch_matches_target", Value: matches}, {Key: "BRANCH_MATCHES_TARGET", Value: matches}, {Key: "branch_strategy_summary", Value: summary}} {
		e.Set(kv.Key, kv.Value)
	}
	e.Set("runtime_vars", rv)
	e.Set("NOW_UTC_ISO", now)
	e.Set("branch_context", bc)
	return e
}

// CurrentBranch is get_current_branch ("" when detached / not a repo).
func CurrentBranch(dir string) string {
	b := gitx.CurrentBranch(dir)
	if b == "HEAD" {
		return ""
	}
	return b
}

// FeatureTargetBranch is _resolve_feature_target_branch: meta.target_branch,
// else the current branch, else "main".
func FeatureTargetBranch(featureDir, repoRoot string) string {
	meta := mission.ReadMetaOrEmpty(featureDir)
	if t := strings.TrimSpace(meta.GetString("target_branch")); t != "" {
		return meta.GetString("target_branch")
	}
	if c := CurrentBranch(repoRoot); c != "" {
		return c
	}
	return "main"
}
