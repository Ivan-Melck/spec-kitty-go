package tracker

import (
	"sort"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// TrackerError is a spec_kitty_tracker exception (SpecKittyTrackerError
// subclasses derive from Exception, so the CLI does not catch them unless
// the engine records them as a SyncFailure).
type TrackerError struct {
	Class        string // ConnectorRequestError, CapabilityNotSupportedError, IssuePayloadContractError, IssueNotFoundError
	Msg          string
	FailureClass string // ConnectorRequestError only
}

func (e *TrackerError) Error() string { return e.Msg }

func (e *TrackerError) retryable() bool {
	return e.Class == "ConnectorRequestError" && (e.FailureClass == "transient" || e.FailureClass == "rate_limit")
}

func requestError(msg string) error {
	return &TrackerError{Class: "ConnectorRequestError", Msg: msg, FailureClass: "non_retryable"}
}

func capabilityError(msg string) error {
	return &TrackerError{Class: "CapabilityNotSupportedError", Msg: msg}
}

func payloadError(msg string) error {
	return &TrackerError{Class: "IssuePayloadContractError", Msg: msg}
}

// AsCrash converts an uncaught tracker exception into a traceback crash.
func AsCrash(err error) error {
	if te, ok := err.(*TrackerError); ok {
		return &Crash{Type: te.Class, Msg: te.Msg}
	}
	return err
}

func nonAborting(err error) (*TrackerError, bool) {
	te, ok := err.(*TrackerError)
	if !ok {
		return nil, false
	}
	switch te.Class {
	case "ConnectorRequestError", "CapabilityNotSupportedError", "IssuePayloadContractError":
		return te, true
	}
	return nil, false
}

// Capabilities is TrackerCapabilities (the flags the engine consults).
type Capabilities struct {
	SupportsAssignment         bool
	SupportsTerminalTransition bool
}

// Page is one list_issues page.
type Page struct {
	Items      []*Issue
	NextCursor *string
}

// Patch is an outgoing field patch: the changed CORE_ISSUE_FIELDS with
// their values taken from Src.
type Patch struct {
	Fields []string
	Src    *Issue
}

// Has reports whether field is patched.
func (p *Patch) Has(field string) bool {
	for _, f := range p.Fields {
		if f == field {
			return true
		}
	}
	return false
}

func (p *Patch) del(field string) {
	out := p.Fields[:0]
	for _, f := range p.Fields {
		if f != field {
			out = append(out, f)
		}
	}
	p.Fields = out
}

// Connector is the TaskTrackerConnector surface the engine uses.
type Connector interface {
	Name() string
	Capabilities() Capabilities
	ListIssues(updatedSince *PyTime, cursor *string, limit int) (*Page, error)
	GetIssue(ref ExternalRef) (*Issue, error)
	CreateIssue(issue *Issue) (*Issue, error)
	UpdateIssue(ref ExternalRef, patch *Patch) (*Issue, error)
}

// Ownership owners.
const (
	OwnerExternal = "external"
	OwnerLocal    = "local"
	OwnerShared   = "shared"
)

// Policy is OwnershipPolicy.
type Policy struct {
	Mode        string
	FieldOwners map[string]string
}

// PolicyFor mirrors LocalTrackerService._build_engine's policy choice.
func PolicyFor(c ProjectConfig) Policy {
	mode := strings.ToLower(strings.TrimSpace(c.OwnershipMode))
	if mode == "" {
		mode = "external_authoritative"
	}
	switch mode {
	case "external_authoritative":
		return Policy{Mode: mode}
	case "spec_kitty_authoritative":
		return Policy{Mode: mode}
	}
	owners := map[string]string{}
	if c.FieldOwners != nil {
		for _, kv := range c.FieldOwners.Pairs() {
			v, _ := kv.Value.(string)
			if v == OwnerExternal || v == OwnerLocal || v == OwnerShared {
				owners[kv.Key] = v
			}
		}
	}
	return Policy{Mode: "split_ownership", FieldOwners: owners}
}

// OwnerFor is owner_for.
func (p Policy) OwnerFor(field string) string {
	switch p.Mode {
	case "external_authoritative":
		return OwnerExternal
	case "spec_kitty_authoritative":
		return OwnerLocal
	}
	if o, ok := p.FieldOwners[field]; ok {
		return o
	}
	return OwnerShared
}

// LocalCanWrite is local_can_write.
func (p Policy) LocalCanWrite(field string) bool {
	o := p.OwnerFor(field)
	return o == OwnerLocal || o == OwnerShared
}

// Conflict is a ConflictRecord (the fields the CLI renders).
type Conflict struct {
	Field  string
	Manual bool
}

// Failure is SyncFailure.
type Failure struct {
	Ref       *ExternalRef
	Operation string
	Message   string
}

func (f Failure) String() string {
	ref := "<none>"
	if f.Ref != nil {
		ref = f.Ref.Identity()
	}
	return f.Operation + "[" + ref + "]: " + f.Message
}

// Stats is SyncStats.
type Stats struct {
	PulledCreated, PulledUpdated, PushedCreated, PushedUpdated, Skipped int
}

// SyncResult is spec_kitty_tracker.sync.SyncResult.
type SyncResult struct {
	Stats     Stats
	Conflicts []Conflict
	Errors    []string
	Failures  []Failure
}

// JSON renders LocalTrackerService._sync_result.
func (r *SyncResult) JSON(provider string) *jsonx.Object {
	conflicts := []any{}
	for _, c := range r.Conflicts {
		conflicts = append(conflicts, jsonx.NewObject("field_name", c.Field, "strategy", "newer_timestamp", "manual_review_required", c.Manual))
	}
	errs := []any{}
	for _, e := range r.Errors {
		errs = append(errs, e)
	}
	return jsonx.NewObject(
		"provider", provider,
		"stats", jsonx.NewObject("pulled_created", r.Stats.PulledCreated, "pulled_updated", r.Stats.PulledUpdated,
			"pushed_created", r.Stats.PushedCreated, "pushed_updated", r.Stats.PushedUpdated, "skipped", r.Stats.Skipped),
		"conflicts", conflicts,
		"errors", errs,
	)
}

// Engine is SyncEngine with the CLI's defaults (NEWER_TIMESTAMP strategy,
// no strict manual review, two retries for retryable request errors).
type Engine struct {
	Connector  Connector
	Store      *Store
	Policy     Policy
	Checkpoint Checkpoint
}

const maxRetryAttempts = 2

func (e *Engine) listWithRetry(since *PyTime, cursor *string, limit int) (*Page, error) {
	for attempt := 0; ; attempt++ {
		p, err := e.Connector.ListIssues(since, cursor, limit)
		if err == nil {
			return p, nil
		}
		te, ok := err.(*TrackerError)
		if !ok || !te.retryable() || attempt >= maxRetryAttempts {
			return nil, err
		}
	}
}

func dedupe(items []*Issue) ([]*Issue, error) {
	by := map[string]*Issue{}
	var order []string
	for _, it := range items {
		id := it.Ref.Identity()
		ex, ok := by[id]
		if !ok {
			order = append(order, id)
			by[id] = it
			continue
		}
		if it.UpdatedAt != nil {
			replace := ex.UpdatedAt == nil
			if !replace {
				less, err := timeLess(*it.UpdatedAt, *ex.UpdatedAt)
				if err != nil {
					return nil, err
				}
				replace = !less
			}
			if replace {
				by[id] = it
			}
		}
	}
	out := make([]*Issue, 0, len(order))
	for _, id := range order {
		out = append(out, by[id])
	}
	return out, nil
}

// Pull is SyncEngine.pull.
func (e *Engine) Pull(limit int) (*SyncResult, error) {
	res := &SyncResult{}
	cursor := e.Checkpoint.Cursor
	since := e.Checkpoint.UpdatedSince
	maxUpd := e.Checkpoint.UpdatedSince
	for {
		page, err := e.listWithRetry(since, cursor, limit)
		if err != nil {
			te, ok := err.(*TrackerError)
			if !ok || te.Class != "ConnectorRequestError" {
				return nil, err
			}
			res.Failures = append(res.Failures, Failure{Operation: "pull_page", Message: te.Msg})
			break
		}
		items, err := dedupe(page.Items)
		if err != nil {
			return nil, err
		}
		for _, ext := range items {
			if ext.UpdatedAt != nil {
				newer := maxUpd == nil
				if !newer {
					l, err := timeLess(*maxUpd, *ext.UpdatedAt)
					if err != nil {
						return nil, err
					}
					newer = l
				}
				if newer {
					maxUpd = ext.UpdatedAt
				}
			}
			local, err := e.Store.GetIssue(ext.Ref)
			if err != nil {
				return nil, err
			}
			if local == nil {
				if err := e.Store.UpsertIssue(ext); err != nil {
					return nil, err
				}
				res.Stats.PulledCreated++
				continue
			}
			merged, conflicts, err := e.merge(local, ext)
			if err != nil {
				return nil, err
			}
			res.Conflicts = append(res.Conflicts, conflicts...)
			if err := e.Store.UpsertIssue(merged); err != nil {
				return nil, err
			}
			if !merged.Equal(local) {
				res.Stats.PulledUpdated++
			} else {
				res.Stats.Skipped++
			}
		}
		cursor = page.NextCursor
		e.Checkpoint = Checkpoint{Cursor: cursor, UpdatedSince: maxUpd}
		if cursor == nil {
			break
		}
	}
	res.Errors = failureStrings(res.Failures)
	return res, nil
}

func failureStrings(fs []Failure) []string {
	out := []string{}
	for _, f := range fs {
		out = append(out, f.String())
	}
	return out
}

var createEmptyDefaults = []string{"assignees", "labels", "links", "custom_fields", "priority", "parent"}

func isEmptyField(i *Issue, f string) bool {
	switch f {
	case "assignees":
		return len(i.Assignees) == 0
	case "labels":
		return len(i.Labels) == 0
	case "links":
		return len(i.Links) == 0
	case "custom_fields":
		return i.CustomFields == nil || i.CustomFields.Len() == 0
	case "priority":
		return i.Priority == nil
	case "parent":
		return i.Parent == nil
	}
	return true
}

var forbiddenLegacyKeys = map[string]bool{"feature_slug": true, "feature_number": true, "mission_key": true}

func findLegacyKeys(v any, found map[string]bool) {
	switch t := v.(type) {
	case *jsonx.Object:
		for _, kv := range t.Pairs() {
			if forbiddenLegacyKeys[kv.Key] {
				found[kv.Key] = true
			}
			findLegacyKeys(kv.Value, found)
		}
	case []any:
		for _, x := range t {
			findLegacyKeys(x, found)
		}
	}
}

func legacyFailure(ref ExternalRef, cf *jsonx.Object, op string) *Failure {
	found := map[string]bool{}
	findLegacyKeys(jsonx.NewObject("custom_fields", orEmpty(cf)), found)
	if len(found) == 0 {
		return nil
	}
	keys := make([]string, 0, len(found))
	for k := range found {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	r := ref
	return &Failure{Ref: &r, Operation: op, Message: "Forbidden TeamSpace legacy keys in tracker payload: " + strings.Join(keys, ", ")}
}

// Push is SyncEngine.push.
func (e *Engine) Push(limit int) (*SyncResult, error) {
	res := &SyncResult{}
	index := map[string]*Issue{}
	var cursor *string
	for {
		page, err := e.Connector.ListIssues(nil, cursor, limit)
		if err != nil {
			return nil, err
		}
		for _, it := range page.Items {
			index[it.Ref.Identity()] = it
		}
		if page.NextCursor == nil {
			break
		}
		cursor = page.NextCursor
	}
	locals, err := e.Store.ListIssues(e.Connector.Name(), false)
	if err != nil {
		return nil, err
	}
	caps := e.Connector.Capabilities()
	for _, local := range locals {
		ref := local.Ref
		remote := index[ref.Identity()]
		if remote == nil {
			if !e.Policy.LocalCanWrite("title") {
				res.Stats.Skipped++
				continue
			}
			violated := ""
			for _, f := range createEmptyDefaults {
				if !e.Policy.LocalCanWrite(f) && !isEmptyField(local, f) {
					violated = f
					break
				}
			}
			if violated != "" {
				res.Failures = append(res.Failures, Failure{Ref: &ref, Operation: "create", Message: "create denied: local does not own " + pyjson.ReprString(violated) + " and it is not empty"})
				continue
			}
			if lf := legacyFailure(ref, local.CustomFields, "create"); lf != nil {
				res.Failures = append(res.Failures, *lf)
				continue
			}
			created, err := e.createWithRetry(local)
			if err != nil {
				if te, ok := nonAborting(err); ok {
					res.Failures = append(res.Failures, Failure{Ref: &ref, Operation: "create", Message: te.Msg})
					continue
				}
				return nil, err
			}
			if err := e.Store.UpsertIssue(created); err != nil {
				return nil, err
			}
			res.Stats.PushedCreated++
			continue
		}
		desired, conflicts, err := e.merge(local, remote)
		if err != nil {
			return nil, err
		}
		res.Conflicts = append(res.Conflicts, conflicts...)
		patch := &Patch{Src: desired}
		for _, f := range CoreIssueFields {
			if !remote.fieldEq(f, desired) {
				patch.Fields = append(patch.Fields, f)
			}
		}
		if patch.Has("assignees") && !caps.SupportsAssignment {
			patch.del("assignees")
			res.Failures = append(res.Failures, Failure{Ref: &ref, Operation: "patch_field_denied", Message: "assignees excluded: connector does not support assignment"})
		}
		if patch.Has("status") && (desired.Status == "done" || desired.Status == "canceled") && !caps.SupportsTerminalTransition {
			patch.del("status")
			res.Failures = append(res.Failures, Failure{Ref: &ref, Operation: "patch_field_denied", Message: "status excluded: connector does not support a terminal transition"})
		}
		if len(patch.Fields) == 0 {
			if !desired.Equal(local) {
				if err := e.Store.UpsertIssue(desired); err != nil {
					return nil, err
				}
			}
			res.Stats.Skipped++
			continue
		}
		if patch.Has("custom_fields") {
			if lf := legacyFailure(ref, desired.CustomFields, "update"); lf != nil {
				res.Failures = append(res.Failures, *lf)
				continue
			}
		}
		updated, err := e.updateWithRetry(ref, patch)
		if err != nil {
			if te, ok := nonAborting(err); ok {
				res.Failures = append(res.Failures, Failure{Ref: &ref, Operation: "update", Message: te.Msg})
				continue
			}
			return nil, err
		}
		mergedLocal, post, err := e.merge(local, updated)
		if err != nil {
			return nil, err
		}
		res.Conflicts = append(res.Conflicts, post...)
		if err := e.Store.UpsertIssue(mergedLocal); err != nil {
			return nil, err
		}
		res.Stats.PushedUpdated++
	}
	res.Errors = failureStrings(res.Failures)
	return res, nil
}

func (e *Engine) createWithRetry(issue *Issue) (*Issue, error) {
	for attempt := 0; ; attempt++ {
		out, err := e.Connector.CreateIssue(issue)
		if err == nil {
			return out, nil
		}
		te, ok := err.(*TrackerError)
		if !ok || !te.retryable() || attempt >= maxRetryAttempts {
			return nil, err
		}
	}
}

func (e *Engine) updateWithRetry(ref ExternalRef, patch *Patch) (*Issue, error) {
	for attempt := 0; ; attempt++ {
		out, err := e.Connector.UpdateIssue(ref, patch)
		if err == nil {
			return out, nil
		}
		te, ok := err.(*TrackerError)
		if !ok || !te.retryable() || attempt >= maxRetryAttempts {
			return nil, err
		}
	}
}

// Sync is SyncEngine.sync (pull first, then push with limit >= 100).
func (e *Engine) Sync(limit int) (*SyncResult, error) {
	pull, err := e.Pull(limit)
	if err != nil {
		return nil, err
	}
	pushLimit := limit
	if pushLimit < 100 {
		pushLimit = 100
	}
	push, err := e.Push(pushLimit)
	if err != nil {
		return nil, err
	}
	return &SyncResult{
		Stats: Stats{PulledCreated: pull.Stats.PulledCreated, PulledUpdated: pull.Stats.PulledUpdated,
			PushedCreated: push.Stats.PushedCreated, PushedUpdated: push.Stats.PushedUpdated,
			Skipped: pull.Stats.Skipped + push.Stats.Skipped},
		Conflicts: append(append([]Conflict{}, pull.Conflicts...), push.Conflicts...),
		Errors:    append(append([]string{}, pull.Errors...), push.Errors...),
		Failures:  append(append([]Failure{}, pull.Failures...), push.Failures...),
	}, nil
}

func (e *Engine) merge(local, ext *Issue) (*Issue, []Conflict, error) {
	merged := local.Clone()
	var conflicts []Conflict
	for _, f := range CoreIssueFields {
		if local.fieldEq(f, ext) {
			merged.copyField(f, local)
			continue
		}
		switch e.Policy.OwnerFor(f) {
		case OwnerLocal:
			merged.copyField(f, local)
			continue
		case OwnerExternal:
			merged.copyField(f, ext)
			continue
		}
		// shared field, NEWER_TIMESTAMP strategy
		useExt := true
		switch {
		case local.UpdatedAt == nil:
		case ext.UpdatedAt == nil:
			useExt = false
		default:
			less, err := timeLess(*ext.UpdatedAt, *local.UpdatedAt)
			if err != nil {
				return nil, nil, err
			}
			useExt = !less
		}
		if useExt {
			merged.copyField(f, ext)
		} else {
			merged.copyField(f, local)
		}
		conflicts = append(conflicts, Conflict{Field: f})
	}
	if merged.CreatedAt == nil {
		if ext.CreatedAt != nil {
			merged.CreatedAt = ext.CreatedAt
		} else {
			merged.CreatedAt = local.CreatedAt
		}
	}
	switch {
	case local.UpdatedAt == nil:
		merged.UpdatedAt = ext.UpdatedAt
	case ext.UpdatedAt == nil:
		merged.UpdatedAt = local.UpdatedAt
	default:
		less, err := timeLess(*local.UpdatedAt, *ext.UpdatedAt)
		if err != nil {
			return nil, nil, err
		}
		if less {
			merged.UpdatedAt = ext.UpdatedAt
		} else {
			merged.UpdatedAt = local.UpdatedAt
		}
	}
	if ext.Raw != nil {
		merged.Raw = ext.Raw
	} else {
		merged.Raw = local.Raw
	}
	return merged, conflicts, nil
}
