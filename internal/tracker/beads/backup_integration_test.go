package beads

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

func TestRealBeadsBackupQualification(t *testing.T) {
	fixture := newRealBeadsFixture(t)
	bound := fixture.adapter(t)

	t.Run("jsonl_interchange", func(t *testing.T) {
		created := createRealWork(t, bound, fixture.projectID, 300, []tracker.Criterion{{ID: "export-criterion", Text: "survives interchange", Verifier: tracker.VerifierReview, Required: true}})
		stdout, _, err := fixture.runBD(t, "export", "--json")
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(bytes.NewReader(stdout))
		scanner.Buffer(make([]byte, 64<<10), 2<<20)
		found := false
		count := 0
		for scanner.Scan() {
			count++
			var issue struct {
				ID       string         `json:"id"`
				Metadata map[string]any `json:"metadata"`
			}
			if json.Unmarshal(scanner.Bytes(), &issue) != nil {
				t.Fatal("portable export contains malformed JSONL")
			}
			if issue.ID == created.Record.ExternalRef.ExternalID {
				found = true
				namespace, _ := issue.Metadata["spec_kitty"].(map[string]any)
				if namespace["work_id"] != created.Record.ExternalRef.WorkID.String() {
					t.Fatal("portable export lost the stable work mapping")
				}
			}
		}
		if scanner.Err() != nil || count == 0 || !found {
			t.Fatalf("portable interchange census count=%d found=%t err=%v", count, found, scanner.Err())
		}
	})

	t.Run("native_backup_restore", func(t *testing.T) {
		created := createRealWork(t, bound, fixture.projectID, 301, []tracker.Criterion{{ID: "backup-criterion", Text: "survives native restore", Verifier: tracker.VerifierTest, Required: true}})
		if err := validateRestorePath(fixture.root, fixture.restore, true); err != nil {
			t.Fatal(err)
		}
		if _, _, err := fixture.runBD(t, "backup", "init", fixture.backup); err != nil {
			t.Fatalf("native backup init failed: %v", err)
		}
		if _, _, err := fixture.runBD(t, "backup", "sync"); err != nil {
			t.Fatalf("native backup sync failed: %v", err)
		}

		restoreRepository := filepath.Join(fixture.root, "restore-repository")
		if err := os.Mkdir(restoreRepository, 0o700); err != nil {
			t.Fatal(err)
		}
		if _, _, err := runRealCommand(restoreRepository, realBeadsEnvironment(fixture.root, fixture.restore), "git", "init", "--quiet"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := runRealCommand(restoreRepository, realBeadsEnvironment(fixture.root, fixture.restore), fixture.binary,
			"init", "--stealth", "--skip-agents", "--skip-hooks", "--non-interactive", "--prefix", "skt"); err != nil {
			t.Fatalf("initialize disposable restore workspace: %v", err)
		}
		if _, _, err := runRealCommand(restoreRepository, realBeadsEnvironment(fixture.root, fixture.restore), fixture.binary,
			"backup", "restore", fixture.backup, "--force"); err != nil {
			t.Fatalf("native restore failed: %v", err)
		}
		wherePayload, _, err := runRealCommand(restoreRepository, realBeadsEnvironment(fixture.root, fixture.restore), fixture.binary, "where", "--json")
		if err != nil {
			t.Fatalf("inspect restored Beads workspace: %v", err)
		}
		var where struct {
			Data struct {
				DatabasePath string `json:"database_path"`
			} `json:"data"`
		}
		if decodeRealEnvelope(wherePayload, &where) != nil || !filepath.IsAbs(where.Data.DatabasePath) {
			t.Fatal("restored Beads workspace did not report an absolute database path")
		}

		restored, err := Open(BindingConfig{
			ProjectID: fixture.projectID, ExecutablePath: fixture.binary, WorkspacePath: fixture.restore,
			ProviderDatabaseIdentity: filepath.Clean(where.Data.DatabasePath),
			ProtocolSchema:           1, ConcurrencyMode: tracker.ConcurrencyEmbeddedSerialized, ExpectedExecutableDigest: fixture.digest,
		})
		if err != nil {
			t.Fatal(err)
		}
		record, err := restored.ReadWork(context.Background(), created.Record.ExternalRef)
		if err != nil {
			t.Fatal(err)
		}
		if record.Title != created.Record.Title || len(record.Criteria()) != 1 || record.Criteria()[0].ID != "backup-criterion" {
			t.Fatal("native restore does not match the provider-neutral source observation")
		}
	})
}

func validateRestorePath(root, target string, requireEmpty bool) error {
	if !filepath.IsAbs(root) || !filepath.IsAbs(target) {
		return errors.New("restore paths must be absolute")
	}
	root = filepath.Clean(root)
	target = filepath.Clean(target)
	relative, err := filepath.Rel(root, target)
	if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
		return errors.New("restore target escapes the test-owned root")
	}
	info, err := os.Lstat(target)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return errors.New("restore target must be a real fixture-owned directory")
	}
	if requireEmpty {
		entries, err := os.ReadDir(target)
		if err != nil || len(entries) != 0 {
			return errors.New("restore target must be empty before provider initialization")
		}
	}
	return nil
}

func runRealCommand(directory string, environment []string, executable string, args ...string) ([]byte, []byte, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, executable, args...)
	command.Dir = directory
	command.Env = environment
	var stdout, stderr bytes.Buffer
	command.Stdout, command.Stderr = &stdout, &stderr
	err := command.Run()
	if stdout.Len() > 4<<20 || stderr.Len() > 1<<20 {
		return nil, nil, errors.New("provider output exceeded qualification bound")
	}
	return stdout.Bytes(), stderr.Bytes(), err
}

func TestRestoreTargetGuardRefusesEscapeSymlinkAndExistingData(t *testing.T) {
	root := t.TempDir()
	empty := filepath.Join(root, "empty")
	if err := os.Mkdir(empty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := validateRestorePath(root, empty, true); err != nil {
		t.Fatal(err)
	}
	if err := validateRestorePath(root, filepath.Join(filepath.Dir(root), "outside"), true); err == nil {
		t.Fatal("path escape was accepted")
	}
	nonempty := filepath.Join(root, "nonempty")
	if err := os.Mkdir(nonempty, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nonempty, "user-data"), []byte("preserve"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := validateRestorePath(root, nonempty, true); err == nil {
		t.Fatal("preexisting content was accepted")
	}
	symlink := filepath.Join(root, "symlink")
	if err := os.Symlink(empty, symlink); err != nil {
		t.Fatal(err)
	}
	if err := validateRestorePath(root, symlink, true); err == nil {
		t.Fatal("symlink restore target was accepted")
	}
}
