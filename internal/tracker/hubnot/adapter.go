package hubnot

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

// BoundAdapter has no authority-bearing or unsupported future port assertions.
type BoundAdapter struct {
	config                        BindingConfig
	binding                       tracker.TrackerBinding
	identity                      PublicIdentity
	executableInfo, workspaceInfo os.FileInfo
}
type command struct {
	argv     []string
	input    []byte
	schema   string
	mutating bool
}
type commandResponse struct {
	envelope envelope
	outcome  MutationOutcome
}

func (b *BoundAdapter) execute(ctx context.Context, budget *observationBudget, c command) (commandResponse, error) {
	response := commandResponse{outcome: OutcomeUnstarted}
	fail := func(cause error, code, diagnostic string) (commandResponse, error) {
		return response, &CommandError{Outcome: response.outcome, ProviderCode: code, Diagnostic: diagnostic, Cause: cause}
	}
	if ctx == nil || budget == nil {
		return fail(invalid("context and logical budget are required", nil), "", "")
	}
	if err := ctx.Err(); err != nil {
		return fail(failure(tracker.ErrorTimeout, "bounded_retry", "command canceled before dispatch", err), "", "")
	}
	if err := budget.beforeDispatch(); err != nil {
		kind := tracker.ErrorUnavailable
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			kind = tracker.ErrorTimeout
		}
		return fail(failure(kind, "bounded_retry", "observation budget exhausted", err), "", "")
	}
	if err := b.verifyDispatch(); err != nil {
		return fail(err, "", "")
	}
	if c.mutating && b.binding.ReadOnly {
		return fail(failure(tracker.ErrorCapabilityUnsupported, "use_writable_binding", "binding is read-only", nil), "", "")
	}
	if len(c.input) > maxMutationBytes {
		return fail(invalid("encoded mutation exceeds 256 KiB", nil), "", "")
	}
	childctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(budget.ctx, cancel)
	defer func() { stop(); cancel() }()
	result := runProcessBudget(childctx, b.binding.ExecutablePath, b.binding.WorkspacePath, c.argv, c.input, budget)
	if result.started && c.mutating {
		response.outcome = OutcomeAmbiguous
	}
	budgetErr := budget.charge(0, 0)
	if result.err != nil || budgetErr != nil {
		cause := errors.Join(result.err, budgetErr)
		kind := tracker.ErrorUnavailable
		if errors.Is(cause, context.Canceled) || errors.Is(cause, context.DeadlineExceeded) {
			kind = tracker.ErrorTimeout
		}
		if c.mutating && result.started {
			kind = tracker.ErrorAmbiguous
		}
		return fail(failure(kind, "observe_then_reconcile", "provider command did not complete within its bounds", cause), "", string(result.stderr))
	}
	decoded, err := decodeEnvelope(result.stdout, c.schema, result.exitCode)
	if err != nil {
		return fail(protocolFailure(err), "", string(result.stderr))
	}
	response.envelope = decoded
	if !*decoded.OK {
		if c.mutating && provesNoWrite(decoded.Error.Code) {
			response.outcome = OutcomeNotApplied
		}
		return fail(providerFailure(decoded.Error.Code, decoded.Error.Message), decoded.Error.Code, string(result.stderr))
	}
	// Charge all returned items even if a later typed/semantic check rejects them.
	count := 1
	var object map[string]json.RawMessage
	if err := json.Unmarshal(decoded.Data, &object); err != nil || object == nil {
		return fail(protocolFailure(errors.New("response data must be object")), "", string(result.stderr))
	}
	if raw, present := object["items"]; present {
		var items []json.RawMessage
		if err := json.Unmarshal(raw, &items); err != nil || items == nil {
			return fail(protocolFailure(errors.New("page items must be array")), "", string(result.stderr))
		}
		count = len(items)
	}
	if err := budget.charge(0, count); err != nil {
		return fail(failure(tracker.ErrorUnavailable, "bounded_retry", "observation item budget exhausted", err), "", string(result.stderr))
	}
	if c.mutating {
		var mutation issueMutation
		if err := decodeStrict(decoded.Data, &mutation); err != nil || !validEventID(mutation.IssueID) || !validEventID(mutation.EventID) || mutation.HeadCount < 1 || mutation.Conflict != (mutation.HeadCount > 1) {
			return fail(protocolFailure(errors.New("invalid mutation result")), "", string(result.stderr))
		}
		response.outcome = OutcomeApplied
	} else if c.schema == issueSchema && decoded.Snapshot == "" {
		return fail(protocolFailure(errors.New("read snapshot missing")), "", string(result.stderr))
	}
	return response, nil
}

type query struct {
	kind, id, cursor, historyKind string
	limit                         int
}

func (q query) command() (command, error) {
	if q.limit == 0 {
		q.limit = 8
	}
	if q.limit < 1 || q.limit > 200 || len(q.cursor) > 4096 || !utf8.ValidString(q.cursor) || strings.ContainsRune(q.cursor, 0) {
		return command{}, invalid("invalid page request", nil)
	}
	args := []string{"issue", q.kind}
	switch q.kind {
	case "list":
		if q.id != "" {
			return command{}, invalid("list does not take issue ID", nil)
		}
		args = append(args, "--details")
	case "show", "heads", "history", "graph":
		if !validEventID(q.id) {
			return command{}, invalid("read requires full issue ID", nil)
		}
		args = append(args, q.id)
	default:
		return command{}, invalid("unsupported public query", nil)
	}
	args = append(args, "--json", "--limit", strconv.Itoa(q.limit))
	if q.cursor != "" {
		args = append(args, "--cursor", q.cursor)
	}
	if q.kind == "history" {
		if q.historyKind == "" {
			q.historyKind = "all"
		}
		if q.historyKind != "all" && q.historyKind != "revisions" && q.historyKind != "comments" {
			return command{}, invalid("invalid history kind", nil)
		}
		args = append(args, "--kind", q.historyKind)
	} else if q.historyKind != "" {
		return command{}, invalid("kind is only valid for history", nil)
	}
	return command{argv: args, schema: issueSchema}, nil
}
func readPage[T any](ctx context.Context, b *BoundAdapter, budget *observationBudget, q query) (page[T], envelope, error) {
	var result page[T]
	c, err := q.command()
	if err != nil {
		return result, envelope{}, err
	}
	response, err := b.execute(ctx, budget, c)
	if err != nil {
		return result, response.envelope, err
	}
	if err := decodeStrict(response.envelope.Data, &result); err != nil {
		return result, response.envelope, protocolFailure(err)
	}
	limit := q.limit
	if limit == 0 {
		limit = 8
	}
	if result.Items == nil || result.Total < len(result.Items) || len(result.Items) > limit || result.HeadCount < 0 || result.CycleCount < 0 || q.id != result.IssueID || (response.envelope.NextCursor != "" && len(result.Items) == 0) {
		return result, response.envelope, protocolFailure(errors.New("inconsistent page shape"))
	}
	return result, response.envelope, nil
}
func (b *BoundAdapter) listDetails(ctx context.Context, budget *observationBudget, cursor string) (page[issueDetail], envelope, error) {
	return readPage[issueDetail](ctx, b, budget, query{kind: "list", cursor: cursor})
}
func (b *BoundAdapter) headsPage(ctx context.Context, budget *observationBudget, id, cursor string) (page[issueHead], envelope, error) {
	return readPage[issueHead](ctx, b, budget, query{kind: "heads", id: id, cursor: cursor})
}
func (b *BoundAdapter) historyPage(ctx context.Context, budget *observationBudget, id, kind, cursor string) (page[issueHistoryItem], envelope, error) {
	return readPage[issueHistoryItem](ctx, b, budget, query{kind: "history", id: id, historyKind: kind, cursor: cursor})
}
func (b *BoundAdapter) graphPage(ctx context.Context, budget *observationBudget, id, cursor string) (page[issueGraphItem], envelope, error) {
	return readPage[issueGraphItem](ctx, b, budget, query{kind: "graph", id: id, cursor: cursor})
}
func (b *BoundAdapter) showIssue(ctx context.Context, budget *observationBudget, id, cursor string) (issueShow, envelope, error) {
	var result issueShow
	c, err := (query{kind: "show", id: id, cursor: cursor}).command()
	if err != nil {
		return result, envelope{}, err
	}
	response, err := b.execute(ctx, budget, c)
	if err != nil {
		return result, response.envelope, err
	}
	if err := decodeStrict(response.envelope.Data, &result); err != nil {
		return result, response.envelope, protocolFailure(err)
	}
	if result.ID != id || !validHex(result.Creator, 32) || result.HeadIDs == nil || result.HeadCount < len(result.HeadIDs) || result.HeadCount < 1 || result.Conflict != (result.HeadCount > 1) || result.RevisionCount < 1 || result.CommentCount < 0 || (result.Conflict != (result.State == nil)) {
		return result, response.envelope, protocolFailure(errors.New("invalid issue view"))
	}
	for _, head := range result.HeadIDs {
		if !validEventID(head) {
			return result, response.envelope, protocolFailure(errors.New("invalid head"))
		}
	}
	return result, response.envelope, nil
}
func validOperation(value string) bool {
	if len(value) < 1 || len(value) > 128 {
		return false
	}
	for _, v := range value {
		if v < 33 || v > 126 {
			return false
		}
	}
	return true
}
func (b *BoundAdapter) lookupOperation(ctx context.Context, budget *observationBudget, key string) (issueOperation, envelope, error) {
	var result issueOperation
	if !validOperation(key) {
		return result, envelope{}, invalid("invalid operation key", nil)
	}
	response, err := b.execute(ctx, budget, command{argv: []string{"issue", "operation", "--actor", b.config.ExpectedActor, "--operation", key, "--json"}, schema: issueSchema})
	if err != nil {
		return result, response.envelope, err
	}
	if err := decodeStrict(response.envelope.Data, &result); err != nil {
		return result, response.envelope, protocolFailure(err)
	}
	_, timeErr := time.Parse(time.RFC3339Nano, result.Timestamp)
	if result.Actor != b.config.ExpectedActor || result.Operation != key || !validEventID(result.IssueID) || !validEventID(result.EventID) || !validEventID(result.Request) || result.Parents == nil || len(result.Parents) > 200 || timeErr != nil {
		return result, response.envelope, protocolFailure(errors.New("invalid original operation identity"))
	}
	for i, p := range result.Parents {
		if !validEventID(p) || (i > 0 && result.Parents[i-1] >= p) {
			return result, response.envelope, protocolFailure(errors.New("invalid operation parents"))
		}
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(response.envelope.Data, &fields)
	_, hasState := fields["state"]
	_, hasBody := fields["body"]
	if result.Kind == "issue.comment" && hasState || result.Kind != "issue.comment" && hasBody {
		return result, response.envelope, protocolFailure(errors.New("forbidden original-operation member"))
	}
	valid := false
	switch result.Intent {
	case "open":
		valid = result.Kind == "issue.open" && result.State != nil && result.Body == nil && len(result.Parents) == 0 && result.IssueID == result.EventID
	case "comment":
		valid = result.Kind == "issue.comment" && result.State == nil && result.Body != nil && len(result.Parents) == 0
	case "revise", "close", "reopen":
		valid = result.Kind == "issue.revise" && result.State != nil && result.Body == nil && len(result.Parents) == 1
	case "resolve":
		valid = result.Kind == "issue.revise" && result.State != nil && result.Body == nil && len(result.Parents) >= 2
	}
	if !valid {
		return result, response.envelope, protocolFailure(errors.New("contradictory original operation semantics"))
	}
	return result, response.envelope, nil
}

type mutationRequest struct {
	intent, id, key string
	parents         []string
	state           *issueState
	body            string
	partial         bool
	snapshot        string
}

func (r mutationRequest) command(actor, name string) (command, error) {
	if !validHex(actor, 32) || !validOperation(r.key) {
		return command{}, invalid("mutation requires pinned actor and operation key", nil)
	}
	args := []string{"issue", r.intent}
	if r.intent != "open" {
		if !validEventID(r.id) {
			return command{}, invalid("mutation requires full issue ID", nil)
		}
		args = append(args, r.id)
	} else if r.id != "" {
		return command{}, invalid("open cannot name existing issue", nil)
	}
	args = append(args, "--json", "--actor", actor, "--operation", r.key)
	if len(r.parents) > 200 {
		return command{}, invalid("too many expected parents", nil)
	}
	parents := append([]string(nil), r.parents...)
	sort.Strings(parents)
	for i, p := range parents {
		if !validEventID(p) || (i > 0 && parents[i-1] == p) {
			return command{}, invalid("invalid or duplicate parent", nil)
		}
		args = append(args, "--expect", p)
	}
	var input []byte
	var err error
	switch r.intent {
	case "open", "revise", "resolve":
		if r.state == nil {
			return command{}, invalid("full issue state required", nil)
		}
		if err := validateState(*r.state); err != nil {
			return command{}, invalid("state violates native limits", err)
		}
		input, err = json.Marshal(r.state)
		if err != nil {
			return command{}, invalid("state encoding failed", err)
		}
		args = append(args, "--input", "-")
		if r.intent == "open" && len(parents) != 0 || r.intent == "revise" && len(parents) != 1 || r.intent == "resolve" && len(parents) < 2 {
			return command{}, invalid("wrong parent count", nil)
		}
	case "close", "reopen":
		if len(parents) != 1 || r.state != nil || r.body != "" {
			return command{}, invalid("lifecycle requires one parent only", nil)
		}
	case "comment":
		if len(parents) != 0 || r.state != nil || strings.TrimSpace(r.body) == "" || !utf8.ValidString(r.body) || len(r.body) > 65536 || strings.ContainsRune(r.body, 0) {
			return command{}, invalid("invalid comment", nil)
		}
		args = append(args, "--body", r.body)
	default:
		return command{}, invalid("unsupported mutation", nil)
	}
	if r.intent != "comment" && r.body != "" {
		return command{}, invalid("body belongs in full state", nil)
	}
	if r.partial || r.snapshot != "" {
		if r.intent != "resolve" || !r.partial || !validEventID(r.snapshot) {
			return command{}, invalid("partial resolution requires snapshot", nil)
		}
		args = append(args, "--partial", "--snapshot", r.snapshot)
	}
	// Reserve bounded signed-envelope overhead, including opening's duplicated
	// title/body and actor name. A request that cannot fit is refused, never cut.
	extra := r.body
	if r.intent == "open" {
		extra = r.state.Title + r.state.Body
	}
	encodedExtra, _ := json.Marshal(extra)
	encodedParents, _ := json.Marshal(parents)
	encodedName, _ := json.Marshal(name)
	if len(input)+len(encodedExtra)+len(encodedParents)+len(encodedName)+len(r.key)+1024 > maxMutationBytes {
		return command{}, invalid("encoded signed mutation exceeds native budget", nil)
	}
	return command{argv: args, input: input, schema: issueSchema, mutating: true}, nil
}
func (b *BoundAdapter) mutate(ctx context.Context, budget *observationBudget, request mutationRequest) (issueMutation, MutationOutcome, error) {
	var result issueMutation
	if b == nil {
		return result, OutcomeUnstarted, invalid("bound transport missing", nil)
	}
	c, err := request.command(b.config.ExpectedActor, b.identity.Name)
	if err != nil {
		return result, OutcomeUnstarted, err
	}
	response, err := b.execute(ctx, budget, c)
	if err != nil {
		return result, response.outcome, err
	}
	if err := decodeStrict(response.envelope.Data, &result); err != nil {
		return result, OutcomeAmbiguous, protocolFailure(err)
	}
	return result, response.outcome, nil
}
