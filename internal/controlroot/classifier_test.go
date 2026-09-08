package controlroot

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"io/fs"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestDispositionSchemaMappingAndValidation(t *testing.T) {
	digest := strings.Repeat("a", 64)
	cases := []struct {
		classification Classification
		action         Action
		diagnosis      string
	}{
		{EmptySupported, Initialize, "EMPTY_SUPPORTED"},
		{CurrentSupported, Recover, "CURRENT_SUPPORTED"},
		{Historical, OfflineDispositionRequired, "HISTORICAL_STATE_DETECTED"},
		{Mixed, OfflineDispositionRequired, "MIXED_AUTHORITY_STATE"},
		{Unsafe, OfflineDispositionRequired, "UNSAFE_CONTROL_ROOT"},
		{Malformed, OfflineDispositionRequired, "MALFORMED_CURRENT_STATE"},
		{Ambiguous, OfflineDispositionRequired, "AMBIGUOUS_CONTROL_ROOT"},
	}
	for _, tc := range cases {
		t.Run(string(tc.classification), func(t *testing.T) {
			d := Disposition{SchemaVersion: 1, ControlRootSHA256: digest, Classification: tc.classification, Findings: []Finding{}, CurrentMutationCount: 0, Action: tc.action, DiagnosisCode: tc.diagnosis}
			if tc.action == OfflineDispositionRequired {
				d.Findings = []Finding{{Kind: FindingUncertain, DetailCode: "TEST_EVIDENCE"}}
			}
			if err := d.Validate(); err != nil {
				t.Fatalf("valid disposition: %v", err)
			}
			payload, err := json.Marshal(d)
			if err != nil {
				t.Fatal(err)
			}
			for _, token := range []string{`"schema_version":1`, `"control_root_sha256"`, `"current_mutation_count":0`, `"action":"` + string(tc.action) + `"`} {
				if !bytes.Contains(payload, []byte(token)) {
					t.Fatalf("schema token %q absent from %s", token, payload)
				}
			}
		})
	}

	invalid := Disposition{SchemaVersion: 1, ControlRootSHA256: digest, Classification: EmptySupported, Findings: []Finding{}, Action: Initialize, DiagnosisCode: "EMPTY_SUPPORTED"}
	mutants := []func(*Disposition){
		func(v *Disposition) { v.SchemaVersion = 2 },
		func(v *Disposition) { v.ControlRootSHA256 = "short" },
		func(v *Disposition) { v.Classification = "future" },
		func(v *Disposition) { v.Action = Recover },
		func(v *Disposition) { v.CurrentMutationCount = 1 },
		func(v *Disposition) { v.DiagnosisCode = "lowercase" },
		func(v *Disposition) {
			v.Findings = []Finding{{Kind: FindingHistorical, RelativePath: "../escape", DetailCode: "BAD_PATH"}}
		},
		func(v *Disposition) { v.Findings = []Finding{{Kind: "unknown", DetailCode: "UNKNOWN_KIND"}} },
	}
	for index, mutate := range mutants {
		candidate := invalid
		mutate(&candidate)
		if err := candidate.Validate(); err == nil {
			t.Fatalf("mutant %d accepted: %+v", index, candidate)
		}
	}
	findings := make([]Finding, MaxContractFindings+1)
	for index := range findings {
		findings[index] = Finding{Kind: FindingUncertain, RelativePath: filepath.ToSlash(filepath.Join("x", strings.Repeat("a", index%32+1))), DetailCode: "TOO_MANY_FINDINGS"}
	}
	tooMany := invalid
	tooMany.Classification, tooMany.Action, tooMany.DiagnosisCode, tooMany.Findings = Ambiguous, OfflineDispositionRequired, "AMBIGUOUS_CONTROL_ROOT", findings
	if err := tooMany.Validate(); err == nil {
		t.Fatal("more than the schema's 256 findings accepted")
	}
}

func TestClassificationMatrix(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T, string)
		want  Classification
	}{
		{"empty", func(*testing.T, string) {}, EmptySupported},
		{"current", func(t *testing.T, root string) { writeCurrentRoot(t, root, 0x11) }, CurrentSupported},
		{"historical-named", func(t *testing.T, root string) { writeFile(t, filepath.Join(root, "control.db"), sqliteBytes()) }, Historical},
		{"historical-renamed", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "copied-authority.bin"), sqliteBytes())
		}, Historical},
		{"historical-nested", func(t *testing.T, root string) { writeFile(t, filepath.Join(root, "old", "copy"), sqliteBytes()) }, Historical},
		{"mixed", func(t *testing.T, root string) {
			writeCurrentRoot(t, root, 0x22)
			writeFile(t, filepath.Join(root, "control.db"), sqliteBytes())
		}, Mixed},
		{"split-current", func(t *testing.T, root string) {
			writeCurrentRoot(t, filepath.Join(root, "a"), 0x31)
			writeCurrentRoot(t, filepath.Join(root, "b"), 0x32)
		}, Mixed},
		{"malformed-current", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "IDENTITY"), []byte("GAPID001broken"))
		}, Malformed},
		{"future-current", func(t *testing.T, root string) {
			value := currentIdentity(0x41)
			binary.BigEndian.PutUint16(value[8:10], 2)
			writeFile(t, filepath.Join(root, "IDENTITY"), value)
		}, Malformed},
		{"ambiguous-control-name", func(t *testing.T, root string) { writeFile(t, filepath.Join(root, "control.db"), []byte("plain text")) }, Ambiguous},
		{"ambiguous-orphan-wal", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "control.db-wal"), []byte("plain text"))
		}, Ambiguous},
		{"allowed-documentation", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, "README.md"), []byte("control.db and SQLite format 3 are prose"))
			writeFile(t, filepath.Join(root, "docs", "history.md"), append([]byte("prefix"), sqliteBytes()...))
		}, EmptySupported},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.build(t, root)
			classifier := testClassifier(t)
			first, err := classifier.Classify(t.Context(), root)
			if err != nil {
				t.Fatalf("classify: %v", err)
			}
			second, err := classifier.Classify(t.Context(), root)
			if err != nil {
				t.Fatalf("repeat: %v", err)
			}
			if first.Classification != tc.want {
				t.Fatalf("classification=%s want=%s findings=%+v", first.Classification, tc.want, first.Findings)
			}
			if !reflect.DeepEqual(first, second) {
				t.Fatalf("classification is nondeterministic\nfirst=%+v\nsecond=%+v", first, second)
			}
			if first.CurrentMutationCount != 0 || first.Validate() != nil {
				t.Fatalf("invalid or mutating disposition: %+v", first)
			}
		})
	}
}

func TestAbsentRootIsSafeToInitializeWithoutCreation(t *testing.T) {
	root := filepath.Join(t.TempDir(), "not-created")
	disposition, err := testClassifier(t).Classify(t.Context(), root)
	if err != nil || disposition.Classification != EmptySupported || disposition.Action != Initialize {
		t.Fatalf("disposition=%+v err=%v", disposition, err)
	}
	if _, err := os.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("classifier created absent root: %v", err)
	}
}

func TestHistoricalSQLiteSignatureAndCompanions(t *testing.T) {
	if got := HistoricalSignatureNames(); !slices.Equal(got, []string{"control.db", "control.db-journal", "control.db-shm", "control.db-wal"}) {
		t.Fatalf("historical registry=%v", got)
	}
	cases := []struct {
		name string
		data []byte
		want bool
	}{
		{"exact", sqliteBytes(), true},
		{"one-byte-short", []byte("SQLite format 3"), false},
		{"wrong-nul", []byte("SQLite format 3!"), false},
		{"offset-one", append([]byte{'x'}, sqliteBytes()...), false},
		{"phrase", []byte("the SQLite format 3\\x00 header"), false},
		{"random-near-prefix", []byte("SQLite format 2\x00"), false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := hasSQLiteHeader(tc.data); got != tc.want {
				t.Fatalf("hasSQLiteHeader=%t want=%t", got, tc.want)
			}
		})
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "control.db"), sqliteBytes())
		writeFile(t, filepath.Join(root, "control.db"+suffix), []byte("companion"))
		disposition, err := testClassifier(t).Classify(t.Context(), root)
		if err != nil || disposition.Classification != Historical {
			t.Fatalf("companion %s classification=%s err=%v", suffix, disposition.Classification, err)
		}
	}
}

func TestRootPathOwnershipSymlinkAndHardlinkSafety(t *testing.T) {
	t.Run("non-canonical-path", func(t *testing.T) {
		root := t.TempDir()
		disposition, err := testClassifier(t).Classify(t.Context(), root+string(filepath.Separator)+".."+string(filepath.Separator)+filepath.Base(root))
		if err != nil || disposition.Classification != Unsafe || !hasDetail(disposition, "ROOT_PATH_NON_CANONICAL") {
			t.Fatalf("disposition=%+v err=%v", disposition, err)
		}
	})
	t.Run("root-symlink", func(t *testing.T) {
		target := t.TempDir()
		link := filepath.Join(t.TempDir(), "root")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		disposition, _ := testClassifier(t).Classify(t.Context(), link)
		if disposition.Classification != Unsafe || !hasDetail(disposition, "ROOT_SYMLINK") {
			t.Fatalf("%+v", disposition)
		}
	})
	t.Run("child-symlink", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Symlink(filepath.Join(t.TempDir(), "outside"), filepath.Join(root, "authority")); err != nil {
			t.Fatal(err)
		}
		disposition, _ := testClassifier(t).Classify(t.Context(), root)
		if disposition.Classification != Unsafe || !hasDetail(disposition, "ENTRY_SYMLINK") {
			t.Fatalf("%+v", disposition)
		}
	})
	t.Run("ancestor-symlink", func(t *testing.T) {
		target := t.TempDir()
		child := filepath.Join(target, "child")
		if err := os.Mkdir(child, 0o700); err != nil {
			t.Fatal(err)
		}
		link := filepath.Join(t.TempDir(), "parent")
		if err := os.Symlink(target, link); err != nil {
			t.Fatal(err)
		}
		disposition, _ := testClassifier(t).Classify(t.Context(), filepath.Join(link, "child"))
		if disposition.Classification != Unsafe || !hasDetail(disposition, "ROOT_ANCESTOR_SYMLINK") {
			t.Fatalf("%+v", disposition)
		}
	})
	t.Run("foreign-owner-policy", func(t *testing.T) {
		root := t.TempDir()
		budgets := DefaultBudgets()
		classifier, err := NewClassifier(budgets, TrustPolicy{OwnerUID: uint32(os.Geteuid()) + 1, OwnerGID: uint32(os.Getegid())})
		if err != nil {
			t.Fatal(err)
		}
		disposition, _ := classifier.Classify(t.Context(), root)
		if disposition.Classification != Unsafe || !hasDetail(disposition, "ROOT_OWNER_MISMATCH") {
			t.Fatalf("%+v", disposition)
		}
	})
	t.Run("world-writable", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Chmod(root, 0o707); err != nil {
			t.Fatal(err)
		}
		disposition, _ := testClassifier(t).Classify(t.Context(), root)
		if disposition.Classification != Unsafe || !hasDetail(disposition, "ROOT_WORLD_WRITABLE") {
			t.Fatalf("%+v", disposition)
		}
	})
	t.Run("unexpected-group-writable", func(t *testing.T) {
		root := t.TempDir()
		if err := os.Chmod(root, 0o720); err != nil {
			t.Fatal(err)
		}
		budgets := DefaultBudgets()
		classifier, err := NewClassifier(budgets, TrustPolicy{OwnerUID: uint32(os.Geteuid()), OwnerGID: uint32(os.Getegid())})
		if err != nil {
			t.Fatal(err)
		}
		disposition, _ := classifier.Classify(t.Context(), root)
		if disposition.Classification != Unsafe || !hasDetail(disposition, "ROOT_GROUP_WRITABLE") {
			t.Fatalf("%+v", disposition)
		}
	})
	t.Run("hard-link", func(t *testing.T) {
		root := t.TempDir()
		first := filepath.Join(root, "control.db")
		writeFile(t, first, sqliteBytes())
		if err := os.Link(first, filepath.Join(root, "copy")); err != nil {
			t.Fatal(err)
		}
		disposition, _ := testClassifier(t).Classify(t.Context(), root)
		if disposition.Classification != Unsafe || !hasDetail(disposition, "ENTRY_HARD_LINK") {
			t.Fatalf("%+v", disposition)
		}
	})
}

func TestBoundedInspectionFailsClosed(t *testing.T) {
	t.Run("entries", func(t *testing.T) {
		root := t.TempDir()
		for index := 0; index < 3; index++ {
			writeFile(t, filepath.Join(root, string(rune('a'+index))), []byte("x"))
		}
		budgets := DefaultBudgets()
		budgets.MaxEntries = 2
		classifier := mustClassifier(t, budgets)
		disposition, _ := classifier.Classify(t.Context(), root)
		if disposition.Classification != Ambiguous || !hasDetail(disposition, "ENTRY_BUDGET_EXCEEDED") {
			t.Fatalf("%+v", disposition)
		}
	})
	t.Run("depth", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "a", "b", "c"), []byte("x"))
		budgets := DefaultBudgets()
		budgets.MaxDepth = 1
		disposition, _ := mustClassifier(t, budgets).Classify(t.Context(), root)
		if disposition.Classification != Ambiguous || !hasDetail(disposition, "DEPTH_BUDGET_EXCEEDED") {
			t.Fatalf("%+v", disposition)
		}
	})
	t.Run("bytes", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "unknown-a.bin"), bytes.Repeat([]byte{'a'}, 16))
		writeFile(t, filepath.Join(root, "unknown-b.bin"), bytes.Repeat([]byte{'b'}, 16))
		budgets := DefaultBudgets()
		budgets.MaxSignatureReadBytes = 16
		budgets.MaxTotalReadBytes = 16
		disposition, _ := mustClassifier(t, budgets).Classify(t.Context(), root)
		if disposition.Classification != Ambiguous || !hasDetail(disposition, "READ_BUDGET_EXCEEDED") {
			t.Fatalf("%+v", disposition)
		}
	})
	t.Run("cancellation", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		disposition, _ := testClassifier(t).Classify(ctx, t.TempDir())
		if disposition.Classification != Ambiguous || !hasDetail(disposition, "INSPECTION_CANCELLED") {
			t.Fatalf("%+v", disposition)
		}
	})
}

func TestFilesystemFaultsFailClosed(t *testing.T) {
	t.Run("disappearing-entry", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "gone"), []byte("value"))
		source := faultSource{base: osInspectionSource{}, lstat: func(path string) (fileIdentity, error) {
			if filepath.Base(path) == "gone" {
				return fileIdentity{}, os.ErrNotExist
			}
			return osInspectionSource{}.Lstat(path)
		}}
		disposition, _ := classifierWithSource(t, source).Classify(t.Context(), root)
		if disposition.Classification != Ambiguous || !hasDetail(disposition, "ENTRY_IDENTITY_CHANGED") {
			t.Fatalf("%+v", disposition)
		}
	})
	t.Run("replaced-during-read", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "unknown"), []byte("value"))
		source := faultSource{base: osInspectionSource{}, readPrefix: func(string, fileIdentity, int64) ([]byte, error) {
			return nil, errIdentityChanged
		}}
		disposition, _ := classifierWithSource(t, source).Classify(t.Context(), root)
		if disposition.Classification != Ambiguous || !hasDetail(disposition, "READ_IDENTITY_CHANGED") {
			t.Fatalf("%+v", disposition)
		}
	})
	t.Run("directory-permission", func(t *testing.T) {
		root := t.TempDir()
		source := faultSource{base: osInspectionSource{}, readDirectory: func(string, fileIdentity, int) ([]string, error) {
			return nil, os.ErrPermission
		}}
		disposition, _ := classifierWithSource(t, source).Classify(t.Context(), root)
		if disposition.Classification != Unsafe || !hasDetail(disposition, "DIRECTORY_PERMISSION_DENIED") {
			t.Fatalf("%+v", disposition)
		}
	})
	t.Run("device-crossing", func(t *testing.T) {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, "foreign"), []byte("value"))
		source := faultSource{base: osInspectionSource{}, lstat: func(path string) (fileIdentity, error) {
			identity, err := osInspectionSource{}.Lstat(path)
			if err == nil && filepath.Base(path) == "foreign" {
				identity.Device++
			}
			return identity, err
		}}
		disposition, _ := classifierWithSource(t, source).Classify(t.Context(), root)
		if disposition.Classification != Unsafe || !hasDetail(disposition, "DEVICE_BOUNDARY_CROSSED") {
			t.Fatalf("%+v", disposition)
		}
	})
	t.Run("special-file", func(t *testing.T) {
		root := t.TempDir()
		if err := syscall.Mkfifo(filepath.Join(root, "pipe"), 0o600); err != nil {
			t.Fatal(err)
		}
		disposition, _ := testClassifier(t).Classify(t.Context(), root)
		if disposition.Classification != Unsafe || !hasDetail(disposition, "ENTRY_SPECIAL_FILE") {
			t.Fatalf("%+v", disposition)
		}
	})
}

func TestTerminalDirectoryRevalidationFailsClosed(t *testing.T) {
	t.Run("root-insertion", func(t *testing.T) {
		root := t.TempDir()
		calls := 0
		source := faultSource{base: osInspectionSource{}}
		source.readDirectory = func(path string, identity fileIdentity, limit int) ([]string, error) {
			names, err := source.base.ReadDirectory(path, identity, limit)
			if err == nil && path == root && calls == 0 {
				calls++
				writeFile(t, filepath.Join(root, "control.db"), sqliteBytes())
			}
			return names, err
		}
		disposition, _ := classifierWithSource(t, source).Classify(t.Context(), root)
		if disposition.Action != OfflineDispositionRequired || disposition.Classification != Ambiguous || !hasDetail(disposition, "TERMINAL_DIRECTORY_IDENTITY_CHANGED") {
			t.Fatalf("post-enumeration insertion was admitted: %+v", disposition)
		}
	})

	t.Run("nested-removal", func(t *testing.T) {
		root := t.TempDir()
		nested := filepath.Join(root, "old")
		note := filepath.Join(nested, "control.db")
		writeFile(t, note, sqliteBytes())
		calls := 0
		source := faultSource{base: osInspectionSource{}}
		source.readPrefix = func(path string, identity fileIdentity, maximum int64) ([]byte, error) {
			value, err := source.base.ReadPrefix(path, identity, maximum)
			if err == nil && path == note && calls == 0 {
				calls++
				if removeErr := os.Remove(note); removeErr != nil {
					t.Fatal(removeErr)
				}
			}
			return value, err
		}
		disposition, _ := classifierWithSource(t, source).Classify(t.Context(), root)
		if disposition.Action != OfflineDispositionRequired || disposition.Classification != Ambiguous || !hasDetail(disposition, "TERMINAL_DIRECTORY_IDENTITY_CHANGED") {
			t.Fatalf("nested removal was admitted: %+v", disposition)
		}
	})

	t.Run("nested-rename-to-symlink", func(t *testing.T) {
		root := t.TempDir()
		nested := filepath.Join(root, "docs")
		writeFile(t, filepath.Join(nested, "note.md"), []byte("safe"))
		lstats := 0
		source := faultSource{base: osInspectionSource{}}
		source.lstat = func(path string) (fileIdentity, error) {
			if path == nested {
				lstats++
				if lstats == 2 {
					moved := filepath.Join(root, "docs-moved")
					if err := os.Rename(nested, moved); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink(moved, nested); err != nil {
						t.Fatal(err)
					}
				}
			}
			return source.base.Lstat(path)
		}
		disposition, _ := classifierWithSource(t, source).Classify(t.Context(), root)
		if disposition.Action != OfflineDispositionRequired || disposition.Classification != Ambiguous || !hasDetail(disposition, "TERMINAL_DIRECTORY_IDENTITY_CHANGED") {
			t.Fatalf("directory symlink substitution was admitted: %+v", disposition)
		}
	})
}

func TestEveryEntryTypeParticipatesInDispositionAndDigest(t *testing.T) {
	fixedRoot := fileIdentity{Mode: fs.ModeDir | 0o700, Device: 1, Inode: 1, Links: 1, UID: 1, GID: 1}
	emptyState := &inspectionState{rootIdentity: &fixedRoot}
	withDirectory := &inspectionState{rootIdentity: &fixedRoot, entries: []fileObservation{{
		RelativePath: "unknown-authority",
		Identity:     fileIdentity{Mode: fs.ModeDir | 0o700, Device: 1, Inode: 2, Links: 1, UID: 1, GID: 1},
	}}}
	if digestRoot(emptyState, nil) == digestRoot(withDirectory, nil) {
		t.Fatal("directory observation did not alter canonical root digest")
	}
	cases := []struct {
		name   string
		want   Classification
		detail string
	}{
		{"IDENTITY", Malformed, "CURRENT_LAYOUT_INCOMPLETE_OR_INVALID"},
		{"control.db", Unsafe, "HISTORICAL_MARKER_WRONG_TYPE"},
		{"unknown-authority", Ambiguous, "UNKNOWN_CONTROL_ROOT_DIRECTORY"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			if err := os.Mkdir(filepath.Join(root, tc.name), 0o700); err != nil {
				t.Fatal(err)
			}
			disposition, _ := testClassifier(t).Classify(t.Context(), root)
			if disposition.Classification != tc.want || disposition.Action != OfflineDispositionRequired || !hasDetail(disposition, tc.detail) {
				t.Fatalf("wrong-type/unknown directory was admitted: %+v", disposition)
			}
		})
	}
}

func TestGapDBV1CurrentAuthorityRequiresCompleteCoherence(t *testing.T) {
	mutants := []struct {
		name   string
		mutate func(*testing.T, string)
	}{
		{"snapshot-header-only", func(t *testing.T, root string) {
			writeFile(t, filepath.Join(root, snapshotFilename(0)), mustReadFile(t, filepath.Join(root, snapshotFilename(0)))[:64])
		}},
		{"manifest-snapshot-digest", func(t *testing.T, root string) {
			mutateManifest(t, root, func(manifest *gapManifest) { manifest.SnapshotSHA256 = strings.Repeat("f", 64) })
		}},
		{"manifest-noncanonical", func(t *testing.T, root string) {
			manifest := mustDecodeManifest(t, root)
			payload := []byte(`{"generation":1,"database_id":"` + manifest.DatabaseID + `","snapshot_file":"` + manifest.SnapshotFile + `","snapshot_revision":0,"snapshot_sha256":"` + manifest.SnapshotSHA256 + `","wal_file":"` + manifest.WALFile + `","wal_start_revision":1}`)
			writeFile(t, filepath.Join(root, gapManifestName), encodeManifestPayload(payload))
		}},
		{"snapshot-count", func(t *testing.T, root string) {
			mutateSnapshotAndBindManifest(t, root, func(snapshot []byte) { binary.BigEndian.PutUint64(snapshot[36:44], 1) })
		}},
		{"snapshot-payload-size", func(t *testing.T, root string) {
			mutateSnapshotAndBindManifest(t, root, func(snapshot []byte) { binary.BigEndian.PutUint64(snapshot[44:52], 1) })
		}},
		{"snapshot-trailer", func(t *testing.T, root string) {
			path := filepath.Join(root, snapshotFilename(0))
			snapshot := mustReadFile(t, path)
			snapshot[len(snapshot)-1] ^= 0xff
			writeFile(t, path, snapshot)
		}},
		{"wal-not-consecutive", func(t *testing.T, root string) {
			mutateManifest(t, root, func(manifest *gapManifest) {
				manifest.WALStartRevision = 2
				manifest.WALFile = snapshotFilenameFor("wal-", 2)
			})
		}},
	}
	for _, mutant := range mutants {
		t.Run(mutant.name, func(t *testing.T) {
			root := t.TempDir()
			writeCurrentRoot(t, root, 0x81)
			mutant.mutate(t, root)
			disposition, _ := testClassifier(t).Classify(t.Context(), root)
			if disposition.Classification == CurrentSupported || disposition.Action == Recover {
				t.Fatalf("incoherent GapDB authority was admitted: %+v", disposition)
			}
		})
	}

	t.Run("snapshot-exceeds-evidence-budget", func(t *testing.T) {
		root := t.TempDir()
		writeCurrentRoot(t, root, 0x82)
		writeLargeValidSnapshot(t, root, 9<<10)
		disposition, _ := testClassifier(t).Classify(t.Context(), root)
		if disposition.Classification != Ambiguous || disposition.Action != OfflineDispositionRequired || !hasDetail(disposition, "CURRENT_LAYOUT_EVIDENCE_INCOMPLETE") {
			t.Fatalf("partially inspected snapshot was admitted: %+v", disposition)
		}
	})
}

func TestOfflineGateRefusesBeforeMutation(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "control.db"), sqliteBytes())
	decision, err := testClassifier(t).Decide(t.Context(), root)
	var refusal *OfflineDispositionError
	if !errors.As(err, &refusal) || refusal.Code != OfflineDispositionRequiredCode {
		t.Fatalf("typed refusal=%T %v", err, err)
	}
	if decision.Action != OfflineDispositionRequired || decision.Disposition.CurrentMutationCount != 0 || refusal.Disposition.ControlRootSHA256 != decision.Disposition.ControlRootSHA256 {
		t.Fatalf("decision=%+v refusal=%+v", decision, refusal)
	}
	mutations := 0
	startup := func(root string) error {
		_, gateErr := testClassifier(t).Decide(t.Context(), root)
		if gateErr != nil {
			return gateErr
		}
		mutations++
		return nil
	}
	if err := startup(root); err == nil || mutations != 0 {
		t.Fatalf("offline root admitted or mutation ran: err=%v mutations=%d", err, mutations)
	}
	empty := t.TempDir()
	if decision, err = testClassifier(t).Decide(t.Context(), empty); err != nil || decision.Action != Initialize {
		t.Fatalf("empty decision=%+v err=%v", decision, err)
	}
	current := t.TempDir()
	writeCurrentRoot(t, current, 0x51)
	if decision, err = testClassifier(t).Decide(t.Context(), current); err != nil || decision.Action != Recover {
		t.Fatalf("current decision=%+v err=%v", decision, err)
	}
}

func TestClassificationIsReadOnly(t *testing.T) {
	root := t.TempDir()
	writeFile(t, filepath.Join(root, "control.db"), sqliteBytes())
	before := snapshotRoot(t, root)
	for index := 0; index < 10; index++ {
		if _, err := testClassifier(t).Classify(t.Context(), root); err != nil {
			t.Fatal(err)
		}
	}
	after := snapshotRoot(t, root)
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("classification mutated root\nbefore=%v\nafter=%v", before, after)
	}
}

func TestConcurrentReadersAndReplacementRemainConservative(t *testing.T) {
	t.Run("shared-readers", func(t *testing.T) {
		root := t.TempDir()
		writeCurrentRoot(t, root, 0x69)
		classifier := testClassifier(t)
		const readers = 32
		results := make(chan Disposition, readers)
		errorsFound := make(chan error, readers)
		for range readers {
			go func() {
				value, err := classifier.Classify(t.Context(), root)
				results <- value
				errorsFound <- err
			}()
		}
		var first Disposition
		for index := range readers {
			if err := <-errorsFound; err != nil {
				t.Fatal(err)
			}
			value := <-results
			if index == 0 {
				first = value
			} else if !reflect.DeepEqual(first, value) {
				t.Fatalf("readers disagreed: first=%+v current=%+v", first, value)
			}
		}
	})
	t.Run("replacement", func(t *testing.T) {
		root := t.TempDir()
		path := filepath.Join(root, "control.db")
		writeFile(t, path, sqliteBytes())
		stop := make(chan struct{})
		done := make(chan struct{})
		go func() {
			defer close(done)
			for {
				select {
				case <-stop:
					return
				default:
				}
				temporary := filepath.Join(root, "replacement")
				_ = os.WriteFile(temporary, []byte("short"), 0o600)
				_ = os.Rename(temporary, path)
				_ = os.WriteFile(temporary, sqliteBytes(), 0o600)
				_ = os.Rename(temporary, path)
			}
		}()
		classifier := testClassifier(t)
		for range 50 {
			disposition, err := classifier.Classify(t.Context(), root)
			if err != nil {
				t.Fatal(err)
			}
			if disposition.Classification == EmptySupported || disposition.Classification == CurrentSupported {
				t.Fatalf("replacement became supported: %+v", disposition)
			}
		}
		close(stop)
		select {
		case <-done:
		case <-time.After(time.Second):
			t.Fatal("replacement writer did not stop")
		}
	})
}

func TestClassificationPrecedence(t *testing.T) {
	tests := []struct {
		name  string
		build func(*testing.T, string)
		want  Classification
	}{
		{"current-plus-unsafe", func(t *testing.T, r string) {
			writeCurrentRoot(t, r, 0x61)
			if err := os.Symlink("missing", filepath.Join(r, "bad")); err != nil {
				t.Fatal(err)
			}
		}, Unsafe},
		{"historical-plus-unsafe", func(t *testing.T, r string) {
			writeFile(t, filepath.Join(r, "control.db"), sqliteBytes())
			if err := os.Symlink("missing", filepath.Join(r, "bad")); err != nil {
				t.Fatal(err)
			}
		}, Unsafe},
		{"malformed-plus-historical", func(t *testing.T, r string) {
			writeFile(t, filepath.Join(r, "IDENTITY"), []byte("GAPID001bad"))
			writeFile(t, filepath.Join(r, "control.db"), sqliteBytes())
		}, Mixed},
		{"ambiguous-plus-empty", func(t *testing.T, r string) { writeFile(t, filepath.Join(r, "unknown.bin"), []byte("?")) }, Ambiguous},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			tc.build(t, root)
			got, err := testClassifier(t).Classify(t.Context(), root)
			if err != nil || got.Classification != tc.want {
				t.Fatalf("got=%+v err=%v", got, err)
			}
		})
	}
}

func TestFixtureAndMutantCensusIsClosed(t *testing.T) {
	production := readProductionSources(t)
	type mutant struct {
		id   string
		kill func(*testing.T)
	}
	classifyFile := func(t *testing.T, name string, value []byte) Disposition {
		root := t.TempDir()
		writeFile(t, filepath.Join(root, name), value)
		disposition, err := testClassifier(t).Classify(t.Context(), root)
		if err != nil {
			t.Fatal(err)
		}
		return disposition
	}
	cases := []mutant{
		{"filename-only", func(t *testing.T) {
			if d := classifyFile(t, "control.db", []byte("text")); d.Classification == Historical {
				t.Fatal("name alone became historical")
			}
		}},
		{"extension-only", func(t *testing.T) {
			if d := classifyFile(t, "random.db", []byte("text")); d.Classification == Historical {
				t.Fatal("extension alone became historical")
			}
		}},
		{"symlink-follow", func(t *testing.T) {
			root := t.TempDir()
			if err := os.Symlink("outside", filepath.Join(root, "state")); err != nil {
				t.Fatal(err)
			}
			d, _ := testClassifier(t).Classify(t.Context(), root)
			if d.Classification != Unsafe {
				t.Fatal("symlink followed or ignored")
			}
		}},
		{"unknown-ignore", func(t *testing.T) {
			if d := classifyFile(t, "unknown", []byte("value")); d.Classification != Ambiguous {
				t.Fatal("unknown entry became safe")
			}
		}},
		{"truncated-safe", func(t *testing.T) {
			if d := classifyFile(t, "control.db", []byte("SQLite format 3")); d.Classification != Ambiguous {
				t.Fatal("truncated header became safe")
			}
		}},
		{"mixed-as-current", func(t *testing.T) {
			root := t.TempDir()
			writeCurrentRoot(t, root, 0x71)
			writeFile(t, filepath.Join(root, "control.db"), sqliteBytes())
			d, _ := testClassifier(t).Classify(t.Context(), root)
			if d.Classification != Mixed {
				t.Fatal("mixed root preferred current")
			}
		}},
		{"historical-as-empty", func(t *testing.T) {
			if d := classifyFile(t, "copy", sqliteBytes()); d.Classification != Historical {
				t.Fatal("historical content became empty")
			}
		}},
		{"sql-import", func(t *testing.T) { assertSourceAbsent(t, production, "database/sql") }},
		{"sql-subprocess", func(t *testing.T) { assertSourceAbsent(t, production, "os/exec") }},
		{"implicit-importer", func(t *testing.T) { assertSourceAbsent(t, production, "import"+"er") }},
		{"waiver-force", func(t *testing.T) { assertSourceAbsent(t, production, "--"+"force") }},
		{"nonzero-mutation", func(t *testing.T) {
			for _, name := range []string{"x", "control.db"} {
				d := classifyFile(t, name, []byte("value"))
				if d.CurrentMutationCount != 0 {
					t.Fatal("classification reported a mutation")
				}
			}
		}},
	}
	ids := make([]string, len(cases))
	for index, candidate := range cases {
		ids[index] = candidate.id
	}
	if len(ids) != 12 || digestStrings(ids) != "0793ba58774395633a23eed6a01eea6c10a19ed3d9318ec1f4912f4259168ca5" {
		t.Fatalf("mutant census changed: count=%d sha=%s", len(ids), digestStrings(ids))
	}
	for _, candidate := range cases {
		t.Run(candidate.id, candidate.kill)
	}
	for _, forbidden := range []string{"database/sql", "modernc.org/sqlite", "go-sqlite3", "os/exec", "--force", "ignore-legacy", "importer", "backend selector"} {
		if strings.Contains(production, forbidden) {
			t.Fatalf("forbidden production surface %q", forbidden)
		}
	}
}

func TestCycleOneFixtureAndMutantEvidenceIsSourceBound(t *testing.T) {
	type evidenceCase struct {
		ID             string         `json:"id"`
		Construction   string         `json:"construction"`
		Classification Classification `json:"classification"`
		Action         Action         `json:"action"`
		Detail         string         `json:"detail"`
	}
	cases := []evidenceCase{
		{"fixture-gapdb-v1", "current:v1:empty-snapshot:revision-0:wal-start-1", CurrentSupported, Recover, "CURRENT_AUTHORITY_COHERENT"},
		{"fixture-identity-directory", "directory:IDENTITY", Malformed, OfflineDispositionRequired, "CURRENT_LAYOUT_INCOMPLETE_OR_INVALID"},
		{"fixture-control-db-directory", "directory:control.db", Unsafe, OfflineDispositionRequired, "HISTORICAL_MARKER_WRONG_TYPE"},
		{"fixture-unknown-directory", "directory:unknown-authority", Ambiguous, OfflineDispositionRequired, "UNKNOWN_CONTROL_ROOT_DIRECTORY"},
		{"mutant-root-post-list-insertion", "concurrent:root-insert:control.db:sqlite-header", Ambiguous, OfflineDispositionRequired, "TERMINAL_DIRECTORY_IDENTITY_CHANGED"},
		{"mutant-snapshot-truncated", "current-mutant:snapshot:first-64-bytes", Malformed, OfflineDispositionRequired, "CURRENT_LAYOUT_INCOMPLETE_OR_INVALID"},
		{"mutant-manifest-digest", "current-mutant:manifest:snapshot-sha256=ff*32:recrc", Malformed, OfflineDispositionRequired, "CURRENT_LAYOUT_INCOMPLETE_OR_INVALID"},
		{"mutant-manifest-order", "current-mutant:manifest:generation-before-database-id:recrc", Malformed, OfflineDispositionRequired, "CURRENT_LAYOUT_INCOMPLETE_OR_INVALID"},
		{"mutant-snapshot-count", "current-mutant:snapshot:count=1:payload=0:redigest:rebind", Malformed, OfflineDispositionRequired, "CURRENT_LAYOUT_INCOMPLETE_OR_INVALID"},
		{"mutant-snapshot-payload", "current-mutant:snapshot:payload-bytes=1:redigest:rebind", Malformed, OfflineDispositionRequired, "CURRENT_LAYOUT_INCOMPLETE_OR_INVALID"},
		{"mutant-snapshot-trailer", "current-mutant:snapshot:trailer-last-byte-xor-ff", Malformed, OfflineDispositionRequired, "CURRENT_LAYOUT_INCOMPLETE_OR_INVALID"},
		{"mutant-wal-continuity", "current-mutant:manifest:wal-start=2:wal-file=2:recrc", Malformed, OfflineDispositionRequired, "CURRENT_LAYOUT_INCOMPLETE_OR_INVALID"},
		{"mutant-evidence-budget", "current:v1:snapshot-record-value=9216:max-signature=8192", Ambiguous, OfflineDispositionRequired, "CURRENT_LAYOUT_EVIDENCE_INCOMPLETE"},
	}
	encoded, err := json.Marshal(cases)
	if err != nil {
		t.Fatal(err)
	}
	const wantEvidenceSHA256 = "613fea9485fb5e68f241b0392d1953528c833b7102f2c9ada2c1fef8c9da09a8"
	digest := sha256.Sum256(encoded)
	gotDigest := hex.EncodeToString(digest[:])
	if gotDigest != wantEvidenceSHA256 {
		t.Fatalf("cycle-one evidence census changed: count=%d sha=%s", len(cases), gotDigest)
	}
	withoutOne, _ := json.Marshal(cases[:len(cases)-1])
	if sha256.Sum256(withoutOne) == digest {
		t.Fatal("fixture deletion did not alter evidence census")
	}
	substituted := slices.Clone(cases)
	substituted[0].Construction = "current:v1:decoy"
	substitutedBytes, _ := json.Marshal(substituted)
	if sha256.Sum256(substitutedBytes) == digest {
		t.Fatal("fixture construction substitution did not alter evidence census")
	}
	substituted = slices.Clone(cases)
	substituted[0].Action = Initialize
	substitutedBytes, _ = json.Marshal(substituted)
	if sha256.Sum256(substitutedBytes) == digest {
		t.Fatal("expected-outcome substitution did not alter evidence census")
	}
	for _, candidate := range cases {
		t.Run(candidate.ID, func(t *testing.T) {
			disposition := executeCycleOneEvidence(t, candidate.Construction)
			if disposition.Classification != candidate.Classification || disposition.Action != candidate.Action || !hasDetail(disposition, candidate.Detail) {
				t.Fatalf("construction %q produced %+v", candidate.Construction, disposition)
			}
		})
	}
}

func executeCycleOneEvidence(t *testing.T, construction string) Disposition {
	t.Helper()
	root := t.TempDir()
	writeCurrent := func() { writeCurrentRoot(t, root, 0x91) }
	classifier := testClassifier(t)
	switch construction {
	case "current:v1:empty-snapshot:revision-0:wal-start-1":
		writeCurrent()
	case "directory:IDENTITY", "directory:control.db", "directory:unknown-authority":
		if err := os.Mkdir(filepath.Join(root, strings.TrimPrefix(construction, "directory:")), 0o700); err != nil {
			t.Fatal(err)
		}
	case "concurrent:root-insert:control.db:sqlite-header":
		calls := 0
		source := faultSource{base: osInspectionSource{}}
		source.readDirectory = func(path string, identity fileIdentity, limit int) ([]string, error) {
			names, err := source.base.ReadDirectory(path, identity, limit)
			if err == nil && path == root && calls == 0 {
				calls++
				writeFile(t, filepath.Join(root, "control.db"), sqliteBytes())
			}
			return names, err
		}
		classifier = classifierWithSource(t, source)
	case "current-mutant:snapshot:first-64-bytes":
		writeCurrent()
		path := filepath.Join(root, snapshotFilename(0))
		writeFile(t, path, mustReadFile(t, path)[:64])
	case "current-mutant:manifest:snapshot-sha256=ff*32:recrc":
		writeCurrent()
		mutateManifest(t, root, func(manifest *gapManifest) { manifest.SnapshotSHA256 = strings.Repeat("f", 64) })
	case "current-mutant:manifest:generation-before-database-id:recrc":
		writeCurrent()
		manifest := mustDecodeManifest(t, root)
		payload := []byte(`{"generation":1,"database_id":"` + manifest.DatabaseID + `","snapshot_file":"` + manifest.SnapshotFile + `","snapshot_revision":0,"snapshot_sha256":"` + manifest.SnapshotSHA256 + `","wal_file":"` + manifest.WALFile + `","wal_start_revision":1}`)
		writeFile(t, filepath.Join(root, gapManifestName), encodeManifestPayload(payload))
	case "current-mutant:snapshot:count=1:payload=0:redigest:rebind":
		writeCurrent()
		mutateSnapshotAndBindManifest(t, root, func(snapshot []byte) { binary.BigEndian.PutUint64(snapshot[36:44], 1) })
	case "current-mutant:snapshot:payload-bytes=1:redigest:rebind":
		writeCurrent()
		mutateSnapshotAndBindManifest(t, root, func(snapshot []byte) { binary.BigEndian.PutUint64(snapshot[44:52], 1) })
	case "current-mutant:snapshot:trailer-last-byte-xor-ff":
		writeCurrent()
		path := filepath.Join(root, snapshotFilename(0))
		snapshot := mustReadFile(t, path)
		snapshot[len(snapshot)-1] ^= 0xff
		writeFile(t, path, snapshot)
	case "current-mutant:manifest:wal-start=2:wal-file=2:recrc":
		writeCurrent()
		mutateManifest(t, root, func(manifest *gapManifest) {
			manifest.WALStartRevision = 2
			manifest.WALFile = snapshotFilenameFor("wal-", 2)
		})
	case "current:v1:snapshot-record-value=9216:max-signature=8192":
		writeCurrent()
		writeLargeValidSnapshot(t, root, 9<<10)
	default:
		t.Fatalf("unbound evidence construction %q", construction)
	}
	disposition, err := classifier.Classify(t.Context(), root)
	if err != nil {
		t.Fatal(err)
	}
	return disposition
}

func assertSourceAbsent(t *testing.T, source, forbidden string) {
	t.Helper()
	if strings.Contains(source, forbidden) {
		t.Fatalf("forbidden production surface %q", forbidden)
	}
}

func FuzzSQLiteHeader(f *testing.F) {
	f.Add(sqliteBytes())
	f.Add([]byte("SQLite format 3"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, value []byte) {
		got := hasSQLiteHeader(value)
		want := len(value) >= len(SQLiteHeader) && bytes.Equal(value[:len(SQLiteHeader)], []byte(SQLiteHeader))
		if got != want {
			t.Fatalf("got=%t want=%t value=%x", got, want, value)
		}
	})
}

func FuzzClassifyArbitraryFilenameAndPrefix(f *testing.F) {
	f.Add("random.bin", []byte("value"))
	f.Add("copied", sqliteBytes())
	f.Add("control.db", []byte("lookalike"))
	f.Fuzz(func(t *testing.T, name string, value []byte) {
		if name == "" || name == "." || name == ".." || filepath.Base(name) != name || len(name) > 120 || strings.ContainsRune(name, 0) || len(value) > 1024 {
			t.Skip()
		}
		root := t.TempDir()
		writeFile(t, filepath.Join(root, name), value)
		classifier := testClassifier(t)
		first, err := classifier.Classify(t.Context(), root)
		if err != nil {
			t.Fatal(err)
		}
		second, err := classifier.Classify(t.Context(), root)
		if err != nil {
			t.Fatal(err)
		}
		if err := first.Validate(); err != nil || first.CurrentMutationCount != 0 || !reflect.DeepEqual(first, second) {
			t.Fatalf("invalid or nondeterministic disposition: first=%+v second=%+v err=%v", first, second, err)
		}
	})
}

func testClassifier(t *testing.T) *Classifier { t.Helper(); return mustClassifier(t, DefaultBudgets()) }
func mustClassifier(t *testing.T, budgets Budgets) *Classifier {
	t.Helper()
	c, err := NewClassifier(budgets, TrustPolicy{OwnerUID: uint32(os.Geteuid()), OwnerGID: uint32(os.Getegid()), AllowGroupWritable: true})
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func writeFile(t *testing.T, path string, value []byte) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, value, 0o600); err != nil {
		t.Fatal(err)
	}
}
func sqliteBytes() []byte {
	return append(append([]byte{}, []byte(SQLiteHeader)...), bytes.Repeat([]byte{0}, 112)...)
}
func currentIdentity(seed byte) []byte {
	v := make([]byte, 64)
	copy(v, "GAPID001")
	binary.BigEndian.PutUint16(v[8:10], 1)
	binary.BigEndian.PutUint16(v[10:12], 64)
	for i := 12; i < 28; i++ {
		v[i] = seed
	}
	binary.BigEndian.PutUint64(v[28:36], 64)
	binary.BigEndian.PutUint64(v[36:44], 1)
	binary.BigEndian.PutUint32(v[60:64], crc32.Checksum(v[:60], crc32.MakeTable(crc32.Castagnoli)))
	return v
}
func writeCurrentRoot(t *testing.T, root string, seed byte) {
	t.Helper()
	if err := os.MkdirAll(root, 0o700); err != nil {
		t.Fatal(err)
	}
	id := currentIdentity(seed)
	idHex := hex.EncodeToString(id[12:28])
	snapshotName := "snapshot-00000000000000000000.gdb"
	walName := "wal-00000000000000000001.gdb"
	snapshot := make([]byte, 96)
	copy(snapshot, "GAPSNAP1")
	binary.BigEndian.PutUint16(snapshot[8:10], 1)
	binary.BigEndian.PutUint16(snapshot[10:12], 64)
	copy(snapshot[12:28], id[12:28])
	sum := sha256.Sum256(snapshot[:64])
	copy(snapshot[64:], sum[:])
	wal := make([]byte, 64)
	copy(wal, "GAPWAL01")
	binary.BigEndian.PutUint16(wal[8:10], 1)
	binary.BigEndian.PutUint16(wal[10:12], 64)
	copy(wal[12:28], id[12:28])
	binary.BigEndian.PutUint64(wal[28:36], 1)
	binary.BigEndian.PutUint64(wal[36:44], 1)
	binary.BigEndian.PutUint32(wal[60:64], crc32.Checksum(wal[:60], crc32.MakeTable(crc32.Castagnoli)))
	payload, _ := json.Marshal(map[string]any{"database_id": idHex, "generation": 1, "snapshot_file": snapshotName, "snapshot_revision": 0, "snapshot_sha256": hex.EncodeToString(sum[:]), "wal_file": walName, "wal_start_revision": 1})
	manifest := make([]byte, 16+len(payload)+4)
	copy(manifest, "GAPCUR01")
	binary.BigEndian.PutUint16(manifest[8:10], 1)
	binary.BigEndian.PutUint32(manifest[12:16], uint32(len(payload)))
	copy(manifest[16:], payload)
	binary.BigEndian.PutUint32(manifest[len(manifest)-4:], crc32.Checksum(manifest[:len(manifest)-4], crc32.MakeTable(crc32.Castagnoli)))
	writeFile(t, filepath.Join(root, "IDENTITY"), id)
	writeFile(t, filepath.Join(root, "CURRENT"), manifest)
	writeFile(t, filepath.Join(root, snapshotName), snapshot)
	writeFile(t, filepath.Join(root, walName), wal)
}

func snapshotFilename(revision uint64) string {
	return snapshotFilenameFor("snapshot-", revision)
}

func snapshotFilenameFor(prefix string, revision uint64) string {
	return fmt.Sprintf("%s%020d.gdb", prefix, revision)
}

func mustReadFile(t *testing.T, path string) []byte {
	t.Helper()
	value, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func mustDecodeManifest(t *testing.T, root string) gapManifest {
	t.Helper()
	manifest, err := decodeGapManifest(mustReadFile(t, filepath.Join(root, gapManifestName)))
	if err != nil {
		t.Fatal(err)
	}
	return manifest
}

func encodeManifestPayload(payload []byte) []byte {
	manifest := make([]byte, 16+len(payload)+4)
	copy(manifest, "GAPCUR01")
	binary.BigEndian.PutUint16(manifest[8:10], 1)
	binary.BigEndian.PutUint32(manifest[12:16], uint32(len(payload)))
	copy(manifest[16:], payload)
	binary.BigEndian.PutUint32(manifest[len(manifest)-4:], crc32.Checksum(manifest[:len(manifest)-4], crc32.MakeTable(crc32.Castagnoli)))
	return manifest
}

func mutateManifest(t *testing.T, root string, mutate func(*gapManifest)) {
	t.Helper()
	manifest := mustDecodeManifest(t, root)
	mutate(&manifest)
	payload, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(root, gapManifestName), encodeManifestPayload(payload))
}

func mutateSnapshotAndBindManifest(t *testing.T, root string, mutate func([]byte)) {
	t.Helper()
	path := filepath.Join(root, snapshotFilename(0))
	snapshot := mustReadFile(t, path)
	mutate(snapshot)
	digest := sha256.Sum256(snapshot[:len(snapshot)-sha256.Size])
	copy(snapshot[len(snapshot)-sha256.Size:], digest[:])
	writeFile(t, path, snapshot)
	mutateManifest(t, root, func(manifest *gapManifest) { manifest.SnapshotSHA256 = hex.EncodeToString(digest[:]) })
}

func writeLargeValidSnapshot(t *testing.T, root string, valueBytes int) {
	t.Helper()
	identity := mustReadFile(t, filepath.Join(root, gapIdentityName))
	const itemHeader = 40
	payloadBytes := itemHeader + 1 + valueBytes
	snapshot := make([]byte, gapHeaderSize+payloadBytes+sha256.Size)
	copy(snapshot, "GAPSNAP1")
	binary.BigEndian.PutUint16(snapshot[8:10], gapVersion)
	binary.BigEndian.PutUint16(snapshot[10:12], gapHeaderSize)
	copy(snapshot[12:28], identity[12:28])
	binary.BigEndian.PutUint64(snapshot[28:36], 1)
	binary.BigEndian.PutUint64(snapshot[36:44], 1)
	binary.BigEndian.PutUint64(snapshot[44:52], uint64(payloadBytes))
	offset := gapHeaderSize
	binary.BigEndian.PutUint32(snapshot[offset:offset+4], 1)
	binary.BigEndian.PutUint32(snapshot[offset+4:offset+8], uint32(valueBytes))
	binary.BigEndian.PutUint64(snapshot[offset+8:offset+16], 1)
	offset += itemHeader
	snapshot[offset] = 'a'
	digest := sha256.Sum256(snapshot[:len(snapshot)-sha256.Size])
	copy(snapshot[len(snapshot)-sha256.Size:], digest[:])
	snapshotName := snapshotFilename(1)
	writeFile(t, filepath.Join(root, snapshotName), snapshot)

	walName := snapshotFilenameFor("wal-", 2)
	wal := make([]byte, gapHeaderSize)
	copy(wal, "GAPWAL01")
	binary.BigEndian.PutUint16(wal[8:10], gapVersion)
	binary.BigEndian.PutUint16(wal[10:12], gapHeaderSize)
	copy(wal[12:28], identity[12:28])
	binary.BigEndian.PutUint64(wal[28:36], 2)
	binary.BigEndian.PutUint64(wal[36:44], 1)
	binary.BigEndian.PutUint32(wal[60:64], crc32.Checksum(wal[:60], crc32.MakeTable(crc32.Castagnoli)))
	writeFile(t, filepath.Join(root, walName), wal)
	mutateManifest(t, root, func(manifest *gapManifest) {
		manifest.SnapshotFile = snapshotName
		manifest.SnapshotRevision = 1
		manifest.SnapshotSHA256 = hex.EncodeToString(digest[:])
		manifest.WALFile = walName
		manifest.WALStartRevision = 2
	})
}
func hasDetail(d Disposition, code string) bool {
	for _, f := range d.Findings {
		if f.DetailCode == code {
			return true
		}
	}
	return false
}
func snapshotRoot(t *testing.T, root string) map[string]string {
	t.Helper()
	out := map[string]string{}
	err := filepath.WalkDir(root, func(path string, e os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(root, path)
		if e.IsDir() {
			out[filepath.ToSlash(rel)] = "dir"
			return nil
		}
		info, err := e.Info()
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		out[filepath.ToSlash(rel)] = hex.EncodeToString(sum[:]) + ":" + info.Mode().String()
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return out
}
func digestStrings(v []string) string {
	sum := sha256.Sum256([]byte(strings.Join(v, "\n")))
	return hex.EncodeToString(sum[:])
}
func readProductionSources(t *testing.T) string {
	t.Helper()
	var b strings.Builder
	for _, name := range []string{"model.go", "signatures.go", "classifier.go"} {
		value, err := os.ReadFile(filepath.Join(".", name))
		if err != nil {
			t.Fatal(err)
		}
		b.Write(value)
	}
	return b.String()
}

type faultSource struct {
	base          inspectionSource
	lstat         func(string) (fileIdentity, error)
	readDirectory func(string, fileIdentity, int) ([]string, error)
	readPrefix    func(string, fileIdentity, int64) ([]byte, error)
}

func (s faultSource) Lstat(path string) (fileIdentity, error) {
	if s.lstat != nil {
		return s.lstat(path)
	}
	return s.base.Lstat(path)
}

func (s faultSource) ReadDirectory(path string, identity fileIdentity, limit int) ([]string, error) {
	if s.readDirectory != nil {
		return s.readDirectory(path, identity, limit)
	}
	return s.base.ReadDirectory(path, identity, limit)
}

func (s faultSource) ReadPrefix(path string, identity fileIdentity, maximum int64) ([]byte, error) {
	if s.readPrefix != nil {
		return s.readPrefix(path, identity, maximum)
	}
	return s.base.ReadPrefix(path, identity, maximum)
}

func classifierWithSource(t *testing.T, source inspectionSource) *Classifier {
	t.Helper()
	classifier := testClassifier(t)
	classifier.source = source
	return classifier
}
