package invocation

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/gitx"
)

type PrimaryTracking struct {
	Tracked bool
	Primary string
}

type commitPolicy struct{ tracking *PrimaryTracking }

func (p *commitPolicy) trackingFor(root string) (*PrimaryTracking, error) {
	if p.tracking != nil {
		return p.tracking, nil
	}
	t, err := ProbePrimaryTracking(root)
	if err == nil {
		p.tracking = t
	}
	return t, err
}

var opRecordPath = regexp.MustCompile(`(?:^|/)kitty-ops/[0-9A-HJKMNP-TV-Z]{26}\.jsonl$`)

// ProbePrimaryTracking deliberately has no current-branch fallback: a topic
// branch's first Op commit must never authorize all later Op commits.
func ProbePrimaryTracking(root string) (*PrimaryTracking, error) {
	var names []string
	if head, err := gitx.Run(root, "symbolic-ref", "--quiet", "--short", "refs/remotes/origin/HEAD"); err == nil {
		if name := strings.TrimPrefix(strings.TrimSpace(head), "origin/"); name != "" {
			names = append(names, name)
		}
	}
	for _, name := range []string{"main", "master", "develop"} {
		found := false
		for _, n := range names {
			if n == name {
				found = true
			}
		}
		if !found {
			names = append(names, name)
		}
	}
	for _, name := range names {
		var refs []string
		for _, ref := range []string{"refs/heads/" + name, "refs/remotes/origin/" + name} {
			if gitx.OK(root, "rev-parse", "--verify", "--quiet", ref+"^{commit}") {
				refs = append(refs, ref)
			}
		}
		if len(refs) == 0 {
			continue
		}
		tracking := &PrimaryTracking{Primary: name}
		for _, ref := range refs {
			r, err := gitx.Run(root, "ls-tree", "-r", "--name-only", "-z", ref, "--", "kitty-ops/")
			if err != nil {
				return nil, fmt.Errorf("listing kitty-ops/ on %s failed: %v", ref, err)
			}
			for _, p := range strings.Split(r, "\x00") {
				if opRecordPath.MatchString(p) {
					tracking.Tracked = true
					return tracking, nil
				}
			}
		}
		return tracking, nil
	}
	return &PrimaryTracking{}, nil
}

func OpCommitSkipReason(root, rel string, tracking *PrimaryTracking) (string, error) {
	r, err := gitx.Exec(root, nil, nil, "check-ignore", "-q", "--no-index", "--", rel)
	if err != nil {
		return "", err
	}
	if r.Code == 0 {
		return rel + " matches an ignore rule in this repository", nil
	}
	if r.Code != 1 {
		return "", fmt.Errorf("git check-ignore %s failed: %s", rel, strings.TrimSpace(r.Stderr))
	}
	if !tracking.Tracked {
		if tracking.Primary == "" {
			return "primary branch unresolved, so it cannot be shown to track Op records", nil
		}
		return "the primary branch (" + tracking.Primary + ") tracks no Op records under kitty-ops/", nil
	}
	return "", nil
}
