package setup

// Shared primitives for the ported upgrade migrations
// (specify_cli/upgrade/migrations/*.py): fail-closed ignore-file reads,
// tool-owned tree removal (kernel.tree_removal), the asset-preservation
// guards (specify_cli.asset_preservation), the generated-file writer
// (runtime.generated_writer) and the package mission tree (the wheel's
// specify_cli/missions data, embedded under internal/assets).

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/agents"
	"github.com/spec-kitty/go-kitty3/internal/assets"
	"github.com/spec-kitty/go-kitty3/internal/kittify"
	"github.com/spec-kitty/go-kitty3/internal/project"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// failClosedError is GitignorePathError / MigrationStateUnreadableError: the
// runner records a migration that raises it as failed instead of skipped.
type failClosedError struct{ msg string }

func (e *failClosedError) Error() string { return e.msg }

func failClosed(format string, a ...any) error { return &failClosedError{fmt.Sprintf(format, a...)} }

func isFailClosed(err error) bool {
	var f *failClosedError
	return errors.As(err, &f)
}

func isSymlink(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

// pathExists is Path.exists() (follows symlinks).
func pathExists(p string) bool { _, err := os.Stat(p); return err == nil }

// lexists is exists() or is_symlink().
func lexists(p string) bool { _, err := os.Lstat(p); return err == nil }

func isDir(p string) bool { st, err := os.Stat(p); return err == nil && st.IsDir() }

// listDir is sorted(Path.iterdir()); upstream's unsorted iterdir follows the
// filesystem's directory order, which the port normalises to name order.
func listDir(p string) []string {
	ents, err := os.ReadDir(p)
	if err != nil {
		return nil
	}
	out := make([]string, 0, len(ents))
	for _, e := range ents {
		out = append(out, filepath.Join(p, e.Name()))
	}
	return out
}

// subdirs lists the directories directly under p (iterdir + is_dir).
func subdirs(p string) []string {
	var out []string
	for _, c := range listDir(p) {
		if isDir(c) {
			out = append(out, c)
		}
	}
	return out
}

// globFiles is sorted(dir.glob(pattern)) for a single-segment pattern.
func globFiles(dir, pattern string) []string {
	var out []string
	for _, c := range listDir(dir) {
		if ok, _ := path.Match(pattern, filepath.Base(c)); ok {
			out = append(out, c)
		}
	}
	return out
}

func readText(p string) (string, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// readTextSig reads with encoding="utf-8-sig" (a leading BOM is dropped).
func readTextSig(p string) (string, error) {
	s, err := readText(p)
	return strings.TrimPrefix(s, "\ufeff"), err
}

func decodeIgnoreErrors(b []byte) string {
	if s, err := pyjson.DecodeUTF8(b); err == nil {
		return s
	}
	return strings.ToValidUTF8(string(b), "")
}

// readIgnoreFileText is gitignore_manager.read_ignore_file_text with
// encoding="utf-8-sig", errors="ignore": "" when absent, fail-closed on a
// symlink.
func readIgnoreFileText(p string) (string, error) {
	if isSymlink(p) {
		return "", failClosed("%s is a symlink; refusing to read through it", p)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return "", nil
		}
		return "", err
	}
	return strings.TrimPrefix(decodeIgnoreErrors(b), "\ufeff"), nil
}

// pyRepr is repr() of a str.
func pyRepr(s string) string { return pyjson.ReprString(s) }

// pySplitLines is str.splitlines().
func pySplitLines(s string) []string { return agents.SplitLines(s) }

// pySplitLinesKeep is str.splitlines(keepends=True).
func pySplitLinesKeep(s string) []string {
	var out []string
	rest := s
	for rest != "" {
		lines := agents.SplitLines(rest)
		first := lines[0]
		end := len(first)
		if end < len(rest) {
			r := rest[end:]
			switch {
			case strings.HasPrefix(r, "\r\n"):
				end += 2
			default:
				_, size := firstRune(r)
				end += size
			}
		}
		out = append(out, rest[:end])
		rest = rest[end:]
	}
	return out
}

func firstRune(s string) (rune, int) {
	for i, r := range s {
		if i == 0 {
			n := len(string(r))
			return r, n
		}
	}
	return 0, 0
}

// ---------------------------------------------------------------------------
// kernel.tree_removal.remove_tool_owned_tree
// ---------------------------------------------------------------------------

func resolvePath(p string) string {
	abs, err := filepath.Abs(p)
	if err != nil {
		abs = p
	}
	if r, err := filepath.EvalSymlinks(abs); err == nil {
		return r
	}
	// Path.resolve(strict=False): resolve the existing prefix.
	dir, base := filepath.Split(abs)
	if dir == abs || dir == "" {
		return abs
	}
	return filepath.Join(resolvePath(strings.TrimSuffix(dir, string(filepath.Separator))), base)
}

func removeToolOwnedTree(p, toolRoot, reason string) (bool, error) {
	if !lexists(p) {
		return false, nil
	}
	resolved, root := resolvePath(p), resolvePath(toolRoot)
	if resolved != root && !strings.HasPrefix(resolved, root+string(filepath.Separator)) {
		return false, fmt.Errorf("TOOL_OWNED_PATH_UNPROVEN: %s is not inside the owned root %s (%s); nothing was deleted", p, toolRoot, reason)
	}
	for cursor := resolved; ; cursor = filepath.Dir(cursor) {
		if lexists(filepath.Join(cursor, ".git")) {
			return false, fmt.Errorf("TOOL_OWNED_PATH_UNPROVEN: %s is a git checkout (%s); delete it through guarded_tree_delete, nothing was deleted", cursor, reason)
		}
		if cursor == root || cursor == filepath.Dir(cursor) {
			break
		}
	}
	if isSymlink(p) {
		return true, os.Remove(p)
	}
	if isDir(resolved) {
		return true, rmtreeWritable(resolved)
	}
	return true, os.Remove(resolved)
}

// rmtreeWritable is shutil.rmtree with a retry that makes entries writable.
func rmtreeWritable(p string) error {
	if err := os.RemoveAll(p); err == nil {
		return nil
	}
	filepath.Walk(p, func(q string, info os.FileInfo, err error) error {
		if err == nil && info.IsDir() {
			os.Chmod(q, 0o700)
		}
		return nil
	})
	return os.RemoveAll(p)
}

// ---------------------------------------------------------------------------
// runtime.generated_writer.write_generated_file
// ---------------------------------------------------------------------------

func writeGeneratedFile(p string, content []byte, readOnly bool) error {
	if isSymlink(p) {
		return fmt.Errorf("write_generated_file refuses to write through a symlink: %s (the managed generated-file tree is expected to contain only regular files; replace or remove the symlink before writing)", p)
	}
	tmp := filepath.Join(filepath.Dir(p), fmt.Sprintf(".%s.tmp%d", filepath.Base(p), os.Getpid()))
	if lexists(tmp) {
		return fmt.Errorf("Refusing to replace an unproven atomic writer artifact: %s", tmp)
	}
	if st, err := os.Stat(p); err == nil && st.Mode().Perm()&0o222 == 0 {
		os.Chmod(p, st.Mode().Perm()|0o222)
	}
	err := os.WriteFile(tmp, content, 0o666&^umask())
	if err == nil {
		err = os.Rename(tmp, p)
	}
	if err != nil {
		os.Remove(tmp)
	}
	if readOnly {
		if st, serr := os.Stat(p); serr == nil {
			os.Chmod(p, st.Mode().Perm()&^0o222)
		}
	}
	return err
}

// ---------------------------------------------------------------------------
// asset_preservation: ownership provers and destructive-operation guards
// ---------------------------------------------------------------------------

type ownershipProof struct{ kind, detail string }

type prover func(p, root string) *ownershipProof

func relPosix(p, root string) (string, bool) {
	r, err := filepath.Rel(root, p)
	if err != nil || r == ".." || strings.HasPrefix(r, ".."+string(filepath.Separator)) {
		return "", false
	}
	return filepath.ToSlash(r), true
}

const commandVersionMarker = "<!-- spec-kitty-command-version:"

// canonicalContentProver is CanonicalContentProver (marker, optional
// canonical bytes, optional scan window).
func canonicalContentProver(canonical []byte, scanBytes int, checkMarker bool) prover {
	return func(p, root string) *ownershipProof {
		rel, ok := relPosix(p, root)
		if !ok || isSymlink(p) || !isFile(p) {
			return nil
		}
		content, err := os.ReadFile(p)
		if err != nil {
			return nil
		}
		window := content
		if scanBytes > 0 && len(window) > scanBytes {
			window = window[:scanBytes]
		}
		if checkMarker && bytes.Contains(window, []byte(commandVersionMarker)) {
			return &ownershipProof{"canonical_content", "version-marker:" + rel}
		}
		if canonical != nil && bytes.Equal(content, canonical) {
			return &ownershipProof{"canonical_content", "canonical-bytes:" + rel}
		}
		return nil
	}
}

func defaultCanonicalProver() prover { return canonicalContentProver(nil, 0, true) }

// manifestProver is ManifestProver (skills manifest / command-skills manifest).
func manifestProver(checkManaged, checkCommand bool) prover {
	var proveFile func(p, rel, root string) *ownershipProof
	proveFile = func(p, rel, root string) *ownershipProof {
		if checkManaged {
			if m := agents.LoadSkillsManifest(root); m != nil {
				if b, err := os.ReadFile(p); err == nil {
					h := "sha256:" + sha256Hex(b)
					for _, e := range m.Entries {
						if e.InstalledPath == rel && e.DeliveryMode == "copy" && e.ContentHash == h {
							return &ownershipProof{"manifest", ".kittify/skills-manifest.json:" + rel}
						}
					}
				}
			}
		}
		if checkCommand && pathExists(filepath.Join(root, ".kittify", "command-skills-manifest.json")) {
			if entries := agents.LoadCommandSkillsManifest(root); len(entries) > 0 {
				if b, err := os.ReadFile(p); err == nil {
					h := sha256Hex(b)
					for _, e := range entries {
						if e.Path == rel && e.ContentHash == h {
							return &ownershipProof{"manifest", ".kittify/command-skills-manifest.json:" + rel}
						}
					}
				}
			}
		}
		return nil
	}
	return func(p, root string) *ownershipProof {
		rel, ok := relPosix(p, root)
		if !ok || isSymlink(p) {
			return nil
		}
		if isDir(p) {
			var members []string
			filepath.Walk(p, func(q string, info os.FileInfo, err error) error {
				if err == nil && q != p && (info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0) {
					members = append(members, q)
				}
				return nil
			})
			if len(members) == 0 {
				return nil
			}
			for _, m := range members {
				if isSymlink(m) || !isFile(m) {
					return nil
				}
				mr, ok := relPosix(m, root)
				if !ok || proveFile(m, mr, root) == nil {
					return nil
				}
			}
			return &ownershipProof{"manifest", "manifest-dir:" + rel}
		}
		if isFile(p) {
			return proveFile(p, rel, root)
		}
		return nil
	}
}

func managedPathProver(managed []string) prover {
	set := map[string]bool{}
	for _, m := range managed {
		set[m] = true
	}
	return func(p, root string) *ownershipProof {
		if rel, ok := relPosix(p, root); ok && set[rel] {
			return &ownershipProof{"managed_path", "package-managed:" + rel}
		}
		return nil
	}
}

func anyProver(ps ...prover) prover {
	return func(p, root string) *ownershipProof {
		for _, pr := range ps {
			if proof := pr(p, root); proof != nil {
				return proof
			}
		}
		return nil
	}
}

type ownershipVerdict struct {
	owned      bool
	preserved  string
	backupPath string
	diagnostic string
}

func displayRel(p, root string) string {
	if r, ok := relPosix(p, root); ok {
		return r
	}
	return filepath.Base(p)
}

func removeNode(p string, isTree bool) error {
	if isSymlink(p) || isFile(p) {
		if err := os.Remove(p); err != nil {
			os.Chmod(filepath.Dir(p), 0o755)
			return os.Remove(p)
		}
		return nil
	}
	if isDir(p) {
		if isTree {
			return rmtreeWritable(p)
		}
		return os.Remove(p)
	}
	return nil
}

// guardDestructiveRemoval is asset_preservation.guard_destructive_removal.
func guardDestructiveRemoval(p, root string, pr prover, isTree bool, backupParent string, dryRun bool) (ownershipVerdict, error) {
	rel := displayRel(p, root)
	if !lexists(p) {
		return ownershipVerdict{diagnostic: "Skipped " + rel + "; path is absent"}, nil
	}
	if proof := pr(p, root); proof != nil {
		verb := "Removed"
		if dryRun {
			verb = "Would remove"
		} else if err := removeNode(p, isTree); err != nil {
			return ownershipVerdict{}, err
		}
		return ownershipVerdict{owned: true, diagnostic: fmt.Sprintf("%s package-owned %s (proof: %s)", verb, rel, proof.kind)}, nil
	}
	const reason = "not package-owned"
	if backupParent == "" {
		return ownershipVerdict{preserved: p, diagnostic: fmt.Sprintf("Preserved %s; %s (name is not ownership proof) — left in place", rel, reason)}, nil
	}
	if dryRun {
		return ownershipVerdict{preserved: p, diagnostic: fmt.Sprintf("Would preserve %s; %s — would archive before parent removal", rel, reason)}, nil
	}
	archived, err := archiveInto(p, root, backupParent)
	if err != nil {
		return ownershipVerdict{}, err
	}
	if err := removeNode(p, isTree); err != nil {
		return ownershipVerdict{}, err
	}
	return ownershipVerdict{preserved: archived, backupPath: archived,
		diagnostic: fmt.Sprintf("Preserved %s; %s — archived to %s before parent removal", rel, reason, displayRel(archived, root))}, nil
}

type overwriteVerdict struct {
	proceed    bool
	reason     string
	diagnostic string
	backupPath string
}

// guardDestructiveOverwrite is asset_preservation.guard_destructive_overwrite.
func guardDestructiveOverwrite(dest, root string, substantive, authorized bool, pr prover, backupParent string) overwriteVerdict {
	rel := displayRel(dest, root)
	if !lexists(dest) {
		if !substantive {
			return overwriteVerdict{reason: "absent-empty-replacement", diagnostic: "Refused to fabricate an empty " + rel + "; destination is absent and replacement is not substantive"}
		}
		return overwriteVerdict{proceed: true, reason: "absent", diagnostic: "Proceeding; " + rel + " is absent"}
	}
	if pr != nil {
		if proof := pr(dest, root); proof != nil {
			return overwriteVerdict{proceed: true, reason: "package-owned (" + proof.kind + ")", diagnostic: fmt.Sprintf("Proceeding over package-owned %s (proof: %s)", rel, proof.kind)}
		}
	}
	if !substantive {
		return overwriteVerdict{reason: "would-truncate-to-empty", diagnostic: "Refused to truncate " + rel + " to empty; existing bytes are not proven package-owned"}
	}
	if !authorized {
		return overwriteVerdict{reason: "unauthorized", diagnostic: "Refused to overwrite " + rel + "; not proven package-owned and not authorized"}
	}
	v := overwriteVerdict{proceed: true, reason: "authorized-overwrite", diagnostic: "Proceeding to overwrite " + rel + "; authorized"}
	if backupParent != "" {
		if b, err := archiveInto(dest, root, backupParent); err == nil {
			v.backupPath = b
			v.diagnostic = "Proceeding to overwrite " + rel + "; archived to " + displayRel(b, root) + " first"
		}
	}
	return v
}

func compactStamp() string { return nowUTC().Format("20060102T150405Z") }

func nowUTC() time.Time {
	if v := os.Getenv("SPEC_KITTY_FAKE_NOW"); v != "" {
		if t, err := time.Parse(time.RFC3339Nano, v); err == nil {
			return t.UTC()
		}
	}
	return time.Now().UTC()
}

// allocateBackupDir is template.manager._allocate_backup_dir.
func allocateBackupDir(parent string) (string, error) {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	ts := compactStamp()
	for suffix := 0; ; suffix++ {
		name := ".backup-" + ts
		if suffix > 0 {
			name = fmt.Sprintf("%s-%d", name, suffix)
		}
		c := filepath.Join(parent, name)
		if err := os.Mkdir(c, 0o777&^umask()); err == nil {
			return c, nil
		} else if !os.IsExist(err) {
			return "", err
		}
	}
}

func archiveInto(p, root, backupParent string) (string, error) {
	dir, err := allocateBackupDir(backupParent)
	if err != nil {
		return "", err
	}
	rel, ok := relPosix(p, root)
	if !ok {
		rel = filepath.Base(p)
	}
	dest := filepath.Join(dir, filepath.FromSlash(rel))
	return dest, copyNodeVerbatim(p, dest)
}

func copyNodeVerbatim(src, dest string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	switch {
	case st.Mode()&os.ModeSymlink != 0:
		os.MkdirAll(filepath.Dir(dest), 0o755)
		t, _ := os.Readlink(src)
		return os.Symlink(t, dest)
	case st.IsDir():
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.Mkdir(dest, 0o755); err != nil {
			return err
		}
		for _, c := range listDir(src) {
			if err := copyNodeVerbatim(c, filepath.Join(dest, filepath.Base(c))); err != nil {
				return err
			}
		}
		os.Chmod(dest, st.Mode().Perm())
		os.Chtimes(dest, st.ModTime(), st.ModTime())
		return nil
	default:
		return copyFile2(src, dest)
	}
}

// copyFile2 is shutil.copy2 (content, mode bits, mtime).
func copyFile2(src, dest string) error {
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	b, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(dest); err == nil && fi.IsDir() {
		dest = filepath.Join(dest, filepath.Base(src))
	}
	recordUpgradeMutation(src, dest, false)
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil && !pathExists(filepath.Dir(dest)) {
		return err
	}
	if fi, err := os.Stat(dest); err == nil && fi.Mode().Perm()&0o200 == 0 {
		return &os.PathError{Op: "open", Path: dest, Err: fs.ErrPermission}
	}
	if err := os.WriteFile(dest, b, st.Mode().Perm()); err != nil {
		return err
	}
	os.Chmod(dest, st.Mode().Perm())
	return os.Chtimes(dest, st.ModTime(), st.ModTime())
}

// copyTree is shutil.copytree(src, dest) for an on-disk source.
func copyTree(src, dest string) error {
	recordUpgradeMutation(src, dest, false)
	if lexists(dest) {
		return &os.PathError{Op: "mkdir", Path: dest, Err: fs.ErrExist}
	}
	st, err := os.Stat(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dest, 0o755); err != nil {
		return err
	}
	for _, c := range listDir(src) {
		t := filepath.Join(dest, filepath.Base(c))
		if isSymlink(c) {
			l, _ := os.Readlink(c)
			if err := os.Symlink(l, t); err != nil {
				return err
			}
		} else if isDir(c) {
			if err := copyTree(c, t); err != nil {
				return err
			}
		} else if err := copyFile2(c, t); err != nil {
			return err
		}
	}
	os.Chmod(dest, st.Mode().Perm())
	return nil
}

// ---------------------------------------------------------------------------
// The wheel's specify_cli package data (embedded assets)
// ---------------------------------------------------------------------------

// pkgAsset maps a path under the installed specify_cli package to its
// embedded asset name.
func pkgAsset(rel string) string { return path.Join("specify_cli", rel) }

func pkgExists(rel string) bool { return assets.Exists(pkgAsset(rel)) }

func pkgRead(rel string) ([]byte, error) { return assets.ReadFile(pkgAsset(rel)) }

// copyPkgTree is shutil.copytree from a package directory: wheel files are
// 0644, directories 0755; mtimes are the copy time.
func copyPkgTree(rel, dest string) error {
	if lexists(dest) {
		return &os.PathError{Op: "mkdir", Path: dest, Err: fs.ErrExist}
	}
	root := pkgAsset(rel)
	return fs.WalkDir(assets.FS, root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		r := strings.TrimPrefix(strings.TrimPrefix(p, root), "/")
		t := filepath.Join(dest, filepath.FromSlash(r))
		if d.IsDir() {
			return os.MkdirAll(t, 0o755)
		}
		b, err := fs.ReadFile(assets.FS, p)
		if err != nil {
			return err
		}
		return os.WriteFile(t, b, 0o644)
	})
}

// copyPkgFile is shutil.copy2 from a package file.
func copyPkgFile(rel, dest string) error {
	b, err := pkgRead(rel)
	if err != nil {
		return err
	}
	if fi, err := os.Stat(dest); err == nil && fi.Mode().Perm()&0o200 == 0 {
		return &os.PathError{Op: "open", Path: dest, Err: fs.ErrPermission}
	}
	if err := os.WriteFile(dest, b, 0o644); err != nil {
		return err
	}
	return os.Chmod(dest, 0o644)
}

func countFiles(dir string) int {
	n := 0
	filepath.Walk(dir, func(p string, info os.FileInfo, err error) error {
		if err == nil && info.Mode().IsRegular() {
			n++
		}
		return nil
	})
	return n
}

// ---------------------------------------------------------------------------
// Project helpers shared by several migrations
// ---------------------------------------------------------------------------

type agentDir struct{ root, subdir string }

func allAgentDirs() []agentDir {
	out := make([]agentDir, 0, len(agents.AgentDirs))
	for _, d := range agents.AgentDirs {
		out = append(out, agentDir{d[0], d[1]})
	}
	return out
}

// migrationAgentDirs is agent_utils.directories.get_agent_dirs_for_project:
// configured agents' command directories, or all of them when none are
// configured; an invalid config.yaml propagates.
func migrationAgentDirs(root string) ([]agentDir, error) {
	ac, err := kittify.LoadAgentConfig(root)
	if err != nil {
		if _, ok := err.(*kittify.ErrConfig); ok {
			return nil, err
		}
		return allAgentDirs(), nil
	}
	if len(ac.Available) == 0 {
		return allAgentDirs(), nil
	}
	set := map[string]bool{}
	for _, a := range ac.Available {
		set[a] = true
	}
	var out []agentDir
	for _, d := range allAgentDirs() {
		if set[agents.AgentDirToKey[d.root]] {
			out = append(out, d)
		}
	}
	return out, nil
}

// globalRuntimeConfigured is upgrade.compat.global_runtime_configured.
func globalRuntimeConfigured() bool {
	return isFile(filepath.Join(project.GlobalKittify(), "cache", "version.lock"))
}

// usesCentralizedRuntime is upgrade.compat.uses_centralized_runtime.
func usesCentralizedRuntime(root string) bool {
	if !globalRuntimeConfigured() {
		return false
	}
	if md := kittify.LoadMetadata(root); md != nil {
		return versionAtLeast(md.Version, "2.0.0")
	}
	return !pathExists(filepath.Join(root, ".kittify")) && pathExists(filepath.Join(root, "kitty-specs"))
}

func versionAtLeast(v, min string) bool {
	c, ok := versionCmp(v, min)
	return ok && c >= 0
}

func worktreeDirs(root string) []string { return subdirs(filepath.Join(root, ".worktrees")) }

var _ = io.EOF

// umask returns the process umask.
func umask() os.FileMode { return os.FileMode(currentUmask()) }

func errnoOf(err error) int {
	var errno syscall.Errno
	if errors.As(err, &errno) {
		return int(errno)
	}
	return 0
}

// pyStrerror is os.strerror for the error's errno.
func pyStrerror(err error) string {
	var errno syscall.Errno
	msg := err.Error()
	if errors.As(err, &errno) {
		msg = errno.Error()
	}
	if msg != "" {
		r, n := utf8.DecodeRuneInString(msg)
		msg = string(unicode.ToUpper(r)) + msg[n:]
	}
	return msg
}
