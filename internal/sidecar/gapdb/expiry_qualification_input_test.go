//go:build spec_kitty_expiry_host_qualification && linux && amd64

package gapdb

import (
	"archive/zip"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"runtime/debug"
	"sort"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// All tests here are provider-free. No command is started and no cached provider
// image, native receipt, root or real gate is used as a qualification witness.
func TestExpiryInputFixedSpecimenAndTypedCoordinates(t *testing.T) {
	raw := []byte(expiryInputCanonical + "\n")
	if len(raw) != 4691 || expiryRawSHA256(raw) != expiryInputRecordSHA256 {
		t.Fatal("compiled R specimen drift")
	}
	record, err := decodeExpiryInput(raw)
	if err != nil {
		t.Fatal(err)
	}
	tuple, err := expiryCanonical(record.Tuple)
	if err != nil || len(tuple) != 3011 {
		t.Fatalf("tuple bytes=%d err=%v", len(tuple), err)
	}
	if len(record.Tuple.Source.Delta) != 4 || len(record.PriorScopedEvidence) != 11 || len(record.Tuple.Launch.Environment) != 3 {
		t.Fatal("fixed array shape drift")
	}
	if expiryRawSHA256(tuple) == expiryTechnicalTupleSHA256 || expiryInputRecordSHA256 == expiryTechnicalTupleSHA256 || expiryModifiedInventorySHA256 == expiryTechnicalTupleSHA256 {
		t.Fatal("raw and typed coordinates conflated")
	}
	if reflect.TypeOf(ExecutableAdmission{}).NumField() != 7 || reflect.TypeOf(ExecutableProvenance{}).NumField() != 13 {
		t.Fatal("legacy public/private value shapes changed")
	}
	if reflect.TypeOf(VerifiedExecutable{}).Field(0).Name != "expiry" {
		t.Fatal("conditional state must be first")
	}
}

func expiryTestCanonical(t *testing.T, value any) []byte {
	t.Helper()
	raw, err := expiryCanonical(value)
	if err != nil {
		t.Fatal(err)
	}
	return append(raw, '\n')
}

func expiryTestRefuseInput(t *testing.T, raw []byte) {
	t.Helper()
	if record, err := decodeExpiryInput(raw); err == nil || !reflect.DeepEqual(record, expiryInputRecord{}) {
		t.Fatal("mutated input did not return zero record and refusal")
	}
}

// Every required object key and every scalar/array is mutated independently.
// Exact byte matching complements the unique canonical decoder, not vice versa.
func TestExpiryInputClosedAtEveryDepth(t *testing.T) {
	decoder := json.NewDecoder(strings.NewReader(expiryInputCanonical))
	decoder.UseNumber()
	var root map[string]any
	if err := decoder.Decode(&root); err != nil {
		t.Fatal(err)
	}
	var walk func(any, string)
	walk = func(value any, location string) {
		switch node := value.(type) {
		case map[string]any:
			keys := make([]string, 0, len(node))
			for key := range node {
				keys = append(keys, key)
			}
			sort.Strings(keys)
			t.Run(location+"/unknown", func(t *testing.T) {
				node["unknown_member"] = false
				expiryTestRefuseInput(t, expiryTestCanonical(t, root))
				delete(node, "unknown_member")
			})
			original, err := json.Marshal(node)
			if err != nil {
				t.Fatal(err)
			}
			for _, key := range keys {
				old := node[key]
				t.Run(location+"/missing_"+key, func(t *testing.T) {
					delete(node, key)
					expiryTestRefuseInput(t, expiryTestCanonical(t, root))
					node[key] = old
				})
				t.Run(location+"/alias_"+key, func(t *testing.T) {
					delete(node, key)
					node[strings.ToUpper(key)] = old
					expiryTestRefuseInput(t, expiryTestCanonical(t, root))
					delete(node, strings.ToUpper(key))
					node[key] = old
				})
				t.Run(location+"/null_"+key, func(t *testing.T) {
					node[key] = nil
					expiryTestRefuseInput(t, expiryTestCanonical(t, root))
					node[key] = old
				})
				t.Run(location+"/duplicate_"+key, func(t *testing.T) {
					keyBytes, _ := json.Marshal(key)
					valueBytes, _ := json.Marshal(old)
					duplicate := append([]byte{'{'}, append(append(keyBytes, ':'), valueBytes...)...)
					duplicate = append(duplicate, ',')
					duplicate = append(duplicate, original[1:]...)
					// encoding/json sorts every object, so the original subtree is
					// located exactly even when nested inside a fixed ordered array.
					whole, _ := json.Marshal(root)
					mutated := bytes.Replace(whole, original, duplicate, 1)
					if bytes.Equal(whole, mutated) {
						t.Fatal("duplicate mutation did not touch subtree")
					}
					expiryTestRefuseInput(t, append(mutated, '\n'))
				})
				switch scalar := old.(type) {
				case string:
					node[key] = scalar + "-changed"
				case bool:
					node[key] = !scalar
				case json.Number:
					node[key] = json.Number("18446744073709551616")
				}
				if _, object := old.(map[string]any); !object {
					if _, array := old.([]any); !array {
						expiryTestRefuseInput(t, expiryTestCanonical(t, root))
					}
				}
				node[key] = old
				if _, number := old.(json.Number); number {
					for _, invalid := range []json.Number{"-1", "1.0", "1e0"} {
						node[key] = invalid
						expiryTestRefuseInput(t, expiryTestCanonical(t, root))
					}
					node[key] = old
				}
				walk(old, location+"/"+key)
			}
		case []any:
			// Mutate lengths/order through the owning reference without adding a
			// production selector. In-place replacement exercises each element.
			if len(node) > 1 {
				node[0], node[1] = node[1], node[0]
				expiryTestRefuseInput(t, expiryTestCanonical(t, root))
				node[0], node[1] = node[1], node[0]
			}
			for index, item := range node {
				node[index] = nil
				expiryTestRefuseInput(t, expiryTestCanonical(t, root))
				node[index] = item
				walk(item, fmt.Sprintf("%s/%d", location, index))
			}
		}
	}
	walk(root, "input")
	if _, err := decodeExpiryInput(expiryTestCanonical(t, root)); err != nil {
		t.Fatalf("mutation did not restore fixture: %v", err)
	}
}

func TestExpiryInputEncodingAndArrayRefusals(t *testing.T) {
	valid := []byte(expiryInputCanonical + "\n")
	for name, raw := range map[string][]byte{
		"missingLF": valid[:len(valid)-1], "doubleLF": append(append([]byte(nil), valid...), '\n'),
		"trailingValue": append(append([]byte(nil), valid...), []byte("{}")...),
		"whitespace":    append([]byte(" "), valid...), "oversize": bytes.Repeat([]byte(" "), 65537),
		"empty": nil, "null": []byte("null\n"),
		"escapedAlias":     bytes.Replace(valid, []byte(`"adopted"`), []byte(`"\u0061dopted"`), 1),
		"swapRT":           bytes.Replace(valid, []byte(expiryTechnicalTupleSHA256), []byte(expiryInputRecordSHA256), 1),
		"environmentShort": bytes.Replace(valid, []byte(`,"SPEC_KITTY_EXPIRY_GATE_FD=3"`), nil, 1),
		"environmentLong":  bytes.Replace(valid, []byte(`"LANG=C","TZ=UTC"`), []byte(`"LANG=C","LANG=C","TZ=UTC"`), 1),
	} {
		t.Run(name, func(t *testing.T) { expiryTestRefuseInput(t, raw) })
	}
	for _, array := range []string{"prior_scoped_evidence", "delta", "environment"} {
		var root map[string]any
		if json.Unmarshal(valid, &root) != nil {
			t.Fatal("fixture")
		}
		var owner map[string]any
		switch array {
		case "prior_scoped_evidence":
			owner = root
		case "delta":
			owner = root["tuple"].(map[string]any)["source"].(map[string]any)
		case "environment":
			owner = root["tuple"].(map[string]any)["launch"].(map[string]any)
		}
		original := owner[array].([]any)
		owner[array] = original[:len(original)-1]
		expiryTestRefuseInput(t, expiryTestCanonical(t, root))
		owner[array] = append(append([]any(nil), original...), original[0])
		expiryTestRefuseInput(t, expiryTestCanonical(t, root))
	}
}

func TestExpiryInputClientMetadataRefusesReplacementAndDrift(t *testing.T) {
	good := debug.Module{Path: expiryClientModule, Version: expiryClientVersion, Sum: expiryClientModuleH1}
	if err := verifyExpiryClient(&debug.BuildInfo{Deps: []*debug.Module{&good}}); err != nil {
		t.Fatal(err)
	}
	for _, mutate := range []func(*debug.Module){
		func(m *debug.Module) { m.Path += "/other" }, func(m *debug.Module) { m.Version += ".other" },
		func(m *debug.Module) { m.Sum = expiryClientGoModH1 }, func(m *debug.Module) { m.Replace = &good },
	} {
		changed := good
		mutate(&changed)
		if verifyExpiryClient(&debug.BuildInfo{Deps: []*debug.Module{&changed}}) == nil {
			t.Fatal("client drift accepted")
		}
	}
	if verifyExpiryClient(nil) == nil || verifyExpiryClient(&debug.BuildInfo{}) == nil || verifyExpiryClient(&debug.BuildInfo{Deps: []*debug.Module{&good, &good}}) == nil {
		t.Fatal("missing/duplicate client accepted")
	}
}

func TestExpiryInputArtifactsBoundedNoFollowMetadata(t *testing.T) {
	directory := t.TempDir()
	filename := filepath.Join(directory, "fixed-input")
	raw := []byte(expiryInputCanonical + "\n")
	if err := os.WriteFile(filename, raw, 0o600); err != nil {
		t.Fatal(err)
	}
	report := new(expiryCleanupReport)
	got, err := readExpiryArtifact(filename, uint64(len(raw)), expiryRawSHA256(raw), report)
	if err != nil || !bytes.Equal(raw, got) || report.cleanupError() != nil {
		t.Fatalf("held read err=%v cleanup=%v", err, report.cleanupError())
	}
	for name, change := range map[string]func(string){
		"symlink": func(target string) {
			if err := os.Symlink(filename, target); err != nil {
				t.Fatal(err)
			}
		},
		"hardlink": func(target string) {
			if err := os.Link(filename, target); err != nil {
				t.Fatal(err)
			}
		},
		"directory": func(target string) {
			if err := os.Mkdir(target, 0o700); err != nil {
				t.Fatal(err)
			}
		},
		"fifo": func(target string) {
			if err := unix.Mkfifo(target, 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"writable": func(target string) {
			if err := os.WriteFile(target, raw, 0o622); err != nil {
				t.Fatal(err)
			}
			if err := os.Chmod(target, 0o622); err != nil {
				t.Fatal(err)
			}
		},
		"short": func(target string) {
			if err := os.WriteFile(target, raw[:len(raw)-1], 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"long": func(target string) {
			if err := os.WriteFile(target, append(append([]byte(nil), raw...), 0), 0o600); err != nil {
				t.Fatal(err)
			}
		},
		"digest": func(target string) {
			altered := append([]byte(nil), raw...)
			altered[0] ^= 1
			if err := os.WriteFile(target, altered, 0o600); err != nil {
				t.Fatal(err)
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			target := filepath.Join(directory, name)
			change(target)
			if _, err := readExpiryArtifact(target, uint64(len(raw)), expiryRawSHA256(raw), new(expiryCleanupReport)); err == nil {
				t.Fatal("unsafe or changed artifact accepted")
			}
			if err := os.Remove(target); err != nil {
				t.Fatal(err)
			}
		})
	}
	if _, err := readExpiryArtifact(filename, 2<<20+1, expiryRawSHA256(raw), nil); err == nil {
		t.Fatal("oversize artifact accepted")
	}
	if _, err := readExpiryArtifact(filename, uint64(len(raw)), strings.ToUpper(expiryRawSHA256(raw)), nil); err == nil {
		t.Fatal("non-lowercase digest accepted")
	}
	parentLink := filepath.Join(directory, "ancestor-link")
	if err := os.Symlink(directory, parentLink); err != nil {
		t.Fatal(err)
	}
	if _, err := readExpiryArtifact(filepath.Join(parentLink, "fixed-input"), uint64(len(raw)), expiryRawSHA256(raw), nil); err == nil {
		t.Fatal("symlink ancestor accepted")
	}
}

func TestExpiryInputInventoriesAreClosedCanonicalMaps(t *testing.T) {
	good := map[string]string{"go.mod": strings.Repeat("a", 64), "internal/engine/expiry.go": strings.Repeat("b", 64)}
	if _, err := decodeExpiryInventory(expiryTestCanonical(t, good), 2); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"", "/absolute", "../parent", "a/../b", "a//b", "a/./b", "a\\b", "a\x00b", "a\nb", "a/"} {
		if expirySafeRelative(bad) {
			t.Fatalf("unsafe relative path %q accepted", bad)
		}
	}
	for _, bad := range []string{"ABC", strings.Repeat("A", 64), strings.Repeat("0", 63), strings.Repeat("g", 64)} {
		if expiryLowerSHA256(bad) {
			t.Fatal("malformed hash accepted")
		}
	}
	for name, raw := range map[string][]byte{
		"duplicate": []byte(`{"go.mod":"` + strings.Repeat("a", 64) + `","go.mod":"` + strings.Repeat("a", 64) + `"}` + "\n"),
		"nullValue": []byte("{\"go.mod\":null}\n"), "nullMap": []byte("null\n"),
		"unsafe":    expiryTestCanonical(t, map[string]string{"../bad": strings.Repeat("a", 64)}),
		"upper":     expiryTestCanonical(t, map[string]string{"go.mod": strings.Repeat("A", 64)}),
		"missingLF": []byte(`{"go.mod":"` + strings.Repeat("a", 64) + `"}`),
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := decodeExpiryInventory(raw, 1); err == nil {
				t.Fatal("bad inventory accepted")
			}
		})
	}
	if _, err := decodeExpiryInventory(expiryTestCanonical(t, good), 403); err == nil {
		t.Fatal("entry count accepted")
	}
}

type expiryTestMember struct {
	name string
	data []byte
	mode os.FileMode
}

func expiryTestZIP(t *testing.T, members []expiryTestMember) ([]byte, string, string) {
	t.Helper()
	var raw bytes.Buffer
	writer := zip.NewWriter(&raw)
	known := make(map[string][]byte)
	for _, member := range members {
		header := &zip.FileHeader{Name: member.name, Method: zip.Deflate}
		header.SetMode(member.mode)
		file, err := writer.CreateHeader(header)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := file.Write(member.data); err != nil {
			t.Fatal(err)
		}
		known[member.name] = member.data
	}
	if err := writer.Close(); err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(known))
	for name := range known {
		names = append(names, name)
	}
	sort.Strings(names)
	var lines strings.Builder
	for _, name := range names {
		fmt.Fprintf(&lines, "%s  %s\n", expiryRawSHA256(known[name]), name)
	}
	hash := sha256.Sum256([]byte(lines.String()))
	prefix := expiryClientModule + "@" + expiryClientVersion + "/"
	modHash := sha256.Sum256([]byte(expiryRawSHA256(known[prefix+"go.mod"]) + "  go.mod\n"))
	return raw.Bytes(), "h1:" + base64.StdEncoding.EncodeToString(hash[:]), "h1:" + base64.StdEncoding.EncodeToString(modHash[:])
}

func expiryTestZIPMembers() []expiryTestMember {
	prefix := expiryClientModule + "@" + expiryClientVersion + "/"
	members := []expiryTestMember{{prefix + "go.mod", []byte("module test\n"), 0o644}}
	for i := 1; i < 400; i++ {
		members = append(members, expiryTestMember{prefix + fmt.Sprintf("file-%03d", i), []byte(fmt.Sprintf("source %d\n", i)), 0o644})
	}
	return members
}

func TestExpiryInputZIPBoundsPathsTypesHashZipAndGoMod(t *testing.T) {
	members := expiryTestZIPMembers()
	raw, h1, modH1 := expiryTestZIP(t, members)
	inventory, err := authenticateExpiryZIP(raw, expiryClientModule, expiryClientVersion, h1, modH1)
	if err != nil || len(inventory) != 400 {
		t.Fatalf("bounded ZIP err=%v", err)
	}
	for name, mutate := range map[string]func([]expiryTestMember) []expiryTestMember{
		"duplicate": func(m []expiryTestMember) []expiryTestMember { m[399] = m[1]; return m },
		"prefix":    func(m []expiryTestMember) []expiryTestMember { m[1].name = "other@version/file"; return m },
		"unsafe": func(m []expiryTestMember) []expiryTestMember {
			m[1].name = expiryClientModule + "@" + expiryClientVersion + "/../escape"
			return m
		},
		"symlink": func(m []expiryTestMember) []expiryTestMember { m[1].mode = os.ModeSymlink | 0o777; return m },
		"directory": func(m []expiryTestMember) []expiryTestMember {
			m[1].mode = os.ModeDir | 0o755
			m[1].name += "/"
			m[1].data = nil
			return m
		},
		"missing":      func(m []expiryTestMember) []expiryTestMember { return m[:399] },
		"extra":        func(m []expiryTestMember) []expiryTestMember { return append(m, m[0]) },
		"missingGoMod": func(m []expiryTestMember) []expiryTestMember { m[0].name += ".not-go-mod"; return m },
	} {
		t.Run(name, func(t *testing.T) {
			changed := mutate(append([]expiryTestMember(nil), members...))
			bad, badH1, badMod := expiryTestZIP(t, changed)
			if _, err := authenticateExpiryZIP(bad, expiryClientModule, expiryClientVersion, badH1, badMod); err == nil {
				t.Fatal("invalid ZIP accepted")
			}
		})
	}
	if _, err := authenticateExpiryZIP(raw, expiryClientModule, expiryClientVersion, "h1:wrong", modH1); err == nil {
		t.Fatal("wrong HashZip accepted")
	}
	if _, err := authenticateExpiryZIP(raw, expiryClientModule, expiryClientVersion, h1, "h1:wrong"); err == nil {
		t.Fatal("wrong go.mod h1 accepted")
	}
	// Patch only the central-directory declared uncompressed length. The parser
	// must reject it before reading/decompressing that member (no 32MiB fixture).
	oversize := append([]byte(nil), raw...)
	central := bytes.Index(oversize, []byte{'P', 'K', 1, 2})
	if central < 0 {
		t.Fatal("central header absent")
	}
	binary.LittleEndian.PutUint32(oversize[central+24:central+28], uint32(expiryZIPUncompressedMaximum+1))
	if _, err := authenticateExpiryZIP(oversize, expiryClientModule, expiryClientVersion, h1, modH1); err == nil {
		t.Fatal("uncompressed size bound bypassed")
	}
	corrupt := append([]byte(nil), raw...)
	nameBytes := int(binary.LittleEndian.Uint16(corrupt[26:28]))
	extraBytes := int(binary.LittleEndian.Uint16(corrupt[28:30]))
	corrupt[30+nameBytes+extraBytes] ^= 0xff
	if _, err := authenticateExpiryZIP(corrupt, expiryClientModule, expiryClientVersion, h1, modH1); err == nil {
		t.Fatal("corrupt ZIP accepted")
	}
}

func TestExpiryInputExactOneReplaceThreeAddRelation(t *testing.T) {
	base := make(map[string]string, 400)
	for i := 0; i < 399; i++ {
		base[fmt.Sprintf("base-%03d", i)] = strings.Repeat("a", 64)
	}
	before := []byte("package engine\n\tsort.Slice(due, unchanged)\n")
	base["internal/engine/expiry.go"] = expiryRawSHA256(before)
	modified := make(map[string]string, 403)
	for name, digest := range base {
		modified[name] = digest
	}
	deltas := []expiryDeltaRef{
		{Path: "internal/engine/expiry.go", Operation: "replace", BeforeSHA256: expiryRawSHA256(before), After: expiryBlobRef{Name: "delta/expiry.go"}},
		{Path: "internal/engine/spk_expiry_gate.go", Operation: "add", After: expiryBlobRef{Name: "delta/gate.go"}},
		{Path: "internal/engine/spk_expiry_gate_disabled.go", Operation: "add", After: expiryBlobRef{Name: "delta/gate_disabled.go"}},
		{Path: "internal/engine/spk_expiry_gate_test.go", Operation: "add", After: expiryBlobRef{Name: "delta/gate_test.go"}},
	}
	artifacts := make(map[string][]byte, 4)
	for i := range deltas {
		raw := []byte("added source " + deltas[i].Path)
		if i == 0 {
			raw = bytes.Replace(before, []byte("\tsort.Slice"), []byte("\tspkExpiryEvidenceHold() // isolated qualification only\n\tsort.Slice"), 1)
		}
		deltas[i].After.Bytes = uint64(len(raw))
		deltas[i].After.SHA256 = expiryRawSHA256(raw)
		artifacts[deltas[i].After.Name] = raw
		modified[deltas[i].Path] = deltas[i].After.SHA256
	}
	if err := authenticateExpiryDelta(base, modified, artifacts, deltas); err != nil {
		t.Fatal(err)
	}
	old := modified["base-000"]
	modified["base-000"] = strings.Repeat("b", 64)
	if authenticateExpiryDelta(base, modified, artifacts, deltas) == nil {
		t.Fatal("unlisted replacement accepted")
	}
	modified["base-000"] = old
	delete(modified, "base-000")
	modified["unlisted"] = old
	if authenticateExpiryDelta(base, modified, artifacts, deltas) == nil {
		t.Fatal("delete/add accepted")
	}
	delete(modified, "unlisted")
	modified["base-000"] = old
	for _, invalid := range []string{"add", "delete", "Replace"} {
		changed := append([]expiryDeltaRef(nil), deltas...)
		changed[0].Operation = invalid
		if authenticateExpiryDelta(base, modified, artifacts, changed) == nil {
			t.Fatal("bad operation accepted")
		}
	}
	changed := append([]expiryDeltaRef(nil), deltas...)
	changed[1].BeforeSHA256 = strings.Repeat("a", 64)
	if authenticateExpiryDelta(base, modified, artifacts, changed) == nil {
		t.Fatal("addition before hash accepted")
	}
	raw := artifacts["delta/expiry.go"]
	artifacts["delta/expiry.go"] = append(append([]byte(nil), raw...), []byte("\tspkExpiryEvidenceHold() // isolated qualification only\n")...)
	if authenticateExpiryDelta(base, modified, artifacts, deltas) == nil {
		t.Fatal("second hold accepted")
	}
}

func TestExpiryInputOrdinaryVerifierStillRejectsNewRecord(t *testing.T) {
	directory := t.TempDir()
	image := filepath.Join(directory, "not-executed")
	record := filepath.Join(directory, "input.json")
	if err := os.WriteFile(image, []byte("not a provider"), 0o500); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(record, []byte(expiryInputCanonical+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if verified, err := VerifyExecutable(VerifyConfig{ExecutablePath: image, AdmissionRecordPath: record}); verified != nil || !IsFailure(err, FailureAdmissionMismatch) {
		t.Fatalf("ordinary verifier accepted expiry input: %v", err)
	}
	// The first missing detached artifact must refuse before executable open or
	// any command/process operation; a caller admission cannot fill that gap.
	if verified, err := verifyExpiryQualificationExecutable(VerifyConfig{ExecutablePath: image, AdmissionRecordPath: record, Admission: historicalM1ExecutableAdmission()}); verified != nil || !IsFailure(err, FailureAdmissionMismatch) {
		t.Fatalf("fixed verifier ignored missing artifacts: %v", err)
	}
}

func TestExpiryInputCommonImageFactsRecheckAndFirstCloseReports(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "never-executed")
	raw := []byte("provider-free sealed-image unit fixture\n")
	if err := os.WriteFile(filename, raw, 0o500); err != nil {
		t.Fatal(err)
	}
	report := new(expiryCleanupReport)
	facts := ExecutableProvenance{ExecutablePath: "false", SHA256: "false", Size: 999, Device: 999, Inode: 999, OwnerUID: 999, OwnerGID: 999, Mode: 999, ContractRevision: expiryInputContractRevision}
	verified, err := verifyExecutableImage(filename, expiryRawSHA256(raw), int64(len(raw)), facts, report)
	if err != nil {
		t.Fatal(err)
	}
	verified.expiry.report = report
	p := verified.Provenance()
	if p.ExecutablePath != filename || p.SHA256 != expiryRawSHA256(raw) || p.Size != int64(len(raw)) || p.Inode == 999 || p.SourceRevision != "" || p.SourceTree != "" || p.ModuleVersion != "" || p.AdmissionRecordSHA256 != "" {
		t.Fatalf("untruthful image coordinates: %+v", p)
	}
	if err := verified.checkCommandImage(); err != nil {
		t.Fatal(err)
	}
	command, err := verified.command([]string{"unit-only"}, []string{"LANG=C", "TZ=UTC"})
	if err != nil || command.Path != "/proc/self/fd/3" || len(command.ExtraFiles) != 1 {
		t.Fatalf("unmarked command mapping changed: %v", err)
	}
	if _, err := verified.execution.WriteAt([]byte{1}, 0); err == nil {
		t.Fatal("sealed image is writable")
	}
	if err := verified.Close(); err != nil {
		t.Fatal(err)
	}
	if report.cleanupError() != nil || verified.Close() != nil {
		t.Fatal("successful first close not recorded")
	}
	if err := verified.checkCommandImage(); !IsFailure(err, FailureIdentityUnprovable) {
		t.Fatal("closed image accepted")
	}
	for _, category := range []string{"source", "image"} {
		t.Run(category, func(t *testing.T) {
			r := new(expiryCleanupReport)
			v, err := verifyExecutableImage(filename, expiryRawSHA256(raw), int64(len(raw)), ExecutableProvenance{}, r)
			if err != nil {
				t.Fatal(err)
			}
			v.expiry.report = r
			if category == "source" {
				if err := v.source.Close(); err != nil {
					t.Fatal(err)
				}
			} else {
				if err := v.execution.Close(); err != nil {
					t.Fatal(err)
				}
			}
			// Safe closed File object, not a raw-FD close/reuse/reclose. This is
			// returned-error custody, not physical failed-FIRST syscall evidence.
			first := v.Close()
			if first == nil || r.cleanupError() == nil {
				t.Fatal("actual first owned close error lost")
			}
			if v.Close() != first || r.cleanupError() == nil {
				t.Fatal("repeat replaced sticky FIRST close failure")
			}
		})
	}
}

// Compare a sealed descriptor without starting it or invoking a provider.
func TestExpiryInputSealedDigestIsActualHeldBytes(t *testing.T) {
	filename := filepath.Join(t.TempDir(), "held")
	raw := []byte("immutable held bytes")
	if err := os.WriteFile(filename, raw, 0o700); err != nil {
		t.Fatal(err)
	}
	v, err := verifyExecutableImage(filename, expiryRawSHA256(raw), int64(len(raw)), ExecutableProvenance{}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer v.Close()
	got, err := io.ReadAll(io.NewSectionReader(v.execution, 0, int64(len(raw))))
	if err != nil || expiryRawSHA256(got) != v.executed.SHA256 || !bytes.Equal(got, raw) {
		t.Fatalf("held bytes err=%v", err)
	}
	if err := os.WriteFile(filename, bytes.Repeat([]byte{'X'}, len(raw)), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := v.checkCommandImage(); err != nil {
		t.Fatal("source mutation changed sealed image", err)
	}
}
