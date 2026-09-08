// Package controlroot classifies a Spec Kitty control root before the
// production runtime is allowed to start or mutate current authority.
package controlroot

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

const (
	DispositionSchemaVersion       = 1
	MaxContractFindings            = 256
	MaxContractRelativePathBytes   = 512
	OfflineDispositionRequiredCode = "OFFLINE_DISPOSITION_REQUIRED"
)

// Classification is the closed historical-state-disposition-v1 vocabulary.
type Classification string

const (
	EmptySupported   Classification = "empty_supported"
	CurrentSupported Classification = "current_supported"
	Historical       Classification = "historical"
	Mixed            Classification = "mixed"
	Unsafe           Classification = "unsafe"
	Malformed        Classification = "malformed"
	Ambiguous        Classification = "ambiguous"
)

// Action is the only continuation a caller may derive from a disposition.
type Action string

const (
	Initialize                 Action = "initialize"
	Recover                    Action = "recover"
	OfflineDispositionRequired Action = "offline_disposition_required"
)

// FindingKind is closed so callers cannot manufacture new machine semantics.
type FindingKind string

const (
	FindingRoot       FindingKind = "root"
	FindingPath       FindingKind = "path"
	FindingOwnership  FindingKind = "ownership"
	FindingPermission FindingKind = "permission"
	FindingFilesystem FindingKind = "filesystem"
	FindingHistorical FindingKind = "historical"
	FindingCompanion  FindingKind = "historical.companion"
	FindingCurrent    FindingKind = "current"
	FindingMalformed  FindingKind = "current.malformed"
	FindingUncertain  FindingKind = "uncertain"
	FindingBudget     FindingKind = "budget"
	FindingCancelled  FindingKind = "cancelled"
	FindingConcurrent FindingKind = "concurrent-change"
)

var findingKinds = []FindingKind{
	FindingRoot, FindingPath, FindingOwnership, FindingPermission,
	FindingFilesystem, FindingHistorical, FindingCompanion, FindingCurrent,
	FindingMalformed, FindingUncertain, FindingBudget, FindingCancelled,
	FindingConcurrent,
}

// Finding is stable, bounded machine evidence. It deliberately carries no
// ambient error prose or absolute host path.
type Finding struct {
	Kind           FindingKind `json:"kind"`
	RelativePath   string      `json:"relative_path,omitempty"`
	ObservedSHA256 string      `json:"observed_sha256,omitempty"`
	DetailCode     string      `json:"detail_code"`
}

// Disposition exactly mirrors historical-state-disposition-v1.schema.json.
type Disposition struct {
	SchemaVersion        int            `json:"schema_version"`
	ControlRootSHA256    string         `json:"control_root_sha256"`
	Classification       Classification `json:"classification"`
	Findings             []Finding      `json:"findings"`
	CurrentMutationCount int            `json:"current_mutation_count"`
	Action               Action         `json:"action"`
	DiagnosisCode        string         `json:"diagnosis_code"`
}

// Decision is the narrow pre-sidecar gate result consumed by the production
// host. It performs neither Initialize nor Recover itself.
type Decision struct {
	Action      Action
	Disposition Disposition
}

// OfflineDispositionError is the typed refusal for every non-admissible root.
type OfflineDispositionError struct {
	Code        string
	Disposition Disposition
}

func (e *OfflineDispositionError) Error() string {
	if e == nil {
		return OfflineDispositionRequiredCode
	}
	return OfflineDispositionRequiredCode + ": " + e.Disposition.DiagnosisCode
}

// IsOfflineDispositionRequired recognizes the stable typed refusal.
func IsOfflineDispositionRequired(err error) bool {
	var target *OfflineDispositionError
	return errors.As(err, &target) && target.Code == OfflineDispositionRequiredCode
}

// Budgets bound every externally controlled inspection dimension.
type Budgets struct {
	MaxEntries            int
	MaxDepth              int
	MaxFindings           int
	MaxPathBytes          int
	MaxSignatureReadBytes int64
	MaxTotalReadBytes     int64
	MaxCandidateFileBytes int64
	ElapsedLimit          time.Duration
}

// DefaultBudgets are deliberately smaller than provider limits; the
// classifier reads only headers and bounded evidence, never authority data.
func DefaultBudgets() Budgets {
	return Budgets{
		MaxEntries:            4096,
		MaxDepth:              16,
		MaxFindings:           MaxContractFindings,
		MaxPathBytes:          MaxContractRelativePathBytes,
		MaxSignatureReadBytes: 8192,
		MaxTotalReadBytes:     4 << 20,
		MaxCandidateFileBytes: 1 << 30,
		ElapsedLimit:          2 * time.Second,
	}
}

func (b Budgets) validate() error {
	if b.MaxEntries <= 0 || b.MaxDepth <= 0 || b.MaxFindings <= 0 ||
		b.MaxFindings > MaxContractFindings || b.MaxPathBytes <= 0 ||
		b.MaxPathBytes > MaxContractRelativePathBytes ||
		b.MaxSignatureReadBytes < 16 || b.MaxSignatureReadBytes > 1<<20 ||
		b.MaxTotalReadBytes < b.MaxSignatureReadBytes ||
		b.MaxCandidateFileBytes < b.MaxSignatureReadBytes ||
		b.ElapsedLimit <= 0 {
		return errors.New("control-root inspection budgets are invalid")
	}
	return nil
}

// TrustPolicy is explicit numeric ownership authority. It never resolves an
// ambient username or infers trust from process display names.
type TrustPolicy struct {
	OwnerUID           uint32
	OwnerGID           uint32
	AllowGroupWritable bool
}

// Validate enforces both the JSON Schema and the stronger closed product
// invariants relied on by startup.
func (d Disposition) Validate() error {
	if d.SchemaVersion != DispositionSchemaVersion {
		return fmt.Errorf("disposition schema_version=%d, want %d", d.SchemaVersion, DispositionSchemaVersion)
	}
	if !isLowerHexDigest(d.ControlRootSHA256) {
		return errors.New("control_root_sha256 must be 64 lowercase hexadecimal characters")
	}
	wantAction, wantDiagnosis, ok := dispositionMapping(d.Classification)
	if !ok {
		return fmt.Errorf("unknown classification %q", d.Classification)
	}
	if d.Action != wantAction {
		return fmt.Errorf("classification %s maps to %s, not %s", d.Classification, wantAction, d.Action)
	}
	if d.DiagnosisCode != wantDiagnosis || !isStableCode(d.DiagnosisCode) {
		return fmt.Errorf("classification %s diagnosis=%q, want %q", d.Classification, d.DiagnosisCode, wantDiagnosis)
	}
	if d.CurrentMutationCount != 0 {
		return fmt.Errorf("current_mutation_count=%d, want zero", d.CurrentMutationCount)
	}
	if len(d.Findings) > MaxContractFindings {
		return fmt.Errorf("findings=%d exceeds %d", len(d.Findings), MaxContractFindings)
	}
	if d.Action == OfflineDispositionRequired && len(d.Findings) == 0 {
		return errors.New("offline disposition requires evidence")
	}
	seen := make(map[string]bool, len(d.Findings))
	previous := ""
	for index, finding := range d.Findings {
		if !slices.Contains(findingKinds, finding.Kind) {
			return fmt.Errorf("finding[%d] has unknown kind %q", index, finding.Kind)
		}
		if !isStableCode(finding.DetailCode) {
			return fmt.Errorf("finding[%d] detail_code=%q is invalid", index, finding.DetailCode)
		}
		if finding.RelativePath != "" {
			if len(finding.RelativePath) > MaxContractRelativePathBytes || filepath.IsAbs(finding.RelativePath) || filepath.Clean(finding.RelativePath) != finding.RelativePath || finding.RelativePath == "." || finding.RelativePath == ".." || strings.HasPrefix(finding.RelativePath, ".."+string(filepath.Separator)) || filepath.ToSlash(finding.RelativePath) != finding.RelativePath {
				return fmt.Errorf("finding[%d] relative_path=%q is not canonical", index, finding.RelativePath)
			}
		}
		if finding.ObservedSHA256 != "" && !isLowerHexDigest(finding.ObservedSHA256) {
			return fmt.Errorf("finding[%d] observed_sha256 is invalid", index)
		}
		key := findingKey(finding)
		if seen[key] {
			return fmt.Errorf("finding[%d] is duplicated", index)
		}
		if previous != "" && key < previous {
			return fmt.Errorf("findings are not in canonical order at %d", index)
		}
		seen[key] = true
		previous = key
	}
	return nil
}

func dispositionMapping(classification Classification) (Action, string, bool) {
	switch classification {
	case EmptySupported:
		return Initialize, "EMPTY_SUPPORTED", true
	case CurrentSupported:
		return Recover, "CURRENT_SUPPORTED", true
	case Historical:
		return OfflineDispositionRequired, "HISTORICAL_STATE_DETECTED", true
	case Mixed:
		return OfflineDispositionRequired, "MIXED_AUTHORITY_STATE", true
	case Unsafe:
		return OfflineDispositionRequired, "UNSAFE_CONTROL_ROOT", true
	case Malformed:
		return OfflineDispositionRequired, "MALFORMED_CURRENT_STATE", true
	case Ambiguous:
		return OfflineDispositionRequired, "AMBIGUOUS_CONTROL_ROOT", true
	default:
		return "", "", false
	}
}

func canonicalFindings(findings []Finding) ([]Finding, bool) {
	result := slices.Clone(findings)
	slices.SortFunc(result, func(a, b Finding) int { return strings.Compare(findingKey(a), findingKey(b)) })
	write := 0
	for _, finding := range result {
		if write > 0 && findingKey(result[write-1]) == findingKey(finding) {
			continue
		}
		result[write] = finding
		write++
	}
	return result[:write], len(result[:write]) <= MaxContractFindings
}

func findingKey(f Finding) string {
	return string(f.Kind) + "\x00" + f.RelativePath + "\x00" + f.ObservedSHA256 + "\x00" + f.DetailCode
}

func isStableCode(value string) bool {
	if len(value) == 0 || len(value) > 64 || value[0] < 'A' || value[0] > 'Z' {
		return false
	}
	for _, c := range value {
		if (c < 'A' || c > 'Z') && (c < '0' || c > '9') && c != '_' {
			return false
		}
	}
	return true
}

func isLowerHexDigest(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, c := range value {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}
