package gitrepo

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/workspace"
	"golang.org/x/sys/unix"
)

// WithTargetAuthority serializes Spec Kitty participants that publish or
// update the bound target ref. It deliberately does not claim to fence raw,
// non-cooperating filesystem or Git writers.
func (r *Repository) WithTargetAuthority(ctx context.Context, publish func(string) error) error {
	return r.withTargetLock(ctx, func() error {
		head, err := r.TargetHead(ctx)
		if err != nil {
			return err
		}
		return publish(head)
	})
}

func (r *Repository) withTargetLock(ctx context.Context, publish func() error) error {
	return r.withRepositoryLock(ctx, "spec-kitty-target-ref.lock", "target-ref authority", publish)
}

func (r *Repository) withIntegrationFenceLock(ctx context.Context, publish func() error) error {
	key := operationSuffix(r.binding.RepositoryID.String() + "\x00" + r.binding.TargetRef)
	return r.withRepositoryLock(ctx, "spec-kitty-integration-fence-"+key+".lock", "integration-fence acquisition", publish)
}

// withWorktreeMetadataLock serializes every cooperating mutation of Git's
// shared worktrees/ metadata. A file lock, rather than an in-process mutex,
// covers independent agent processes and independently bound Repository
// values that share the same Git common directory.
func (r *Repository) withWorktreeMetadataLock(ctx context.Context, mutate func() error) error {
	return r.withRepositoryLock(ctx, "spec-kitty-worktree-metadata.lock", "worktree-metadata mutation", mutate)
}

func (r *Repository) withRepositoryLock(ctx context.Context, name, purpose string, publish func() error) error {
	lockPath := filepath.Join(r.binding.CommonDir, name)
	lock, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return &workspace.Error{Kind: workspace.KindGitFailure, Message: "open " + purpose + " lock", Cause: err}
	}
	defer lock.Close()
	for {
		err = unix.Flock(int(lock.Fd()), unix.LOCK_EX|unix.LOCK_NB)
		if err == nil {
			break
		}
		if err != unix.EWOULDBLOCK && err != unix.EAGAIN {
			return &workspace.Error{Kind: workspace.KindGitFailure, Message: "acquire " + purpose + " lock", Cause: err}
		}
		select {
		case <-ctx.Done():
			return &workspace.Error{Kind: workspace.KindGitFailure, Message: "wait for " + purpose + " lock", Cause: ctx.Err()}
		case <-time.After(5 * time.Millisecond):
		}
	}
	defer func() { _ = unix.Flock(int(lock.Fd()), unix.LOCK_UN) }()
	return publish()
}

// UpdateTarget is the cooperating target-ref update seam. It uses the same
// repository-scoped authority lock as ready publication.
func (r *Repository) UpdateTarget(ctx context.Context, expected, target string) error {
	if !isObjectID(expected) || !isObjectID(target) {
		return &workspace.Error{Kind: workspace.KindInvalid, Message: "target update requires full object IDs"}
	}
	return r.WithTargetAuthority(ctx, func(current string) error {
		if current != expected {
			return &workspace.Error{Kind: workspace.KindTargetMoved, Message: "target changed before cooperating update", Observed: map[string]string{"target_head": current}, Expected: map[string]string{"target_head": expected}}
		}
		if _, err := r.runner.Run(ctx, r.binding.CommonDir, "update-ref", r.binding.TargetRef, target, expected); err != nil {
			return &workspace.Error{Kind: workspace.KindGitFailure, Message: "update target ref", Cause: err}
		}
		return nil
	})
}

func (r *Repository) TargetHead(ctx context.Context) (string, error) {
	head, err := r.runner.Run(ctx, r.binding.CommonDir, "rev-parse", "--verify", r.binding.TargetRef+"^{commit}")
	if err != nil {
		return "", &workspace.Error{Kind: workspace.KindGitFailure, Message: "resolve repository target head", Cause: err}
	}
	return head, nil
}

func (r *Repository) WorkspacePath(workspaceID string) (string, error) {
	path := filepath.Join(r.binding.ManagedRoot, workspaceID)
	parent, err := canonicalExistingDirectory(filepath.Dir(path))
	if err != nil {
		return "", unsafe("workspace parent is not canonical", err)
	}
	if parent != r.binding.ManagedRoot || filepath.Base(path) != workspaceID {
		return "", unsafe("workspace path escaped the managed root", nil)
	}
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", unsafe("workspace path is a symlink", nil)
	} else if err != nil && !isNotExist(err) {
		return "", unsafe("workspace path cannot be inspected", err)
	}
	return path, nil
}

func (r *Repository) Create(ctx context.Context, record workspace.Record) error {
	return r.withWorktreeMetadataLock(ctx, func() error { return r.createLocked(ctx, record) })
}

func (r *Repository) createLocked(ctx context.Context, record workspace.Record) error {
	path, err := r.WorkspacePath(record.WorkspaceID.String())
	if err != nil {
		return err
	}
	if path != record.CanonicalPath {
		return &workspace.Error{Kind: workspace.KindPathUnsafe, Message: "reserved workspace path differs from repository construction", Observed: map[string]string{"path": record.CanonicalPath}, Expected: map[string]string{"path": path}}
	}
	if _, err := os.Lstat(path); err == nil {
		probe, probeErr := r.Probe(ctx, record)
		if probeErr == nil && probe.Registered && probe.Branch == record.OwnedBranch {
			return nil
		}
		return &workspace.Error{Kind: workspace.KindWorkspaceConflict, Message: "workspace path already exists without matching registration", Cause: probeErr}
	} else if !isNotExist(err) {
		return &workspace.Error{Kind: workspace.KindGitFailure, Message: "inspect reserved workspace path", Cause: err}
	}
	if _, err := r.runner.Run(ctx, r.binding.CommonDir, "worktree", "add", "-b", record.OwnedBranch, path, record.PinnedBase); err != nil {
		return &workspace.Error{Kind: workspace.KindWorkspaceConflict, Message: "create owned Git worktree", Cause: err}
	}
	return nil
}

func (r *Repository) Probe(ctx context.Context, record workspace.Record) (workspace.Probe, error) {
	output, err := r.runner.Run(ctx, r.binding.CommonDir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return workspace.Probe{}, &workspace.Error{Kind: workspace.KindGitFailure, Message: "list Git worktrees", Cause: err}
	}
	registered := false
	for _, field := range strings.Split(output, "\x00") {
		if strings.HasPrefix(field, "worktree ") {
			candidate, canonicalErr := canonicalExistingDirectory(strings.TrimPrefix(field, "worktree "))
			if canonicalErr == nil && candidate == record.CanonicalPath {
				registered = true
				break
			}
		}
	}
	if !registered {
		return workspace.Probe{Registered: false}, nil
	}
	head, err := r.runner.Run(ctx, record.CanonicalPath, "rev-parse", "HEAD")
	if err != nil {
		return workspace.Probe{}, &workspace.Error{Kind: workspace.KindGitFailure, Message: "read workspace HEAD", Cause: err}
	}
	branch, err := r.runner.Run(ctx, record.CanonicalPath, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil {
		return workspace.Probe{}, &workspace.Error{Kind: workspace.KindWorkspaceConflict, Message: "workspace is not on an owned branch", Cause: err}
	}
	gitDir, err := r.runner.Run(ctx, record.CanonicalPath, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return workspace.Probe{}, &workspace.Error{Kind: workspace.KindGitFailure, Message: "resolve workspace Git directory", Cause: err}
	}
	gitDir, err = canonicalExistingDirectory(gitDir)
	if err != nil {
		return workspace.Probe{}, &workspace.Error{Kind: workspace.KindPathUnsafe, Message: "workspace Git directory is not canonical", Cause: err}
	}
	indexPath, err := r.runner.Run(ctx, record.CanonicalPath, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return workspace.Probe{}, &workspace.Error{Kind: workspace.KindGitFailure, Message: "resolve workspace index", Cause: err}
	}
	indexPath = filepath.Clean(indexPath)
	if !within(gitDir, r.binding.CommonDir) || !within(indexPath, gitDir) {
		return workspace.Probe{}, &workspace.Error{Kind: workspace.KindPathUnsafe, Message: "workspace Git metadata escaped the common directory"}
	}
	status, err := r.runner.Run(ctx, record.CanonicalPath, "status", "--porcelain=v1", "--untracked-files=all")
	if err != nil {
		return workspace.Probe{}, &workspace.Error{Kind: workspace.KindGitFailure, Message: "inspect workspace status", Cause: err}
	}
	return workspace.Probe{Registered: true, Head: head, Branch: branch, GitDir: gitDir, IndexPath: indexPath, Clean: status == ""}, nil
}

// RegistrationCensus is an immutable, attempt-scoped observation of the Git
// worktree registry. Repository.RegistrationCensus constructs a fresh value on
// every call; it is deliberately not cached on Repository because a later
// observation attempt must see later registrations.
type RegistrationCensus struct {
	primaryPath string
	managedRoot string
	paths       map[string]struct{}
}

// Registered reports whether canonicalPath was present in the exact Git
// registry snapshot used to construct the census. The query is canonicalized
// again so a path replaced by a symlink after the snapshot cannot inherit the
// old path's registration fact.
func (c RegistrationCensus) Registered(canonicalPath string) (bool, error) {
	absolute, err := filepath.Abs(canonicalPath)
	if err != nil {
		return false, unsafe("workspace census query cannot be made absolute", err)
	}
	absolute = filepath.Clean(absolute)
	path, err := canonicalProspective(canonicalPath)
	if err != nil {
		return false, unsafe("workspace census query cannot be canonicalized", err)
	}
	if path != absolute {
		return false, unsafe("workspace census query is not canonical", nil)
	}
	if path != c.primaryPath && !within(path, c.managedRoot) {
		return false, unsafe("workspace census query escaped the repository binding", nil)
	}
	_, registered := c.paths[path]
	return registered, nil
}

// RegistrationCensus reads Git's complete worktree registry exactly once and
// returns a defensively encapsulated snapshot suitable for one observation
// attempt. Mutation paths continue to use Probe, which validates the complete
// workspace identity and filesystem state.
func (r *Repository) RegistrationCensus(ctx context.Context) (RegistrationCensus, error) {
	output, err := r.runner.Run(ctx, r.binding.CommonDir, "worktree", "list", "--porcelain", "-z")
	if err != nil {
		return RegistrationCensus{}, &workspace.Error{Kind: workspace.KindGitFailure, Message: "list Git worktrees for registration census", Cause: err}
	}
	return parseRegistrationCensus(r.binding, output)
}

func parseRegistrationCensus(binding workspace.RepositoryBinding, output string) (RegistrationCensus, error) {
	malformed := func(message string) (RegistrationCensus, error) {
		return RegistrationCensus{}, &workspace.Error{Kind: workspace.KindGitFailure, Message: "malformed Git worktree census: " + message}
	}
	if output == "" || !strings.HasSuffix(output, "\x00\x00") {
		return malformed("registry is empty or lacks its terminal record delimiter")
	}
	fields := strings.Split(output, "\x00")
	fields = fields[:len(fields)-1]
	paths := make(map[string]struct{})
	recordOpen := false
	attributes := make(map[string]struct{})
	for _, field := range fields {
		if field == "" {
			if !recordOpen {
				return malformed("empty record has no worktree path")
			}
			if _, present := attributes["HEAD"]; !present {
				return malformed("worktree record omits HEAD")
			}
			identityKinds := 0
			for _, kind := range []string{"branch", "detached", "bare"} {
				if _, present := attributes[kind]; present {
					identityKinds++
				}
			}
			if identityKinds != 1 {
				return malformed("worktree record must have one branch, detached, or bare identity")
			}
			recordOpen = false
			attributes = make(map[string]struct{})
			continue
		}
		if strings.HasPrefix(field, "worktree ") {
			if recordOpen {
				return malformed("worktree records are not separated")
			}
			rawPath := strings.TrimPrefix(field, "worktree ")
			if rawPath == "" {
				return malformed("worktree path is empty")
			}
			path, err := canonicalExistingDirectory(rawPath)
			if err != nil {
				return malformed("worktree path is not an existing canonical directory")
			}
			if path != binding.PrimaryPath && !within(path, binding.ManagedRoot) {
				return RegistrationCensus{}, unsafe("Git worktree census escaped the repository binding", nil)
			}
			if _, duplicate := paths[path]; duplicate {
				return malformed("duplicate canonical worktree path")
			}
			paths[path] = struct{}{}
			recordOpen = true
			continue
		}
		kind, valid := worktreeCensusAttributeKind(field)
		if !recordOpen || !valid {
			return malformed("record contains an unexpected attribute")
		}
		if _, duplicate := attributes[kind]; duplicate {
			return malformed("record contains a duplicate attribute")
		}
		attributes[kind] = struct{}{}
	}
	if recordOpen {
		return malformed("last worktree record is not terminated")
	}
	if _, present := paths[binding.PrimaryPath]; !present {
		return malformed("primary checkout is absent")
	}
	return RegistrationCensus{primaryPath: binding.PrimaryPath, managedRoot: binding.ManagedRoot, paths: paths}, nil
}

func worktreeCensusAttributeKind(field string) (string, bool) {
	switch {
	case strings.HasPrefix(field, "HEAD "):
		return "HEAD", isObjectID(strings.TrimPrefix(field, "HEAD "))
	case strings.HasPrefix(field, "branch "):
		return "branch", strings.TrimPrefix(field, "branch ") != ""
	case field == "detached", field == "bare", field == "locked", field == "prunable":
		return field, true
	case strings.HasPrefix(field, "locked "):
		return "locked", strings.TrimSpace(strings.SplitN(field, " ", 2)[1]) != ""
	case strings.HasPrefix(field, "prunable "):
		return "prunable", strings.TrimSpace(strings.SplitN(field, " ", 2)[1]) != ""
	default:
		return "", false
	}
}

func (r *Repository) PrepareDependencies(ctx context.Context, record workspace.Record, dependencies []workspace.Dependency) (string, error) {
	ordered, err := workspace.CanonicalDependencies(dependencies)
	if err != nil {
		return "", err
	}
	probe, err := r.Probe(ctx, record)
	if err != nil {
		return "", err
	}
	if !probe.Registered || probe.Branch != record.OwnedBranch || !probe.Clean {
		return "", &workspace.Error{Kind: workspace.KindWorkspaceConflict, Message: "dependency preparation requires the clean registered owned branch", Observed: map[string]string{"branch": probe.Branch, "clean": fmt.Sprint(probe.Clean)}, Expected: map[string]string{"branch": record.OwnedBranch, "clean": "true"}}
	}
	current := record.PinnedBase
	for _, dependency := range ordered {
		if _, err := r.runner.Run(ctx, r.binding.CommonDir, "cat-file", "-e", dependency.Candidate+"^{commit}"); err != nil {
			return "", &workspace.Error{Kind: workspace.KindInvalid, Message: "dependency candidate is not a commit", Observed: map[string]string{"candidate": dependency.Candidate}, Cause: err}
		}
		output, mergeErr := r.runner.Run(ctx, r.binding.CommonDir, "merge-tree", "--write-tree", "--messages", current, dependency.Candidate)
		lines := strings.Split(output, "\n")
		if mergeErr != nil || len(lines) == 0 || !isObjectID(strings.TrimSpace(lines[0])) {
			return "", &workspace.Error{Kind: workspace.KindDependencyConflict, Message: "accepted dependency conflicts with the prepared head", Observed: map[string]string{"prepared_head": current, "dependency_work_id": dependency.WorkID.String(), "candidate": dependency.Candidate}, Expected: map[string]string{"dependency_merge": "conflict-free"}, Cause: mergeErr}
		}
		tree := strings.TrimSpace(lines[0])
		environment := []string{
			"GIT_AUTHOR_NAME=Spec Kitty", "GIT_AUTHOR_EMAIL=spec-kitty@example.invalid", "GIT_AUTHOR_DATE=2000-01-01T00:00:00Z",
			"GIT_COMMITTER_NAME=Spec Kitty", "GIT_COMMITTER_EMAIL=spec-kitty@example.invalid", "GIT_COMMITTER_DATE=2000-01-01T00:00:00Z",
		}
		current, err = r.runner.RunEnv(ctx, r.binding.CommonDir, environment, "commit-tree", tree, "-p", current, "-p", dependency.Candidate, "-m", "Prepare dependency "+dependency.WorkID.String())
		if err != nil {
			return "", &workspace.Error{Kind: workspace.KindGitFailure, Message: "create deterministic dependency merge commit", Cause: err}
		}
	}
	if probe.Head != record.PinnedBase {
		if probe.Head == current {
			return current, nil
		}
		return "", &workspace.Error{Kind: workspace.KindTargetMoved, Message: "workspace HEAD moved from its pinned or deterministic dependency head", Observed: map[string]string{"head": probe.Head}, Expected: map[string]string{"pinned_base": record.PinnedBase, "dependency_head": current}}
	}
	if current != record.PinnedBase {
		if _, err := r.runner.Run(ctx, record.CanonicalPath, "reset", "--hard", current); err != nil {
			return "", &workspace.Error{Kind: workspace.KindGitFailure, Message: "publish dependency-prepared head to owned branch", Cause: err}
		}
	}
	return current, nil
}

func isObjectID(value string) bool {
	if len(value) != 40 && len(value) != 64 {
		return false
	}
	for _, character := range value {
		if !strings.ContainsRune("0123456789abcdefABCDEF", character) {
			return false
		}
	}
	return true
}
