# Governor Go-layout remap — PASS

Read-only independent inspection supports the exact proposal in `.kittify/config.yaml`:

```yaml
project:
  path_conventions:
    workspace: internal/
    tests: internal/
```

The installed runtime is `/home/lynn/.local/share/uv/tools/spec-kitty-cli/lib/python3.11/site-packages/specify_cli/`. Its `config/path_conventions.py` reads exactly `project.path_conventions`, validates supported nonblank relative string values and explicitly documents Go `internal/` as an intended use. `validators/paths.py` applies remaps only to already-declared keys, preserves artifact routing and rejects artifact-token collisions. `acceptance/summary_core.py:194` passes the loaded overrides into path validation; its subsequent strict branch still returns blocking violations for missing resolved paths. No `--lenient`, threshold, test selector, application test or validation setting changes.

Installed software-dev defaults are `workspace: src/`, `tests: tests/`, `deliverables: contracts/`, `documentation: docs/` (missions/software-dev/mission.yaml:151–155). Current primary checkout has no src/ or tests/; internal/ is a real nonsymlink directory with 445 tracked Go files, including 218 colocated `_test.go` files. Thus mapping both concepts to internal/ describes actual source/test organization; it neither fabricates directories nor substitutes evidence artifacts for source. This existence check is not a test census or coverage assertion, and does not replace the separately required Go gates.

Only these two mappings should be added, preserving existing project identity, docs/artifact paths and strict acceptance defaults. This is a project-level layout declaration: any future mission that declares `workspace` also receives that mapping. Its `documentation` and `deliverables` checks remain unchanged; no claim is made that the override is software-dev-only.

No runtime mutation, config edit, test or provider execution was performed in this review. Root may apply the exact governance correction separately.

Root verification: strict `spec-kitty accept --diagnose --json` now reports no path violations with workspace/tests mapped to the real internal directory. WP10 and its acceptance matrix remain pending. This is a project-level tooling configuration, not application qualification.
