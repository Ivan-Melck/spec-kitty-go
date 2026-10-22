package healthcheck

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"sort"

	"github.com/spec-kitty/go-kitty3/internal/project"
)

// The packaged mission-state survey fixtures (specify_cli/audit/fixtures in
// the wheel), audited by `doctor mission-state --include-fixtures`.
//
//go:embed all:auditfixtures
var auditFixtures embed.FS

const auditFixturesDir = "auditfixtures"

func auditFixturesDigest() string {
	var names []string
	fs.WalkDir(auditFixtures, auditFixturesDir, func(p string, d fs.DirEntry, err error) error {
		if err == nil && !d.IsDir() {
			names = append(names, p)
		}
		return nil
	})
	sort.Strings(names)
	h := sha256.New()
	for _, n := range names {
		b, _ := auditFixtures.ReadFile(n)
		h.Write([]byte(n + "\x00"))
		h.Write(b)
		h.Write([]byte{0})
	}
	return hex.EncodeToString(h.Sum(nil))[:12]
}

// BundledAuditFixtures returns the on-disk root of the packaged audit
// fixtures (upstream: <site-packages>/specify_cli/audit/fixtures). The Go
// build materializes its embedded copy once under
// ~/.kittify/cache/audit-<digest>/fixtures and reuses it afterwards, so the
// tree persists across runs like the installed package's does.
func BundledAuditFixtures() string {
	root := filepath.Join(project.GlobalKittify(), "cache", "audit-"+auditFixturesDigest(), "fixtures")
	if st, err := os.Stat(root); err == nil && st.IsDir() {
		return root
	}
	tmp := root + ".tmp"
	os.RemoveAll(tmp)
	err := fs.WalkDir(auditFixtures, auditFixturesDir, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(auditFixturesDir, filepath.FromSlash(p))
		dst := filepath.Join(tmp, rel)
		if d.IsDir() {
			return os.MkdirAll(dst, 0o755)
		}
		b, err := auditFixtures.ReadFile(path.Clean(p))
		if err != nil {
			return err
		}
		return os.WriteFile(dst, b, 0o644)
	})
	if err != nil || os.Rename(tmp, root) != nil {
		os.RemoveAll(tmp)
	}
	return root
}
