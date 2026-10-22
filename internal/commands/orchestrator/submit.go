package orchestrator

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
	"github.com/spec-kitty/go-kitty3/internal/safecommit"
)

var digestRe = regexp.MustCompile(`^(absent|[a-f0-9]{64})$`)

// submission is design.models.Submission.
type submission struct {
	Artifacts     []*artifactInput
	Parents       *jsonx.Object
	ContextSHA256 string
}

var errSchema = derr("DESIGN_REQUEST_INVALID", "Invalid artifact submission schema")

func onlyKeys(o *jsonx.Object, allowed ...string) bool {
	set := map[string]bool{}
	for _, a := range allowed {
		set[a] = true
	}
	for _, k := range o.Keys() {
		if !set[k] {
			return false
		}
	}
	return true
}

func digestValue(v any) (string, bool) {
	s, ok := v.(string)
	return s, ok && digestRe.MatchString(s)
}

// parseSubmission is design.authoring.parse_submission (strict pydantic).
func parseSubmission(raw string) (*submission, error) {
	if len(raw) > orch.MaxRequestBytes {
		return nil, derr("DESIGN_BOUNDS_EXCEEDED", "Request exceeds the JSON transport bound")
	}
	v, err := pyjson.Loads(raw)
	if err != nil {
		return nil, errSchema
	}
	o, ok := v.(*jsonx.Object)
	if !ok || !onlyKeys(o, "artifacts", "parents", "context_sha256") {
		return nil, errSchema
	}
	sub := &submission{Parents: jsonx.NewObject()}
	av, ok := o.Get("artifacts")
	list, isList := av.([]any)
	if !ok || !isList || len(list) < 1 || len(list) > orch.MaxArtifacts {
		return nil, errSchema
	}
	for _, x := range list {
		ao, ok := x.(*jsonx.Object)
		if !ok || !onlyKeys(ao, "kind", "artifact_id", "content", "expected_sha256") {
			return nil, errSchema
		}
		kind, ok1 := mustGet(ao, "kind").(string)
		content, ok2 := mustGet(ao, "content").(string)
		exp, ok3 := digestValue(mustGet(ao, "expected_sha256"))
		if !ok1 || !ok2 || !ok3 {
			return nil, errSchema
		}
		item := &artifactInput{Kind: kind, Content: content, Expected: exp}
		if idv, present := ao.Get("artifact_id"); present && idv != nil {
			s, ok := idv.(string)
			if !ok {
				return nil, errSchema
			}
			item.ArtifactID = &s
		}
		sub.Artifacts = append(sub.Artifacts, item)
	}
	if pv, present := o.Get("parents"); present {
		po, ok := pv.(*jsonx.Object)
		if !ok {
			return nil, errSchema
		}
		for _, kv := range po.Pairs() {
			if _, ok := digestValue(kv.Value); !ok {
				return nil, errSchema
			}
		}
		sub.Parents = po
	}
	cs, ok := digestValue(mustGet(o, "context_sha256"))
	if !ok {
		return nil, errSchema
	}
	sub.ContextSHA256 = cs
	for _, k := range sub.Parents.Keys() {
		if k != "specification" && k != "plan" && k != "outline" {
			return nil, derr("DESIGN_REQUEST_INVALID", "Unknown parent revision key")
		}
	}
	total := 0
	for _, a := range sub.Artifacts {
		n := len(a.Content)
		if n > orch.MaxArtifactBytes {
			return nil, derr("DESIGN_BOUNDS_EXCEEDED", "Artifact or batch exceeds the published byte bound")
		}
		total += n
	}
	if total > orch.MaxBatchBytes {
		return nil, derr("DESIGN_BOUNDS_EXCEEDED", "Artifact or batch exceeds the published byte bound")
	}
	return sub, nil
}

type preparedArtifact struct {
	item   *artifactInput
	path   string
	sha256 string
}

// prepare is design.authoring._prepare.
func (d *design) prepare(sub *submission) ([]*preparedArtifact, error) {
	var manifest *orch.WpsManifest
	var outline *string
	for _, a := range sub.Artifacts {
		if a.Kind == "outline" {
			c := a.Content
			outline = &c
			break
		}
	}
	var err error
	if outline != nil {
		manifest, err = d.validatedManifest(*outline)
	} else {
		manifest, err = d.loadManifest()
	}
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	var out []*preparedArtifact
	for _, a := range sub.Artifacts {
		p, err := d.artifactPath(a.Kind, a.ArtifactID, manifest)
		if err != nil {
			return nil, err
		}
		if seen[p] {
			return nil, derr("DESIGN_REQUEST_INVALID", "A batch cannot address a target twice")
		}
		seen[p] = true
		cur, err := contentDigest(p)
		if err != nil {
			return nil, err
		}
		if cur != a.Expected {
			return nil, derr("DESIGN_REVISION_CONFLICT", "Artifact revision changed", "kind", a.Kind, "current_sha256", cur)
		}
		if err := d.validateContent(a, manifest); err != nil {
			return nil, err
		}
		out = append(out, &preparedArtifact{item: a, path: p, sha256: orch.SHA256Hex([]byte(a.Content))})
	}
	return out, nil
}

// checkContextAndParents is design.authoring._check_context_and_parents.
func (d *design) checkContextAndParents(sub *submission, prepared []*preparedArtifact) (string, error) {
	actions := map[string]bool{}
	for _, p := range prepared {
		actions[artifactAction(p.item.Kind)] = true
	}
	if len(actions) != 1 {
		return "", derr("DESIGN_REQUEST_INVALID", "A submission batch belongs to one design stage")
	}
	var action string
	for a := range actions {
		action = a
	}
	dg, err := d.contextDigest(action)
	if err != nil {
		return "", err
	}
	if dg != sub.ContextSHA256 {
		return "", derr("DESIGN_CONTEXT_STALE", "Resolved template or governance context changed")
	}
	if err := d.resolvedInterviewAnswers(action); err != nil {
		return "", err
	}
	required := map[string]bool{}
	if action != "specify" {
		required["specification"] = true
	}
	if action == "tasks" {
		required["plan"] = true
	}
	prospective := map[string]string{}
	for _, p := range prepared {
		if p.item.Kind == "work_package" {
			required["outline"] = true
		}
		if p.item.Kind == "outline" {
			prospective["outline"] = p.sha256
		}
	}
	for _, k := range sub.Parents.Keys() {
		required[k] = true
	}
	for _, parent := range []string{"specification", "plan", "outline"} {
		if !required[parent] {
			continue
		}
		cur, inProspect := prospective[parent]
		if !inProspect || cur == "" {
			pp, err := d.artifactPath(parent, nil, nil)
			if err != nil {
				return "", err
			}
			if cur, err = contentDigest(pp); err != nil {
				return "", err
			}
		}
		want, has := sub.Parents.Get(parent)
		if cur == orch.Absent || !has || want != cur {
			return "", derr("DESIGN_PARENT_STALE", "Parent revision is missing or changed", "parent", parent, "current_sha256", cur)
		}
		if parent != "outline" || !inProspect {
			if err := d.requireCommittedContent(parent); err != nil {
				return "", err
			}
		}
	}
	return action, nil
}

// submitArtifacts is design.authoring.submit_artifacts.
func (d *design) submitArtifacts(raw, actor string) (*jsonx.Object, error) {
	sub, err := parseSubmission(raw)
	if err != nil {
		return nil, err
	}
	if actor == "" || len(actor) > orch.MaxActorBytes || isBlank(actor) {
		return nil, derr("DESIGN_REQUEST_INVALID", "An attributable bounded actor is required")
	}
	orch.TouchAuthoringLock(d.root, d.slug)
	if err := requireUnfinalized(d.m); err != nil {
		return nil, err
	}
	receipts, err := orch.ReadReceipts(d.root, d.slug)
	if err != nil {
		return nil, err
	}
	if receipts == nil {
		receipts = orch.NewReceiptSet()
	}
	prepared, err := d.prepare(sub)
	if err != nil {
		return nil, err
	}
	action, err := d.checkContextAndParents(sub, prepared)
	if err != nil {
		return nil, err
	}
	if prepared, err = d.prepare(sub); err != nil {
		return nil, err
	}
	if _, err := d.checkContextAndParents(sub, prepared); err != nil {
		return nil, err
	}
	if err := orch.EnableAuthoring(d.root, d.slug, receipts); err != nil {
		return nil, err
	}
	var files []string
	for _, p := range prepared {
		if err := os.MkdirAll(filepath.Dir(p.path), 0o755); err == nil {
			err = os.WriteFile(p.path+".tmp", []byte(p.item.Content), 0o644)
			if err == nil {
				err = os.Rename(p.path+".tmp", p.path)
			}
			if err != nil {
				return nil, derr("DESIGN_IO_FAILED", "Artifact materialization requires reconciliation", "effect_state", "reconciliation_required")
			}
		} else {
			return nil, derr("DESIGN_IO_FAILED", "Artifact materialization requires reconciliation", "effect_state", "reconciliation_required")
		}
		files = append(files, p.path)
	}
	msg := fmt.Sprintf("Record governed %s content for %s by %s", action, d.slug, actor)
	res := safecommit.CommitForMission(d.root, d.slug, files, msg, authoringKinds[prepared[0].item.Kind], safecommit.RouterOptions{})
	if res.Status != safecommit.StatusCommitted && res.Status != safecommit.StatusUnchanged {
		return nil, derr("DESIGN_COMMIT_FAILED", "Authored content materialized but the host commit did not complete",
			"effect_state", "reconciliation_required", "commit_status", res.Status, "surfaces", res.SurfacesPayload())
	}
	for _, p := range prepared {
		parents := jsonx.NewObject()
		for _, kv := range sub.Parents.Pairs() {
			if kv.Key != p.item.Kind {
				parents.Set(kv.Key, kv.Value)
			}
		}
		receipts.Put(artifactKey(p.item.Kind, p.item.ArtifactID), &orch.Receipt{Kind: p.item.Kind, ArtifactID: p.item.ArtifactID,
			SHA256: p.sha256, Parents: parents, ContextSHA256: sub.ContextSHA256, ContextAction: action, Actor: actor})
	}
	if err := orch.SaveReceipts(d.root, d.slug, receipts); err != nil {
		return nil, err
	}
	arts := []any{}
	for _, p := range prepared {
		var id any
		if p.item.ArtifactID != nil {
			id = *p.item.ArtifactID
		}
		arts = append(arts, jsonx.NewObject("kind", p.item.Kind, "artifact_id", id, "sha256", p.sha256))
	}
	var hash any
	if res.CommitHash != "" {
		hash = res.CommitHash
	}
	return jsonx.NewObject("artifacts", arts, "commit_status", res.Status, "commit_hash", hash,
		"provenance", "host_local_nonportable", "surfaces", res.SurfacesPayload()), nil
}

func isBlank(s string) bool {
	for _, r := range s {
		if r != ' ' && r != '\t' && r != '\n' && r != '\r' && r != '\v' && r != '\f' {
			return false
		}
	}
	return true
}

// requestBody is design_authoring._request_body.
func requestBody(ctx *cli.Context, raw string) (string, error) {
	if raw != "-" {
		return raw, nil
	}
	b, err := io.ReadAll(io.LimitReader(ctx.Stdin, orch.MaxRequestBytes+1))
	if err != nil {
		return "", derr("DESIGN_IO_FAILED", "Could not read bounded request input")
	}
	if len(b) > orch.MaxRequestBytes {
		return "", derr("DESIGN_BOUNDS_EXCEEDED", "Request exceeds the JSON transport bound")
	}
	if !utf8.Valid(b) {
		return "", derr("DESIGN_REQUEST_INVALID", "Request input must be UTF-8")
	}
	return string(b), nil
}

func runArtifactSubmit(ctx *cli.Context) error {
	v := newVerb(ctx, "artifact-submit")
	pol := optString(ctx, "policy")
	if pol == nil {
		return v.fail("POLICY_METADATA_REQUIRED", "--policy is required for artifact-submit", nil)
	}
	if _, err := v.parsePolicy(*pol); err != nil {
		return err
	}
	return v.invokeDesign(ctx, func(d *design) (*jsonx.Object, error) {
		body, err := requestBody(ctx, ctx.String("request-json"))
		if err != nil {
			return nil, err
		}
		return d.submitArtifacts(body, ctx.String("actor"))
	})
}
