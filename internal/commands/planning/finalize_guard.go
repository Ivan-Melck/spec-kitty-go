package planning

import (
	"bytes"
	"os"
	"path/filepath"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// ScopeRefusal is a typed DesignError raised by the API-authored finalize
// scope (code, message, safe details).
type ScopeRefusal struct {
	Code, Message string
	Details       *jsonx.Object
}

// FinalizeScope is the entered native_finalization_scope: Confirm
// re-stamps the work-package receipts after a successful finalize (nil when
// nothing is enforced); Close releases the authoring lock.
type FinalizeScope struct {
	Confirm, Close func()
}

// APIAuthoredFinalizeScope is native_finalization_scope (#5879), installed
// by the orchestrator-api package: for an API-authored Mission it holds the
// authoring lock across the finalize and, while tasks are not finalized
// yet, proves the planning content is current (a refusal otherwise).
var APIAuthoredFinalizeScope func(root, slug string) (*FinalizeScope, *ScopeRefusal)

// enterAPIAuthoredScope is _enter_api_authored_finalize_scope.
func (f *finalizeRun) enterAPIAuthoredScope() (*FinalizeScope, error) {
	if f.validateOnly || APIAuthoredFinalizeScope == nil {
		return nil, nil
	}
	scope, ref := APIAuthoredFinalizeScope(f.root, f.slug)
	if ref == nil {
		return scope, nil
	}
	if f.json {
		p := jsonx.NewObject("result", "error", "error", ref.Message, "error_code", ref.Code)
		if ref.Details != nil && ref.Details.Len() > 0 {
			p.Set("details", ref.Details)
		}
		emitJSON(f.ctx.Stdout, p)
	} else {
		say(f.ctx.Stdout, "[red]Error:[/red] "+ref.Message+" ("+ref.Code+")")
	}
	return nil, cli.Exit(1)
}

// writeScope is the FR-015/NFR-001 mission-directory byte guard: a refused
// run (before its finalize commit landed) puts back every file it changed
// under the mission directory and removes the files it created. meta.json
// has its own revert (the target-branch override persist).
type writeScope struct {
	dir      string
	before   map[string][]byte
	metaPath string
	metaOrig []byte
	metaHad  bool
}

func scopeFiles(dir string) []string {
	var out []string
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() && info.Name() != "meta.json" {
			out = append(out, p)
		}
		return nil
	})
	return out
}

func snapshotWriteScope(dir string) *writeScope {
	ws := &writeScope{dir: dir, before: map[string][]byte{}, metaPath: filepath.Join(dir, "meta.json")}
	for _, p := range scopeFiles(dir) {
		if b, err := os.ReadFile(p); err == nil {
			ws.before[p] = b
		}
	}
	if b, err := os.ReadFile(ws.metaPath); err == nil {
		ws.metaOrig, ws.metaHad = b, true
	}
	return ws
}

// restore is _restore_mission_write_scope (plus the meta.json revert when
// the override was persisted but never committed).
func (ws *writeScope) restore(revertMeta bool) {
	all := map[string]bool{}
	for p := range ws.before {
		all[p] = true
	}
	for _, p := range scopeFiles(ws.dir) {
		all[p] = true
	}
	paths := make([]string, 0, len(all))
	for p := range all {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		orig, had := ws.before[p]
		now, err := os.ReadFile(p)
		exists := err == nil
		if had && exists && bytes.Equal(now, orig) {
			continue
		}
		if !had {
			if exists {
				os.Remove(p)
			}
			continue
		}
		os.MkdirAll(filepath.Dir(p), 0o755)
		os.WriteFile(p, orig, 0o644)
	}
	if revertMeta && ws.metaHad {
		if now, err := os.ReadFile(ws.metaPath); err != nil || !bytes.Equal(now, ws.metaOrig) {
			os.WriteFile(ws.metaPath, ws.metaOrig, 0o644)
		}
	}
}
