package charter

import (
	"sort"
	"strings"

	"fmt"
	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// AgentProfileSet is the agent_profile_repository of an activation-aware
// charter service built with org_roots=[] (built-in + project layer; the
// activation wrapper does not filter the repository itself): the merged
// profile data sorted by id, and the skipped (invalid) source files.
type AgentProfileSet struct {
	Profiles   []*jsonx.Object
	Skipped    []SkippedProfile
	Collisions []LayerCollision // ArtifactLayerCollisionWarnings, in load order
}

// LoadAgentProfileSet loads the built-in profiles overlaid with the
// project layer of repoRoot's charter packs (no org layer).
func LoadAgentProfileSet(repoRoot string) AgentProfileSet {
	_, proj := kindDirs(nil, ProjectPackRootIfExists(repoRoot), dct.AgentProfile)
	pl := loadAgentProfiles("", true, nil, proj)
	ids := make([]string, 0, len(pl.data))
	for id := range pl.data {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	set := AgentProfileSet{Skipped: pl.skipped, Collisions: pl.collisions}
	for _, id := range ids {
		set.Profiles = append(set.Profiles, pl.data[id])
	}
	return set
}

// Repr is repr(SkippedProfile(...)) (a dataclass).
func (s SkippedProfile) Repr() string {
	pid := "None"
	if s.ProfileID != nil {
		pid = pyjson.ReprString(*s.ProfileID)
	}
	return "SkippedProfile(layer=" + pyjson.ReprString(s.Layer) + ", path=" + pyjson.ReprString(s.Path) +
		", profile_id=" + pid + ", error_summary=" + pyjson.ReprString(s.ErrorSummary) + ")"
}

// SkippedReprList is repr() of a list of skipped profiles.
func SkippedReprList(xs []SkippedProfile) string {
	parts := make([]string, len(xs))
	for i, s := range xs {
		parts[i] = s.Repr()
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

// WarningMessage is the ArtifactLayerCollisionWarning text.
func (c LayerCollision) WarningMessage() string {
	return fmt.Sprintf("Artifact override: %s %s from %s shadowed %s (%d field(s) replaced; %d field(s) inherited).", c.Kind, c.ItemID, c.Higher, c.Lower, c.Replaced, c.Inherited)
}
