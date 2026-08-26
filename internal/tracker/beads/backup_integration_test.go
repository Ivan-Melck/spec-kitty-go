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
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

type representativeBackupGraph struct {
	blocked, blocker       tracker.WorkRecord
	criteria               []tracker.Criterion
	dependency             tracker.DependencyRef
	adapterAuthor          string
	adapterBody            string
	adapterOperation       ids.OperationID
	humanAuthor, humanBody string
}

func TestRealBeadsBackupQualification(t *testing.T) {
	fixture := newRealBeadsFixture(t)
	bound := fixture.adapter(t)
	graph := seedRepresentativeBackupGraph(t, fixture, bound)

	t.Run("jsonl_interchange", func(t *testing.T) {
		stdout, _, err := fixture.runBD(t, "export", "--json")
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(bytes.NewReader(stdout))
		scanner.Buffer(make([]byte, 64<<10), 2<<20)
		foundBlocked, foundBlocker := false, false
		count := 0
		totalBytes := 0
		for scanner.Scan() {
			count++
			totalBytes += len(scanner.Bytes()) + 1
			if count > 10_000 || int64(totalBytes) > defaultJSONLAggregateLimit {
				t.Fatal("portable interchange exceeded its record or aggregate bound")
			}
			var issue struct {
				Type         string          `json:"_type"`
				ID           string          `json:"id"`
				Metadata     json.RawMessage `json:"metadata"`
				Dependencies []struct {
					IssueID     string `json:"issue_id"`
					DependsOnID string `json:"depends_on_id"`
					Type        string `json:"type"`
				} `json:"dependencies"`
				Comments []struct {
					Author string `json:"author"`
					Text   string `json:"text"`
				} `json:"comments"`
			}
			if json.Unmarshal(scanner.Bytes(), &issue) != nil {
				t.Fatal("portable export contains malformed JSONL")
			}
			if issue.Type != "issue" {
				t.Fatalf("portable interchange record type=%q", issue.Type)
			}
			metadata, metadataErr := metadataPayload(issue.Metadata)
			if metadataErr != nil {
				t.Fatal(metadataErr)
			}
			document, metadataErr := decodeMetadata(metadata)
			if metadataErr != nil {
				t.Fatal(metadataErr)
			}
			switch issue.ID {
			case graph.blocked.ExternalRef.ExternalID:
				foundBlocked = true
				if document.kitty.WorkID != graph.blocked.ExternalRef.WorkID || !reflect.DeepEqual(document.kitty.Criteria, graph.criteria) {
					t.Fatal("portable interchange lost the blocked mapping or structured criteria")
				}
				if len(issue.Dependencies) != 1 || issue.Dependencies[0].IssueID != graph.blocked.ExternalRef.ExternalID || issue.Dependencies[0].DependsOnID != graph.blocker.ExternalRef.ExternalID || issue.Dependencies[0].Type != "blocks" {
					t.Fatalf("portable interchange lost asymmetric dependency: %+v", issue.Dependencies)
				}
				adapterSeen, humanSeen := false, false
				for _, comment := range issue.Comments {
					body, marker, marked := parseDiscussionBody(comment.Text)
					adapterSeen = adapterSeen || marked && marker == graph.adapterOperation.String() && comment.Author == graph.adapterAuthor && body == graph.adapterBody
					humanSeen = humanSeen || !marked && comment.Author == graph.humanAuthor && body == graph.humanBody
				}
				if !adapterSeen || !humanSeen {
					t.Fatalf("portable interchange lost adapter or human discussion: %+v", issue.Comments)
				}
			case graph.blocker.ExternalRef.ExternalID:
				foundBlocker = document.kitty.WorkID == graph.blocker.ExternalRef.WorkID
			}
		}
		if scanner.Err() != nil || count < 2 || !foundBlocked || !foundBlocker {
			t.Fatalf("portable interchange census count=%d blocked=%t blocker=%t err=%v", count, foundBlocked, foundBlocker, scanner.Err())
		}
	})

	t.Run("native_backup_restore", func(t *testing.T) {
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
		inspection, err := restored.Inspect(context.Background(), tracker.InspectRequest{ProjectID: fixture.projectID})
		if err != nil || inspection.Health != tracker.HealthHealthy || inspection.Binding.WorkspacePath != filepath.Clean(fixture.restore) || inspection.Binding.WorkspaceIdentity != deriveWorkspaceIdentity(filepath.Clean(fixture.restore), filepath.Clean(where.Data.DatabasePath)) {
			t.Fatalf("restored adapter inspection or workspace identity failed: inspection=%+v err=%v", inspection, err)
		}
		record, err := restored.ReadWork(context.Background(), graph.blocked.ExternalRef)
		if err != nil {
			t.Fatal(err)
		}
		blocker, err := restored.ReadWork(context.Background(), graph.blocker.ExternalRef)
		if err != nil {
			t.Fatal(err)
		}
		if record.Title != graph.blocked.Title || blocker.Title != graph.blocker.Title || !reflect.DeepEqual(record.Criteria(), graph.criteria) {
			t.Fatal("native restore does not match the complete provider-neutral catalog observation")
		}
		edges, err := restored.ListDependencies(context.Background(), graph.blocked.ExternalRef)
		if err != nil || len(edges) != 1 || edges[0] != graph.dependency {
			t.Fatalf("native restore lost asymmetric dependency: %+v err=%v", edges, err)
		}
		discussion, err := restored.ListDiscussion(context.Background(), graph.blocked.ExternalRef)
		if err != nil {
			t.Fatal(err)
		}
		adapterSeen, humanSeen := false, false
		for _, entry := range discussion {
			adapterSeen = adapterSeen || entry.Author == graph.adapterAuthor && entry.Body == graph.adapterBody && entry.OperationMarker == graph.adapterOperation.String()
			humanSeen = humanSeen || entry.Author == graph.humanAuthor && entry.Body == graph.humanBody && entry.OperationMarker == ""
		}
		if !adapterSeen || !humanSeen {
			t.Fatalf("native restore lost adapter or human discussion: %+v", discussion)
		}
	})
}

func seedRepresentativeBackupGraph(t *testing.T, fixture *realBeadsFixture, bound *BoundAdapter) representativeBackupGraph {
	t.Helper()
	graph := representativeBackupGraph{
		criteria: []tracker.Criterion{
			{ID: "backup-required", Text: "survives interchange and native restore", Verifier: tracker.VerifierTest, Required: true, EvidenceHint: "go test ./internal/tracker/beads"},
			{ID: "backup-review", Text: "retains human review context", Verifier: tracker.VerifierReview, Required: false, EvidenceHint: "inspect restored discussion"},
		},
		adapterAuthor: "qualified-adapter", adapterBody: "adapter discussion survives recovery", adapterOperation: ids.OperationID("op-backup-discussion"),
		humanAuthor: "qualified-human", humanBody: "human discussion survives recovery",
	}
	graph.blocked = createRealWork(t, bound, fixture.projectID, 300, graph.criteria).Record
	graph.blocker = createRealWork(t, bound, fixture.projectID, 301, nil).Record
	graph.dependency = tracker.DependencyRef{BlockedWorkID: graph.blocked.ExternalRef.WorkID, BlockingWorkID: graph.blocker.ExternalRef.WorkID, Kind: tracker.DependencyBlocks}
	if _, err := bound.PutBlockingDependency(context.Background(), tracker.DependencyRequest{ProjectID: fixture.projectID, OperationID: ids.OperationID("op-backup-dependency"), Dependency: graph.dependency}); err != nil {
		t.Fatal(err)
	}
	entries, observation, err := bound.observeDiscussion(context.Background(), graph.blocked.ExternalRef)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		observation.Revision = entries[len(entries)-1].Observation
	}
	if _, err := bound.AppendDiscussion(context.Background(), tracker.AppendDiscussionRequest{ExternalRef: graph.blocked.ExternalRef, OperationID: graph.adapterOperation, Author: graph.adapterAuthor, Body: graph.adapterBody, ExpectedObservation: observation.Revision}); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fixture.runBD(t, "comments", "add", graph.blocked.ExternalRef.ExternalID, graph.humanBody, "--author", graph.humanAuthor, "--json"); err != nil {
		t.Fatal(err)
	}
	return graph
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
