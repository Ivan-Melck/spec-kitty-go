package gitrepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/workspace"
)

type Repository struct {
	runner  Runner
	binding workspace.RepositoryBinding
}

func Bind(ctx context.Context, runner Runner, repositoryID ids.RepositoryID, projectID ids.ProjectID, primaryPath, managedRoot, targetRef string, now time.Time) (*Repository, error) {
	if _, err := ids.ParseRepositoryID(repositoryID.String()); err != nil {
		return nil, invalid("repository ID is invalid", err)
	}
	if _, err := ids.ParseProjectID(projectID.String()); err != nil {
		return nil, invalid("project ID is invalid", err)
	}
	primary, err := canonicalExistingDirectory(primaryPath)
	if err != nil {
		return nil, invalid("primary checkout is not canonical", err)
	}
	top, err := runner.Run(ctx, primary, "rev-parse", "--show-toplevel")
	if err != nil {
		return nil, invalid("primary path is not a Git checkout", err)
	}
	top, err = canonicalExistingDirectory(top)
	if err != nil || top != primary {
		return nil, invalid("primary path must be the checkout root", err)
	}
	common, err := runner.Run(ctx, primary, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return nil, invalid("resolve Git common directory", err)
	}
	common, err = canonicalExistingDirectory(common)
	if err != nil {
		return nil, invalid("canonicalize Git common directory", err)
	}
	root, err := canonicalProspective(managedRoot)
	if err != nil {
		return nil, unsafe("managed workspace root cannot be canonicalized", err)
	}
	if pathsOverlap(root, primary) || pathsOverlap(root, common) {
		return nil, unsafe("managed workspace root must be outside the primary checkout and Git common directory", nil)
	}
	if targetRef == "" {
		targetRef = "refs/heads/main"
	}
	snapshot, err := snapshotPrimary(ctx, runner, primary)
	if err != nil {
		return nil, err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	binding := workspace.RepositoryBinding{
		RepositoryID: repositoryID, ProjectID: projectID, CommonDir: common, PrimaryPath: primary,
		ManagedRoot: root, TargetRef: targetRef, Primary: snapshot, CreatedAt: now.UTC(), VerifiedAt: now.UTC(),
	}
	return &Repository{runner: runner, binding: binding}, nil
}

func (r *Repository) Binding() workspace.RepositoryBinding { return r.binding }

func (r *Repository) PrepareManagedRoot() error {
	prospective, err := canonicalProspective(r.binding.ManagedRoot)
	if err != nil || prospective != r.binding.ManagedRoot || pathsOverlap(prospective, r.binding.PrimaryPath) || pathsOverlap(prospective, r.binding.CommonDir) {
		return unsafe("managed workspace root changed after validation", err)
	}
	if err := os.MkdirAll(prospective, 0o700); err != nil {
		return unsafe("managed workspace root cannot be created", err)
	}
	canonical, err := canonicalExistingDirectory(prospective)
	if err != nil || canonical != r.binding.ManagedRoot || pathsOverlap(canonical, r.binding.PrimaryPath) || pathsOverlap(canonical, r.binding.CommonDir) {
		return unsafe("managed workspace root resolved inside the primary checkout or Git common directory", err)
	}
	return nil
}

func (r *Repository) SnapshotPrimary(ctx context.Context) (workspace.PrimarySnapshot, error) {
	return snapshotPrimary(ctx, r.runner, r.binding.PrimaryPath)
}

func (r *Repository) ValidateExternalPath(path string) error {
	canonical, err := canonicalProspective(path)
	if err != nil {
		return unsafe("external store path cannot be canonicalized", err)
	}
	if within(canonical, r.binding.PrimaryPath) || within(canonical, r.binding.CommonDir) || within(canonical, r.binding.ManagedRoot) {
		return unsafe("control and tracker stores must be outside Git and managed workspaces", nil)
	}
	return nil
}

// PrepareExternalPath validates every existing symlink component before making
// the parent directory. Callers must use the returned path when opening a store.
func (r *Repository) PrepareExternalPath(path string) (string, error) {
	if err := r.ValidateExternalPath(path); err != nil {
		return "", err
	}
	canonical, err := canonicalProspective(path)
	if err != nil {
		return "", unsafe("external store path cannot be canonicalized", err)
	}
	if err := os.MkdirAll(filepath.Dir(canonical), 0o700); err != nil {
		return "", unsafe("external store parent cannot be created", err)
	}
	canonical, err = canonicalProspective(canonical)
	if err != nil {
		return "", unsafe("external store path cannot be canonicalized after parent creation", err)
	}
	if within(canonical, r.binding.PrimaryPath) || within(canonical, r.binding.CommonDir) || within(canonical, r.binding.ManagedRoot) {
		return "", unsafe("control and tracker stores must be outside Git and managed workspaces", nil)
	}
	return canonical, nil
}

func snapshotPrimary(ctx context.Context, runner Runner, primary string) (workspace.PrimarySnapshot, error) {
	head, err := runner.Run(ctx, primary, "rev-parse", "HEAD")
	if err != nil {
		return workspace.PrimarySnapshot{}, &workspace.Error{Kind: workspace.KindGitFailure, Message: "read primary HEAD", Cause: err}
	}
	indexPath, err := runner.Run(ctx, primary, "rev-parse", "--path-format=absolute", "--git-path", "index")
	if err != nil {
		return workspace.PrimarySnapshot{}, &workspace.Error{Kind: workspace.KindGitFailure, Message: "resolve primary index", Cause: err}
	}
	indexDigest, err := digestFile(indexPath)
	if err != nil {
		return workspace.PrimarySnapshot{}, &workspace.Error{Kind: workspace.KindGitFailure, Message: "hash primary index", Cause: err}
	}
	treeDigest, err := digestTree(primary)
	if err != nil {
		return workspace.PrimarySnapshot{}, &workspace.Error{Kind: workspace.KindGitFailure, Message: "hash primary checkout tree", Cause: err}
	}
	return workspace.PrimarySnapshot{Head: head, IndexDigest: indexDigest, TreeDigest: treeDigest}, nil
}

func digestFile(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return "", err
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func digestTree(root string) (string, error) {
	var paths []string
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		if relative == ".git" || strings.HasPrefix(relative, ".git"+string(filepath.Separator)) {
			if entry.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}
		if relative != "." {
			paths = append(paths, relative)
		}
		return nil
	})
	if err != nil {
		return "", err
	}
	sort.Strings(paths)
	hash := sha256.New()
	for _, relative := range paths {
		path := filepath.Join(root, relative)
		info, err := os.Lstat(path)
		if err != nil {
			return "", err
		}
		_, _ = fmt.Fprintf(hash, "%s\x00%d\x00", filepath.ToSlash(relative), info.Mode())
		switch {
		case info.Mode()&os.ModeSymlink != 0:
			target, err := os.Readlink(path)
			if err != nil {
				return "", err
			}
			_, _ = io.WriteString(hash, target)
		case info.Mode().IsRegular():
			file, err := os.Open(path)
			if err != nil {
				return "", err
			}
			_, copyErr := io.Copy(hash, file)
			closeErr := file.Close()
			if copyErr != nil {
				return "", copyErr
			}
			if closeErr != nil {
				return "", closeErr
			}
		}
		_, _ = hash.Write([]byte{0})
	}
	return hex.EncodeToString(hash.Sum(nil)), nil
}

func canonicalExistingDirectory(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	canonical, err := filepath.EvalSymlinks(absolute)
	if err != nil {
		return "", err
	}
	info, err := os.Stat(canonical)
	if err != nil {
		return "", err
	}
	if !info.IsDir() {
		return "", fmt.Errorf("%q is not a directory", canonical)
	}
	return filepath.Clean(canonical), nil
}

func canonicalProspective(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	absolute = filepath.Clean(absolute)
	if _, err := os.Lstat(absolute); err == nil {
		return filepath.EvalSymlinks(absolute)
	} else if !isNotExist(err) {
		return "", err
	}
	current := absolute
	var suffix []string
	for {
		parent := filepath.Dir(current)
		if parent == current {
			return "", fmt.Errorf("no existing ancestor for %q", absolute)
		}
		suffix = append(suffix, filepath.Base(current))
		current = parent
		if _, err := os.Lstat(current); err == nil {
			break
		} else if !isNotExist(err) {
			return "", err
		}
	}
	resolved, err := filepath.EvalSymlinks(current)
	if err != nil {
		return "", err
	}
	for index := len(suffix) - 1; index >= 0; index-- {
		resolved = filepath.Join(resolved, suffix[index])
	}
	return filepath.Clean(resolved), nil
}

func pathsOverlap(left, right string) bool { return within(left, right) || within(right, left) }

func within(candidate, root string) bool {
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func invalid(message string, cause error) error {
	return &workspace.Error{Kind: workspace.KindInvalid, Message: message, Cause: cause}
}

func unsafe(message string, cause error) error {
	return &workspace.Error{Kind: workspace.KindPathUnsafe, Message: message, Cause: cause}
}

func isNotExist(err error) bool { return errors.Is(err, os.ErrNotExist) }
