package charter

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strings"

	dct "github.com/spec-kitty/go-kitty3/internal/doctrine"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// This file ports charter.offering.packs.pack_validator.validate_pack plus
// the org-charter leg (validate_pack_with_org_charter) and
// render_validation_result.

// ValidationIssue is one pack-validation finding.
type ValidationIssue struct {
	Severity     string // error | advisory
	ArtifactType string
	ArtifactID   *string
	File         string
	Message      string
	Category     string // "" = None
}

// Object is ValidationIssue.to_dict.
func (i ValidationIssue) Object() *jsonx.Object {
	o := jsonx.NewObject("severity", i.Severity, "artifact_type", i.ArtifactType, "artifact_id", optAny(i.ArtifactID),
		"file", i.File, "message", i.Message)
	if i.Category != "" {
		o.Set("category", i.Category)
	}
	return o
}

// PackValidationResult is ValidationResult.
type PackValidationResult struct {
	Errors, Advisories []ValidationIssue
}

// OK reports success (advisories do not count).
func (r *PackValidationResult) OK() bool { return len(r.Errors) == 0 }

// Object is ValidationResult.to_dict.
func (r *PackValidationResult) Object() *jsonx.Object {
	es, as := []any{}, []any{}
	for _, e := range r.Errors {
		es = append(es, e.Object())
	}
	for _, a := range r.Advisories {
		as = append(as, a.Object())
	}
	return jsonx.NewObject("ok", r.OK(), "errors", es, "advisories", as)
}

func sp(s string) *string { return &s }

// RenderValidationResult is render_validation_result.
func RenderValidationResult(w io.Writer, r *PackValidationResult, jsonMode bool) {
	if jsonMode {
		fmt.Fprintln(w, jsonx.Dumps(r.Object(), jsonx.Line))
		return
	}
	prefix := func(i ValidationIssue) (string, string) {
		p := i.ArtifactType
		if i.ArtifactID != nil && *i.ArtifactID != "" {
			p += "/" + *i.ArtifactID
		}
		c := ""
		if i.Category != "" {
			c = " (" + i.Category + ")"
		}
		return p, c
	}
	for _, i := range r.Errors {
		p, c := prefix(i)
		fmt.Fprintf(w, "✗ %s [%s]%s — Error: %s\n", i.File, p, c, i.Message)
	}
	for _, i := range r.Advisories {
		p, c := prefix(i)
		fmt.Fprintf(w, "⚠ advisory [%s]%s: %s\n", p, c, i.Message)
	}
	es, as := "s", "ies"
	if len(r.Errors) == 1 {
		es = ""
	}
	if len(r.Advisories) == 1 {
		as = "y"
	}
	fmt.Fprintf(w, "Pack validation: %d error%s, %d advisor%s\n", len(r.Errors), es, len(r.Advisories), as)
}

// validationRegistry is artifact_schema_registry (ArtifactKind order).
var validationRegistry = []struct {
	plural, suffix string
	kind           dct.Kind
}{
	{"directives", ".directive.yaml", dct.Directive}, {"tactics", ".tactic.yaml", dct.Tactic},
	{"styleguides", ".styleguide.yaml", dct.Styleguide}, {"toolguides", ".toolguide.yaml", dct.Toolguide},
	{"paradigms", ".paradigm.yaml", dct.Paradigm}, {"procedures", ".procedure.yaml", dct.Procedure},
	{"agent_profiles", ".agent.yaml", dct.AgentProfile}, {"mission_step_contracts", ".step-contract.yaml", dct.MissionStepContract},
	{"assets", ".asset.yaml", dct.Asset},
}

// firstSchemaError returns the first pydantic error message of the kind's
// schema ("" when valid).
func firstSchemaError(plural string, data *jsonx.Object) string {
	m := schemaModels[plural]
	if m == nil {
		return ""
	}
	if plural == "agent_profiles" {
		if rv, hasRole := data.Get("role"); hasRole {
			if _, hasRoles := data.Get("roles"); !hasRoles {
				nd := &jsonx.Object{}
				for _, kv := range data.Pairs() {
					if kv.Key != "role" {
						nd.Set(kv.Key, kv.Value)
					}
				}
				if s, ok := rv.(string); ok {
					rv = strings.ToLower(s)
				}
				nd.Set("roles", []any{rv})
				data = nd
			}
		}
	}
	var errs []pydErr
	validateModel(data, m, "", &errs)
	if len(errs) == 0 {
		return ""
	}
	return errs[0].msg
}

func validationScanFiles(dir, suffix string) []string {
	base := filepath.Base(dir)
	if base == "styleguides" || base == "assets" {
		return rglob(dir, suffix)
	}
	return globSuffix(dir, suffix)
}

func validationSafeLoad(p string) (*jsonx.Object, string) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, "YAML parse error: " + err.Error()
	}
	v, err := yamlx.ParseWith(b, yamlx.ParseOptions{Safe: true, File: p})
	if err != nil {
		return nil, "YAML parse error: " + err.Error()
	}
	if v == nil {
		return nil, "empty YAML document"
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return nil, "expected a YAML mapping at top level"
	}
	return o, ""
}

type artifactRec struct {
	data *jsonx.Object
	file string
}

type packScan struct {
	urns    map[string]bool
	trusted map[string]bool
	idsPer  map[string]map[string]bool
	data    map[string]map[string]artifactRec
}

func nonEmptyStr(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && s != ""
}

func scanArtifactDir(plural, dir, suffix string, kind dct.Kind, errs *[]ValidationIssue, ps *packScan) {
	seen := map[string]string{}
	aug := AugmentationPluralKinds()
	addData := func(id string, d *jsonx.Object, f string) {
		if ps.data[plural] == nil {
			ps.data[plural] = map[string]artifactRec{}
		}
		if _, ok := ps.data[plural][id]; !ok {
			ps.data[plural][id] = artifactRec{d, f}
		}
	}
	addID := func(id string) {
		if ps.idsPer[plural] == nil {
			ps.idsPer[plural] = map[string]bool{}
		}
		ps.idsPer[plural][id] = true
	}
	for _, f := range validationScanFiles(dir, suffix) {
		data, perr := validationSafeLoad(f)
		if perr != "" {
			*errs = append(*errs, ValidationIssue{"error", plural, nil, f, perr, "parse_error"})
			continue
		}
		idv, _ := data.Get("id")
		id, idOK := nonEmptyStr(idv)
		ov, _ := data.Get("overrides")
		ev, _ := data.Get("enhances")
		_, o1 := nonEmptyStr(ov)
		_, e1 := nonEmptyStr(ev)
		if idOK && aug[plural] {
			addData(id, data, f)
			if o1 && e1 {
				addID(id)
				seen[id] = f
				ps.urns[string(kind)+":"+id] = true
				continue
			}
		}
		if msg := firstSchemaError(plural, data); msg != "" {
			var aid *string
			if idv != nil && idv != "" && idv != false {
				aid = sp(pyStrOf(idv))
			}
			*errs = append(*errs, ValidationIssue{"error", plural, aid, f, "schema validation failed: " + msg, "schema_invalid"})
			continue
		}
		ident := ""
		if kind == dct.AgentProfile {
			ident = data.GetString("profile-id")
		} else {
			ident = data.GetString("id")
		}
		if ident != "" {
			ps.trusted[string(kind)+":"+ident] = true
		}
		if !idOK {
			continue
		}
		if prev, ok := seen[id]; ok {
			*errs = append(*errs, ValidationIssue{"error", plural, sp(id), f, fmt.Sprintf("duplicate id '%s' (also defined in %s)", id, filepath.Base(prev)), "duplicate_id"})
			continue
		}
		seen[id] = f
		addID(id)
		addData(id, data, f)
		ps.urns[string(kind)+":"+id] = true
	}
}

// orgLoadFinding is _org_load_finding.
func orgLoadFinding(err error, fallback string) ValidationIssue {
	file, cat, msg := fallback, "unreadable_file", err.Error()
	var se *OrgPackSchemaError
	var pe *OrgPackParseError
	var me *OrgPackMissingError
	var pathErr *os.PathError
	switch {
	case errors.As(err, &se):
		cat = "schema_invalid"
		if se.SourceFile != "" {
			file = se.SourceFile
		}
	case errors.As(err, &pe):
		cat = "parse_error"
		if pe.SourceFile != "" {
			file = pe.SourceFile
		}
	case errors.As(err, &me):
		cat, file = "org_pack_missing", me.Path
	case errors.As(err, &pathErr):
		file = pathErr.Path
		msg = "unreadable org-pack file: " + err.Error()
	default:
		msg = "unreadable org-pack file: " + err.Error()
	}
	return ValidationIssue{"error", "drg", nil, file, msg, cat}
}

type boundEdge struct {
	src, tgt, rawSrc, rawTgt string
	srcCause, tgtCause       string
}

func bindEndpoint(raw string, local map[string]string, builtins map[string]bool) (string, string) {
	u, err := ResolveEdgeEndpoint(raw, local, builtins)
	if err != nil {
		return raw, err.(*EndpointError).Kind
	}
	return u, ""
}

func orgNodeURN(kind, id string) string {
	if s, ok := OrgPluralToSingular[kind]; ok {
		return s + ":" + id
	}
	return kind + ":" + id
}

func validateAuthoredEndpoints(file string, frag *OrgFragment, trusted map[string]bool, catURNs map[string]bool) ([]ValidationIssue, []ValidationIssue) {
	if frag == nil {
		return nil, nil
	}
	declared := map[string]bool{}
	loaded := map[string]bool{}
	for _, n := range frag.Nodes {
		u := orgNodeURN(n.Kind, n.ID)
		loaded[u] = true
		if !n.Discovered {
			declared[u] = true
		}
	}
	tr := map[string]bool{}
	for u := range trusted {
		tr[u] = true
	}
	local := OrgLocalEndpointMap(frag, tr)
	var extra []string
	for u := range trusted {
		if !loaded[u] {
			extra = append(extra, u)
		}
	}
	sort.Strings(extra)
	for _, u := range extra {
		_, id, _ := strings.Cut(u, ":")
		local[id] = u
	}
	known := map[string]bool{}
	for _, m := range []map[string]bool{catURNs, trusted, declared} {
		for u := range m {
			known[u] = true
		}
	}
	var errs, advs []ValidationIssue
	for _, e := range frag.Edges {
		if e.Projected {
			continue
		}
		s, sc := bindEndpoint(e.Source, local, catURNs)
		t, tc := bindEndpoint(e.Target, local, catURNs)
		be := boundEdge{s, t, e.Source, e.Target, sc, tc}
		if known[be.src] && known[be.tgt] {
			continue
		}
		for _, side := range []struct{ role, urn, raw, cause string }{{"source", be.src, be.rawSrc, be.srcCause}, {"target", be.tgt, be.rawTgt, be.tgtCause}} {
			if known[side.urn] {
				continue
			}
			if side.cause == "" {
				advs = append(advs, ValidationIssue{"advisory", "drg", sp(side.raw), file,
					fmt.Sprintf("external DRG endpoint — %s endpoint %s is neither built-in nor declared in this pack; it may live in a sibling org pack. Configure both packs and run `spec-kitty doctor charter-packs` to check the assembled closure", side.role, dct.PyRepr(side.raw)),
					"drg_external_endpoint"})
				continue
			}
			errs = append(errs, ValidationIssue{"error", "drg", sp(side.raw), file,
				fmt.Sprintf("dangling DRG edge — %s endpoint %s (%s; qualify as <kind>:<id>) cannot bind to a built-in or to this pack's declared/schema-trusted identities; correct the endpoint", side.role, dct.PyRepr(side.raw), side.cause),
				"drg_dangling_edge"})
		}
	}
	return errs, advs
}

type graphLoadError struct {
	schema bool
	msg    string
}

func (e *graphLoadError) Error() string { return e.msg }

// loadGraphDocument is loader.load_graph for one *.graph.yaml file.
func loadGraphDocument(p string) (*MergedGraph, error) {
	b, err := os.ReadFile(p)
	if err != nil {
		return nil, &graphLoadError{false, err.Error()}
	}
	v, err := yamlx.ParseWith(b, yamlx.ParseOptions{Safe: true, File: p})
	if err != nil {
		return nil, &graphLoadError{false, fmt.Sprintf("YAML parse error in %s: %v", p, err)}
	}
	if o, ok := v.(*jsonx.Object); ok {
		var unknown []string
		for _, kv := range o.Pairs() {
			switch kv.Key {
			case "schema_version", "generated_at", "generated_by", "nodes", "edges":
			default:
				unknown = append(unknown, kv.Key)
			}
		}
		if len(unknown) > 0 {
			sort.Strings(unknown)
			return nil, &graphLoadError{true, fmt.Sprintf("%s: graph document declares unknown top-level key(s) %s", p, pyListRepr(unknown))}
		}
	}
	if _, ok := v.(*jsonx.Object); !ok {
		return nil, &graphLoadError{false, fmt.Sprintf("Expected a YAML mapping at top level in %s", p)}
	}
	if errs := drgGraphErrors(v); len(errs) > 0 {
		return nil, &graphLoadError{false, fmt.Sprintf("Validation error in %s: %s", p, formatPydErrors("DRGGraph", errs))}
	}
	g := ParseDRGGraphDocument(v)
	if g == nil {
		return nil, &graphLoadError{false, fmt.Sprintf("Validation error in %s", p)}
	}
	return g, nil
}

func validateDRGFragments(drgDir string, packURNs map[string]bool, catURNs map[string]bool, catKinds map[string]string) ([]ValidationIssue, []ValidationIssue) {
	var errs, advs []ValidationIssue
	frags := globSuffix(drgDir, ".graph.yaml")
	if len(frags) == 0 {
		return nil, nil
	}
	known := map[string]bool{}
	for u := range catURNs {
		known[u] = true
	}
	for u := range packURNs {
		known[u] = true
	}
	seenEdges := map[[3]string]string{}
	for _, f := range frags {
		g, err := loadGraphDocument(f)
		if err != nil {
			ge := err.(*graphLoadError)
			if ge.schema {
				errs = append(errs, ValidationIssue{"error", "drg", nil, f, ge.msg, "schema_invalid"})
			} else {
				errs = append(errs, ValidationIssue{"error", "drg", nil, f, "failed to load DRG fragment: " + ge.msg, ""})
			}
			continue
		}
		for _, n := range g.Nodes {
			if bk, ok := catKinds[n.URN]; ok && bk != n.Kind {
				errs = append(errs, ValidationIssue{"error", "drg", sp(n.URN), f, fmt.Sprintf("node %s attempts to change built-in kind %s → %s", n.URN, dct.PyRepr(bk), dct.PyRepr(n.Kind)), "drg_kind_drift"})
			}
			known[n.URN] = true
		}
		for _, e := range g.Edges {
			if known[e.Source] && known[e.Target] {
				continue
			}
			for _, side := range [][2]string{{"source", e.Source}, {"target", e.Target}} {
				if !known[side[1]] {
					errs = append(errs, ValidationIssue{"error", "drg", sp(side[1]), f, fmt.Sprintf("dangling DRG edge — %s URN %s not in built-in or pack artifact set", side[0], dct.PyRepr(side[1])), "drg_dangling_edge"})
				}
			}
		}
		for _, e := range g.Edges {
			k := [3]string{e.Source, e.Target, e.Relation}
			if prev, ok := seenEdges[k]; ok {
				advs = append(advs, ValidationIssue{"advisory", "drg", nil, f, fmt.Sprintf("duplicate edge (%s -[%s]-> %s) already present in %s", e.Source, e.Relation, e.Target, filepath.Base(prev)), "duplicate_drg_edge"})
			} else {
				seenEdges[k] = f
			}
		}
	}
	return errs, advs
}

// pyMimeTypes is the subset of Python's mimetypes.types_map consulted for
// asset manifests.
var pyMimeTypes = map[string]string{
	".png": "image/png", ".jpg": "image/jpeg", ".jpeg": "image/jpeg", ".gif": "image/gif", ".svg": "image/svg+xml",
	".webp": "image/webp", ".ico": "image/vnd.microsoft.icon", ".bmp": "image/bmp", ".tif": "image/tiff", ".tiff": "image/tiff",
	".txt": "text/plain", ".md": "text/markdown", ".markdown": "text/markdown", ".html": "text/html", ".htm": "text/html",
	".css": "text/css", ".csv": "text/csv", ".js": "text/javascript", ".mjs": "text/javascript", ".json": "application/json",
	".xml": "text/xml", ".pdf": "application/pdf", ".zip": "application/zip", ".gz": "application/gzip", ".tar": "application/x-tar",
	".sh": "application/x-sh", ".py": "text/x-python", ".yaml": "application/yaml", ".yml": "application/yaml",
	".mp3": "audio/mpeg", ".wav": "audio/x-wav", ".mp4": "video/mp4", ".woff": "font/woff", ".woff2": "font/woff2",
	".ttf": "font/ttf", ".otf": "font/otf", ".rtf": "application/rtf", ".doc": "application/msword",
}

func validateAssetManifests(packDir string, assets map[string]artifactRec) []ValidationIssue {
	if len(assets) == 0 {
		return nil
	}
	root := pyResolve(filepath.Join(packDir, "assets"))
	ids := make([]string, 0, len(assets))
	for id := range assets {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	var errs []ValidationIssue
	for _, id := range ids {
		rec := assets[id]
		pv, _ := rec.data.Get("path")
		raw, ok := nonEmptyStr(pv)
		if !ok {
			continue
		}
		if _, err := ResolveWithinRoot(root, raw); err != nil {
			errs = append(errs, ValidationIssue{"error", "assets", sp(id), rec.file, fmt.Sprintf("asset path %s escapes the pack's assets/ root: %v", dct.PyRepr(raw), err), "asset_path_escape"})
			continue
		}
		mv, _ := rec.data.Get("mime")
		mime, ok := nonEmptyStr(mv)
		if !ok {
			continue
		}
		t, sub, found := strings.Cut(mime, "/")
		if !found || t == "" || sub == "" {
			errs = append(errs, ValidationIssue{"error", "assets", sp(id), rec.file, fmt.Sprintf("asset mime %s is not a well-formed 'type/subtype' value", dct.PyRepr(mime)), "asset_mime_invalid"})
			continue
		}
		if g, ok := pyMimeTypes[strings.ToLower(filepath.Ext(raw))]; ok && g != mime {
			errs = append(errs, ValidationIssue{"error", "assets", sp(id), rec.file, fmt.Sprintf("asset mime %s is inconsistent with path %s (guessed %s from its extension)", dct.PyRepr(mime), dct.PyRepr(raw), dct.PyRepr(g)), "asset_mime_invalid"})
		}
	}
	return errs
}

func builtInIDsPerKind() map[string]map[string]bool {
	out := map[string]map[string]bool{}
	bi := dct.BuiltIn()
	for _, r := range validationRegistry {
		if !r.kind.HasBuiltInContentDir() {
			continue
		}
		set := map[string]bool{}
		for _, rel := range bi.Walk(r.plural, r.suffix, true) {
			b, err := bi.ReadFile(rel)
			if err != nil {
				continue
			}
			o, err := yamlx.ParseObject(b)
			if err != nil || o == nil {
				continue
			}
			if id, ok := o.Get("id"); ok {
				if s, ok := id.(string); ok {
					set[s] = true
				}
			}
		}
		if len(set) > 0 {
			out[r.plural] = set
		}
	}
	return out
}

func kindSingular(plural string) string {
	if k, ok := dct.KindFromPlural(plural); ok {
		return string(k)
	}
	if s, ok := OrgPluralToSingular[plural]; ok {
		return s
	}
	return plural
}

type intentRec struct {
	rel  map[string]string
	file string
}

func intentAwareCollisions(arts map[string]map[string]artifactRec, builtIns map[string]map[string]bool, frag map[string]map[string]*intentRec) ([]ValidationIssue, []ValidationIssue) {
	var errs, advs []ValidationIssue
	plurals := make([]string, 0, len(arts))
	for p := range arts {
		plurals = append(plurals, p)
	}
	sort.Strings(plurals)
	for _, plural := range plurals {
		bis := builtIns[plural]
		sing := kindSingular(plural)
		ids := make([]string, 0, len(arts[plural]))
		for id := range arts[plural] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			rec := arts[plural][id]
			ov, _ := rec.data.Get("overrides")
			ev, _ := rec.data.Get("enhances")
			o, oset := nonEmptyStr(ov)
			e, eset := nonEmptyStr(ev)
			switch {
			case oset && eset:
				errs = append(errs, ValidationIssue{"error", plural, sp(id), rec.file, fmt.Sprintf("overrides and enhances are mutually exclusive on %s %s", sing, id), "intent_conflict"})
				continue
			case oset && !bis[o]:
				errs = append(errs, ValidationIssue{"error", plural, sp(id), rec.file, fmt.Sprintf("%s %s declares overrides: %s, but no built-in %s with that id exists", sing, id, o, sing), "unknown_target"})
				continue
			case eset && !bis[e]:
				errs = append(errs, ValidationIssue{"error", plural, sp(id), rec.file, fmt.Sprintf("%s %s declares enhances: %s, but no built-in %s with that id exists", sing, id, e, sing), "unknown_target"})
				continue
			case oset || eset:
				continue
			}
			if bis[id] {
				if r := frag[plural][id]; r != nil && (r.rel["enhances"] == id || r.rel["overrides"] == id) {
					continue
				}
				advs = append(advs, ValidationIssue{"advisory", plural, sp(id), rec.file,
					fmt.Sprintf("artifact id %s will field-merge into the built-in %s — declare 'enhances: %s' to suppress this advisory, or 'overrides: %s' to declare a full replacement", dct.PyRepr(id), sing, id, id),
					"same_id_collision"})
			}
		}
	}
	return errs, advs
}

func urnToPlural(urn string) (string, string, bool) {
	s, id, ok := strings.Cut(urn, ":")
	if !ok || s == "" || id == "" {
		return "", "", false
	}
	for _, ak := range augmentationKinds {
		if ak[0] == s {
			return ak[1], id, true
		}
	}
	return "", "", false
}

func foldAugmentation(edges [][3]string, file string, intent map[string]map[string]*intentRec) {
	aug := AugmentationPluralKinds()
	for _, e := range edges {
		if e[2] != "enhances" && e[2] != "overrides" {
			continue
		}
		sp_, sid, ok1 := urnToPlural(e[0])
		_, tid, ok2 := urnToPlural(e[1])
		if !ok1 || !ok2 || !aug[sp_] {
			continue
		}
		if intent[sp_] == nil {
			intent[sp_] = map[string]*intentRec{}
		}
		r := intent[sp_][sid]
		if r == nil {
			r = &intentRec{rel: map[string]string{}, file: file}
			intent[sp_][sid] = r
		}
		r.rel[e[2]] = tid
	}
}

func qualifyEndpoint(raw string, local map[string]string, fallback string) (string, bool) {
	u, err := ResolveEdgeEndpoint(raw, local, nil)
	if err != nil {
		if fallback == "" || strings.Contains(raw, ":") {
			return "", false
		}
		return fallback + ":" + raw, true
	}
	return u, true
}

func collectFragmentIntent(drgDir string, frag *OrgFragment) map[string]map[string]*intentRec {
	intent := map[string]map[string]*intentRec{}
	for _, f := range globSuffix(drgDir, ".graph.yaml") {
		g, err := loadGraphDocument(f)
		if err != nil {
			continue
		}
		var es [][3]string
		for _, e := range g.Edges {
			es = append(es, [3]string{e.Source, e.Target, e.Relation})
		}
		foldAugmentation(es, f, intent)
	}
	if frag != nil {
		local := map[string]string{}
		for _, n := range frag.Nodes {
			if s, ok := OrgPluralToSingular[n.Kind]; ok {
				local[n.ID] = s + ":" + n.ID
			}
		}
		var es [][3]string
		for _, e := range frag.Edges {
			s, ok := qualifyEndpoint(e.Source, local, "")
			if !ok {
				continue
			}
			k, _, _ := strings.Cut(s, ":")
			t, ok := qualifyEndpoint(e.Target, local, k)
			if !ok {
				continue
			}
			es = append(es, [3]string{s, t, e.Relation})
		}
		foldAugmentation(es, filepath.Join(filepath.Dir(drgDir), "drg", "fragment.yaml"), intent)
	}
	return intent
}

func intentCollisionsFromEdges(intent map[string]map[string]*intentRec, builtIns map[string]map[string]bool, arts map[string]map[string]artifactRec) []ValidationIssue {
	var errs []ValidationIssue
	plurals := make([]string, 0, len(intent))
	for p := range intent {
		plurals = append(plurals, p)
	}
	sort.Strings(plurals)
	for _, plural := range plurals {
		bis := builtIns[plural]
		sing := kindSingular(plural)
		ids := make([]string, 0, len(intent[plural]))
		for id := range intent[plural] {
			ids = append(ids, id)
		}
		sort.Strings(ids)
		for _, id := range ids {
			if rec, ok := arts[plural][id]; ok {
				ov, _ := rec.data.Get("overrides")
				ev, _ := rec.data.Get("enhances")
				_, o1 := nonEmptyStr(ov)
				_, e1 := nonEmptyStr(ev)
				if o1 || e1 || !bis[id] {
					continue
				}
			}
			r := intent[plural][id]
			o, e := r.rel["overrides"], r.rel["enhances"]
			if o != "" && e != "" {
				errs = append(errs, ValidationIssue{"error", plural, sp(id), r.file, fmt.Sprintf("overrides and enhances are mutually exclusive on %s %s (declared via DRG fragment edges)", sing, id), "intent_conflict"})
				continue
			}
			for _, rt := range [][2]string{{"overrides", o}, {"enhances", e}} {
				if rt[1] != "" && !bis[rt[1]] {
					errs = append(errs, ValidationIssue{"error", plural, sp(id), r.file, fmt.Sprintf("%s %s declares %s: %s (via DRG fragment edge), but no built-in %s with that id exists", sing, id, rt[0], rt[1], sing), "unknown_target"})
				}
			}
		}
	}
	return errs
}

func validatePackSanction(packDir string, builtIn map[string]bool, frag *OrgFragment) ([]ValidationIssue, []ValidationIssue) {
	sf := filepath.Join(packDir, PackPolicyFilename)
	pol, err := loadPackSanction(filepath.Base(packDir), packDir)
	if err != nil {
		return []ValidationIssue{{"error", "pack", nil, sf, err.Error(), "pack_sanction"}}, nil
	}
	var packURNs map[string]bool
	if frag != nil {
		packURNs = map[string]bool{}
		for _, n := range frag.Nodes {
			packURNs[orgNodeURN(n.Kind, n.ID)] = true
		}
	}
	var errs, advs []ValidationIssue
	for _, e := range pol.Entries {
		if SanctionReasonMissing(e.URN, e.Reason) {
			errs = append(errs, ValidationIssue{"error", "pack", sp(e.URN), sf, fmt.Sprintf("%s: directive override '%s' requires a non-empty reason", PackPolicyFilename, e.URN), "pack_sanction"})
		} else if packURNs != nil && len(builtIn) > 0 && !(builtIn[e.URN] && packURNs[e.URN]) {
			advs = append(advs, ValidationIssue{"advisory", "pack", sp(e.URN), sf, fmt.Sprintf("%s: '%s' is not a built-in this pack overrides; the entry is inert", PackPolicyFilename, e.URN), "pack_sanction"})
		}
	}
	return errs, advs
}

// validatePackDescriptor is _validate_pack_descriptor (bare path: no
// configured position).
func validatePackDescriptor(packDir string) []ValidationIssue {
	p := filepath.Join(packDir, "pack.yaml")
	if st, err := os.Stat(p); err != nil || st.IsDir() {
		return nil
	}
	var issues []ValidationIssue
	b, err := os.ReadFile(p)
	if err != nil {
		return []ValidationIssue{{"error", "pack", nil, p, "unreadable pack.yaml: " + err.Error(), "parse_error"}}
	}
	v, err := yamlx.Parse(b)
	if err != nil {
		return []ValidationIssue{{"error", "pack", nil, p, "unreadable pack.yaml: " + err.Error(), "parse_error"}}
	}
	o, isObj := v.(*jsonx.Object)
	if isObj && !o.Has("pack_level") {
		issues = append(issues, ValidationIssue{"advisory", "pack", nil, p, "PACK_LEVEL_MISSING: pack.yaml has no 'pack_level'; add 'pack_level: built-in|org|project' (required once the deprecation window closes)", "PACK_LEVEL_MISSING"})
		o2 := o.Clone()
		o2.Set("pack_level", "org")
		v = o2
	}
	if isObj && o.Has("accompanies_doctrine_pack") {
		return []ValidationIssue{{"error", "pack", sp("accompanies_doctrine_pack"), p,
			fmt.Sprintf("RETIRED_PACK_FIELD: %s: 'accompanies_doctrine_pack' is retired; delete the field; presets ship inside the pack (presets/<name>.yaml)", p), "RETIRED_PACK_FIELD"}}
	}
	if !isObj {
		return []ValidationIssue{{"error", "pack", nil, p, "pack.yaml schema validation failed: Input should be a valid dictionary or instance of PackDescriptor", "schema_invalid"}}
	}
	var errs []pydErr
	validateModel(v.(*jsonx.Object), packDescriptorModel, "", &errs)
	if len(errs) == 0 {
		if lv, _ := v.(*jsonx.Object).Get("pack_level"); lv == "built-in" {
			if pv, ok := v.(*jsonx.Object).Get("pack_version"); !ok || pv == nil {
				errs = append(errs, pydErr{"", "Value error, pack_version is required when pack_level is 'built-in'", "value_error", v})
			}
		}
	}
	if len(errs) > 0 {
		return []ValidationIssue{{"error", "pack", nil, p, "pack.yaml schema validation failed: " + errs[0].msg, "schema_invalid"}}
	}
	return issues
}

// resolvePackLayout is _resolve_layout (bare path).
func resolvePackLayout(packDir string) (string, string) {
	convCharter := filepath.Join(packDir, "org-charter.yaml")
	convDRG := filepath.Join(packDir, "drg", "fragment.yaml")
	p := filepath.Join(packDir, "pack.yaml")
	b, err := os.ReadFile(p)
	if err != nil {
		return convCharter, convDRG
	}
	v, err := yamlx.Parse(b)
	if err != nil || ValidatePackDescriptor(v) != "" {
		return convCharter, convDRG
	}
	o := v.(*jsonx.Object)
	level := o.GetString("pack_level")
	charterPath := ""
	if ac := o.GetString("active_charter"); ac != "" {
		r, err := ResolveWithinRoot(packDir, ac)
		if err != nil {
			return convCharter, convDRG
		}
		charterPath = r
	} else if level == "org" {
		charterPath = convCharter
	}
	drg := convDRG
	if pd := o.GetString("pack_drg"); pd != "" {
		r, err := ResolveWithinRoot(packDir, pd)
		if err != nil {
			return convCharter, convDRG
		}
		drg = r
	} else if level == "project" {
		drg = filepath.Join(packDir, "graph.yaml")
	} else if level == "built-in" {
		drg = convDRG
	}
	return charterPath, drg
}

func validatePresets(packDir string, frag *OrgFragment) []ValidationIssue {
	files := PresetFiles(packDir)
	if len(files) == 0 {
		return nil
	}
	var issues []ValidationIssue
	var known map[string]bool
	canon := func(kind, id string) string {
		if kind == "directive" {
			return "directive:" + dct.NormalizeDirectiveID(id)
		}
		return kind + ":" + id
	}
	for _, f := range files {
		st := stem(f)
		pr, err := LoadPresetFile(f)
		if err != nil {
			pe, ok := err.(*PresetFormatError)
			if !ok {
				continue
			}
			issues = append(issues, ValidationIssue{"error", "preset", sp(st), f, fmt.Sprintf("malformed preset %s: %s: %s", filepath.Base(f), pe.Field, pe.Detail), "preset_format"})
			continue
		}
		if known == nil {
			known = map[string]bool{}
			if g, err := dct.BuiltInGraph(); err == nil {
				for _, n := range g.Nodes {
					k, id, _ := strings.Cut(n.URN, ":")
					known[canon(k, id)] = true
				}
			}
			if frag != nil {
				for _, n := range frag.Nodes {
					k, id, _ := strings.Cut(orgNodeURN(n.Kind, n.ID), ":")
					known[canon(k, id)] = true
				}
			}
		}
		var listed []dct.Kind
		for _, k := range governedKinds {
			if _, ok := pr.Activations["activated_"+k.Plural()]; ok {
				listed = append(listed, k)
			}
		}
		for _, k := range listed {
			key := "activated_" + k.Plural()
			for _, id := range pr.Activations[key] {
				if !known[canon(string(k), id)] {
					issues = append(issues, ValidationIssue{"error", "preset", sp(st), f, fmt.Sprintf("preset %s: %s id %s does not resolve in the offering", filepath.Base(f), key, dct.PyRepr(id)), "preset_unresolved_id"})
				}
			}
		}
		for _, mt := range pr.MissionTypes {
			if !known[canon("mission_type", mt)] {
				issues = append(issues, ValidationIssue{"error", "preset", sp(st), f, fmt.Sprintf("preset %s: mission_type_activations id %s does not resolve in the offering", filepath.Base(f), dct.PyRepr(mt)), "preset_unresolved_id"})
			}
		}
		if pr.ActivatedKinds != nil {
			gate := map[string]bool{}
			for _, x := range pr.ActivatedKinds {
				gate[x] = true
			}
			var omitted []string
			for _, k := range listed {
				if !gate[k.Plural()] {
					omitted = append(omitted, k.Plural())
				}
			}
			if len(omitted) > 0 {
				issues = append(issues, ValidationIssue{"error", "preset", sp(st), f, fmt.Sprintf("preset %s: activated_kinds omits %s, which the preset lists ids for", filepath.Base(f), strings.Join(omitted, ", ")), "preset_kind_gate"})
			}
		}
	}
	return issues
}

// ValidateOrgCharterFile is validate_org_charter_file.
func ValidateOrgCharterFile(p string, packDirectives map[string]bool) []ValidationIssue {
	b, err := os.ReadFile(p)
	if err != nil {
		return []ValidationIssue{{"error", "org-charter", nil, p, "YAML parse error: " + err.Error(), ""}}
	}
	v, err := yamlx.ParseWith(b, yamlx.ParseOptions{Safe: true, File: p})
	if err != nil {
		return []ValidationIssue{{"error", "org-charter", nil, p, "YAML parse error: " + err.Error(), ""}}
	}
	if v == nil {
		return []ValidationIssue{{"error", "org-charter", nil, p, "empty YAML document", ""}}
	}
	o, ok := v.(*jsonx.Object)
	if !ok {
		return []ValidationIssue{{"error", "org-charter", nil, p, "expected a YAML mapping at top level", ""}}
	}
	if errs := orgCharterSchemaErrors(o); len(errs) > 0 {
		return []ValidationIssue{{"error", "org-charter", nil, p, "org-charter schema validation failed: " + errs[0].msg, ""}}
	}
	var issues []ValidationIssue
	for _, x := range o.GetList("governance_policies") {
		gp, ok := x.(*jsonx.Object)
		if !ok {
			continue
		}
		enf := "advisory"
		if ev, ok := gp.Get("enforcement"); ok {
			enf = pyStrOf(ev)
		}
		if enf != "advisory" {
			issues = append(issues, ValidationIssue{"advisory", "org-charter", sp(gp.GetString("field")), p, fmt.Sprintf("governance policy uses non-advisory enforcement %s; only 'advisory' is recognised today", dct.PyRepr(enf)), ""})
		}
	}
	for _, x := range o.GetList("required_directives") {
		id := pyStrOf(x)
		if !packDirectives[id] {
			issues = append(issues, ValidationIssue{"advisory", "org-charter", sp(id), p, fmt.Sprintf("required_directive %s not found in this pack's directives/ (may exist in another pack or in built-in doctrine)", dct.PyRepr(id)), ""})
		}
	}
	return issues
}

// ValidatePack is validate_pack_with_org_charter(pack_dir, check_drg_root).
func ValidatePack(packDir string, checkDRGRoot bool) *PackValidationResult {
	res := &PackValidationResult{}
	if st, err := os.Stat(packDir); err != nil || !st.IsDir() {
		res.Errors = append(res.Errors, ValidationIssue{"error", "pack", nil, packDir, "pack directory not found: " + packDir, "not_found"})
		return res
	}
	charterPath, drgFragPath := resolvePackLayout(packDir)
	ps := &packScan{urns: map[string]bool{}, trusted: map[string]bool{}, idsPer: map[string]map[string]bool{}, data: map[string]map[string]artifactRec{}}
	for _, r := range validationRegistry {
		d := filepath.Join(packDir, r.plural)
		if st, err := os.Stat(d); err != nil || !st.IsDir() {
			continue
		}
		scanArtifactDir(r.plural, d, r.suffix, r.kind, &res.Errors, ps)
	}
	flagged := map[string]bool{}
	for _, e := range res.Errors {
		if e.ArtifactType == "agent_profiles" {
			flagged[e.File] = true
		}
	}
	pl := loadAgentProfiles("", true, []string{filepath.Join(packDir, "agent_profiles")}, "")
	for _, s := range pl.skipped {
		if flagged[s.Path] || s.Layer == dct.LayerBuiltIn {
			continue
		}
		res.Errors = append(res.Errors, ValidationIssue{"error", "agent_profiles", s.ProfileID, s.Path, s.ErrorSummary, "profile_skipped"})
	}
	var frag *OrgFragment
	fy := filepath.Join(packDir, "drg", "fragment.yaml")
	if _, err := os.Stat(fy); err == nil {
		f, err := LoadOrgPack(filepath.Base(packDir), packDir, 1)
		if err != nil {
			res.Errors = append(res.Errors, orgLoadFinding(err, fy))
		} else {
			frag = f
		}
	} else if st, err := os.Stat(filepath.Join(packDir, "mission_types")); err == nil && st.IsDir() {
		if _, err := collectGovernanceScopeEdges(packDir); err != nil {
			res.Errors = append(res.Errors, orgLoadFinding(err, packDir))
		}
	}
	drgDir := filepath.Join(packDir, "drg")
	drgIsDir := false
	if st, err := os.Stat(drgDir); err == nil && st.IsDir() {
		drgIsDir = true
		bi, err := dct.BuiltInGraph()
		if err != nil {
			res.Errors = append(res.Errors, ValidationIssue{"error", "drg", nil, drgDir, fmt.Sprintf("built-in DRG unavailable (DRGLoadError: %v); DRG endpoint checks were skipped", err), "drg_builtin_unavailable"})
		} else {
			cat := map[string]bool{}
			kinds := map[string]string{}
			for _, n := range bi.Nodes {
				cat[n.URN] = true
				kinds[n.URN] = n.Kind
			}
			e1, a1 := validateAuthoredEndpoints(drgFragPath, frag, ps.trusted, cat)
			res.Errors = append(res.Errors, e1...)
			res.Advisories = append(res.Advisories, a1...)
			e2, a2 := validateDRGFragments(drgDir, ps.urns, cat, kinds)
			res.Errors = append(res.Errors, e2...)
			res.Advisories = append(res.Advisories, a2...)
		}
	}
	if checkDRGRoot && drgIsDir && len(globSuffix(drgDir, ".graph.yaml")) > 0 && len(globSuffix(packDir, ".graph.yaml")) == 0 {
		res.Errors = append(res.Errors, ValidationIssue{"error", "drg", nil, packDir,
			"DRG content exists under drg/*.graph.yaml with no pack-root *.graph.yaml. The runtime (src/charter/activation/_drg_helpers.py:load_validated_graph) reads pack-root *.graph.yaml and drg/fragment.yaml; drg/*.graph.yaml graph fragments are the unread shape — this pack's drg/*.graph.yaml content will not be read as authored.",
			"drg_root_graph_missing"})
	}
	res.Errors = append(res.Errors, validateAssetManifests(packDir, ps.data["assets"])...)
	builtIns := builtInIDsPerKind()
	intent := map[string]map[string]*intentRec{}
	if drgIsDir {
		intent = collectFragmentIntent(drgDir, frag)
	}
	ie, ia := intentAwareCollisions(ps.data, builtIns, intent)
	res.Errors = append(res.Errors, ie...)
	res.Advisories = append(res.Advisories, ia...)
	if drgIsDir {
		res.Errors = append(res.Errors, intentCollisionsFromEdges(intent, builtIns, ps.data)...)
	}
	if packSanctionPresent(packDir) {
		biURNs := map[string]bool{}
		if g, err := dct.BuiltInGraph(); err == nil {
			for _, n := range g.Nodes {
				biURNs[n.URN] = true
			}
		}
		se, sa := validatePackSanction(packDir, biURNs, frag)
		res.Errors = append(res.Errors, se...)
		res.Advisories = append(res.Advisories, sa...)
	}
	for _, i := range validatePackDescriptor(packDir) {
		if i.Severity == "error" {
			res.Errors = append(res.Errors, i)
		} else {
			res.Advisories = append(res.Advisories, i)
		}
	}
	res.Errors = append(res.Errors, validatePresets(packDir, frag)...)
	if charterPath != "" {
		if _, err := os.Stat(charterPath); err == nil {
			for _, i := range ValidateOrgCharterFile(charterPath, ps.idsPer["directives"]) {
				if i.Severity == "error" {
					res.Errors = append(res.Errors, i)
				} else {
					res.Advisories = append(res.Advisories, i)
				}
			}
		}
	}
	return res
}
