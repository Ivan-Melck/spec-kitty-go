package retrospective

// The runtime's policy-driven learning capture
// (runtime_bridge_retrospective._run_retrospective_learning_capture and its
// facilitator): resolve policy, generate, write the record (an existing
// record is kept), emit RetrospectiveCaptured; every failure is recorded as
// RetrospectiveCaptureFailed.

import (
	"errors"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/status"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

func runtimeActor() *jsonx.Object {
	return jsonx.NewObject("kind", "runtime", "id", "spec-kitty-generator", "display", nil)
}

// TerminusMissionID is _resolve_mission_id_for_terminus: meta.json's
// mission_id, else the feature directory name.
func TerminusMissionID(featureDir string) string {
	if id := mission.ReadMetaOrEmpty(featureDir).GetString("mission_id"); strings.TrimSpace(id) != "" {
		return id
	}
	return filepath.Base(featureDir)
}

// RuntimeCapture runs the facilitator for slug under root with the given
// provenance kind (runtime_post_completion / runtime_strict_gate). A
// disabled policy captures nothing. The returned error is the capture
// failure (already recorded as RetrospectiveCaptureFailed); an emit failure
// after a successful write is recorded but not returned.
func RuntimeCapture(root, slug, missionID, provenance string) error {
	dir := project.Specs(root, slug)
	policy, source, err := ResolvePolicy(root)
	if err != nil {
		emitRuntimeFailure(dir, missionID, slug, provenance, err, ResolutionErrorSource())
		return err
	}
	if !policy.Enabled() {
		return nil
	}
	rec, err := Generate(root, slug, Now())
	if err != nil {
		emitRuntimeFailure(dir, missionID, slug, provenance, err, source)
		return err
	}
	rec.ProvenanceKind, rec.PolicySource = provenance, source
	path := filepath.Join(dir, FileName)
	// write_gen_record(mode="error"): an existing record (canonical or the
	// legacy .kittify/missions/<id>/ path) is kept; Captured is still emitted.
	prior := exists(path) || (rec.MissionID != "" && exists(filepath.Join(root, ".kittify", "missions", rec.MissionID, FileName)))
	if !prior {
		if err := os.WriteFile(path, []byte(Serialize(rec.ToObject())), 0o644); err != nil {
			emitRuntimeFailure(dir, missionID, slug, provenance, err, source)
			return err
		}
	}
	ev := jsonx.NewObject("type", "RetrospectiveCaptured", "schema_version", 1, "event_id", ulid.New(), "lamport", nextLamport(dir),
		"at", Now(), "actor", runtimeActor(),
		"mission_id", rec.MissionID, "mission_slug", rec.Slug, "wp_id", nil, "force", false, "execution_mode", "main",
		"findings_status", rec.FindingsStatus, "record_path", "kitty-specs/"+slug+"/"+FileName, "generator_version", GeneratorVersion,
		"policy_source", source.Clone(), "provenance_kind", provenance, "proposal_count", 0,
		"evidence_ref_count", len(rec.Evidence))
	if err := status.AppendRows(status.EventsPath(dir), ev); err != nil {
		emitRuntimeFailure(dir, missionID, slug, provenance, err, source)
	}
	return nil
}

// emitRuntimeFailure is _classify_and_emit_failure with the runtime actor.
func emitRuntimeFailure(dir, missionID, slug, provenance string, err error, source *jsonx.Object) {
	category := "generator_exception"
	var hint any
	var nf *NotFoundError
	var missing any
	if errors.As(err, &nf) || errors.Is(err, os.ErrNotExist) {
		category = "missing_artifacts"
		hint = "Run `spec-kitty migrate normalize-lifecycle` to repair missing artifacts."
	} else {
		seen := map[string]bool{}
		var vals []string
		for _, k := range source.Keys() {
			if v := source.GetString(k); !seen[v] {
				seen[v] = true
				vals = append(vals, v)
			}
		}
		sort.Strings(vals)
		sources := "unknown"
		if len(vals) > 0 {
			sources = strings.Join(vals, ", ")
		}
		hint = "Check policy configuration at: " + sources
	}
	msg := []rune(err.Error())
	if len(msg) > 400 {
		msg = msg[:400]
	}
	ev := jsonx.NewObject("type", "RetrospectiveCaptureFailed", "schema_version", 1, "event_id", ulid.New(), "lamport", nextLamport(dir),
		"at", Now(), "actor", runtimeActor(), "mission_id", missionID, "mission_slug", slug, "wp_id", nil, "force", false,
		"execution_mode", "main", "failure_category", category, "failure_message", string(msg), "remediation_hint", hint,
		"policy_source", source.Clone(), "attempted_provenance_kind", provenance, "missing_artifacts", missing)
	_ = status.AppendRows(status.EventsPath(dir), ev)
}
