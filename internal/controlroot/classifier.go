package controlroot

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
)

var (
	errInspectionStopped = errors.New("bounded inspection stopped")
	errIdentityChanged   = errors.New("filesystem identity changed")
)

type fileIdentity struct {
	Mode      fs.FileMode
	Size      int64
	ModTimeNS int64
	Device    uint64
	Inode     uint64
	Links     uint64
	UID       uint32
	GID       uint32
}

type fileObservation struct {
	RelativePath string
	Identity     fileIdentity
	Prefix       []byte
	PrefixSHA256 string
}

type directoryObservation struct {
	RelativePath string
	Identity     fileIdentity
	Names        []string
}

type inspectionSource interface {
	Lstat(string) (fileIdentity, error)
	ReadDirectory(string, fileIdentity, int) ([]string, error)
	ReadPrefix(string, fileIdentity, int64) ([]byte, error)
}

type osInspectionSource struct{}

func (osInspectionSource) Lstat(path string) (fileIdentity, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return fileIdentity{}, err
	}
	return identityFromInfo(info)
}

func (osInspectionSource) ReadDirectory(path string, expected fileIdentity, limit int) ([]string, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return readInspectionDirectoryFile(file, expected, limit, (*os.File).Close)
}

// readInspectionDirectoryFile immediately consumes file. The production caller
// supplies the fixed File.Close method; no closer is retained or selected later.
func readInspectionDirectoryFile(file *os.File, expected fileIdentity, limit int, closeFile func(*os.File) error) (names []string, resultErr error) {
	defer func() {
		resultErr = withInspectionClose(resultErr, closeFile(file))
		if resultErr != nil {
			names = nil
		}
	}()
	before, err := file.Stat()
	if err != nil {
		return nil, err
	}
	actual, err := identityFromInfo(before)
	if err != nil || !sameIdentity(actual, expected) || !actual.Mode.IsDir() {
		return nil, errIdentityChanged
	}
	entries, err := file.ReadDir(limit)
	if err != nil && !errors.Is(err, io.EOF) {
		return nil, err
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	afterIdentity, err := identityFromInfo(after)
	if err != nil || !sameIdentity(afterIdentity, expected) {
		return nil, errIdentityChanged
	}
	names = make([]string, len(entries))
	for index, entry := range entries {
		names[index] = entry.Name()
	}
	slices.Sort(names)
	return names, nil
}

func (osInspectionSource) ReadPrefix(path string, expected fileIdentity, maximum int64) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	return readInspectionPrefixFile(file, expected, maximum, (*os.File).Close)
}

// readInspectionPrefixFile has the same single-consumer contract as the directory
// helper. Close failure invalidates even an otherwise complete or empty prefix.
func readInspectionPrefixFile(file *os.File, expected fileIdentity, maximum int64, closeFile func(*os.File) error) (prefix []byte, resultErr error) {
	defer func() {
		resultErr = withInspectionClose(resultErr, closeFile(file))
		if resultErr != nil {
			prefix = nil
		}
	}()
	before, err := file.Stat()
	if err != nil {
		return nil, err
	}
	actual, err := identityFromInfo(before)
	if err != nil || !sameIdentity(actual, expected) || !actual.Mode.IsRegular() {
		return nil, errIdentityChanged
	}
	readBytes := expected.Size
	if readBytes > maximum {
		readBytes = maximum
	}
	value := make([]byte, readBytes)
	if readBytes > 0 {
		n, readErr := io.ReadFull(file, value)
		if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) {
			return nil, readErr
		}
		if int64(n) != readBytes {
			return nil, errIdentityChanged
		}
	}
	after, err := file.Stat()
	if err != nil {
		return nil, err
	}
	afterIdentity, err := identityFromInfo(after)
	if err != nil || !sameIdentity(afterIdentity, expected) {
		return nil, errIdentityChanged
	}
	return value, nil
}

// inspectionCloseError preserves the original inspection diagnostic and visits
// its cause first for errors.Is/As, while also retaining the actual Close cause.
// Only withInspectionClose constructs this private pair, with both causes nonnil.
// This internal error is sanitized into Findings, not published by Classify.
// Added wrapper/retention costs are OWNED_UNQUALIFIED.
type inspectionCloseError struct {
	primary, closeErr error
}

func (e *inspectionCloseError) Error() string { return e.primary.Error() }
func (e *inspectionCloseError) Unwrap() []error {
	return []error{e.primary, e.closeErr}
}

func withInspectionClose(primary, closeErr error) error {
	if closeErr == nil {
		return primary
	}
	if primary == nil {
		return closeErr
	}
	return &inspectionCloseError{primary: primary, closeErr: closeErr}
}

// primaryInspectionError is deliberately exact-type and one level only. The
// classifier must not let a secondary Close permission error override an earlier
// identity/read/stat finding. It does not alter the retained error's Is/As tree.
func primaryInspectionError(err error) error {
	if combined, ok := err.(*inspectionCloseError); ok {
		return combined.primary
	}
	return err
}

func identityFromInfo(info fs.FileInfo) (fileIdentity, error) {
	stat, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return fileIdentity{}, errors.New("numeric filesystem identity is unavailable")
	}
	return fileIdentity{
		Mode: info.Mode(), Size: info.Size(), ModTimeNS: info.ModTime().UnixNano(),
		Device: uint64(stat.Dev), Inode: uint64(stat.Ino), Links: uint64(stat.Nlink),
		UID: stat.Uid, GID: stat.Gid,
	}, nil
}

func sameIdentity(a, b fileIdentity) bool {
	return a.Mode == b.Mode && a.Size == b.Size && a.ModTimeNS == b.ModTimeNS &&
		a.Device == b.Device && a.Inode == b.Inode && a.Links == b.Links &&
		a.UID == b.UID && a.GID == b.GID
}

// Classifier owns only a read-only inspection capability.
type Classifier struct {
	budgets Budgets
	trust   TrustPolicy
	source  inspectionSource
}

// NewClassifier creates a deterministic pre-sidecar classifier.
func NewClassifier(budgets Budgets, trust TrustPolicy) (*Classifier, error) {
	if err := budgets.validate(); err != nil {
		return nil, err
	}
	return &Classifier{budgets: budgets, trust: trust, source: osInspectionSource{}}, nil
}

type inspectionState struct {
	rootIdentity      *fileIdentity
	rootAbsent        bool
	entries           []fileObservation
	directories       []directoryObservation
	findings          []Finding
	entryCount        int
	readBytes         int64
	unsafe            bool
	ambiguous         bool
	findingsExhausted bool
}

// Classify observes the root without creating it, opening a database, or
// acquiring any runtime resource.
func (c *Classifier) Classify(ctx context.Context, root string) (Disposition, error) {
	if c == nil || c.source == nil {
		return Disposition{}, errors.New("control-root classifier is not configured")
	}
	state := &inspectionState{}
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
		state.unsafe = true
		state.add(c.budgets, Finding{Kind: FindingPath, DetailCode: "ROOT_PATH_NON_CANONICAL"})
		return c.finish(state), nil
	}
	inspectionCtx, cancel := context.WithTimeout(ctx, c.budgets.ElapsedLimit)
	defer cancel()
	if c.contextStopped(inspectionCtx, ctx, state) {
		return c.finish(state), nil
	}
	if c.inspectAncestorChain(root, state) {
		return c.finish(state), nil
	}
	rootIdentity, err := c.source.Lstat(root)
	if errors.Is(err, os.ErrNotExist) {
		state.rootAbsent = true
		c.revalidateAbsentRoot(root, state)
		return c.finish(state), nil
	}
	if err != nil {
		state.addSourceError(c.budgets, "", "ROOT", err)
		return c.finish(state), nil
	}
	state.rootIdentity = &rootIdentity
	if rootIdentity.Mode&os.ModeSymlink != 0 {
		state.unsafe = true
		state.add(c.budgets, Finding{Kind: FindingPath, DetailCode: "ROOT_SYMLINK"})
		return c.finish(state), nil
	}
	if !rootIdentity.Mode.IsDir() {
		state.unsafe = true
		state.add(c.budgets, Finding{Kind: FindingRoot, DetailCode: "ROOT_NOT_DIRECTORY"})
		return c.finish(state), nil
	}
	c.checkTrust(state, "", rootIdentity, true)
	if !state.findingsExhausted {
		_ = c.walk(inspectionCtx, ctx, root, "", rootIdentity, 0, state)
	}
	c.revalidateDirectories(inspectionCtx, ctx, root, state)
	return c.finish(state), nil
}

func (c *Classifier) revalidateAbsentRoot(root string, state *inspectionState) {
	if _, err := c.source.Lstat(root); !errors.Is(err, os.ErrNotExist) {
		state.ambiguous = true
		state.add(c.budgets, Finding{Kind: FindingConcurrent, DetailCode: "ROOT_APPEARED_DURING_INSPECTION"})
	}
}

func (c *Classifier) revalidateDirectories(ctx, caller context.Context, root string, state *inspectionState) {
	for _, observed := range state.directories {
		if c.contextStopped(ctx, caller, state) {
			return
		}
		path := root
		if observed.RelativePath != "" {
			path = filepath.Join(root, filepath.FromSlash(observed.RelativePath))
		}
		identity, err := c.source.Lstat(path)
		if err != nil || !identity.Mode.IsDir() || !sameIdentity(identity, observed.Identity) {
			state.addSourceError(c.budgets, observed.RelativePath, "TERMINAL_DIRECTORY", errIdentityChanged)
			continue
		}
		names, err := c.source.ReadDirectory(path, observed.Identity, len(observed.Names)+1)
		if err != nil {
			state.addSourceError(c.budgets, observed.RelativePath, "TERMINAL_DIRECTORY", err)
			continue
		}
		if !slices.Equal(names, observed.Names) {
			state.ambiguous = true
			state.add(c.budgets, Finding{Kind: FindingConcurrent, RelativePath: observed.RelativePath, DetailCode: "DIRECTORY_MEMBERSHIP_CHANGED"})
		}
	}
}

func (c *Classifier) inspectAncestorChain(root string, state *inspectionState) bool {
	parent := filepath.Dir(root)
	var ancestors []string
	for {
		ancestors = append(ancestors, parent)
		next := filepath.Dir(parent)
		if next == parent {
			break
		}
		parent = next
	}
	slices.Reverse(ancestors)
	for _, ancestor := range ancestors {
		identity, err := c.source.Lstat(ancestor)
		if err != nil {
			state.addSourceError(c.budgets, "", "ROOT_ANCESTOR", err)
			return true
		}
		if identity.Mode&os.ModeSymlink != 0 {
			state.unsafe = true
			state.add(c.budgets, Finding{Kind: FindingPath, DetailCode: "ROOT_ANCESTOR_SYMLINK"})
			return true
		}
	}
	return false
}

// Decide is the production admission gate. Offline classifications are always
// returned as a typed refusal carrying the complete canonical evidence.
func (c *Classifier) Decide(ctx context.Context, root string) (Decision, error) {
	disposition, err := c.Classify(ctx, root)
	if err != nil {
		disposition = internalFailureDisposition()
	}
	decision := Decision{Action: disposition.Action, Disposition: disposition}
	if disposition.Action == OfflineDispositionRequired {
		return decision, &OfflineDispositionError{Code: OfflineDispositionRequiredCode, Disposition: disposition}
	}
	return decision, nil
}

func (c *Classifier) walk(ctx, caller context.Context, root, relative string, directory fileIdentity, depth int, state *inspectionState) error {
	if c.contextStopped(ctx, caller, state) {
		return errInspectionStopped
	}
	if depth > c.budgets.MaxDepth {
		state.ambiguous = true
		state.add(c.budgets, Finding{Kind: FindingBudget, RelativePath: relative, DetailCode: "DEPTH_BUDGET_EXCEEDED"})
		return errInspectionStopped
	}
	remaining := c.budgets.MaxEntries - state.entryCount
	if remaining <= 0 {
		state.ambiguous = true
		state.add(c.budgets, Finding{Kind: FindingBudget, RelativePath: relative, DetailCode: "ENTRY_BUDGET_EXCEEDED"})
		return errInspectionStopped
	}
	directoryPath := root
	if relative != "" {
		directoryPath = filepath.Join(root, filepath.FromSlash(relative))
	}
	names, err := c.source.ReadDirectory(directoryPath, directory, remaining+1)
	if err != nil {
		state.addSourceError(c.budgets, relative, "DIRECTORY", err)
		return errInspectionStopped
	}
	if len(names) > remaining {
		state.ambiguous = true
		state.add(c.budgets, Finding{Kind: FindingBudget, RelativePath: relative, DetailCode: "ENTRY_BUDGET_EXCEEDED"})
		return errInspectionStopped
	}
	state.directories = append(state.directories, directoryObservation{
		RelativePath: relative,
		Identity:     directory,
		Names:        slices.Clone(names),
	})
	for _, name := range names {
		if c.contextStopped(ctx, caller, state) {
			return errInspectionStopped
		}
		childRelative := filepath.ToSlash(filepath.Join(relative, name))
		if !validObservedPath(childRelative, c.budgets.MaxPathBytes) {
			state.unsafe = true
			state.add(c.budgets, Finding{Kind: FindingPath, DetailCode: "ENTRY_PATH_INVALID"})
			return errInspectionStopped
		}
		state.entryCount++
		childPath := filepath.Join(root, filepath.FromSlash(childRelative))
		identity, statErr := c.source.Lstat(childPath)
		if statErr != nil {
			state.addSourceError(c.budgets, childRelative, "ENTRY", statErr)
			continue
		}
		if identity.Mode&os.ModeSymlink != 0 {
			state.entries = append(state.entries, fileObservation{RelativePath: childRelative, Identity: identity})
			state.unsafe = true
			state.add(c.budgets, Finding{Kind: FindingPath, RelativePath: childRelative, DetailCode: "ENTRY_SYMLINK"})
			continue
		}
		observationIndex := len(state.entries)
		state.entries = append(state.entries, fileObservation{RelativePath: childRelative, Identity: identity})
		if identity.Device != directory.Device {
			state.unsafe = true
			state.add(c.budgets, Finding{Kind: FindingFilesystem, RelativePath: childRelative, DetailCode: "DEVICE_BOUNDARY_CROSSED"})
			continue
		}
		c.checkTrust(state, childRelative, identity, false)
		if identity.Mode.IsDir() {
			if err := c.walk(ctx, caller, root, childRelative, identity, depth+1, state); err != nil && state.findingsExhausted {
				return err
			}
			continue
		}
		if !identity.Mode.IsRegular() {
			state.unsafe = true
			state.add(c.budgets, Finding{Kind: FindingFilesystem, RelativePath: childRelative, DetailCode: "ENTRY_SPECIAL_FILE"})
			continue
		}
		if identity.Links != 1 {
			state.unsafe = true
			state.add(c.budgets, Finding{Kind: FindingFilesystem, RelativePath: childRelative, DetailCode: "ENTRY_HARD_LINK"})
			continue
		}
		if allowedUnrelatedPath(childRelative) {
			continue
		}
		if identity.Size < 0 || identity.Size > c.budgets.MaxCandidateFileBytes {
			state.ambiguous = true
			state.add(c.budgets, Finding{Kind: FindingBudget, RelativePath: childRelative, DetailCode: "CANDIDATE_SIZE_UNSUPPORTED"})
			continue
		}
		readSize := identity.Size
		if readSize > c.budgets.MaxSignatureReadBytes {
			readSize = c.budgets.MaxSignatureReadBytes
		}
		if readSize > c.budgets.MaxTotalReadBytes-state.readBytes {
			state.ambiguous = true
			state.add(c.budgets, Finding{Kind: FindingBudget, RelativePath: childRelative, DetailCode: "READ_BUDGET_EXCEEDED"})
			return errInspectionStopped
		}
		prefix, readErr := c.source.ReadPrefix(childPath, identity, c.budgets.MaxSignatureReadBytes)
		if readErr != nil {
			state.addSourceError(c.budgets, childRelative, "READ", readErr)
			continue
		}
		state.readBytes += int64(len(prefix))
		digest := sha256.Sum256(prefix)
		state.entries[observationIndex].Prefix = prefix
		state.entries[observationIndex].PrefixSHA256 = hex.EncodeToString(digest[:])
	}
	return nil
}

func (c *Classifier) checkTrust(state *inspectionState, relative string, identity fileIdentity, root bool) {
	prefix := "ENTRY"
	if root {
		prefix = "ROOT"
	}
	if identity.UID != c.trust.OwnerUID || identity.GID != c.trust.OwnerGID {
		state.unsafe = true
		state.add(c.budgets, Finding{Kind: FindingOwnership, RelativePath: relative, DetailCode: prefix + "_OWNER_MISMATCH"})
	}
	if identity.Mode.Perm()&0o002 != 0 {
		state.unsafe = true
		state.add(c.budgets, Finding{Kind: FindingPermission, RelativePath: relative, DetailCode: prefix + "_WORLD_WRITABLE"})
	}
	if identity.Mode.Perm()&0o020 != 0 && !c.trust.AllowGroupWritable {
		state.unsafe = true
		state.add(c.budgets, Finding{Kind: FindingPermission, RelativePath: relative, DetailCode: prefix + "_GROUP_WRITABLE"})
	}
}

func (c *Classifier) contextStopped(inspection, caller context.Context, state *inspectionState) bool {
	if inspection.Err() == nil {
		return false
	}
	state.ambiguous = true
	code := "INSPECTION_DEADLINE_EXCEEDED"
	if caller.Err() != nil {
		code = "INSPECTION_CANCELLED"
	}
	state.add(c.budgets, Finding{Kind: FindingCancelled, DetailCode: code})
	return true
}

func (state *inspectionState) add(budgets Budgets, finding Finding) {
	if state.findingsExhausted {
		return
	}
	if len(state.findings) >= budgets.MaxFindings {
		state.ambiguous = true
		state.findingsExhausted = true
		state.findings = []Finding{{Kind: FindingBudget, DetailCode: "FINDING_BUDGET_EXCEEDED"}}
		return
	}
	state.findings = append(state.findings, finding)
}

func (state *inspectionState) addSourceError(budgets Budgets, relative, operation string, err error) {
	err = primaryInspectionError(err)
	finding := Finding{Kind: FindingFilesystem, RelativePath: relative, DetailCode: operation + "_INSPECTION_FAILED"}
	switch {
	case errors.Is(err, os.ErrPermission):
		state.unsafe = true
		finding.DetailCode = operation + "_PERMISSION_DENIED"
	case errors.Is(err, errIdentityChanged), errors.Is(err, os.ErrNotExist):
		state.ambiguous = true
		finding.Kind = FindingConcurrent
		finding.DetailCode = operation + "_IDENTITY_CHANGED"
	default:
		state.ambiguous = true
	}
	state.add(budgets, finding)
}

func (c *Classifier) finish(state *inspectionState) Disposition {
	historical, currentGroups, malformedCurrent, uncertain := c.analyze(state)
	classification := EmptySupported
	switch {
	case state.unsafe:
		classification = Unsafe
	case historical && (currentGroups > 0 || malformedCurrent):
		classification = Mixed
	case currentGroups > 1 || (currentGroups > 0 && malformedCurrent):
		classification = Mixed
	case state.ambiguous || uncertain:
		classification = Ambiguous
	case historical:
		classification = Historical
	case malformedCurrent:
		classification = Malformed
	case currentGroups == 1:
		classification = CurrentSupported
	}
	findings, withinBound := canonicalFindings(state.findings)
	if !withinBound {
		classification = Ambiguous
		findings = []Finding{{Kind: FindingBudget, DetailCode: "FINDING_BUDGET_EXCEEDED"}}
	}
	action, diagnosis, _ := dispositionMapping(classification)
	disposition := Disposition{
		SchemaVersion: DispositionSchemaVersion, ControlRootSHA256: digestRoot(state, findings),
		Classification: classification, Findings: findings, CurrentMutationCount: 0,
		Action: action, DiagnosisCode: diagnosis,
	}
	if err := disposition.Validate(); err != nil {
		return internalFailureDisposition()
	}
	return disposition
}

func (c *Classifier) analyze(state *inspectionState) (historical bool, currentGroups int, malformedCurrent bool, uncertain bool) {
	groups := make(map[string]map[string]fileObservation)
	historicalBases := make(map[string]bool)
	knownContainers := make(map[string]bool)
	for _, entry := range state.entries {
		if !entry.Identity.Mode.IsRegular() {
			continue
		}
		base, companion := historicalName(entry.RelativePath)
		if !allowedUnrelatedPath(entry.RelativePath) && !hasSQLiteHeader(entry.Prefix) && !base && !companion && !isCurrentMarkerName(filepath.Base(entry.RelativePath)) {
			continue
		}
		for parent := filepath.ToSlash(filepath.Dir(entry.RelativePath)); parent != "." && parent != ""; parent = filepath.ToSlash(filepath.Dir(parent)) {
			knownContainers[parent] = true
		}
	}
	for _, entry := range state.entries {
		dir, name := filepath.ToSlash(filepath.Dir(entry.RelativePath)), filepath.Base(entry.RelativePath)
		if dir == "." {
			dir = ""
		}
		base, companion := historicalName(entry.RelativePath)
		regular := entry.Identity.Mode.IsRegular()
		if allowedUnrelatedPath(entry.RelativePath) {
			continue
		}
		if !regular {
			if isCurrentMarkerName(name) {
				if groups[dir] == nil {
					groups[dir] = make(map[string]fileObservation)
				}
				groups[dir][name] = entry
				continue
			}
			if base || companion {
				state.unsafe = true
				state.add(c.budgets, Finding{Kind: FindingFilesystem, RelativePath: entry.RelativePath, DetailCode: "HISTORICAL_MARKER_WRONG_TYPE"})
				continue
			}
			if entry.Identity.Mode.IsDir() && !knownContainers[entry.RelativePath] {
				uncertain = true
				state.add(c.budgets, Finding{Kind: FindingUncertain, RelativePath: entry.RelativePath, DetailCode: "UNKNOWN_CONTROL_ROOT_DIRECTORY"})
			}
			continue
		}
		sqlite := hasSQLiteHeader(entry.Prefix)
		if sqlite {
			historical = true
			if base {
				historicalBases[dir] = true
			}
			state.add(c.budgets, Finding{Kind: FindingHistorical, RelativePath: entry.RelativePath, ObservedSHA256: entry.PrefixSHA256, DetailCode: "SQLITE_HEADER_AT_OFFSET_ZERO"})
		} else if base {
			uncertain = true
			state.add(c.budgets, Finding{Kind: FindingUncertain, RelativePath: entry.RelativePath, ObservedSHA256: entry.PrefixSHA256, DetailCode: "HISTORICAL_NAME_WITHOUT_HEADER"})
		}
		if companion {
			if (strings.HasSuffix(name, "-wal") && hasSQLiteWALHeader(entry.Prefix)) || historicalBases[dir] {
				historical = true
				state.add(c.budgets, Finding{Kind: FindingCompanion, RelativePath: entry.RelativePath, ObservedSHA256: entry.PrefixSHA256, DetailCode: "HISTORICAL_COMPANION"})
			} else {
				uncertain = true
				state.add(c.budgets, Finding{Kind: FindingUncertain, RelativePath: entry.RelativePath, ObservedSHA256: entry.PrefixSHA256, DetailCode: "ORPHAN_HISTORICAL_COMPANION"})
			}
		}
		if isCurrentMarkerName(name) {
			if groups[dir] == nil {
				groups[dir] = make(map[string]fileObservation)
			}
			groups[dir][name] = entry
			continue
		}
		if !sqlite && !base && !companion {
			uncertain = true
			state.add(c.budgets, Finding{Kind: FindingUncertain, RelativePath: entry.RelativePath, ObservedSHA256: entry.PrefixSHA256, DetailCode: "UNKNOWN_CONTROL_ROOT_ENTRY"})
		}
	}
	groupNames := make([]string, 0, len(groups))
	for name := range groups {
		groupNames = append(groupNames, name)
	}
	slices.Sort(groupNames)
	for _, name := range groupNames {
		identity, err := coherentCurrentGroup(groups[name])
		path := name
		if path == "" {
			path = gapIdentityName
		}
		if err != nil {
			if errors.Is(err, errCurrentEvidenceIncomplete) {
				uncertain = true
				state.add(c.budgets, Finding{Kind: FindingBudget, RelativePath: path, DetailCode: "CURRENT_LAYOUT_EVIDENCE_INCOMPLETE"})
				continue
			}
			malformedCurrent = true
			state.add(c.budgets, Finding{Kind: FindingMalformed, RelativePath: path, DetailCode: "CURRENT_LAYOUT_INCOMPLETE_OR_INVALID"})
			continue
		}
		currentGroups++
		digest := sha256.Sum256([]byte(identity))
		state.add(c.budgets, Finding{Kind: FindingCurrent, RelativePath: path, ObservedSHA256: hex.EncodeToString(digest[:]), DetailCode: "CURRENT_AUTHORITY_COHERENT"})
	}
	return historical, currentGroups, malformedCurrent, uncertain
}

func digestRoot(state *inspectionState, findings []Finding) string {
	hash := sha256.New()
	if state.rootAbsent {
		_, _ = io.WriteString(hash, "root:absent\n")
	} else if state.rootIdentity != nil {
		writeIdentity(hash, "root", *state.rootIdentity)
	} else {
		_, _ = io.WriteString(hash, "root:unobserved\n")
	}
	entries := slices.Clone(state.entries)
	slices.SortFunc(entries, func(a, b fileObservation) int { return strings.Compare(a.RelativePath, b.RelativePath) })
	for _, entry := range entries {
		writeIdentity(hash, entry.RelativePath, entry.Identity)
		_, _ = io.WriteString(hash, entry.PrefixSHA256+"\n")
	}
	for _, finding := range findings {
		_, _ = io.WriteString(hash, findingKey(finding)+"\n")
	}
	return hex.EncodeToString(hash.Sum(nil))
}

func writeIdentity(writer io.Writer, path string, identity fileIdentity) {
	_, _ = fmt.Fprintf(writer, "%s|%o|%d|%d|%d|%d|%d|%d|%d\n", path, identity.Mode, identity.Size, identity.ModTimeNS, identity.Device, identity.Inode, identity.Links, identity.UID, identity.GID)
}

func internalFailureDisposition() Disposition {
	digest := sha256.Sum256([]byte("controlroot:internal-classification-failure:v1"))
	return Disposition{
		SchemaVersion: DispositionSchemaVersion, ControlRootSHA256: hex.EncodeToString(digest[:]),
		Classification:       Ambiguous,
		Findings:             []Finding{{Kind: FindingUncertain, DetailCode: "INTERNAL_CLASSIFICATION_FAILURE"}},
		CurrentMutationCount: 0, Action: OfflineDispositionRequired,
		DiagnosisCode: "AMBIGUOUS_CONTROL_ROOT",
	}
}

func validObservedPath(path string, maximum int) bool {
	return path != "" && path != "." && len(path) <= maximum && !filepath.IsAbs(path) &&
		filepath.Clean(path) == path && filepath.ToSlash(path) == path &&
		path != ".." && !strings.HasPrefix(path, "../")
}

func allowedUnrelatedPath(path string) bool {
	if path == ".gitignore" || path == ".gitkeep" || path == "README.md" {
		return true
	}
	if !strings.HasPrefix(path, "docs/") {
		return false
	}
	extension := strings.ToLower(filepath.Ext(path))
	return extension == ".md" || extension == ".txt"
}
