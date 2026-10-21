package migrate

import (
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// MissionTypeResult is MissionTypeBackfillResult.
type MissionTypeResult struct {
	Slug        string
	Action      string // wrote | skip | needs_manual_resolution | error
	MissionType *string
	LegacyValue *string
	Reason      *string
}

// MissionNotFoundError is specify_cli.mission.MissionNotFoundError.
type MissionNotFoundError struct{ Msg string }

func (e *MissionNotFoundError) Error() string { return e.Msg }

// profileIDs collects the governance-profile ids resolvable at the
// built-in and project layers (MissionTypeProfileRepository.for_project).
func profileIDs(root string) map[string]bool {
	ids := map[string]bool{}
	add := func(b []byte) {
		o, err := yamlx.ParseObject(b)
		if err != nil || o == nil {
			return
		}
		if id, ok := o.Get("id"); ok {
			if s, ok := id.(string); ok && s != "" {
				ids[s] = true
			}
		}
	}
	if files, err := assets.Walk("packs/built-in/missions"); err == nil {
		for _, f := range files {
			if strings.HasSuffix(f, "/governance-profile.yaml") {
				if b, err := assets.ReadFile(f); err == nil {
					add(b)
				}
			}
		}
	}
	proj := project.Kittify(root, "charter-packs", "mission_types")
	filepath.WalkDir(proj, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() && d.Name() == "governance-profile.yaml" {
			if b, err := os.ReadFile(p); err == nil {
				add(b)
			}
		}
		return nil
	})
	return ids
}

// BackfillMissionType is backfill_mission_type_repo.
func BackfillMissionType(root string, dryRun bool, missionSlug *string) ([]MissionTypeResult, error) {
	specs := project.Specs(root)
	notFound := func() error {
		return &MissionNotFoundError{fmt.Sprintf("No mission directory found for slug %s under %s", pyjson.ReprString(*missionSlug), specs)}
	}
	if !SpecsExist(root) {
		if missionSlug != nil {
			return nil, notFound()
		}
		warnf("kitty-specs/ not found at %s", root)
		return nil, nil
	}
	names := MissionDirs(root)
	if missionSlug != nil {
		var one []string
		for _, n := range names {
			if n == *missionSlug {
				one = append(one, n)
			}
		}
		if len(one) == 0 {
			return nil, notFound()
		}
		names = one
	}
	ids := profileIDs(root)
	var out []MissionTypeResult
	for _, name := range names {
		out = append(out, backfillOneMissionType(project.Specs(root, name), ids, dryRun))
	}
	return out, nil
}

func backfillOneMissionType(dir string, ids map[string]bool, dryRun bool) MissionTypeResult {
	slug := filepath.Base(dir)
	if _, err := os.Stat(filepath.Join(dir, "meta.json")); err != nil {
		return MissionTypeResult{Slug: slug, Action: "skip", Reason: strPtr("meta.json not found")}
	}
	meta, err := LoadMetaFailClosed(dir)
	if err != nil {
		return MissionTypeResult{Slug: slug, Action: "error", Reason: strPtr("corrupt json: " + err.Error())}
	}
	if meta == nil {
		meta = &jsonx.Object{}
	}
	if meta.Has("mission_type") {
		return MissionTypeResult{Slug: slug, Action: "skip", Reason: strPtr("mission_type already present")}
	}
	rawV, _ := meta.Get("mission")
	raw, isStr := rawV.(string)
	key := strings.TrimSpace(raw)
	if !isStr || key == "" {
		return MissionTypeResult{Slug: slug, Action: "skip", Reason: strPtr("no legacy mission value")}
	}
	if !ids[key] {
		return MissionTypeResult{Slug: slug, Action: "needs_manual_resolution", LegacyValue: strPtr(raw),
			Reason: strPtr(fmt.Sprintf("no governance profile resolves for %s at any layer", pyjson.ReprString(key)))}
	}
	if !dryRun {
		UpdateMeta(dir, func(fresh *jsonx.Object) bool {
			if fresh.Has("mission_type") {
				return false
			}
			fresh.Set("mission_type", key)
			return true
		})
	}
	return MissionTypeResult{Slug: slug, Action: "wrote", MissionType: strPtr(key), LegacyValue: strPtr(raw)}
}
