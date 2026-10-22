package orchestrator

import (
	"errors"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/commands/planning"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
)

func init() { planning.APIAuthoredFinalizeScope = nativeFinalizationScope }

// nativeFinalizationScope is design.validation.native_finalization_scope:
// nothing is enforced for a Mission without the API-authoring marker or
// whose tasks are already finalized; otherwise the tasks-stage proof runs
// before any mutation and the work-package receipts are re-stamped to the
// bytes finalization wrote once it succeeded.
func nativeFinalizationScope(root, slug string) (func(), *planning.ScopeRefusal) {
	if !orch.AuthoringEnabled(root, slug) {
		return nil, nil
	}
	orch.TouchAuthoringLock(root, slug)
	m, err := orch.ResolveMission(root, slug)
	if err != nil {
		return nil, nil
	}
	done, err := orch.TasksFinalized(m.StatusDir)
	if err != nil {
		return nil, &planning.ScopeRefusal{Code: "DESIGN_STATUS_EVENT_LOG_UNREADABLE", Message: "Canonical mission status cannot be trusted"}
	}
	if done {
		return nil, nil
	}
	receipts, err := orch.ReadReceipts(root, filepath.Base(m.StatusDir))
	if err != nil {
		return nil, scopeRefusal(err)
	}
	if receipts == nil {
		return nil, nil
	}
	d := newDesign(m)
	if _, err := d.validateStage("tasks"); err != nil {
		return nil, scopeRefusal(err)
	}
	return func() {
		fresh, err := orch.ReadReceipts(root, filepath.Base(m.StatusDir))
		if err != nil || fresh == nil {
			return
		}
		for _, k := range fresh.Keys {
			r := fresh.Receipts[k]
			if r.Kind == "work_package" {
				if p, err := d.artifactPath(r.Kind, r.ArtifactID, nil); err == nil {
					r.SHA256 = contentDigestOrAbsent(p)
				}
			}
		}
		_ = orch.SaveReceipts(root, filepath.Base(m.StatusDir), fresh)
	}, nil
}

func scopeRefusal(err error) *planning.ScopeRefusal {
	var de *orch.DesignError
	if errors.As(err, &de) {
		return &planning.ScopeRefusal{Code: de.Code, Message: de.Message, Details: de.Details}
	}
	return &planning.ScopeRefusal{Code: "DESIGN_IO_FAILED", Message: "The host could not read or persist design content"}
}
