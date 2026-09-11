package architecture

import (
	"crypto/sha256"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// Exact bytes copied from the authentic pre-mission local-files baseline.
// This fixture is portable; tests never read or modify user recovery data.
const historicalRecoveryFixture = `{
  "head": "refs/heads/main",
  "remotes": {},
  "backups": {
    "backup_export": {
      "name": "backup_export",
      "url": "file:///home/lynn/projects/go-kitty3/.beads/backup",
      "fetch_specs": [
        "refs/heads/*:refs/remotes/backup_export/*"
      ],
      "params": {}
    }
  },
  "branches": {}
}`
const historicalRecoveryFixturePath = ".beads/embeddeddolt/r6/.dolt/repo_state.json"

func historicalRecoveryBytes(t *testing.T) []byte {
	t.Helper()
	data := []byte(historicalRecoveryFixture)
	if len(data) != 316 || fmt.Sprintf("%x", sha256.Sum256(data)) != "d0f6078373e0c84e8d598ba2f6f56d405a13b4aab0e1ca8dad86e1fe19b7fd7e" {
		t.Fatal("historical fixture differs from recorded bytes")
	}
	return data
}

func historicalRecoveryCandidate(path string, data []byte, mode fs.FileMode) []m2AbsenceFinding {
	var findings []m2AbsenceFinding
	inspectM2Candidate(path, data, mode, func(kind, path, detail string) { findings = append(findings, m2AbsenceFinding{kind, path, detail}) })
	return findings
}

func historicalRecoveryWrite(t *testing.T, root string, data []byte, mode fs.FileMode) string {
	t.Helper()
	path := filepath.Join(root, historicalRecoveryFixturePath)
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestHistoricalRecoveryDispositionExact(t *testing.T) {
	data := historicalRecoveryBytes(t)
	t.Run("existing-candidate-entrypoint", func(t *testing.T) {
		if got := historicalRecoveryCandidate(historicalRecoveryFixturePath, data, 0o777); len(got) != 0 {
			t.Fatalf("recorded inert recovery data refused: %+v", got)
		}
	})
	t.Run("existing-walker-entrypoint", func(t *testing.T) {
		root := t.TempDir()
		path := historicalRecoveryWrite(t, root, data, 0o777)
		got := inspectM2Absence(t, root, false)
		if len(got.Findings) != 0 || got.Files != 1 || got.Bytes != 316 {
			t.Fatalf("recorded recovery scan: %+v", got)
		}
		after, err := os.ReadFile(path)
		if err != nil || string(after) != string(data) {
			t.Fatalf("scan modified data: %v", err)
		}
		info, err := os.Lstat(path)
		if err != nil || info.Mode() != 0o777 {
			t.Fatalf("scan modified mode: %v %v", info, err)
		}
	})
}

func TestHistoricalRecoveryDispositionDrift(t *testing.T) {
	data := historicalRecoveryBytes(t)
	changed := append([]byte(nil), data...)
	changed[5] ^= 1
	cases := []struct {
		name, path string
		data       []byte
		mode       fs.FileMode
	}{
		{"same-length-content", historicalRecoveryFixturePath, changed, 0o777},
		{"shorter", historicalRecoveryFixturePath, data[:len(data)-1], 0o777},
		{"longer", historicalRecoveryFixturePath, append(append([]byte(nil), data...), '\n'), 0o777},
		{"non-executable-mode", historicalRecoveryFixturePath, data, 0o644},
		{"other-executable-mode", historicalRecoveryFixturePath, data, 0o755},
		{"special-mode", historicalRecoveryFixturePath, data, 0o777 | fs.ModeSetuid},
		{"direct-directory-mode", historicalRecoveryFixturePath, data, 0o777 | fs.ModeDir},
		{"direct-symlink-mode", historicalRecoveryFixturePath, data, 0o777 | fs.ModeSymlink},
		{"wrong-path", ".beads/embeddeddolt/r7/.dolt/repo_state.json", data, 0o777},
		{"different-case", strings.ToUpper(historicalRecoveryFixturePath), data, 0o777},
		{"unclean-path", "./" + historicalRecoveryFixturePath, data, 0o777},
		{"unrecognized-json", ".hidden/config.json", []byte("{\"other\":{}}"), 0o777},
		{"hidden-executable", ".hidden/run", []byte("sql" + "ite3 example.db\n"), 0o777},
		{"quoted-executable", ".hidden/run", []byte("'sql'\"ite3\" example.db\n"), 0o777},
	}
	for _, tt := range cases {
		t.Run(tt.name, func(t *testing.T) {
			if got := historicalRecoveryCandidate(tt.path, tt.data, tt.mode); len(got) == 0 {
				t.Fatal("changed or unknown material was admitted")
			}
		})
	}
	t.Run("generic-token-scan-remains", func(t *testing.T) {
		got := historicalRecoveryCandidate(historicalRecoveryFixturePath, []byte("sql"+"ite3 example.db\n"), 0o777)
		mismatch, token := false, false
		for _, finding := range got {
			mismatch = mismatch || finding.Kind == "historical-recovery-mismatch"
			token = token || finding.Kind == "subprocess"
		}
		if !mismatch || !token {
			t.Fatalf("expected both closed disposition and generic token refusal: %+v", got)
		}
	})
}

func TestHistoricalRecoveryDispositionFilesystemTypes(t *testing.T) {
	for _, kind := range []string{"directory", "fifo", "symlink"} {
		t.Run(kind, func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, historicalRecoveryFixturePath)
			if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
				t.Fatal(err)
			}
			var err error
			switch kind {
			case "directory":
				err = os.Mkdir(path, 0o777)
			case "fifo":
				err = syscall.Mkfifo(path, 0o777)
			case "symlink":
				target := filepath.Join(root, "target")
				err = os.WriteFile(target, historicalRecoveryBytes(t), 0o600)
				if err == nil {
					err = os.Symlink(target, path)
				}
			}
			if err != nil {
				t.Fatal(err)
			}
			got := inspectM2Absence(t, root, false)
			found := false
			for _, finding := range got.Findings {
				if finding.Path == historicalRecoveryFixturePath {
					found = true
				}
			}
			if !found {
				t.Fatalf("claimed path with %s type accepted: %+v", kind, got)
			}
		})
	}
}
