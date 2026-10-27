package issuematrix

import (
	"errors"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/coord"
	"github.com/spec-kitty/go-kitty3/internal/mission"
	"github.com/spec-kitty/go-kitty3/internal/placement"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

// Partition is resolve_issue_matrix_partition's (primary_discovery_dir,
// coord_matrix_source) split. Reference discovery reads PrimaryDir; matrix
// verdicts read Content when it is set (post-consolidation ref content),
// else MatrixDir (a materialized coordination dir, or the primary dir on a
// coord-less topology).
type Partition struct {
	PrimaryDir string
	MatrixDir  string
	Content    *string
}

// Rows is load_issue_matrix over the partition's matrix source: the
// canonical rows (an unparseable verdict such as a scaffolded "unknown" is
// excluded), JSON first, then the legacy markdown.
func (p Partition) Rows() []Row {
	dir := p.MatrixDir
	if p.Content != nil {
		dir = p.PrimaryDir
	}
	return Validate(filepath.Join(dir, MDFile), p.Content).Rows
}

// ResolvePartition is mission_runtime.resolve_issue_matrix_partition for
// the mission whose primary dir is primaryDir. On a coordination-routed
// topology the matrix is read from the materialized coordination mission
// dir; when that worktree is not materialized it is read from the ref the
// write path resolves to (the coordination branch, or the Primary Branch
// once the mission is published) and never from the primary residue. A
// failed ref read is a *RefReadError (ISSUE_MATRIX_NOT_AUTHORED when the
// ref holds no matrix at all).
func ResolvePartition(root, primaryDir string) (Partition, error) {
	p := Partition{PrimaryDir: primaryDir, MatrixDir: primaryDir}
	meta := mission.ReadMetaOrEmpty(primaryDir)
	if !coord.RoutesThroughCoordination(coord.TopologyFromMeta(meta, primaryDir)) {
		return p, nil
	}
	slug := filepath.Base(primaryDir)
	dir, err := placement.ReadDir(root, slug, placement.Coord)
	var mismatch *placement.BranchMismatchError
	switch {
	case errors.As(err, &mismatch):
		return p, err
	case err == nil && dir != primaryDir:
		// coord_read_dir_for: a MATERIALIZED coordination surface.
		p.MatrixDir = dir
		return p, nil
	}
	ref, err := safecommit.ResolvePlacement(root, slug, mission.KindIssueMatrix)
	if err != nil {
		return p, err
	}
	main := project.MainRepoRoot(root)
	if main == "" {
		main = root
	}
	content, err := ReadRefContent(main, slug, ref)
	if err != nil {
		return p, err
	}
	p.Content = &content
	return p, nil
}

// IsNotAuthored reports an ISSUE_MATRIX_NOT_AUTHORED ref read.
func IsNotAuthored(err error) bool {
	var re *RefReadError
	return errors.As(err, &re) && re.Code == NotAuthored
}
