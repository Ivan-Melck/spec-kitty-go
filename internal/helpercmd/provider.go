package helpercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"syscall"
	"time"

	"github.com/spec-kitty/go-kitty3/internal/tracker"
	"github.com/spec-kitty/go-kitty3/internal/tracker/hubnot"
)

// This boundary consumes only neutral ports after the admitted binding is opened.
// It neither opens the mission host nor grants execution authority.
type trackerConsumer interface {
	tracker.Catalog
	tracker.WorkDefinitionPort
	tracker.Graph
	tracker.AssignmentProjection
	tracker.AttributedDiscussionPort
	tracker.InventoryPort
	tracker.Inspector
}

const trackerResultSchema = "go-kitty.tracker-result/1"

// Whole-output bound, distinct from the provider's 8 MiB child response bound.
// Includes complete structured values and base64 canonical proof, never a page
// prefix. Inventory's upstream cumulative byte/item/deadline limits still apply.
const maxTrackerResultBytes = 256 << 20

type trackerEnvelope struct {
	Schema     string                    `json:"schema"`
	Action     string                    `json:"action"`
	Outcome    Outcome                   `json:"outcome"`
	Result     any                       `json:"result,omitempty"`
	Diagnostic *trackerCommandDiagnostic `json:"diagnostic,omitempty"`
	Mutation   *trackerMutationFacts     `json:"mutation,omitempty"`
}
type trackerMutationFacts struct {
	Outcome         hubnot.MutationOutcome `json:"outcome"`
	PriorMutation   bool                   `json:"prior_mutation"`
	AppliedThisCall bool                   `json:"applied_this_call"`
}
type trackerCommandDiagnostic struct {
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	Recovery string `json:"recovery"`
}
type trackerProofDTO struct {
	Kind                     string           `json:"kind"`
	ExternalID               string           `json:"external_id"`
	Revision                 tracker.Revision `json:"revision"`
	Digest                   string           `json:"digest"`
	ProviderToken            string           `json:"provider_token"`
	CanonicalFields          []byte           `json:"canonical_fields_base64"`
	ObservedAt               time.Time        `json:"observed_at"`
	ProviderUpdatedAt        time.Time        `json:"provider_updated_at"`
	ProviderUpdatedAtPresent bool             `json:"provider_updated_at_present"`
	Assignee                 string           `json:"assignee"`
	OperationMarker          string           `json:"operation_marker"`
}

func proofDTO(p tracker.ProviderObservation) trackerProofDTO {
	raw := p.CanonicalFields()
	var domain struct {
		Kind   string `json:"kind"`
		Schema string `json:"schema"`
	}
	_ = json.Unmarshal(raw, &domain)
	kind := domain.Kind
	if kind == "" {
		kind = domain.Schema
	}
	return trackerProofDTO{kind, p.ExternalID, p.Revision, p.Digest, p.ProviderToken, raw, p.ObservedAt, p.ProviderUpdatedAt, p.ProviderUpdatedAtPresent, p.Assignee, p.OperationMarker}
}

type trackerDefinitionDTO struct {
	ProjectID            string                  `json:"project_id"`
	WorkID               string                  `json:"work_id"`
	Title                string                  `json:"title"`
	Description          string                  `json:"description"`
	DesignContext        string                  `json:"design_context"`
	CrossDomainReason    string                  `json:"cross_domain_reason"`
	Criteria             []tracker.WorkCriterion `json:"criteria"`
	Dependencies         []string                `json:"dependencies"`
	WriteScopes          []tracker.WriteScope    `json:"write_scopes"`
	Provenance           []tracker.ScopeRef      `json:"provenance"`
	MetadataSchema       uint16                  `json:"metadata_schema"`
	ProviderMetadataJSON string                  `json:"provider_metadata_json"`
}

func definitionDTO(d tracker.WorkDefinition) trackerDefinitionDTO {
	deps := make([]string, 0, len(d.Dependencies()))
	for _, id := range d.Dependencies() {
		deps = append(deps, id.String())
	}
	opaque := d.ProviderMetadata().Bytes()
	if len(opaque) == 0 {
		opaque = []byte("{}")
	}
	return trackerDefinitionDTO{d.ProjectID().String(), d.WorkID().String(), d.Title(), d.Description(), d.DesignContext(), d.CrossDomainReason(), d.Criteria(), deps, d.WriteScopes(), d.Provenance(), d.MetadataSchema(), string(opaque)}
}
func definitionObservationDTO(o tracker.WorkDefinitionObservation) any {
	return map[string]any{"definition": definitionDTO(o.Definition()), "external_ref": referenceDTO(o.ExternalRef()), "proof": proofDTO(o.ProviderObservation()), "observed_at": o.ObservedAt()}
}
func dependencyDTO(d tracker.DependencyRef) any {
	return map[string]any{"blocked_work_id": d.BlockedWorkID, "blocking_work_id": d.BlockingWorkID, "kind": d.Kind, "external_edge_id": d.ExternalEdgeID}
}
func dependenciesDTO(ds []tracker.DependencyRef) []any {
	out := make([]any, 0, len(ds))
	for _, d := range ds {
		out = append(out, dependencyDTO(d))
	}
	return out
}
func discussionEntryDTO(e tracker.DiscussionEntry) map[string]any {
	return map[string]any{"external_comment_id": e.ExternalCommentID, "author": e.Author, "body": e.Body, "operation_marker": e.OperationMarker, "created_at": e.CreatedAt, "observation": e.Observation}
}
func attributedEntryDTO(e tracker.AttributedDiscussionEntry, includeProof bool) any {
	out := discussionEntryDTO(e.Entry())
	out["signing_actor"] = e.SigningActor()
	if includeProof {
		out["proof"] = proofDTO(e.Proof())
	}
	return out
}
func recordDTO(r tracker.WorkRecord) any {
	criteria := make([]providerCriterion, 0, len(r.Criteria()))
	for _, c := range r.Criteria() {
		criteria = append(criteria, providerCriterion{c.ID, c.Text, c.Verifier, c.Required, c.EvidenceHint})
	}
	entries := make([]any, 0, len(r.Discussion()))
	for _, e := range r.Discussion() {
		entries = append(entries, discussionEntryDTO(e))
	}
	return map[string]any{"external_ref": referenceDTO(r.ExternalRef), "title": r.Title, "description": r.Description, "design_context": r.DesignContext, "visible_assignee": r.VisibleAssignee, "assignment_marker": r.AssignmentMarker, "criteria": criteria, "dependencies": dependenciesDTO(r.Dependencies()), "discussion": entries, "proof": proofDTO(r.Observation)}
}
func assignmentDTO(o tracker.AssignmentObservation) any {
	return map[string]any{"external_ref": referenceDTO(o.ExternalRef), "assignee": o.Assignee, "operation_marker": o.OperationMarker, "proof": proofDTO(o.Observation)}
}
func diagnosticsDTO(ds []tracker.Diagnostic) []any {
	out := make([]any, 0, len(ds))
	for _, d := range ds {
		out = append(out, map[string]any{"kind": d.Kind, "recovery_code": d.RecoveryCode, "message": d.Message, "capability": d.Capability, "external_id": d.ExternalID, "expected_revision": d.ExpectedRevision, "observed_revision": d.ObservedRevision})
	}
	return out
}
func bindingDTO(b tracker.TrackerBinding) any {
	return map[string]any{"project_id": b.ProjectID, "provider": b.Provider, "executable_path": b.ExecutablePath, "executable_version": b.ExecutableVersion, "executable_digest": b.ExecutableDigest, "protocol_schema": b.ProtocolSchema, "workspace_path": b.WorkspacePath, "workspace_identity": b.WorkspaceIdentity, "concurrency_mode": b.ConcurrencyMode, "read_only": b.ReadOnly, "observed_at": b.ObservedAt}
}

func invokeTracker(ctx context.Context, p trackerConsumer, action string, r decodedProviderRequest) (any, error) {
	switch action {
	case "inspect":
		v, e := p.Inspect(ctx, tracker.InspectRequest{ProjectID: r.data.ProjectID})
		if e != nil {
			return nil, e
		}
		return map[string]any{"binding": bindingDTO(v.Binding), "capabilities": v.Capabilities, "health": v.Health, "health_scope": "public_transport_and_binding", "diagnostics": diagnosticsDTO(v.Diagnostics())}, nil
	case "inventory":
		v, e := p.Inventory(ctx, tracker.InventoryRequest{ProjectID: r.data.ProjectID})
		if e != nil {
			return nil, e
		}
		values := v.Items()
		items := make([]any, 0, len(values))
		for _, item := range values {
			items = append(items, map[string]any{"definition": definitionDTO(item.Definition()), "external_ref": referenceDTO(item.ExternalRef())})
		}
		return map[string]any{"project_id": v.ProjectID(), "items": items, "common_proof": proofDTO(v.CommonProof())}, nil
	case "read":
		v, e := p.ReadWork(ctx, r.reference)
		if e != nil {
			return nil, e
		}
		return recordDTO(v), nil
	case "create":
		v, e := p.CreateWork(ctx, r.create)
		if e != nil {
			return nil, e
		}
		return map[string]any{"record": recordDTO(v.Record), "guarantee": v.Guarantee, "replayed": v.Replayed, "noop": v.Noop}, nil
	case "update":
		v, e := p.UpdateWork(ctx, r.update)
		if e != nil {
			return nil, e
		}
		return map[string]any{"record": recordDTO(v.Record), "guarantee": v.Guarantee, "replayed": v.Replayed, "noop": v.Noop}, nil
	case "define":
		v, e := p.CreateDefinition(ctx, r.define)
		if e != nil {
			return nil, e
		}
		return map[string]any{"observation": definitionObservationDTO(v.Observation), "replayed": v.Replayed, "noop": v.Noop, "prior_mutation": v.PriorMutation}, nil
	case "definition":
		v, e := p.ObserveDefinition(ctx, r.reference)
		if e != nil {
			return nil, e
		}
		return definitionObservationDTO(v), nil
	case "assign":
		if r.data.Mode == "observe" {
			v, e := p.ObserveAssignment(ctx, r.reference)
			if e != nil {
				return nil, e
			}
			return map[string]any{"mode": "observe", "observation": assignmentDTO(v)}, nil
		}
		v, e := p.ProjectAssignment(ctx, r.assignment)
		if e != nil {
			return nil, e
		}
		return map[string]any{"mode": "project", "observation": assignmentDTO(v.Observation), "guarantee": v.Guarantee, "replayed": v.Replayed, "noop": v.Noop}, nil
	case "comment":
		v, e := p.AppendAttributedDiscussion(ctx, r.comment)
		if e != nil {
			return nil, e
		}
		return map[string]any{"entry": attributedEntryDTO(v.Entry, true), "replayed": v.Replayed, "noop": v.Noop, "prior_mutation": v.PriorMutation}, nil
	case "discussion":
		v, e := p.ObserveDiscussion(ctx, r.reference)
		if e != nil {
			return nil, e
		}
		values := v.Entries()
		common := proofDTO(v.CommonProof())
		entries := make([]any, 0, len(values))
		for _, entry := range values {
			row := attributedEntryDTO(entry, false).(map[string]any)
			row["proof_ref"] = map[string]any{"kind": common.Kind, "digest": common.Digest, "revision": common.Revision}
			entries = append(entries, row)
		}
		return map[string]any{"entries": entries, "common_proof": common, "current_work_observation": proofDTO(v.CurrentWorkObservation())}, nil
	case "dependencies":
		v, e := p.ListDependencies(ctx, r.reference)
		if e != nil {
			return nil, e
		}
		return map[string]any{"items": dependenciesDTO(v), "proof_scope": "portable_relations_only"}, nil
	case "link", "unlink":
		var v tracker.DependencyMutation
		var e error
		if action == "link" {
			v, e = p.PutBlockingDependency(ctx, r.dependency)
		} else {
			v, e = p.RemoveBlockingDependency(ctx, r.dependency)
		}
		if e != nil {
			return nil, e
		}
		return map[string]any{"dependency": dependencyDTO(v.Dependency), "proof": proofDTO(v.Observation), "replayed": v.Replayed, "noop": v.Noop}, nil
	case "ready":
		v, e := p.ReadyProjection(ctx, r.ready)
		if e != nil {
			return nil, e
		}
		result := map[string]any{"provider_ready_ids": v.ProviderReadyIDs, "diagnostics": diagnosticsDTO(v.Diagnostics), "proof": nil, "availability": "unavailable", "authority": "diagnostic_only"}
		// The neutral readiness port can report unavailable graph diagnostics
		// without an observation. Preserve that absence rather than projecting
		// the zero value into a proof-shaped object.
		if v.Observation.Revision != 0 && len(v.Observation.CanonicalFields()) > 0 {
			result["proof"] = proofDTO(v.Observation)
			result["availability"] = "available"
		}
		return result, nil
	}
	return nil, errors.New("unknown tracker action")
}
func trackerFailure(action string, err error) trackerEnvelope {
	result := trackerEnvelope{Schema: trackerResultSchema, Action: action, Outcome: OutcomeUnavailable, Diagnostic: &trackerCommandDiagnostic{"unavailable", "Tracker command could not complete.", "Inspect the existing binding and retry the same operation ID."}}
	// Preserve outer saga facts before inspecting an unwrapped provider cause.
	var operation *hubnot.OperationError
	if errors.As(err, &operation) {
		result.Mutation = &trackerMutationFacts{operation.Outcome, operation.PriorMutation, operation.AppliedThisCall}
	} else {
		var command *hubnot.CommandError
		if errors.As(err, &command) {
			result.Mutation = &trackerMutationFacts{Outcome: command.Outcome}
		}
	}
	var typed *tracker.Error
	if errors.As(err, &typed) {
		result.Diagnostic.Kind = string(typed.Kind)
		switch typed.Kind {
		case tracker.ErrorInvalidRequest:
			result.Outcome = OutcomeInvalid
			result.Diagnostic.Message = "The tracker request or binding is invalid."
			result.Diagnostic.Recovery = "Check tracker --help. Confirm the binding names an existing workspace and admitted executable, and repair the request or binding."
		case tracker.ErrorNotFound:
			result.Outcome = OutcomeNotFound
			result.Diagnostic.Message = "The mapped work or original operation was not found."
			result.Diagnostic.Recovery = "Inspect inventory and reconcile the immutable reference."
		case tracker.ErrorRevisionConflict:
			result.Outcome = OutcomeConflict
			result.Diagnostic.Message = "The current observation does not match the request."
			result.Diagnostic.Recovery = "Read the work again, reconcile changes, and use its current proof revision."
		case tracker.ErrorAttributionLost, tracker.ErrorAmbiguous:
			result.Outcome = OutcomeAmbiguous
			result.Diagnostic.Message = "Operation or work attribution could not be proven."
			result.Diagnostic.Recovery = "Reconcile native history without mutation; preserve the original operation ID."
		case tracker.ErrorUnavailable, tracker.ErrorTimeout, tracker.ErrorLockContended, tracker.ErrorMalformedProtocol:
			result.Outcome = OutcomeUnavailable
			result.Diagnostic.Message = "The provider is unavailable or its response could not be verified."
			result.Diagnostic.Recovery = "Inspect the provider and preserve the original operation ID before retrying."
		default:
			result.Outcome = OutcomeRefused
			result.Diagnostic.Message = "The binding or requested guarantee is not supported."
			result.Diagnostic.Recovery = "Run tracker inspect with the admitted executable and correct existing binding; do not initialize or rebind automatically."
		}
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		result.Outcome = OutcomeCanceled
		result.Diagnostic = &trackerCommandDiagnostic{"canceled", "Tracker command was canceled.", "Inspect the operation before retrying with the same operation ID."}
	}
	if result.Mutation != nil && (result.Mutation.Outcome == hubnot.OutcomeAmbiguous || result.Mutation.PriorMutation || result.Mutation.AppliedThisCall) {
		result.Diagnostic.Recovery = "Some effects may already exist. Preserve the operation ID and original request; inspect native history, then retry that same request."
		if result.Mutation.Outcome == hubnot.OutcomeAmbiguous {
			result.Outcome = OutcomeAmbiguous
		}
	}
	return result
}
func trackerInvalid(action, field string) trackerEnvelope {
	return trackerEnvelope{Schema: trackerResultSchema, Action: action, Outcome: OutcomeInvalid, Diagnostic: &trackerCommandDiagnostic{"invalid_request", "Invalid " + field + ".", "Run tracker --help for request fields and recovery guidance."}}
}
func runTracker(ctx context.Context, options globalOptions, stdin io.Reader, stdout, stderr io.Writer) int {
	action := ""
	if len(options.args) > 0 {
		action = options.args[0]
	}
	if action == "" || action == "help" || action == "--help" || action == "-h" {
		return writeTrackerHelp(ctx, stdout)
	}
	if _, ok := providerActions[action]; !ok {
		return renderTrackerContext(ctx, stdout, stderr, options.output, trackerInvalid(action, "action"))
	}
	flags := flag.NewFlagSet("tracker "+action, flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	var bindingPath, inputPath string
	flags.StringVar(&bindingPath, "binding", "", "existing explicit binding file")
	flags.StringVar(&inputPath, "input", "", "request file, or - for stdin")
	if err := flags.Parse(options.args[1:]); errors.Is(err, flag.ErrHelp) {
		return writeTrackerHelp(ctx, stdout)
	} else if err != nil || flags.NArg() != 0 || bindingPath == "" {
		return renderTrackerContext(ctx, stdout, stderr, options.output, trackerInvalid(action, "arguments"))
	}
	file, err := openTrackerRegularFile(bindingPath)
	if err != nil {
		return renderTrackerContext(ctx, stdout, stderr, options.output, trackerInvalid(action, "binding file"))
	}
	config, err := hubnot.DecodeBinding(file)
	_ = file.Close()
	if err != nil {
		return renderTrackerContext(ctx, stdout, stderr, options.output, trackerFailure(action, err))
	}
	var request decodedProviderRequest
	if inputPath == "" {
		if action != "inspect" && action != "inventory" {
			return renderTrackerContext(ctx, stdout, stderr, options.output, trackerInvalid(action, "input file"))
		}
		request.data.ProjectID = config.ProjectID
	} else {
		reader, closeReader, e := trackerInputReader(inputPath, stdin)
		if e != nil {
			return renderTrackerContext(ctx, stdout, stderr, options.output, trackerInvalid(action, "input file"))
		}
		request, err = decodeProviderRequestContext(ctx, reader, action, config.ProjectID)
		closeReader()
		if err != nil {
			if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
				return renderTrackerContext(ctx, stdout, stderr, options.output, trackerFailure(action, err))
			}
			return renderTrackerContext(ctx, stdout, stderr, options.output, trackerInvalid(action, "request JSON"))
		}
	}
	bound, err := hubnot.Open(ctx, config)
	if err != nil {
		return renderTrackerContext(ctx, stdout, stderr, options.output, trackerFailure(action, err))
	}
	result, err := invokeTracker(ctx, bound, action, request)
	if err != nil {
		return renderTrackerContext(ctx, stdout, stderr, options.output, trackerFailure(action, err))
	}
	return renderTrackerContext(ctx, stdout, stderr, options.output, trackerEnvelope{Schema: trackerResultSchema, Action: action, Outcome: OutcomeSuccess, Result: result})
}

// Named files are regular-file inputs. Nonblocking open plus descriptor
// validation closes the stat/open FIFO race; actual streaming input uses '-'.
func openTrackerRegularFile(path string) (*os.File, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, errors.New("tracker input must be a regular file")
	}
	file, err := os.OpenFile(path, os.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		return nil, err
	}
	info, err = file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("tracker input must remain a regular file")
	}
	return file, nil
}
func trackerInputReader(path string, stdin io.Reader) (io.Reader, func(), error) {
	if path == "-" {
		return stdin, func() {}, nil
	}
	file, err := openTrackerRegularFile(path)
	if err != nil {
		return nil, func() {}, err
	}
	return file, func() { _ = file.Close() }, nil
}

func renderTracker(stdout, stderr io.Writer, mode string, envelope trackerEnvelope) int {
	return renderTrackerContext(context.Background(), stdout, stderr, mode, envelope)
}

func renderTrackerContext(ctx context.Context, stdout, stderr io.Writer, mode string, envelope trackerEnvelope) int {
	if ctx == nil {
		ctx = context.Background()
	}
	// Cancellation before the initial diagnostic should not erase its known
	// effect facts when output is writable. This one bounded delivery attempt
	// has no retry/fallback after an interrupted or failed write.
	if ctx.Err() != nil && envelope.Diagnostic != nil {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
		defer cancel()
	}
	encoded, err := json.Marshal(envelope)
	if err != nil || len(encoded) > maxTrackerResultBytes {
		envelope = trackerEnvelope{Schema: trackerResultSchema, Action: envelope.Action, Outcome: OutcomeUnavailable, Diagnostic: &trackerCommandDiagnostic{"result_limit", "Complete tracker result could not be serialized within 256 MiB.", "Retain the same operation ID; inspect the provider before retrying."}, Mutation: envelope.Mutation}
		encoded, _ = json.Marshal(envelope)
	}
	if mode == "human" {
		var output, diagnostics bytes.Buffer
		_, _ = fmt.Fprintf(&output, "tracker %s: %s\n", envelope.Action, envelope.Outcome)
		if envelope.Diagnostic != nil {
			_, _ = fmt.Fprintf(&diagnostics, "%s %s\n", envelope.Diagnostic.Message, envelope.Diagnostic.Recovery)
		}
		if envelope.Result != nil {
			// Human output preserves values and proof identity without printing
			// large binary evidence. JSON output remains the full artifact.
			var display map[string]any
			decoder := json.NewDecoder(bytes.NewReader(encoded))
			decoder.UseNumber()
			if err := decoder.Decode(&display); err != nil {
				return ExitUnavailable
			}
			omitHumanProofBytes(display)
			formatted, err := json.MarshalIndent(display["result"], "", "  ")
			if err != nil {
				return ExitUnavailable
			}
			output.Write(formatted)
			output.WriteString("\nFull canonical proof bytes: use --output json.\n")
		}
		if envelope.Mutation != nil {
			_, _ = fmt.Fprintf(&diagnostics, "Mutation: %s; prior mutation: %t; applied this call: %t\n", envelope.Mutation.Outcome, envelope.Mutation.PriorMutation, envelope.Mutation.AppliedThisCall)
		}
		if output.Len()+diagnostics.Len() > maxTrackerResultBytes {
			return ExitUnavailable
		}
		if err := writeTrackerBytesContext(ctx, stdout, output.Bytes()); err != nil {
			return trackerOutputExit(err)
		}
		if diagnostics.Len() > 0 {
			if err := writeTrackerBytesContext(ctx, stderr, diagnostics.Bytes()); err != nil {
				return trackerOutputExit(err)
			}
		}
		return exitForOutcome(envelope.Outcome)
	}
	if err := writeTrackerBytesContext(ctx, stdout, append(encoded, '\n')); err != nil {
		return trackerOutputExit(err)
	}
	return exitForOutcome(envelope.Outcome)
}

func trackerOutputExit(err error) int {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return ExitCanceled
	}
	return ExitUnavailable
}

func writeTrackerBytesContext(ctx context.Context, writer io.Writer, data []byte) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	active := writer
	cleanup := func() error { return nil }
	if file, ok := writer.(*os.File); ok {
		owned, finish, err := trackerCancelableFile(file)
		if err != nil {
			return err
		}
		active, cleanup = owned, finish
	}
	var stop func() bool
	closed := make(chan struct{})
	if closer, ok := active.(io.Closer); ok {
		stop = context.AfterFunc(ctx, func() { _ = closer.Close(); close(closed) })
	}
	err := writeTrackerBytes(active, data)
	if stop != nil && !stop() {
		<-closed
	}
	cleanupErr := cleanup()
	// If all bytes were accepted without error, keep the delivered envelope's
	// outcome even if cancellation raced with descriptor cleanup. An
	// interrupted/partial write instead reports cancellation without fallback.
	if canceled := ctx.Err(); canceled != nil && err != nil {
		return canceled
	}
	if cleanupErr != nil {
		return cleanupErr
	}
	return err
}

func writeTrackerBytes(writer io.Writer, data []byte) error {
	n, err := writer.Write(data)
	if err == nil && n != len(data) {
		return io.ErrShortWrite
	}
	return err
}
func writeTrackerHelp(ctx context.Context, writer io.Writer) int {
	if err := writeTrackerBytesContext(ctx, writer, []byte(trackerUsage)); err != nil {
		return trackerOutputExit(err)
	}
	return ExitSuccess
}

func omitHumanProofBytes(value any) {
	switch v := value.(type) {
	case map[string]any:
		delete(v, "canonical_fields_base64")
		for _, child := range v {
			omitHumanProofBytes(child)
		}
	case []any:
		for _, child := range v {
			omitHumanProofBytes(child)
		}
	}
}

const trackerUsage = `spec-kitty tracker — inspect and edit descriptive work in an existing Hubnot repository

Usage:
  spec-kitty [--output json|human] tracker ACTION --binding FILE [--input FILE|-]

Actions:
  inspect       Verify the existing binding, public actor and capabilities (no input required).
  inventory     Read complete current definitions (binding project; input optional).
  read          Read current catalog fields and the current work proof.
  create        Create simple work; criteria may be empty.
  update        Update simple catalog fields with the requested unguarded guarantee.
  define        Create a complete definition, including criteria, scopes and provenance.
  definition    Read the complete definition and current proof.
  assign        mode=project (default) projects assignment; mode=observe reads attribution.
  comment       Append attributed discussion; author and transport signer stay distinct.
  discussion    Read discussion with common proof and separate current work proof.
  dependencies  Read verified incoming portable blocking relations.
  link, unlink  Add or remove a blocking relation.
  ready         Diagnostic open-work readiness; never execution authorization.

Input is a strict object: {"schema":"go-kitty.tracker-request/1","action":"ACTION","data":{...}}
Binding and named input paths must be regular files; use --input - for a pipe.
All actions accept optional project_id matching the binding. Work references use
external_ref with project_id, work_id, provider, external_id, metadata_schema,
created_by_operation and last_observation; copy the complete returned reference.
read/definition/discussion/dependencies need external_ref.
create: work_id, operation_id, title, optional description, design_context, criteria,
metadata_schema (default 1), provider_metadata_json. Criteria: id, text, verifier,
required, evidence_hint. define also requires description, design_context, criteria
with outcome, write_scopes (path/domain/access/serialization_key), provenance
(id/kind); dependencies and cross_domain_reason are optional.
update: external_ref, operation_id, expected_observation, required_guard,
title, description, design_context, criteria. Set required_guard="unguarded";
guarded updates are unsupported.
assign project: external_ref, operation_id, expected_observation, assignee,
required_guarantee="unguarded". assign observe accepts only mode, external_ref,
project_id.
comment: external_ref, operation_id, expected_observation, author, body.
link/unlink: operation_id, blocked_work_id, blocking_work_id, kind (default blocks).
dependencies returns proof_scope=portable_relations_only; it exposes the neutral
relation list without inventing a second-read proof. Use read or definition for
their complete current observation proofs.
ready: optional pinned_ready_ids. No initialization, publishing or rebinding occurs.

Opaque example: "provider_metadata_json":"{\"estimate\":9007199254740993}"
This optional JSON-text field must contain an object; omission means {}. Duplicate
keys, malformed Unicode, explicit null and unknown request fields are rejected.

Example using an existing binding.json (Python only extracts the returned reference):
  spec-kitty --output json tracker create --binding binding.json --input - > created.json <<'JSON'
  {"schema":"go-kitty.tracker-request/1","action":"create","data":{"work_id":"example","operation_id":"create-example","title":"Example"}}
JSON
  python3 -c 'import json,sys; r=json.load(sys.stdin); json.dump({"schema":"go-kitty.tracker-request/1","action":"read","data":{"external_ref":r["result"]["record"]["external_ref"]}},sys.stdout)' < created.json | spec-kitty --output json tracker read --binding binding.json --input -

Results use go-kitty.tracker-result/1. Proof canonical_fields_base64 retains exact
canonical bytes; revision is an equality token, not a counter. Mutation proofs
identify the original signed operation, including on replay. Obtain a fresh read
before using expected_observation; never substitute an original-operation or
inventory revision. Ready reports availability=unavailable and proof=null when
the complete graph cannot be observed; diagnostics explain recovery. Discussion also exposes its separate current_work_observation.
Canceled output may be incomplete after effects have applied. Preserve the operation
ID and original input. A canceled initial diagnostic gets one delivery attempt of
at most 250 ms; an interrupted write is never followed by a fallback write.
After an ambiguous or partial error, preserve the operation ID and original input.
Reconcile native history and retry that same request; do not create a new identity.
`
