package helpercmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"reflect"
	"strconv"
	"strings"
	"syscall"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const trackerRequestSchema = "go-kitty.tracker-request/1"

// provider_metadata_json is independently encoded JSON object text. Keeping it
// outside the envelope's JSON nesting preserves the neutral model's full depth
// and number lexemes. All other fields use the structured spellings below.
type providerReference struct {
	ProjectID          ids.ProjectID    `json:"project_id"`
	WorkID             ids.WorkID       `json:"work_id"`
	Provider           string           `json:"provider"`
	ExternalID         string           `json:"external_id"`
	MetadataSchema     uint16           `json:"metadata_schema"`
	CreatedByOperation ids.OperationID  `json:"created_by_operation"`
	LastObservation    tracker.Revision `json:"last_observation"`
}

func (r providerReference) neutral() tracker.ExternalWorkRef {
	return tracker.ExternalWorkRef{ProjectID: r.ProjectID, WorkID: r.WorkID, Provider: r.Provider, ExternalID: r.ExternalID, MetadataSchema: r.MetadataSchema, CreatedByOperation: r.CreatedByOperation, LastObservation: r.LastObservation}
}
func referenceDTO(r tracker.ExternalWorkRef) providerReference {
	return providerReference{r.ProjectID, r.WorkID, r.Provider, r.ExternalID, r.MetadataSchema, r.CreatedByOperation, r.LastObservation}
}

type providerCriterion struct {
	ID           string               `json:"id"`
	Text         string               `json:"text"`
	Verifier     tracker.VerifierKind `json:"verifier"`
	Required     bool                 `json:"required"`
	EvidenceHint string               `json:"evidence_hint"`
}
type providerData struct {
	ProjectID            ids.ProjectID           `json:"project_id"`
	WorkID               ids.WorkID              `json:"work_id"`
	OperationID          ids.OperationID         `json:"operation_id"`
	ExternalRef          providerReference       `json:"external_ref"`
	Title                string                  `json:"title"`
	Description          string                  `json:"description"`
	DesignContext        string                  `json:"design_context"`
	CrossDomainReason    string                  `json:"cross_domain_reason"`
	Criteria             json.RawMessage         `json:"criteria"`
	Dependencies         []ids.WorkID            `json:"dependencies"`
	WriteScopes          []tracker.WriteScope    `json:"write_scopes"`
	Provenance           []tracker.ScopeRef      `json:"provenance"`
	MetadataSchema       uint16                  `json:"metadata_schema"`
	ProviderMetadataJSON *string                 `json:"provider_metadata_json"`
	ExpectedObservation  tracker.Revision        `json:"expected_observation"`
	RequiredGuard        tracker.UpdateLevel     `json:"required_guard"`
	Mode                 string                  `json:"mode"`
	Assignee             string                  `json:"assignee"`
	RequiredGuarantee    tracker.AssignmentLevel `json:"required_guarantee"`
	Author               string                  `json:"author"`
	Body                 string                  `json:"body"`
	BlockedWorkID        ids.WorkID              `json:"blocked_work_id"`
	BlockingWorkID       ids.WorkID              `json:"blocking_work_id"`
	Kind                 tracker.DependencyKind  `json:"kind"`
	PinnedReadyIDs       []ids.WorkID            `json:"pinned_ready_ids"`
}
type decodedProviderRequest struct {
	data       providerData
	create     tracker.CreateWorkRequest
	update     tracker.UpdateWorkRequest
	define     tracker.CreateWorkDefinitionRequest
	reference  tracker.ExternalWorkRef
	assignment tracker.ProjectAssignmentRequest
	comment    tracker.AppendDiscussionRequest
	dependency tracker.DependencyRequest
	ready      tracker.ReadyRequest
}

var providerActions = map[string]string{
	"inspect": "project_id", "inventory": "project_id", "read": "project_id external_ref", "definition": "project_id external_ref", "discussion": "project_id external_ref", "dependencies": "project_id external_ref",
	"create":  "project_id work_id operation_id title description design_context criteria metadata_schema provider_metadata_json",
	"update":  "project_id external_ref operation_id expected_observation required_guard title description design_context criteria",
	"define":  "project_id work_id operation_id title description design_context cross_domain_reason criteria dependencies write_scopes provenance metadata_schema provider_metadata_json",
	"assign":  "project_id external_ref mode operation_id expected_observation assignee required_guarantee",
	"comment": "project_id external_ref operation_id expected_observation author body",
	"link":    "project_id operation_id blocked_work_id blocking_work_id kind", "unlink": "project_id operation_id blocked_work_id blocking_work_id kind",
	"ready": "project_id pinned_ready_ids",
}

// decodeProviderRequestContext interrupts the command's active closable stream
// (stdin pipe or opened request file) without abandoning a reader goroutine.
// Finite in-memory readers need no close watcher. A stopped watcher cannot close
// a caller's stream after this function has returned.
func decodeProviderRequestContext(ctx context.Context, reader io.Reader, action string, project ids.ProjectID) (decodedProviderRequest, error) {
	if ctx == nil {
		return decodedProviderRequest{}, errors.New("request context is missing")
	}
	if err := ctx.Err(); err != nil {
		return decodedProviderRequest{}, err
	}
	active, cleanup, err := trackerCancelableReader(reader)
	if err != nil {
		return decodedProviderRequest{}, err
	}
	var stop func() bool
	closed := make(chan struct{})
	if closer, ok := active.(io.Closer); ok {
		stop = context.AfterFunc(ctx, func() { _ = closer.Close(); close(closed) })
	}
	request, err := decodeProviderRequest(active, action, project)
	if stop != nil && !stop() {
		<-closed
	}
	cleanupErr := cleanup()
	if canceled := ctx.Err(); canceled != nil {
		return decodedProviderRequest{}, canceled
	}
	if cleanupErr != nil {
		return decodedProviderRequest{}, cleanupErr
	}
	return request, err
}

// Inherited stdin can be a blocking descriptor whose os.File.Close cannot
// interrupt Read. An owned, pollable duplicate makes cancellation effective.
// Dup shares status flags, so capture through SyscallConn (Fd changes flags),
// restore them on every path, and never close the caller's original file. The
// caller exclusively lends this stream for the duration of one request read.
func trackerCancelableReader(reader io.Reader) (io.Reader, func() error, error) {
	nothing := func() error { return nil }
	file, ok := reader.(*os.File)
	if !ok {
		return reader, nothing, nil
	}
	return trackerCancelableFile(file)
}

func trackerCancelableFile(file *os.File) (*os.File, func() error, error) {
	nothing := func() error { return nil }
	raw, err := file.SyscallConn()
	if err != nil {
		return nil, nothing, err
	}
	duplicate := -1
	var flags uintptr
	var captureErr error
	err = raw.Control(func(fd uintptr) {
		value, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_GETFL, 0)
		if errno != 0 {
			captureErr = errno
			return
		}
		flags = value
		duplicate, captureErr = syscall.Dup(int(fd))
		if captureErr == nil {
			syscall.CloseOnExec(duplicate)
		}
	})
	if err != nil {
		return nil, nothing, err
	}
	if captureErr != nil {
		return nil, nothing, captureErr
	}
	restore := func() error {
		var restoreErr error
		controlErr := raw.Control(func(fd uintptr) {
			_, _, errno := syscall.Syscall(syscall.SYS_FCNTL, fd, syscall.F_SETFL, flags)
			if errno != 0 {
				restoreErr = errno
			}
		})
		if controlErr != nil {
			return controlErr
		}
		return restoreErr
	}
	if err = syscall.SetNonblock(duplicate, true); err != nil {
		_ = syscall.Close(duplicate)
		_ = restore()
		return nil, nothing, err
	}
	owned := os.NewFile(uintptr(duplicate), "tracker-stream")
	if owned == nil {
		_ = syscall.Close(duplicate)
		_ = restore()
		return nil, nothing, errors.New("cannot own tracker stream descriptor")
	}
	return owned, func() error { _ = owned.Close(); return restore() }, nil
}

func decodeProviderRequest(reader io.Reader, action string, project ids.ProjectID) (decodedProviderRequest, error) {
	var out decodedProviderRequest
	if reader == nil {
		return out, errors.New("request input is missing")
	}
	raw, err := io.ReadAll(io.LimitReader(reader, MaxRequestBytes+1))
	if err != nil || int64(len(raw)) > MaxRequestBytes {
		return out, errors.New("request exceeds bounded JSON input")
	}
	var envelope struct {
		Schema string          `json:"schema"`
		Action string          `json:"action"`
		Data   json.RawMessage `json:"data"`
	}
	if err = strictProviderJSON(raw, &envelope); err != nil {
		return out, err
	}
	if envelope.Schema != trackerRequestSchema || envelope.Action != action {
		return out, errors.New("request schema or action mismatch")
	}
	allowed, ok := providerActions[action]
	if !ok {
		return out, errors.New("unknown tracker action")
	}
	var members map[string]json.RawMessage
	if err = json.Unmarshal(envelope.Data, &members); err != nil || members == nil {
		return out, errors.New("data must be an object")
	}
	for key := range members {
		if !strings.Contains(" "+allowed+" ", " "+key+" ") {
			return out, fmt.Errorf("field %s is not accepted by %s", key, action)
		}
	}
	if err = strictProviderJSON(envelope.Data, &out.data); err != nil {
		return out, err
	}
	d := out.data
	if d.ProjectID != "" && d.ProjectID != project {
		return out, errors.New("project_id does not match binding")
	}
	d.ProjectID = project
	if _, err = ids.ParseProjectID(project.String()); err != nil {
		return out, err
	}
	if d.MetadataSchema == 0 {
		d.MetadataSchema = 1
	}
	if d.Mode == "" {
		d.Mode = "project"
	}
	if action == "assign" {
		if d.Mode != "observe" && d.Mode != "project" {
			return out, errors.New("assign mode must be observe or project")
		}
		if d.Mode == "observe" {
			for key := range members {
				if key != "mode" && key != "project_id" && key != "external_ref" {
					return out, errors.New("assignment observation rejects mutation fields")
				}
			}
		}
	}
	metadata := tracker.OpaqueMetadata{}
	if d.ProviderMetadataJSON != nil {
		inner := []byte(*d.ProviderMetadataJSON)
		if len(inner) > tracker.MaxOpaqueMetadataBytes {
			return out, errors.New("provider metadata exceeds limit")
		}
		if err = validateProviderJSON(inner); err != nil {
			return out, err
		}
		if len(bytes.TrimSpace(inner)) == 0 || bytes.TrimSpace(inner)[0] != '{' {
			return out, errors.New("provider metadata must be a JSON object")
		}
		metadata, err = tracker.NewOpaqueMetadata(inner)
		if err != nil {
			return out, err
		}
	}
	var criteria []tracker.Criterion
	if len(d.Criteria) > 0 && action != "define" {
		var values []providerCriterion
		if err = strictProviderJSON(d.Criteria, &values); err != nil {
			return out, err
		}
		for _, v := range values {
			c := tracker.Criterion{ID: v.ID, Text: v.Text, Verifier: v.Verifier, Required: v.Required, EvidenceHint: v.EvidenceHint}
			if err = c.Validate(); err != nil {
				return out, err
			}
			criteria = append(criteria, c)
		}
	}
	ref := d.ExternalRef.neutral()
	if strings.Contains(" read update definition assign comment discussion dependencies ", " "+action+" ") {
		if ref.ProjectID != project {
			return out, errors.New("reference project does not match binding")
		}
		if err = ref.Validate(); err != nil {
			return out, err
		}
		out.reference = ref
	}
	mutation := action == "create" || action == "update" || action == "define" || action == "comment" || action == "link" || action == "unlink" || (action == "assign" && d.Mode == "project")
	if mutation {
		if _, err = ids.ParseOperationID(d.OperationID.String()); err != nil {
			return out, err
		}
	}
	if action == "create" || action == "define" {
		if _, err = ids.ParseWorkID(d.WorkID.String()); err != nil {
			return out, err
		}
	}
	out.create = tracker.CreateWorkRequest{ProjectID: project, WorkID: d.WorkID, OperationID: d.OperationID, Title: d.Title, Description: d.Description, DesignContext: d.DesignContext, Criteria: criteria, MetadataSchema: d.MetadataSchema, ProviderMetadata: metadata}
	out.update = tracker.UpdateWorkRequest{ExternalRef: ref, OperationID: d.OperationID, ExpectedObservation: d.ExpectedObservation, RequiredGuard: d.RequiredGuard, Title: d.Title, Description: d.Description, DesignContext: d.DesignContext, Criteria: criteria}
	if action == "define" {
		var values []tracker.WorkCriterion
		if err = strictProviderJSON(d.Criteria, &values); err != nil {
			return out, err
		}
		definition, e := tracker.NewWorkDefinition(tracker.WorkDefinitionParams{ProjectID: project, WorkID: d.WorkID, Title: d.Title, Description: d.Description, DesignContext: d.DesignContext, CrossDomainReason: d.CrossDomainReason, Criteria: values, Dependencies: d.Dependencies, WriteScopes: d.WriteScopes, Provenance: d.Provenance, MetadataSchema: d.MetadataSchema, ProviderMetadata: metadata})
		if e != nil {
			return out, e
		}
		out.define = tracker.CreateWorkDefinitionRequest{OperationID: d.OperationID, Definition: definition}
	}
	out.assignment = tracker.ProjectAssignmentRequest{ExternalRef: ref, OperationID: d.OperationID, Assignee: d.Assignee, ExpectedObservation: d.ExpectedObservation, RequiredGuarantee: d.RequiredGuarantee}
	out.comment = tracker.AppendDiscussionRequest{ExternalRef: ref, OperationID: d.OperationID, Author: d.Author, Body: d.Body, ExpectedObservation: d.ExpectedObservation}
	if d.Kind == "" {
		d.Kind = tracker.DependencyBlocks
	}
	out.dependency = tracker.DependencyRequest{ProjectID: project, OperationID: d.OperationID, Dependency: tracker.DependencyRef{BlockedWorkID: d.BlockedWorkID, BlockingWorkID: d.BlockingWorkID, Kind: d.Kind}}
	out.ready = tracker.ReadyRequest{ProjectID: project, PinnedReadyIDs: d.PinnedReadyIDs}
	out.data = d
	return out, nil
}

// Validate before decoding: encoding/json otherwise repairs invalid Unicode and
// silently merges duplicate keys. Token traversal retains numbers without any
// floating-point conversion and permits the same nesting range as json.Valid.
func validateProviderJSON(raw []byte) error {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return errors.New("invalid JSON or UTF-8")
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		i++
		for i < len(raw) && raw[i] != '"' {
			if raw[i] == '\\' {
				i++
				if raw[i] == 'u' {
					v, _ := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
					i += 4
					if v >= 0xd800 && v <= 0xdbff {
						if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
							return errors.New("unpaired JSON surrogate")
						}
						low, _ := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
						if low < 0xdc00 || low > 0xdfff {
							return errors.New("unpaired JSON surrogate")
						}
						i += 6
					} else if v >= 0xdc00 && v <= 0xdfff {
						return errors.New("unpaired JSON surrogate")
					}
				}
			}
			i++
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var visit func() error
	visit = func() error {
		token, err := dec.Token()
		if err != nil {
			return err
		}
		delim, ok := token.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			seen := map[string]bool{}
			for dec.More() {
				key, e := dec.Token()
				if e != nil {
					return e
				}
				s := key.(string)
				if seen[s] {
					return errors.New("duplicate JSON key")
				}
				seen[s] = true
				if e = visit(); e != nil {
					return e
				}
			}
		case '[':
			for dec.More() {
				if e := visit(); e != nil {
					return e
				}
			}
		default:
			return errors.New("invalid JSON delimiter")
		}
		_, err = dec.Token()
		return err
	}
	return visit()
}
func strictProviderJSON(raw []byte, target any) error {
	if err := validateProviderJSON(raw); err != nil {
		return err
	}
	if err := checkProviderShape(raw, reflect.TypeOf(target).Elem()); err != nil {
		return err
	}
	return json.Unmarshal(raw, target)
}

var rawJSONType = reflect.TypeOf(json.RawMessage{})

func checkProviderShape(raw []byte, t reflect.Type) error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return errors.New("explicit null is not accepted")
	}
	if t == rawJSONType {
		return nil
	}
	if t.Kind() == reflect.Pointer {
		return checkProviderShape(raw, t.Elem())
	}
	switch t.Kind() {
	case reflect.Struct:
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(raw, &fields); err != nil || fields == nil {
			return errors.New("JSON object required")
		}
		allowed := map[string]reflect.Type{}
		for i := 0; i < t.NumField(); i++ {
			f := t.Field(i)
			name := strings.Split(f.Tag.Get("json"), ",")[0]
			if name != "" && name != "-" {
				allowed[name] = f.Type
			}
		}
		for key, value := range fields {
			ft, ok := allowed[key]
			if !ok {
				return fmt.Errorf("unknown field %s", key)
			}
			if err := checkProviderShape(value, ft); err != nil {
				return err
			}
		}
	case reflect.Slice:
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, v := range values {
			if err := checkProviderShape(v, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
