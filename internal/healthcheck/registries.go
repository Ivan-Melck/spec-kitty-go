// Package healthcheck implements the mission/git-state checks behind the
// area-H `spec-kitty doctor` leaves (identity, topology, workspaces,
// sparse-checkout, run-index, provenance, cutover, shim/contract registries,
// review-cycle reconciliation, decisions, mission-state, coordination).
package healthcheck

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

var (
	dottedName = regexp.MustCompile(`^[A-Za-z_]\w*(?:\.[A-Za-z_]\w*)*$`)
	semverRe   = regexp.MustCompile(`^(\d+)\.(\d+)\.(\d+)(?:([a-z])(\d+))?$`)
	trackerRe  = regexp.MustCompile(`^(#\d+|https?://.+)$`)
)

// SchemaError is RegistrySchemaError / ContractRegistrySchemaError.
type SchemaError struct{ Errors []string }

func (e *SchemaError) Error() string { return strings.Join(e.Errors, "\n") }

// ConfigError is a FileNotFoundError / KeyError configuration failure.
type ConfigError struct{ Msg string }

func (e *ConfigError) Error() string { return e.Msg }

// cmpVersion compares X.Y.Z[aN|bN|rcN] strings (PEP 440 subset).
func cmpVersion(a, b string) int {
	pa, pb := parseVer(a), parseVer(b)
	for i := range pa {
		if pa[i] != pb[i] {
			if pa[i] < pb[i] {
				return -1
			}
			return 1
		}
	}
	return 0
}

func parseVer(s string) [5]int {
	var out [5]int
	m := regexp.MustCompile(`^(\d+)(?:\.(\d+))?(?:\.(\d+))?(?:\.?(a|b|rc|c)(\d+))?`).FindStringSubmatch(strings.TrimSpace(strings.TrimPrefix(s, "v")))
	if m == nil {
		return out
	}
	out[0], _ = strconv.Atoi(m[1])
	out[1], _ = strconv.Atoi(m[2])
	out[2], _ = strconv.Atoi(m[3])
	switch m[4] {
	case "a":
		out[3] = 0
	case "b":
		out[3] = 1
	case "rc", "c":
		out[3] = 2
	default:
		out[3] = 3
	}
	out[4], _ = strconv.Atoi(m[5])
	return out
}

// pyprojectVersion reads [project].version from pyproject.toml (minimal
// TOML scan).
func pyprojectVersion(root string) (string, error) {
	p := filepath.Join(root, "pyproject.toml")
	b, err := os.ReadFile(p)
	if err != nil {
		return "", &ConfigError{fmt.Sprintf("pyproject.toml not found at %s", p)}
	}
	section := ""
	hasProject := false
	for _, line := range strings.Split(string(b), "\n") {
		l := strings.TrimSpace(line)
		if strings.HasPrefix(l, "[") {
			section = strings.Trim(l, "[] ")
			if section == "project" {
				hasProject = true
			}
			continue
		}
		if section != "project" {
			continue
		}
		if k, v, ok := strings.Cut(l, "="); ok && strings.TrimSpace(k) == "version" {
			return strings.Trim(strings.TrimSpace(v), `"'`), nil
		}
	}
	if !hasProject {
		return "", &ConfigError{"missing key 'project' in pyproject.toml"}
	}
	return "", &ConfigError{"missing key 'version' in pyproject.toml"}
}

func loadYAMLDoc(p string) (any, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, err
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return nil, &SchemaError{[]string{"YAML parse error: " + err.Error()}}
	}
	return v, nil
}

// ShimEntry is compat.registry.ShimEntry plus its classification.
type ShimEntry struct {
	LegacyPath           string
	CanonicalImport      any // string or []any
	RemovalTargetRelease string
	TrackerIssue         string
	Grandfathered        bool
	Status               string
	ShimExists           bool
}

// ShimReport is ShimRegistryReport.
type ShimReport struct {
	ProjectVersion, RegistryPath string
	Entries                      []ShimEntry
}

// HasOverdue reports any overdue entry.
func (r *ShimReport) HasOverdue() bool {
	for _, e := range r.Entries {
		if e.Status == "overdue" {
			return true
		}
	}
	return false
}

var shimRequired = []string{"canonical_import", "grandfathered", "introduced_in_release", "legacy_path", "removal_target_release", "tracker_issue"}

func validateShims(data any) error {
	o, ok := data.(*jsonx.Object)
	if !ok || !o.Has("shims") {
		return &SchemaError{[]string{"top-level: must be a mapping with a 'shims' key"}}
	}
	list, ok := valueOf(o, "shims").([]any)
	if !ok {
		return &SchemaError{[]string{"top-level.shims: must be a list"}}
	}
	var errs []string
	seen := map[string]bool{}
	known := map[string]bool{"extension_rationale": true, "notes": true}
	for _, k := range shimRequired {
		known[k] = true
	}
	for i, it := range list {
		e, ok := it.(*jsonx.Object)
		if !ok {
			errs = append(errs, fmt.Sprintf("entry[%d]: must be a mapping", i))
			continue
		}
		var unknown []string
		for _, k := range e.Keys() {
			if !known[k] {
				unknown = append(unknown, k)
			}
		}
		sort.Strings(unknown)
		for _, k := range unknown {
			errs = append(errs, fmt.Sprintf("entry[%d].%s: unknown field", i, k))
		}
		missing := false
		for _, k := range shimRequired {
			if !e.Has(k) {
				errs = append(errs, fmt.Sprintf("entry[%d].%s: required field is missing", i, k))
				missing = true
			}
		}
		if missing {
			continue
		}
		lp, isStr := valueOf(e, "legacy_path").(string)
		switch {
		case !isStr || !dottedName.MatchString(lp):
			errs = append(errs, fmt.Sprintf("entry[%d].legacy_path: must be a dotted identifier string", i))
		case seen[lp]:
			errs = append(errs, fmt.Sprintf("entry[%d].legacy_path: duplicate value '%s'", i, lp))
		default:
			seen[lp] = true
		}
		switch ci := valueOf(e, "canonical_import").(type) {
		case string:
			if !dottedName.MatchString(ci) {
				errs = append(errs, fmt.Sprintf("entry[%d].canonical_import: must be a dotted identifier string", i))
			}
		case []any:
			if len(ci) == 0 {
				errs = append(errs, fmt.Sprintf("entry[%d].canonical_import: list must not be empty", i))
			}
			for j, x := range ci {
				if s, ok := x.(string); !ok || !dottedName.MatchString(s) {
					errs = append(errs, fmt.Sprintf("entry[%d].canonical_import[%d]: must be a dotted identifier string", i, j))
				}
			}
		default:
			errs = append(errs, fmt.Sprintf("entry[%d].canonical_import: must be a string or list of strings", i))
		}
		for _, f := range []string{"introduced_in_release", "removal_target_release"} {
			if s, ok := valueOf(e, f).(string); !ok || !semverRe.MatchString(s) {
				errs = append(errs, fmt.Sprintf("entry[%d].%s: must be a semver string like '1.2.3' or '1.2.3a1'", i, f))
			}
		}
		in, ok1 := valueOf(e, "introduced_in_release").(string)
		rm, ok2 := valueOf(e, "removal_target_release").(string)
		if ok1 && ok2 && semverRe.MatchString(in) && semverRe.MatchString(rm) && cmpVersion(rm, in) < 0 {
			errs = append(errs, fmt.Sprintf("entry[%d].removal_target_release: must be >= introduced_in_release", i))
		}
		if s, ok := valueOf(e, "tracker_issue").(string); !ok || !trackerRe.MatchString(s) {
			errs = append(errs, fmt.Sprintf("entry[%d].tracker_issue: must be '#123' or a URL", i))
		}
		if gf, ok := valueOf(e, "grandfathered").(bool); !ok {
			errs = append(errs, fmt.Sprintf("entry[%d].grandfathered: must be a boolean (true/false), got %s", i, pyjson.TypeName(gf)))
		}
		if er, ok := e.Get("extension_rationale"); ok && er != nil {
			if s, ok := er.(string); !ok || strings.TrimSpace(s) == "" {
				errs = append(errs, fmt.Sprintf("entry[%d].extension_rationale: if present, must be a non-empty string", i))
			}
		}
		if n, ok := e.Get("notes"); ok && n != nil {
			if _, ok := n.(string); !ok {
				errs = append(errs, fmt.Sprintf("entry[%d].notes: if present, must be a string", i))
			}
		}
	}
	if len(errs) > 0 {
		return &SchemaError{errs}
	}
	return nil
}

func shimExists(root, legacy string) bool {
	base := filepath.Join(append([]string{root, "src"}, strings.Split(legacy, ".")...)...)
	if _, err := os.Stat(base + ".py"); err == nil {
		return true
	}
	_, err := os.Stat(filepath.Join(base, "__init__.py"))
	return err == nil
}

// CheckShimRegistry is compat.doctor.check_shim_registry.
func CheckShimRegistry(root string) (*ShimReport, error) {
	ver, err := pyprojectVersion(root)
	if err != nil {
		return nil, err
	}
	reg := filepath.Join(root, "docs", "migrations", "shim-registry.yaml")
	if _, err := os.Stat(reg); err != nil {
		return nil, &ConfigError{"Shim registry not found at " + reg}
	}
	data, err := loadYAMLDoc(reg)
	if err != nil {
		return nil, err
	}
	if err := validateShims(data); err != nil {
		return nil, err
	}
	rep := &ShimReport{ProjectVersion: ver, RegistryPath: reg}
	for _, it := range valueOf(data.(*jsonx.Object), "shims").([]any) {
		e := it.(*jsonx.Object)
		se := ShimEntry{LegacyPath: e.GetString("legacy_path"), RemovalTargetRelease: e.GetString("removal_target_release"),
			TrackerIssue: e.GetString("tracker_issue"), Grandfathered: e.GetBool("grandfathered")}
		se.CanonicalImport, _ = e.Get("canonical_import")
		se.ShimExists = shimExists(root, se.LegacyPath)
		switch {
		case se.Grandfathered:
			se.Status = "grandfathered"
		case !se.ShimExists:
			se.Status = "removed"
		case cmpVersion(ver, se.RemovalTargetRelease) >= 0:
			se.Status = "overdue"
		default:
			se.Status = "pending"
		}
		rep.Entries = append(rep.Entries, se)
	}
	return rep, nil
}

func valueOf(o *jsonx.Object, k string) any {
	if o == nil {
		return nil
	}
	v, _ := o.Get(k)
	return v
}

// ContractRecord is contracts.registry.ContractRecord (reported fields).
type ContractRecord struct {
	ID, Kind, Status, Owner, ReplacedBy, Enforcement string
	ScanRoots                                        []string
}

// ContractReport is ContractRegistryReport.
type ContractReport struct {
	RegistryPath string
	Records      []ContractRecord
}

// Counts returns (advisory, enforcing).
func (r *ContractReport) Counts() (int, int) {
	a, e := 0, 0
	for _, x := range r.Records {
		switch x.Enforcement {
		case "advisory":
			a++
		case "enforcing":
			e++
		}
	}
	return a, e
}

var (
	forbiddenPositional = map[string]bool{"file": true, "line": true, "lineno": true, "line_no": true, "file_line": true, "fileline": true}
	trailingLine        = regexp.MustCompile(`:(\d+)$`)
	pathishPrefix       = regexp.MustCompile(`\.[A-Za-z0-9]+$`)
)

// IsFileLineAnchor is contracts.anchoring.is_file_line_anchor.
func IsFileLineAnchor(v string) bool {
	s := strings.TrimSpace(v)
	m := trailingLine.FindStringIndex(s)
	if m == nil {
		return false
	}
	prefix := s[:m[0]]
	if prefix == "" {
		return false
	}
	return strings.ContainsAny(prefix, `/\`) || pathishPrefix.MatchString(prefix)
}

func rejectPositional(prefix string, v any, errs *[]string) {
	switch t := v.(type) {
	case *jsonx.Object:
		for _, kv := range t.Pairs() {
			if forbiddenPositional[kv.Key] {
				*errs = append(*errs, fmt.Sprintf("%s.%s: positional 'file:line' anchoring is forbidden (NFR-003/DIR-041) — anchor on a dotted symbol or a fixed literal", prefix, kv.Key))
			}
			rejectPositional(prefix+"."+kv.Key, kv.Value, errs)
		}
	case []any:
		for i, x := range t {
			rejectPositional(fmt.Sprintf("%s[%d]", prefix, i), x, errs)
		}
	case string:
		if IsFileLineAnchor(t) {
			*errs = append(*errs, fmt.Sprintf("%s: value %s is a positional 'file:line' anchor, which is forbidden (NFR-003/DIR-041) — anchor on content, never a line number", prefix, pyjson.ReprString(t)))
		}
	}
}

func strListOK(prefix string, v any, allowEmpty bool, errs *[]string) {
	l, ok := v.([]any)
	if !ok {
		*errs = append(*errs, prefix+": must be a list of strings")
		return
	}
	if len(l) == 0 && !allowEmpty {
		*errs = append(*errs, prefix+": must not be empty")
	}
	for _, x := range l {
		if _, ok := x.(string); !ok {
			*errs = append(*errs, prefix+": every item must be a string")
			break
		}
	}
}

var contractRequired = []string{"anchor", "consumers", "id", "kind", "owner", "replaced_by", "retirement", "status", "verification"}

func validateContract(i int, it any, seen map[string]bool, errs *[]string) {
	p := fmt.Sprintf("contracts[%d]", i)
	e, ok := it.(*jsonx.Object)
	if !ok {
		*errs = append(*errs, p+": must be a mapping")
		return
	}
	known := map[string]bool{"notes": true}
	for _, k := range contractRequired {
		known[k] = true
	}
	var unknown []string
	for _, k := range e.Keys() {
		if !known[k] {
			unknown = append(unknown, k)
		}
	}
	sort.Strings(unknown)
	for _, k := range unknown {
		*errs = append(*errs, fmt.Sprintf("%s.%s: unknown field", p, k))
	}
	missing := false
	for _, k := range contractRequired {
		if !e.Has(k) {
			*errs = append(*errs, fmt.Sprintf("%s.%s: required field is missing", p, k))
			missing = true
		}
	}
	rejectPositional(p, e, errs)
	if missing {
		return
	}
	id, isStr := valueOf(e, "id").(string)
	switch {
	case !isStr || id == "":
		*errs = append(*errs, p+".id: must be a non-empty string")
	case seen[id]:
		*errs = append(*errs, fmt.Sprintf("%s.id: duplicate value '%s'", p, id))
	default:
		seen[id] = true
	}
	kind := valueOf(e, "kind")
	if k, _ := kind.(string); k != "fallback_name" && k != "retired_literal" {
		*errs = append(*errs, p+".kind: must be one of ['fallback_name', 'retired_literal']")
	}
	if s, _ := valueOf(e, "status").(string); s != "active" && s != "deprecated" && s != "retired" {
		*errs = append(*errs, p+".status: must be one of ['active', 'deprecated', 'retired']")
	}
	for _, f := range []string{"owner", "replaced_by"} {
		if s, ok := valueOf(e, f).(string); !ok || s == "" {
			*errs = append(*errs, fmt.Sprintf("%s.%s: must be a non-empty string", p, f))
		}
	}
	if n, ok := e.Get("notes"); ok && n != nil {
		if _, ok := n.(string); !ok {
			*errs = append(*errs, p+".notes: if present, must be a string")
		}
	}
	// anchor
	ap := p + ".anchor"
	if a, ok := valueOf(e, "anchor").(*jsonx.Object); !ok {
		*errs = append(*errs, ap+": must be a mapping")
	} else if kind == "fallback_name" {
		if s, ok := valueOf(a, "symbol").(string); !ok || !dottedName.MatchString(s) {
			*errs = append(*errs, ap+".symbol: must be a dotted identifier for kind 'fallback_name'")
		}
		if a.Has("literals") {
			*errs = append(*errs, ap+": kind 'fallback_name' uses 'symbol', not 'literals'")
		}
	} else if kind == "retired_literal" {
		lits, ok := valueOf(a, "literals").([]any)
		if !ok || len(lits) == 0 {
			*errs = append(*errs, ap+".literals: must be a non-empty list for kind 'retired_literal'")
		} else {
			for j, l := range lits {
				lp := fmt.Sprintf("%s.literals[%d]", ap, j)
				lo, ok := l.(*jsonx.Object)
				if !ok {
					*errs = append(*errs, lp+": each literal must be a mapping with 'value' or 'fragments'")
					continue
				}
				if lo.Has("value") == lo.Has("fragments") {
					*errs = append(*errs, lp+": exactly one of 'value' or 'fragments' is required")
					continue
				}
				if lo.Has("value") {
					if s, ok := valueOf(lo, "value").(string); !ok || s == "" {
						*errs = append(*errs, lp+".value: must be a non-empty string")
					}
					continue
				}
				fr, ok := valueOf(lo, "fragments").([]any)
				if !ok || len(fr) == 0 {
					*errs = append(*errs, lp+".fragments: must be a non-empty list of strings")
					continue
				}
				joined := ""
				bad := false
				for _, x := range fr {
					s, ok := x.(string)
					if !ok {
						bad = true
						break
					}
					joined += s
				}
				if bad {
					*errs = append(*errs, lp+".fragments: every fragment must be a string")
				} else if joined == "" {
					*errs = append(*errs, lp+".fragments: joined value must be non-empty")
				} else if IsFileLineAnchor(joined) {
					*errs = append(*errs, fmt.Sprintf("%s.fragments: joined value %s is a positional 'file:line' anchor, which is forbidden (NFR-003/DIR-041) — fragments must not reconstruct a line number", lp, pyjson.ReprString(joined)))
				}
			}
		}
		if a.Has("symbol") {
			*errs = append(*errs, ap+": kind 'retired_literal' uses 'literals', not 'symbol'")
		}
	}
	// retirement
	rp := p + ".retirement"
	if r, ok := valueOf(e, "retirement").(*jsonx.Object); !ok {
		*errs = append(*errs, rp+": must be a mapping")
	} else {
		for _, k := range []string{"introduced_in", "removal_target", "tracker_issue"} {
			if !r.Has(k) {
				*errs = append(*errs, fmt.Sprintf("%s.%s: required field is missing", rp, k))
			}
		}
		intro, ok1 := valueOf(r, "introduced_in").(string)
		rem, ok2 := valueOf(r, "removal_target").(string)
		if !ok1 || !semverRe.MatchString(intro) {
			*errs = append(*errs, rp+".introduced_in: must be a semver string like '3.4.0'")
		}
		if !ok2 || !semverRe.MatchString(rem) {
			*errs = append(*errs, rp+".removal_target: must be a semver string like '3.4.0'")
		}
		if ok1 && ok2 && semverRe.MatchString(intro) && semverRe.MatchString(rem) && cmpVersion(rem, intro) < 0 {
			*errs = append(*errs, rp+".removal_target: must be >= introduced_in")
		}
		if s, ok := valueOf(r, "tracker_issue").(string); !ok || !trackerRe.MatchString(s) {
			*errs = append(*errs, rp+".tracker_issue: must be '#123' or a URL")
		}
	}
	// consumers
	cp := p + ".consumers"
	if c, ok := valueOf(e, "consumers").(*jsonx.Object); !ok {
		*errs = append(*errs, cp+": must be a mapping")
	} else {
		knownC := map[string]bool{"scan_roots": true, "exemptions": true, "test_shards": true, "call_sites": true}
		var unk []string
		for _, k := range c.Keys() {
			if !knownC[k] {
				unk = append(unk, k)
			}
		}
		sort.Strings(unk)
		for _, k := range unk {
			*errs = append(*errs, fmt.Sprintf("%s.%s: unknown field", cp, k))
		}
		for _, k := range []string{"exemptions", "scan_roots"} {
			if !c.Has(k) {
				*errs = append(*errs, fmt.Sprintf("%s.%s: required field is missing", cp, k))
			}
		}
		if c.Has("scan_roots") {
			strListOK(cp+".scan_roots", valueOf(c, "scan_roots"), false, errs)
		}
		if c.Has("exemptions") {
			strListOK(cp+".exemptions", valueOf(c, "exemptions"), true, errs)
		}
		for _, k := range []string{"test_shards", "call_sites"} {
			if c.Has(k) {
				strListOK(cp+"."+k, valueOf(c, k), true, errs)
			}
		}
	}
	vp := p + ".verification"
	if v, ok := valueOf(e, "verification").(*jsonx.Object); !ok {
		*errs = append(*errs, vp+": must be a mapping")
	} else if en, _ := valueOf(v, "enforcement").(string); en != "advisory" && en != "enforcing" {
		*errs = append(*errs, vp+".enforcement: must be one of ['advisory', 'enforcing']")
	}
}

// CheckContractRegistry is contracts.registry.check_contract_registry.
func CheckContractRegistry(root string) (*ContractReport, error) {
	reg := filepath.Join(root, "docs", "contracts", "contract-registry.yaml")
	if _, err := os.Stat(reg); err != nil {
		return nil, &ConfigError{"Contract registry not found at " + reg}
	}
	data, err := loadYAMLDoc(reg)
	if err != nil {
		return nil, err
	}
	o, ok := data.(*jsonx.Object)
	if !ok || !o.Has("contracts") {
		return nil, &SchemaError{[]string{"top-level: must be a mapping with a 'contracts' key"}}
	}
	list, ok := valueOf(o, "contracts").([]any)
	if !ok {
		return nil, &SchemaError{[]string{"top-level.contracts: must be a list"}}
	}
	var errs []string
	seen := map[string]bool{}
	for i, it := range list {
		validateContract(i, it, seen, &errs)
	}
	if len(errs) > 0 {
		return nil, &SchemaError{errs}
	}
	rep := &ContractReport{RegistryPath: reg}
	for _, it := range list {
		e := it.(*jsonx.Object)
		rec := ContractRecord{ID: pyjson.Str(valueOf(e, "id")), Kind: pyjson.Str(valueOf(e, "kind")), Status: pyjson.Str(valueOf(e, "status")),
			Owner: pyjson.Str(valueOf(e, "owner")), ReplacedBy: pyjson.Str(valueOf(e, "replaced_by")),
			Enforcement: pyjson.Str(valueOf(e.GetObject("verification"), "enforcement"))}
		for _, x := range e.GetObject("consumers").GetList("scan_roots") {
			rec.ScanRoots = append(rec.ScanRoots, pyjson.Str(x))
		}
		rep.Records = append(rep.Records, rec)
	}
	return rep, nil
}
