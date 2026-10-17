package controlroot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"syscall"
	"testing"
)

// These are UNRUN normal component definitions, not a physical failed-Close
// experiment, constructor trace, public cause API, or whole-resource receipt.
// Returned-error callbacks call the real File.Close once, then substitute the
// returned error ONLY after that actual Close succeeds. There is no repair Close.
func inspectionD1Open(t *testing.T, path string, flags int) (*os.File, fileIdentity) {
	t.Helper()
	identity, err := (osInspectionSource{}).Lstat(path)
	if err != nil {
		t.Fatal(err)
	}
	file, err := os.OpenFile(path, flags, 0)
	if err != nil {
		t.Fatal(err)
	}
	return file, identity
}

func inspectionD1ReturnedClose(t *testing.T, owned *os.File, injected error, calls *int, actual *error) func(*os.File) error {
	t.Helper()
	return func(file *os.File) error {
		*calls = *calls + 1
		if file != owned {
			t.Error("consumer passed a different owned File")
		}
		*actual = (*os.File).Close(file)
		if *actual != nil {
			return *actual
		}
		return injected
	}
}

func inspectionD1ClosedOnce(t *testing.T, file *os.File, calls int, actual error) {
	t.Helper()
	if calls != 1 || actual != nil {
		t.Fatalf("actual Close calls=%d err=%v; want one successful physical Close", calls, actual)
	}
	// Stat is a closed-object diagnostic, NOT another Close or proof that a
	// physical Close syscall failed. It cannot erase a returned-error refusal.
	if _, err := file.Stat(); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("consumed object still usable: %v", err)
	}
}

func TestInspectionD1HealthyFullOSWrappers(t *testing.T) {
	dir := t.TempDir()
	writeFile(t, filepath.Join(dir, "z"), []byte("abcdef"))
	writeFile(t, filepath.Join(dir, "a"), nil)
	source := osInspectionSource{}
	identity, err := source.Lstat(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, limit := range []int{0, -1, 3} {
		names, err := source.ReadDirectory(dir, identity, limit)
		if err != nil || !slices.Equal(names, []string{"a", "z"}) {
			t.Fatalf("limit=%d names=%v err=%v", limit, names, err)
		}
	}
	names, err := source.ReadDirectory(dir, identity, 1)
	if err != nil || len(names) != 1 || (names[0] != "a" && names[0] != "z") {
		t.Fatalf("bounded directory names=%v err=%v", names, err)
	}
	for _, fixture := range []struct {
		name string
		maximum int64
		want []byte
	}{
		{"z", 3, []byte("abc")},
		{"z", 6, []byte("abcdef")},
		{"z", 12, []byte("abcdef")},
		{"z", 0, []byte{}},
		{"a", 12, []byte{}},
	} {
		path := filepath.Join(dir, fixture.name)
		identity, err := source.Lstat(path)
		if err != nil {
			t.Fatal(err)
		}
		prefix, err := source.ReadPrefix(path, identity, fixture.maximum)
		if err != nil || prefix == nil || !bytes.Equal(prefix, fixture.want) {
			t.Fatalf("%s maximum=%d prefix=%q err=%v", fixture.name, fixture.maximum, prefix, err)
		}
	}
	missing := filepath.Join(dir, "missing")
	if names, err := source.ReadDirectory(missing, fileIdentity{}, 1); names != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("directory open failure names=%v err=%v", names, err)
	}
	if prefix, err := source.ReadPrefix(missing, fileIdentity{}, 1); prefix != nil || !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("prefix open failure prefix=%q err=%v", prefix, err)
	}
}

func TestInspectionD1OwnedConsumersCloseOnlyAndIdentityPrimary(t *testing.T) {
	for _, operation := range []string{"directory", "prefix", "zero-prefix"} {
		for _, mode := range []string{"healthy", "close-only", "close-only-generic", "identity-only", "identity-and-permission-close"} {
			t.Run(operation+"/"+mode, func(t *testing.T) {
				path := t.TempDir()
				if operation != "directory" {
					path = filepath.Join(path, "file")
					writeFile(t, path, []byte("prefix"))
				}
				file, expected := inspectionD1Open(t, path, os.O_RDONLY)
				if strings.HasPrefix(mode, "identity-") {
					expected.Inode++
				}
				var injected error
				if mode == "close-only" || mode == "identity-and-permission-close" {
					injected = &os.PathError{Op: "close", Path: "private-close-diagnostic", Err: os.ErrPermission}
				}
				if mode == "close-only-generic" {
					injected = errors.New("returned Close error")
				}
				calls := 0
				var actual error
				closeFile := inspectionD1ReturnedClose(t, file, injected, &calls, &actual)
				var resultErr error
				var usable bool
				if operation == "directory" {
					names, err := readInspectionDirectoryFile(file, expected, 1, closeFile)
					resultErr, usable = err, names != nil
				} else {
					maximum := int64(6)
					if operation == "zero-prefix" {
						maximum = 0
					}
					prefix, err := readInspectionPrefixFile(file, expected, maximum, closeFile)
					resultErr, usable = err, prefix != nil
				}
				inspectionD1ClosedOnce(t, file, calls, actual)
				switch mode {
				case "healthy":
					if resultErr != nil || !usable {
						t.Fatalf("healthy result err=%v usable=%t", resultErr, usable)
					}
				case "close-only", "close-only-generic":
					if resultErr != injected || usable {
						t.Fatalf("sole Close error must escape unchanged, with nil value: %v usable=%t", resultErr, usable)
					}
				case "identity-only":
					if resultErr != errIdentityChanged || usable {
						t.Fatalf("primary must escape unchanged: %v usable=%t", resultErr, usable)
					}
				case "identity-and-permission-close":
					if usable || resultErr == nil || resultErr.Error() != errIdentityChanged.Error() || !errors.Is(resultErr, errIdentityChanged) || !errors.Is(resultErr, os.ErrPermission) {
						t.Fatalf("lost primary/Close cause or kept usable value: %v usable=%t", resultErr, usable)
					}
					var closePath *os.PathError
					if !errors.As(resultErr, &closePath) || closePath != injected {
						t.Fatalf("actual returned Close type lost: %v", resultErr)
					}
					inspectionD1AssertPrimaryPolicy(t, resultErr, errIdentityChanged, FindingConcurrent, "READ_IDENTITY_CHANGED", Ambiguous)
				}
			})
		}
	}
}

func TestInspectionD1ReadFullPrimaryAndCloseCause(t *testing.T) {
	for _, mode := range []string{"read-error", "eof", "short-read"} {
		t.Run(mode, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "file")
			writeFile(t, path, []byte("abcdef"))
			flags := os.O_RDONLY
			if mode == "read-error" {
				flags = os.O_WRONLY // real owned regular File: Stat succeeds, Read fails
			}
			file, expected := inspectionD1Open(t, path, flags)
			if mode != "read-error" {
				offset := int64(6)
				if mode == "short-read" {
					offset = 5
				}
				if _, err := file.Seek(offset, io.SeekStart); err != nil {
					// Setup failure consumes this object once, not a repair path.
					closeErr := file.Close()
					t.Fatalf("setup seek=%v close=%v", err, closeErr)
				}
			}
			injected := &os.PathError{Op: "close", Path: "secondary-close-only", Err: os.ErrPermission}
			calls := 0
			var actual error
			prefix, err := readInspectionPrefixFile(file, expected, 6, inspectionD1ReturnedClose(t, file, injected, &calls, &actual))
			inspectionD1ClosedOnce(t, file, calls, actual)
			if prefix != nil || err == nil || !errors.Is(err, injected) {
				t.Fatalf("prefix=%q err=%v", prefix, err)
			}
			combined, ok := err.(*inspectionCloseError)
			if !ok || combined.closeErr != injected || err.Error() != combined.primary.Error() {
				t.Fatalf("primary text/order or actual Close error lost: %v", err)
			}
			switch mode {
			case "read-error":
				var primary *os.PathError
				if !errors.As(err, &primary) || primary.Op != "read" || primary.Path != path || !errors.Is(primary, syscall.EBADF) || combined.primary != primary {
					t.Fatalf("first errors.As must find original read PathError: %v", err)
				}
				inspectionD1AssertPrimaryPolicy(t, err, primary, FindingFilesystem, "READ_INSPECTION_FAILED", Ambiguous)
			case "eof":
				if combined.primary != io.EOF || !errors.Is(err, io.EOF) {
					t.Fatalf("ReadFull EOF policy changed: %v", err)
				}
			case "short-read":
				if combined.primary != errIdentityChanged || errors.Is(err, io.ErrUnexpectedEOF) {
					t.Fatalf("short ReadFull must retain original identity refusal: %v", err)
				}
			}
		})
	}
}

func TestInspectionD1DirectoryEOFAndBoundedNames(t *testing.T) {
	for _, injected := range []error{nil, errors.New("returned directory Close error")} {
		dir := t.TempDir()
		writeFile(t, filepath.Join(dir, "z"), nil)
		writeFile(t, filepath.Join(dir, "a"), nil)
		file, expected := inspectionD1Open(t, dir, os.O_RDONLY)
		// Consume entries without changing filesystem identity. The helper's
		// positive-limit ReadDir now reaches genuine io.EOF; it normalizes EOF.
		if _, err := file.ReadDir(-1); err != nil {
			closeErr := file.Close()
			t.Fatalf("setup ReadDir=%v close=%v", err, closeErr)
		}
		calls := 0
		var actual error
		names, err := readInspectionDirectoryFile(file, expected, 1, inspectionD1ReturnedClose(t, file, injected, &calls, &actual))
		inspectionD1ClosedOnce(t, file, calls, actual)
		if injected == nil {
			if err != nil || names == nil || len(names) != 0 {
				t.Fatalf("EOF not normalized to original usable empty names: %v %v", names, err)
			}
		} else if err != injected || names != nil {
			t.Fatalf("normalized EOF must not hide Close failure: %v %v", names, err)
		}
	}
}

func inspectionD1AssertPrimaryPolicy(t *testing.T, combined, primary error, kind FindingKind, detail string, classification Classification) {
	t.Helper()
	if primaryInspectionError(combined) != primary {
		t.Fatal("exact private pair did not select original primary")
	}
	state := &inspectionState{}
	state.addSourceError(DefaultBudgets(), "file", "READ", combined)
	c := testClassifier(t)
	d := c.finish(state)
	if d.Classification != classification || !hasDetail(d, detail) || len(d.Findings) != 1 || d.Findings[0].Kind != kind {
		t.Fatalf("secondary cause reclassified primary: %+v", d)
	}
}

func TestInspectionD1ErrorPairAndBoundedExtractor(t *testing.T) {
	// A constructed typed stat error exercises aggregation/classification only.
	// It is NOT an observed fstat failure. Full stat branches and second-stat
	// identity refusal are independently bound in the source-control definitions.
	primary := &os.PathError{Op: "stat", Path: "private-stat-diagnostic", Err: errIdentityChanged}
	secondary := &os.PathError{Op: "close", Path: "private-close-diagnostic", Err: os.ErrPermission}
	if withInspectionClose(nil, nil) != nil || withInspectionClose(primary, nil) != primary || withInspectionClose(nil, secondary) != secondary {
		t.Fatal("nil/sole-error identity policy changed")
	}
	combined := withInspectionClose(primary, secondary)
	var first *os.PathError
	if combined.Error() != primary.Error() || !errors.Is(combined, errIdentityChanged) || !errors.Is(combined, os.ErrPermission) || !errors.As(combined, &first) || first != primary {
		t.Fatalf("primary text/type/Is/first As policy changed: %v", combined)
	}
	children := combined.(interface{ Unwrap() []error }).Unwrap()
	if len(children) != 2 || children[0] != primary || children[1] != secondary {
		t.Fatalf("not primary-first two-cause unwrap: %v", children)
	}
	inspectionD1AssertPrimaryPolicy(t, combined, primary, FindingConcurrent, "READ_IDENTITY_CHANGED", Ambiguous)
	other := fmt.Errorf("unrelated outer: %w", combined)
	if primaryInspectionError(other) != other {
		t.Fatal("extractor widened to arbitrary wrappers")
	}
	joined := errors.Join(primary, secondary)
	if primaryInspectionError(joined) != joined {
		t.Fatal("extractor widened to arbitrary multi-error trees")
	}
	// Ordinary permission priority is unchanged, including ordinary wrapped
	// and joined source errors. The exception is ONLY the exact private pair.
	state := &inspectionState{}
	state.addSourceError(DefaultBudgets(), "file", "READ", joined)
	if !state.unsafe || !hasDetail(testClassifier(t).finish(state), "READ_PERMISSION_DENIED") {
		t.Fatal("ordinary permission-first classification changed")
	}
	inspectionD1AssertPrimaryPolicy(t, withInspectionClose(secondary, primary), secondary, FindingFilesystem, "READ_PERMISSION_DENIED", Unsafe)
}

func TestInspectionD1OriginalPrimaryClassificationAndDigest(t *testing.T) {
	for _, cause := range []error{os.ErrPermission, errIdentityChanged, os.ErrNotExist, syscall.EIO, io.EOF} {
		primary := &os.PathError{Op: "stat", Path: "primary", Err: cause}
		for _, secondaryCause := range []error{os.ErrPermission, errIdentityChanged, syscall.EIO} {
			secondary := &os.PathError{Op: "close", Path: "secondary", Err: secondaryCause}
			combined := withInspectionClose(primary, secondary)
			var first *os.PathError
			if combined.Error() != primary.Error() || !errors.Is(combined, cause) || !errors.Is(combined, secondaryCause) || !errors.As(combined, &first) || first != primary {
				t.Fatalf("primary/cause ordering changed: %v", combined)
			}
			original, corrected := &inspectionState{}, &inspectionState{}
			original.addSourceError(DefaultBudgets(), "file", "READ", primary)
			corrected.addSourceError(DefaultBudgets(), "file", "READ", combined)
			c := testClassifier(t)
			if !reflect.DeepEqual(c.finish(original), c.finish(corrected)) {
				t.Fatalf("secondary Close altered primary classification/digest: primary=%v secondary=%v", primary, secondary)
			}
		}
	}
}

// This uses only the existing private inspectionSource seam. It is NOT a real
// filesystem Close-fault experiment or an actual osInspectionSource receipt.
func TestInspectionD1SourceSeamClassifyDecideRefuseSanitized(t *testing.T) {
	for _, site := range []string{"root-directory", "nested-directory", "terminal-directory", "prefix"} {
		for _, policy := range []string{"close-only", "close-only-generic", "identity-primary-close-permission", "read-primary-close-permission"} {
			t.Run(site+"/"+policy, func(t *testing.T) {
				root := t.TempDir()
				if site == "nested-directory" {
					writeFile(t, filepath.Join(root, "docs", "note.md"), []byte("note"))
				}
				if site == "prefix" {
					writeCurrentRoot(t, root, 0x71)
				}
				secondary := &os.PathError{Op: "close", Path: "PRIVATE_SECONDARY_HOST_PATH", Err: os.ErrPermission}
				var sourceErr error = secondary
				wantKind, suffix, wantClass := FindingFilesystem, "PERMISSION_DENIED", Unsafe
				if policy == "close-only-generic" {
					sourceErr = errors.New("PRIVATE_RETURNED_CLOSE_ERROR")
					wantKind, suffix, wantClass = FindingFilesystem, "INSPECTION_FAILED", Ambiguous
				}
				if policy == "identity-primary-close-permission" {
					sourceErr = withInspectionClose(errIdentityChanged, secondary)
					wantKind, suffix, wantClass = FindingConcurrent, "IDENTITY_CHANGED", Ambiguous
				}
				if policy == "read-primary-close-permission" {
					sourceErr = withInspectionClose(&os.PathError{Op: "read", Path: "PRIVATE_PRIMARY_HOST_PATH", Err: syscall.EIO}, secondary)
					wantKind, suffix, wantClass = FindingFilesystem, "INSPECTION_FAILED", Ambiguous
				}
				makeClassifier := func() (*Classifier, *int) {
					injections, rootReads := 0, 0
					source := faultSource{base: osInspectionSource{}}
					source.readDirectory = func(path string, identity fileIdentity, limit int) ([]string, error) {
						if path == root {
							rootReads++
						}
						if site == "root-directory" && path == root || site == "nested-directory" && path == filepath.Join(root, "docs") || site == "terminal-directory" && path == root && rootReads == 2 {
							injections++
							return nil, sourceErr
						}
						return source.base.ReadDirectory(path, identity, limit)
					}
					source.readPrefix = func(path string, identity fileIdentity, maximum int64) ([]byte, error) {
						if site == "prefix" && filepath.Base(path) == gapManifestName {
							injections++
							return nil, sourceErr
						}
						return source.base.ReadPrefix(path, identity, maximum)
					}
					return classifierWithSource(t, source), &injections
				}
				classifier, count := makeClassifier()
				d, err := classifier.Classify(t.Context(), root)
				if err != nil || *count != 1 || d.Classification != wantClass || d.Action != OfflineDispositionRequired || d.Validate() != nil || d.CurrentMutationCount != 0 {
					t.Fatalf("source seam admitted/reclassified root: %+v err=%v injections=%d", d, err, *count)
				}
				operation, relative := "DIRECTORY", ""
				if site == "nested-directory" {
					relative = "docs"
				}
				if site == "terminal-directory" {
					operation = "TERMINAL_DIRECTORY"
				}
				if site == "prefix" {
					operation, relative = "READ", gapManifestName
				}
				found := false
				for _, finding := range d.Findings {
					if finding.Kind == wantKind && finding.RelativePath == relative && finding.DetailCode == operation+"_"+suffix {
						found = true
					}
				}
				if !found {
					t.Fatalf("missing original primary finding: %+v", d.Findings)
				}
				classifier, count = makeClassifier()
				decision, err := classifier.Decide(t.Context(), root)
				refusal, ok := err.(*OfflineDispositionError)
				if !ok || refusal == nil || *count != 1 || !IsOfflineDispositionRequired(err) || !reflect.DeepEqual(decision.Disposition, d) || !reflect.DeepEqual(refusal.Disposition, d) || decision.Action != OfflineDispositionRequired || refusal.Code != OfflineDispositionRequiredCode || err.Error() != OfflineDispositionRequiredCode+": "+d.DiagnosisCode {
					t.Fatalf("original typed sanitized gate changed: decision=%+v err=%v", decision, err)
				}
				var leaked *os.PathError
				if errors.Is(err, secondary) || errors.As(err, &leaked) {
					t.Fatal("private causes leaked through unchanged public refusal")
				}
				encoded, marshalErr := json.Marshal(d)
				if marshalErr != nil || bytes.Contains(encoded, []byte("PRIVATE_")) || bytes.Contains(encoded, []byte(root)) || strings.Contains(err.Error(), "PRIVATE_") {
					t.Fatalf("host diagnostic escaped sanitization: %s %v", encoded, marshalErr)
				}
			})
		}
	}
}
