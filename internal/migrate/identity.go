package migrate

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/ulid"
)

// IdentityResult is backfill_identity.BackfillResult.
type IdentityResult struct {
	Slug          string
	Action        string // wrote | skip | error
	MissionID     *string
	NumberCoerced bool
	Reason        *string
}

// CoerceError is the ValueError raised for an uncoercible string
// mission_number (sentinels like "pending"): upstream lets it escape as an
// uncaught exception.
type CoerceError struct{ Msg string }

func (e *CoerceError) Error() string { return e.Msg }

var numberSentinels = map[string]bool{"pending": true, "unassigned": true, "TBD": true}

// coerceMissionNumberString is _coerce_mission_number for a non-blank str.
func coerceMissionNumberString(raw string) (int, error) {
	s := strings.TrimSpace(raw)
	msg := fmt.Sprintf("meta.json mission_number must be int or null, got %s. Run `spec-kitty migrate backfill-identity` to migrate.", pyjson.ReprString(raw))
	if numberSentinels[s] {
		return 0, fmt.Errorf("%s", msg)
	}
	stripped := strings.TrimLeft(s, "0")
	if stripped == "" {
		return 0, nil
	}
	n, err := strconv.Atoi(strings.ReplaceAll(stripped, "_", ""))
	if err != nil || strings.HasPrefix(stripped, "_") || strings.HasSuffix(stripped, "_") || strings.Contains(stripped, "__") {
		return 0, fmt.Errorf("%s", msg)
	}
	return n, nil
}

// BackfillMissionIdentity is backfill_identity.backfill_mission.
func BackfillMissionIdentity(dir string, dryRun bool) (IdentityResult, error) {
	slug := filepath.Base(dir)
	if _, err := os.Stat(filepath.Join(dir, mission.MetaFile)); err != nil {
		return IdentityResult{Slug: slug, Action: "skip", Reason: strPtr("meta.json not found")}, nil
	}
	meta, err := LoadMetaFailClosed(dir)
	if err != nil {
		warnf("Corrupt meta.json in %s: %v", slug, err)
		return IdentityResult{Slug: slug, Action: "error", Reason: strPtr("corrupt json: " + err.Error())}, nil
	}
	if meta == nil {
		meta = &jsonx.Object{}
	}
	changed := false
	var existing *string
	if v, ok := meta.Get("mission_id"); ok && pyjson.Truthy(v) {
		s := pyjson.Str(v)
		existing = &s
	}
	skipID := existing != nil
	newID := ""
	if skipID {
		newID = *existing
	} else {
		newID = ulid.New()
		meta.Set("mission_id", newID)
		changed = true
	}
	coerced := false
	rawNum, _ := meta.Get("mission_number")
	if raw, ok := rawNum.(string); ok && strings.TrimSpace(raw) != "" {
		n, err := coerceMissionNumberString(raw)
		if err != nil {
			return IdentityResult{}, &CoerceError{Msg: fmt.Sprintf("Cannot coerce mission_number %s in %s: %v", pyjson.ReprString(raw), slug, err)}
		}
		meta.Set("mission_number", n)
		coerced = true
		changed = true
	}
	if changed && !dryRun {
		if err := mission.WriteMeta(dir, meta, false); err != nil {
			return IdentityResult{}, err
		}
	}
	if skipID && !coerced {
		return IdentityResult{Slug: slug, Action: "skip", MissionID: existing, Reason: strPtr("mission_id already present")}, nil
	}
	action := "skip"
	if !skipID {
		action = "wrote"
	}
	if coerced && !dryRun {
		action = "wrote"
	}
	id := newID
	return IdentityResult{Slug: slug, Action: action, MissionID: &id, NumberCoerced: coerced}, nil
}

// BackfillIdentity is backfill_identity.backfill_repo.
func BackfillIdentity(root string, dryRun bool, missionSlug *string) ([]IdentityResult, error) {
	var out []IdentityResult
	for _, name := range scopedMissionDirs(root, missionSlug) {
		r, err := BackfillMissionIdentity(project.Specs(root, name), dryRun)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}
