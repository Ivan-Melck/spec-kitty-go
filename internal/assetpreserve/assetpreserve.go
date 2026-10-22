// Package assetpreserve is the prove-ownership-or-preserve guard
// (specify_cli.asset_preservation): the single decision surface mutating
// flows route destructive removals and overwrites of user-visible paths
// through, so name/directory identity never authorizes deleting or
// clobbering user-authored content.
package assetpreserve

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/clock"
)

// Proof is tool_surface.operations.OwnershipProof.
type Proof struct {
	Kind   string // manifest | managed_path | canonical_content
	Detail string
}

// Prover proves package ownership of path, or returns nil (preserve).
type Prover interface {
	Prove(path, project string) *Proof
}

func relPosix(path, project string) (string, bool) {
	r, err := filepath.Rel(project, path)
	if err != nil || r == ".." || strings.HasPrefix(r, "../") {
		return "", false
	}
	return filepath.ToSlash(r), true
}

func isSymlink(p string) bool {
	st, err := os.Lstat(p)
	return err == nil && st.Mode()&os.ModeSymlink != 0
}

func isFile(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func isDir(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func sha256Hex(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// ManagedPathProver proves manifest-less package-managed / regenerable paths
// (project-relative ManagedRelPaths) and paths this invocation created
// (absolute RunCreated).
type ManagedPathProver struct {
	ManagedRelPaths []string
	RunCreated      []string
}

// Prove implements Prover.
func (m ManagedPathProver) Prove(path, project string) *Proof {
	for _, p := range m.RunCreated {
		if p == path {
			return &Proof{"managed_path", "run-created:" + filepath.Base(path)}
		}
	}
	if rel, ok := relPosix(path, project); ok {
		for _, r := range m.ManagedRelPaths {
			if r == rel {
				return &Proof{"managed_path", "package-managed:" + rel}
			}
		}
	}
	return nil
}

// VersionMarker is the single command version marker.
var VersionMarker = []byte("<!-- spec-kitty-command-version:")

// CanonicalContentProver proves ownership from the version marker (unless
// SkipMarker) within the first ScanBytes (0 = whole file) or an exact match
// with Canonical.
type CanonicalContentProver struct {
	Marker     []byte
	ScanBytes  int
	Canonical  []byte
	SkipMarker bool
}

// Prove implements Prover.
func (c CanonicalContentProver) Prove(path, project string) *Proof {
	rel, ok := relPosix(path, project)
	if !ok || isSymlink(path) || !isFile(path) {
		return nil
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	marker := c.Marker
	if marker == nil {
		marker = VersionMarker
	}
	window := content
	if c.ScanBytes > 0 && len(window) > c.ScanBytes {
		window = window[:c.ScanBytes]
	}
	if !c.SkipMarker && bytes.Contains(window, marker) {
		return &Proof{"canonical_content", "version-marker:" + rel}
	}
	if c.Canonical != nil && bytes.Equal(content, c.Canonical) {
		return &Proof{"canonical_content", "canonical-bytes:" + rel}
	}
	return nil
}

// AnyProver is an ordered composite: the first proof wins.
type AnyProver []Prover

// Prove implements Prover.
func (a AnyProver) Prove(path, project string) *Proof {
	for _, p := range a {
		if pr := p.Prove(path, project); pr != nil {
			return pr
		}
	}
	return nil
}

// ManifestProver proves ownership from the managed-skills
// (.kittify/skills-manifest.json, copy delivery, "sha256:<hex>" hashes)
// and/or command-skills (.kittify/command-skills-manifest.json, bare hex)
// manifests. A directory is owned only when every regular member is.
type ManifestProver struct {
	SkipManaged, SkipCommand bool
}

// Prove implements Prover.
func (m ManifestProver) Prove(path, project string) *Proof {
	rel, ok := relPosix(path, project)
	if !ok || isSymlink(path) {
		return nil
	}
	if isDir(path) {
		var members []string
		_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error {
			if err == nil && p != path && (!d.IsDir() || d.Type()&os.ModeSymlink != 0) {
				members = append(members, p)
			}
			return nil
		})
		if len(members) == 0 {
			return nil
		}
		for _, mb := range members {
			if isSymlink(mb) || !isFile(mb) {
				return nil
			}
			mrel, ok := relPosix(mb, project)
			if !ok || m.proveFile(mb, mrel, project) == nil {
				return nil
			}
		}
		return &Proof{"manifest", "manifest-dir:" + rel}
	}
	if isFile(path) {
		return m.proveFile(path, rel, project)
	}
	return nil
}

type manifestDoc struct {
	Skills []struct {
		InstalledPath string `json:"installed_path"`
		DeliveryMode  string `json:"delivery_mode"`
		ContentHash   string `json:"content_hash"`
	} `json:"entries"`
}

func (m ManifestProver) proveFile(path, rel, project string) *Proof {
	content, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if !m.SkipManaged {
		if b, err := os.ReadFile(filepath.Join(project, ".kittify", "skills-manifest.json")); err == nil {
			var doc manifestDoc
			if json.Unmarshal(b, &doc) == nil {
				for _, e := range doc.Skills {
					if e.InstalledPath == rel && e.DeliveryMode == "copy" && e.ContentHash == "sha256:"+sha256Hex(content) {
						return &Proof{"manifest", ".kittify/skills-manifest.json:" + rel}
					}
				}
			}
		}
	}
	if !m.SkipCommand {
		if b, err := os.ReadFile(filepath.Join(project, ".kittify", "command-skills-manifest.json")); err == nil {
			var doc struct {
				Entries []struct {
					Path        string `json:"path"`
					ContentHash string `json:"content_hash"`
				} `json:"entries"`
			}
			if json.Unmarshal(b, &doc) == nil {
				for _, e := range doc.Entries {
					if e.Path == rel && e.ContentHash == sha256Hex(content) {
						return &Proof{"manifest", ".kittify/command-skills-manifest.json:" + rel}
					}
				}
			}
		}
	}
	return nil
}

func displayRel(path, project string) string {
	if r, ok := relPosix(path, project); ok {
		return r
	}
	return filepath.Base(path)
}

// OverwriteVerdict is the guard's decision for one candidate overwrite.
type OverwriteVerdict struct {
	Proceed    bool
	Reason     string
	Diagnostic string
	BackupPath string // "" = none
}

// GuardDestructiveOverwrite is guard_destructive_overwrite: absent +
// non-substantive refuses; absent + substantive proceeds; an existing
// proven package-owned dest proceeds; an existing unproven dest refuses a
// non-substantive replacement, refuses when unauthorized, and otherwise
// proceeds (archiving under backupParent first when given).
func GuardDestructiveOverwrite(dest, project string, replacementSubstantive, authorized bool, prover Prover, backupParent string) (OverwriteVerdict, error) {
	rel := displayRel(dest, project)
	_, err := os.Lstat(dest)
	if err != nil {
		if !replacementSubstantive {
			return OverwriteVerdict{false, "absent-empty-replacement", fmt.Sprintf("Refused to fabricate an empty %s; destination is absent and replacement is not substantive", rel), ""}, nil
		}
		return OverwriteVerdict{true, "absent", fmt.Sprintf("Proceeding; %s is absent", rel), ""}, nil
	}
	if prover != nil {
		if pr := prover.Prove(dest, project); pr != nil {
			return OverwriteVerdict{true, "package-owned (" + pr.Kind + ")", fmt.Sprintf("Proceeding over package-owned %s (proof: %s)", rel, pr.Kind), ""}, nil
		}
	}
	if !replacementSubstantive {
		return OverwriteVerdict{false, "would-truncate-to-empty", fmt.Sprintf("Refused to truncate %s to empty; existing bytes are not proven package-owned", rel), ""}, nil
	}
	if !authorized {
		return OverwriteVerdict{false, "unauthorized", fmt.Sprintf("Refused to overwrite %s; not proven package-owned and not authorized", rel), ""}, nil
	}
	diag := fmt.Sprintf("Proceeding to overwrite %s; authorized", rel)
	backup := ""
	if backupParent != "" {
		b, err := ArchiveInto(dest, project, backupParent)
		if err != nil {
			return OverwriteVerdict{}, err
		}
		backup = b
		diag = fmt.Sprintf("Proceeding to overwrite %s; archived to %s first", rel, displayRel(b, project))
	}
	return OverwriteVerdict{true, "authorized-overwrite", diag, backup}, nil
}

// OwnershipVerdict is the guard's decision for one candidate removal.
type OwnershipVerdict struct {
	Owned         bool
	Proof         *Proof
	PreservedPath string
	BackupPath    string
	Reason        string
	Diagnostic    string
}

func remove(path string, isTree bool) error {
	if isSymlink(path) || isFile(path) {
		if err := os.Remove(path); err != nil {
			makeWritable(path)
			makeWritable(filepath.Dir(path))
			return os.Remove(path)
		}
		return nil
	}
	if isDir(path) {
		if isTree {
			if err := os.RemoveAll(path); err != nil {
				_ = filepath.WalkDir(path, func(p string, d fs.DirEntry, err error) error { makeWritable(p); return nil })
				return os.RemoveAll(path)
			}
			return nil
		}
		if err := os.Remove(path); err != nil {
			makeWritable(path)
			makeWritable(filepath.Dir(path))
			return os.Remove(path)
		}
	}
	return nil
}

func makeWritable(p string) {
	if st, err := os.Stat(p); err == nil {
		_ = os.Chmod(p, st.Mode()|0o200)
	}
}

// GuardDestructiveRemoval is guard_destructive_removal: proven-owned paths
// are removed by the guard; unproven ones are preserved in place, or (when
// backupParent is given) archived verbatim before removal.
func GuardDestructiveRemoval(path, project string, prover Prover, isTree bool, backupParent string, dryRun bool) (OwnershipVerdict, error) {
	rel := displayRel(path, project)
	if _, err := os.Lstat(path); err != nil {
		return OwnershipVerdict{Reason: "absent", Diagnostic: fmt.Sprintf("Skipped %s; path is absent", rel)}, nil
	}
	if pr := prover.Prove(path, project); pr != nil {
		verb := "Removed"
		if dryRun {
			verb = "Would remove"
		} else if err := remove(path, isTree); err != nil {
			return OwnershipVerdict{}, err
		}
		return OwnershipVerdict{Owned: true, Proof: pr, Reason: "package-owned (" + pr.Kind + ")",
			Diagnostic: fmt.Sprintf("%s package-owned %s (proof: %s)", verb, rel, pr.Kind)}, nil
	}
	const reason = "not package-owned"
	if backupParent == "" {
		return OwnershipVerdict{PreservedPath: path, Reason: reason,
			Diagnostic: fmt.Sprintf("Preserved %s; %s (name is not ownership proof) — left in place", rel, reason)}, nil
	}
	if dryRun {
		return OwnershipVerdict{PreservedPath: path, Reason: reason,
			Diagnostic: fmt.Sprintf("Would preserve %s; %s — would archive before parent removal", rel, reason)}, nil
	}
	archived, err := ArchiveInto(path, project, backupParent)
	if err != nil {
		return OwnershipVerdict{}, err
	}
	if err := remove(path, isTree); err != nil {
		return OwnershipVerdict{}, err
	}
	return OwnershipVerdict{PreservedPath: archived, BackupPath: archived, Reason: reason,
		Diagnostic: fmt.Sprintf("Preserved %s; %s — archived to %s before parent removal", rel, reason, displayRel(archived, project))}, nil
}

// CompactStamp is kernel.clock.now_utc_compact_stamp (%Y%m%dT%H%M%SZ).
func CompactStamp() string { return clock.Now().UTC().Format("20060102T150405Z") }

// AllocateBackupDir is template.manager._allocate_backup_dir: a fresh
// ".backup-<stamp>[-N]" directory under parent.
func AllocateBackupDir(parent string) (string, error) {
	if err := os.MkdirAll(parent, 0o755); err != nil {
		return "", err
	}
	stamp := CompactStamp()
	for suffix := 0; ; suffix++ {
		name := ".backup-" + stamp
		if suffix > 0 {
			name = fmt.Sprintf(".backup-%s-%d", stamp, suffix)
		}
		c := filepath.Join(parent, name)
		if err := os.Mkdir(c, 0o755); err == nil {
			return c, nil
		} else if !os.IsExist(err) {
			return "", err
		}
	}
}

// WriteFileVerbatim copies a regular file byte-exact (mode + mtime kept),
// refusing to clobber an existing destination.
func WriteFileVerbatim(src, dest string) error {
	st, err := os.Lstat(src)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
		return err
	}
	f, err := os.OpenFile(dest, os.O_WRONLY|os.O_CREATE|os.O_EXCL, st.Mode().Perm())
	if err != nil {
		return err
	}
	if _, err := f.Write(content); err != nil {
		f.Close()
		return err
	}
	f.Close()
	_ = os.Chmod(dest, st.Mode().Perm())
	return os.Chtimes(dest, st.ModTime(), st.ModTime())
}

func copyNode(src, dest string) error {
	if isSymlink(src) {
		t, err := os.Readlink(src)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		return os.Symlink(t, dest)
	}
	if isDir(src) {
		st, _ := os.Stat(src)
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		if err := os.Mkdir(dest, st.Mode().Perm()); err != nil {
			return err
		}
		entries, _ := os.ReadDir(src)
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}
		sort.Strings(names)
		for _, n := range names {
			if err := copyNode(filepath.Join(src, n), filepath.Join(dest, n)); err != nil {
				return err
			}
		}
		_ = os.Chmod(dest, st.Mode().Perm())
		return os.Chtimes(dest, st.ModTime(), st.ModTime())
	}
	return WriteFileVerbatim(src, dest)
}

// BackupBeforeOverwrite creates the "<path>.<stamp>" sidecar (symlinks are
// recreated, never dereferenced) and returns it.
func BackupBeforeOverwrite(path string) (string, error) {
	sidecar := path + "." + CompactStamp()
	if isSymlink(path) {
		t, err := os.Readlink(path)
		if err != nil {
			return "", err
		}
		return sidecar, os.Symlink(t, sidecar)
	}
	return sidecar, WriteFileVerbatim(path, sidecar)
}

// ArchiveInto copies path verbatim into a fresh timestamped directory under
// backupParent (keeping its project-relative layout) and returns the copy.
func ArchiveInto(path, project, backupParent string) (string, error) {
	dir, err := AllocateBackupDir(backupParent)
	if err != nil {
		return "", err
	}
	rel, ok := relPosix(path, project)
	if !ok {
		rel = filepath.Base(path)
	}
	dest := filepath.Join(dir, filepath.FromSlash(rel))
	return dest, copyNode(path, dest)
}
