package bootstrap

import (
	"bufio"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/spec-kitty/go-kitty3/internal/testkit"
)

func TestGlobalAssetRecheckOracle(t *testing.T) {
	f, err := os.Open(testkit.Oracle(t))
	if err != nil {
		t.Fatal(err)
	}
	line, _ := bufio.NewReader(f).ReadString('\n')
	f.Close()
	python := strings.TrimSpace(strings.TrimPrefix(line, "#!"))
	s := testkit.NewSandbox(t)
	result := s.Exec("", python, "-c", `
import json, os, tempfile
from pathlib import Path
from specify_cli.runtime.asset_preparation import AssetPreparation, apply_assets, _write_asset, _write_order
from specify_cli.tool_surface.operations import OperationRoot, ApplyConsent
results = {}
for scenario in ("unchanged", "edited-output", "source-drift", "source-role-sticky", "extra-child", "directory-replaced", "marker-only", "canonical-peer", "environment", "root-anchor"):
    root = Path(tempfile.mkdtemp())
    home = root / "home"
    directory = home / "skills"
    directory.mkdir(parents=True)
    os.environ["HOME"] = str(home)
    os.environ["SPEC_KITTY_HOME"] = str(home / ".kittify")
    prep = AssetPreparation("test_owner", OperationRoot("test_owner", "global", Path("/") if scenario == "root-anchor" else root), home / ".kittify/cache", ".test.lock", ApplyConsent(automatic=True))
    target = directory / "SKILL.md"
    if scenario == "root-anchor":
        target = directory / "new/SKILL.md"
    prep.observe(directory, members=True, role="destination_probe")
    prep.asset(target, b"canonical\n", 0o644)
    source = root / "source"
    source.write_bytes(b"input\n")
    prep.source(source)
    if scenario == "source-role-sticky":
        prep.observe(source, role="destination_probe")
    assessment = prep.finish(home / ".kittify/cache/version.lock", "test")
    if scenario == "edited-output":
        target.write_bytes(b"operator edit\n")
    elif scenario in ("source-drift", "source-role-sticky"):
        source.write_bytes(b"different input\n")
    elif scenario == "extra-child":
        (directory / "custom").write_bytes(b"keep\n")
    elif scenario == "directory-replaced":
        directory.rename(str(directory) + ".old")
        directory.mkdir()
    elif scenario == "marker-only":
        prep.inventory.parent.mkdir(parents=True)
        prep.inventory.write_bytes(prep.writes[prep.inventory].content)
    elif scenario == "canonical-peer":
        for write in sorted(assessment.prepared.writes, key=_write_order):
            _write_asset(write)
    elif scenario == "environment":
        os.environ["SPEC_KITTY_PACKS_ROOT"] = str(root / "other")
    outcome = apply_assets(assessment, ApplyConsent(automatic=True))
    results[scenario] = outcome.outcome in ("applied", "skipped")
print(json.dumps(results))
`)
	if result.Code != 0 {
		t.Fatalf("oracle: %s%s", result.Stdout, result.Stderr)
	}
	var results map[string]bool
	if err := json.Unmarshal([]byte(result.Stdout), &results); err != nil {
		t.Fatal(err)
	}
	if len(results) != 10 {
		t.Fatalf("oracle omitted cases: %v", results)
	}
	for scenario, success := range results {
		if success != (scenario == "unchanged" || scenario == "canonical-peer" || scenario == "root-anchor") {
			t.Fatalf("oracle disagrees with Go recheck policy for %s: %v", scenario, results)
		}
	}
}
