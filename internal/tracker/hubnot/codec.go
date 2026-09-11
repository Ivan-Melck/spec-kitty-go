package hubnot

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/ids"
	"github.com/spec-kitty/go-kitty3/internal/tracker"
)

const ownedPrefix = "go-kitty/"
const codecSchema = "go-kitty.work/1"
const codecFixedKeys = 8

// CreateDependencies is immutable creation-saga intent, never current graph state.
// Operation's object schema belongs to the shared operation engine. The codec
// preserves it without confusing it with the opening's immutable identity.
type workContent struct {
	ProjectID          ids.ProjectID           `json:"project_id"`
	WorkID             ids.WorkID              `json:"work_id"`
	CreatedBy          ids.OperationID         `json:"created_by"`
	Complete           bool                    `json:"complete"`
	Title              string                  `json:"title"`
	Description        string                  `json:"description"`
	DesignContext      string                  `json:"design_context"`
	CrossDomainReason  string                  `json:"cross_domain_reason"`
	Criteria           []tracker.WorkCriterion `json:"criteria"`
	CreateDependencies []ids.WorkID            `json:"create_dependencies"`
	WriteScopes        []tracker.WriteScope    `json:"write_scopes"`
	Provenance         []tracker.ScopeRef      `json:"provenance"`
	Opaque             json.RawMessage         `json:"-"`
	Operation          json.RawMessage         `json:"-"`
}

// Arbitrary JSON is framed as text to avoid consuming the opaque value's
// supported nesting depth in the codec's own object wrapper.
type contentFields workContent

type codecDocument struct {
	Fields    contentFields `json:"fields"`
	Opaque    string        `json:"opaque"`
	Operation string        `json:"operation,omitempty"`
}

// Direct marshaling must preserve the same complete meaning as stored framing;
// the alias inside codecDocument prevents recursion and accidental omissions.
func (c workContent) MarshalJSON() ([]byte, error) { return contentData(c) }

func contentData(c workContent) ([]byte, error) {
	return json.Marshal(codecDocument{Fields: contentFields(c), Opaque: string(c.Opaque), Operation: string(c.Operation)})
}

func fromDefinition(d tracker.WorkDefinition, operation ids.OperationID) (workContent, error) {
	c := workContent{ProjectID: d.ProjectID(), WorkID: d.WorkID(), CreatedBy: operation, Complete: true, Title: d.Title(), Description: d.Description(), DesignContext: d.DesignContext(), CrossDomainReason: d.CrossDomainReason(), Criteria: d.Criteria(), CreateDependencies: d.Dependencies(), WriteScopes: d.WriteScopes(), Provenance: d.Provenance(), Opaque: d.ProviderMetadata().Bytes()}
	return normalizeContent(c)
}
func fromCatalog(r tracker.CreateWorkRequest) (workContent, error) {
	if r.MetadataSchema != 1 {
		return workContent{}, invalid("unsupported catalog metadata schema", nil)
	}
	criteria := make([]tracker.WorkCriterion, len(r.Criteria))
	for i, c := range r.Criteria {
		criteria[i] = tracker.WorkCriterion{ID: c.ID, Text: c.Text, Verifier: c.Verifier, Required: c.Required, EvidenceHint: c.EvidenceHint}
	}
	return normalizeContent(workContent{ProjectID: r.ProjectID, WorkID: r.WorkID, CreatedBy: r.OperationID, Title: r.Title, Description: r.Description, DesignContext: r.DesignContext, Criteria: criteria, Opaque: r.ProviderMetadata.Bytes()})
}
func (c workContent) definition(dependencies []ids.WorkID) (tracker.WorkDefinition, error) {
	if !c.Complete {
		return tracker.WorkDefinition{}, failure(tracker.ErrorCapabilityUnsupported, "use_complete_definition", "issue is a simple catalog record", nil)
	}
	metadata, err := tracker.NewOpaqueMetadata(c.Opaque)
	if err != nil {
		return tracker.WorkDefinition{}, err
	}
	return tracker.NewWorkDefinition(tracker.WorkDefinitionParams{ProjectID: c.ProjectID, WorkID: c.WorkID, Title: c.Title, Description: c.Description, DesignContext: c.DesignContext, CrossDomainReason: c.CrossDomainReason, Criteria: c.Criteria, Dependencies: dependencies, WriteScopes: c.WriteScopes, Provenance: c.Provenance, MetadataSchema: 1, ProviderMetadata: metadata})
}
func normalizeContent(c workContent) (workContent, error) {
	if _, err := ids.ParseProjectID(c.ProjectID.String()); err != nil {
		return c, invalid("invalid project mapping", err)
	}
	if _, err := ids.ParseWorkID(c.WorkID.String()); err != nil {
		return c, invalid("invalid work mapping", err)
	}
	if _, err := ids.ParseOperationID(c.CreatedBy.String()); err != nil {
		return c, invalid("creation operation required", err)
	}
	if len(c.Opaque) == 0 {
		c.Opaque = json.RawMessage(`{}`)
	}
	var err error
	c.Opaque, err = canonicalPayloadJSON(c.Opaque)
	if err != nil {
		return c, invalid("invalid opaque JSON", err)
	}
	if len(c.Operation) == 0 {
		c.Operation = nil
	}
	if len(c.Operation) > 0 {
		c.Operation, err = canonicalPayloadJSON(c.Operation)
		if err != nil {
			return c, invalid("invalid operation stamp JSON", err)
		}
	}
	if c.Complete {
		if _, err = c.definition(c.CreateDependencies); err != nil {
			return c, invalid("invalid complete definition", err)
		}
	} else {
		if c.Title == "" || c.CrossDomainReason != "" || len(c.WriteScopes) != 0 || len(c.Provenance) != 0 || len(c.CreateDependencies) != 0 {
			return c, invalid("invalid simple catalog content", nil)
		}
		for _, criterion := range c.Criteria {
			if criterion.Outcome != "" {
				return c, invalid("simple criterion has complete outcome", nil)
			}
			if err := (tracker.Criterion{ID: criterion.ID, Text: criterion.Text, Verifier: criterion.Verifier, Required: criterion.Required, EvidenceHint: criterion.EvidenceHint}).Validate(); err != nil {
				return c, invalid("invalid criterion", err)
			}
		}
	}
	// Reject invalid Unicode in all directly supplied strings before Marshal can
	// repair them, including strings nested in criterion and provenance structs.
	if !validContentStrings(reflect.ValueOf(c)) {
		return c, invalid("content contains invalid UTF-8", nil)
	}
	return cloneContent(c), nil
}
func validContentStrings(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.String:
		return utf8.ValidString(v.String())
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if !validContentStrings(v.Field(i)) {
				return false
			}
		}
	case reflect.Slice:
		if v.Type().Elem().Kind() == reflect.Uint8 {
			return true
		}
		for i := 0; i < v.Len(); i++ {
			if !validContentStrings(v.Index(i)) {
				return false
			}
		}
	}
	return true
}
func cloneContent(c workContent) workContent {
	c.Criteria = append([]tracker.WorkCriterion(nil), c.Criteria...)
	c.CreateDependencies = append([]ids.WorkID(nil), c.CreateDependencies...)
	c.WriteScopes = append([]tracker.WriteScope(nil), c.WriteScopes...)
	c.Provenance = append([]tracker.ScopeRef(nil), c.Provenance...)
	c.Opaque = bytes.Clone(c.Opaque)
	c.Operation = bytes.Clone(c.Operation)
	return c
}
func cloneIssueState(s issueState) issueState {
	s.Criteria = append([]issueCriterion{}, s.Criteria...)
	s.Labels = append([]string{}, s.Labels...)
	s.Assignees = append([]string{}, s.Assignees...)
	s.Relations = append([]issueRelation{}, s.Relations...)
	m := make(map[string]string, len(s.Metadata))
	for k, v := range s.Metadata {
		m[k] = v
	}
	s.Metadata = m
	return s
}
func encodeWorkState(base issueState, c workContent) (issueState, error) {
	c, err := normalizeContent(c)
	if err != nil {
		return issueState{}, err
	}
	for key := range base.Metadata {
		if strings.HasPrefix(key, ownedPrefix) {
			if _, _, err := decodeWorkState(base); err != nil {
				return issueState{}, err
			}
			break
		}
	}
	raw, err := contentData(c)
	if err != nil {
		return issueState{}, invalid("cannot encode work payload", err)
	}
	raw, err = canonicalPayloadJSON(raw)
	if err != nil {
		return issueState{}, invalid("cannot canonicalize work payload", err)
	}
	s := cloneIssueState(base)
	if s.Status == "" {
		s.Status = "open"
	}
	s.Title = c.Title
	s.Body = c.Description
	s.Criteria = make([]issueCriterion, len(c.Criteria))
	for i, criterion := range c.Criteria {
		s.Criteria[i] = issueCriterion{ID: criterion.ID, Text: criterion.Text}
	}
	for k := range s.Metadata {
		if strings.HasPrefix(k, ownedPrefix) {
			delete(s.Metadata, k)
		}
	}
	s.Metadata[ownedPrefix+"schema"] = codecSchema
	s.Metadata[ownedPrefix+"project"] = c.ProjectID.String()
	s.Metadata[ownedPrefix+"work"] = c.WorkID.String()
	s.Metadata[ownedPrefix+"created-by"] = c.CreatedBy.String()
	mode := "simple"
	if c.Complete {
		mode = "complete"
	}
	s.Metadata[ownedPrefix+"mode"] = mode
	s.Metadata[ownedPrefix+"bytes"] = strconv.Itoa(len(raw))
	sum := sha256.Sum256(raw)
	s.Metadata[ownedPrefix+"digest"] = hex.EncodeToString(sum[:])
	remaining := raw
	count := 0
	for len(remaining) > 0 {
		size := min(4096, len(remaining))
		for size < len(remaining) && !utf8.RuneStart(remaining[size]) {
			size--
		}
		s.Metadata[fmt.Sprintf("%schunk-%03d", ownedPrefix, count)] = string(remaining[:size])
		remaining = remaining[size:]
		count++
	}
	s.Metadata[ownedPrefix+"chunks"] = strconv.Itoa(count)
	if count > 64-codecFixedKeys || len(s.Metadata) > 64 {
		return issueState{}, invalid("owned and foreign metadata exceed native key capacity", nil)
	}
	if err := validateState(s); err != nil {
		return issueState{}, invalid("encoded work exceeds native state limits", err)
	}
	// Full signed-envelope overhead is checked by WP01's mutation builder before
	// dispatch, including operation key, parents, actor name and opening fields.
	return s, nil
}
func decodeWorkState(s issueState) (workContent, bool, error) {
	if err := validateState(s); err != nil {
		return workContent{}, false, protocolFailure(err)
	}
	owned := 0
	for k := range s.Metadata {
		if strings.HasPrefix(k, ownedPrefix) {
			owned++
		}
	}
	if owned == 0 {
		return workContent{}, false, nil
	}
	bad := func(message string) (workContent, bool, error) {
		return workContent{}, true, protocolFailure(errors.New(message))
	}
	if s.Metadata[ownedPrefix+"schema"] != codecSchema {
		return bad("unknown owned schema")
	}
	count, err := strconv.Atoi(s.Metadata[ownedPrefix+"chunks"])
	if err != nil || count < 1 || count > 64-codecFixedKeys || strconv.Itoa(count) != s.Metadata[ownedPrefix+"chunks"] || owned != codecFixedKeys+count {
		return bad("invalid owned chunk census")
	}
	size, err := strconv.Atoi(s.Metadata[ownedPrefix+"bytes"])
	if err != nil || size < 1 || size > maxMutationBytes || strconv.Itoa(size) != s.Metadata[ownedPrefix+"bytes"] {
		return bad("invalid payload byte count")
	}
	var payload bytes.Buffer
	for i := 0; i < count; i++ {
		chunk, ok := s.Metadata[fmt.Sprintf("%schunk-%03d", ownedPrefix, i)]
		if !ok || chunk == "" || len(chunk) > 4096 || !utf8.ValidString(chunk) {
			return bad("missing or malformed chunk")
		}
		payload.WriteString(chunk)
	}
	if payload.Len() != size {
		return bad("payload byte count mismatch")
	}
	sum := sha256.Sum256(payload.Bytes())
	if hex.EncodeToString(sum[:]) != s.Metadata[ownedPrefix+"digest"] {
		return bad("payload digest mismatch")
	}
	canonical, err := canonicalPayloadJSON(payload.Bytes())
	if err != nil || !bytes.Equal(canonical, payload.Bytes()) {
		return bad("payload is not strict canonical JSON")
	}
	var document codecDocument
	dec := json.NewDecoder(bytes.NewReader(canonical))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&document); err != nil {
		return bad("invalid owned payload shape")
	}
	if err := checkShape(canonical, reflect.TypeFor[codecDocument]()); err != nil {
		return bad("missing or unknown owned fields")
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		return bad("invalid codec document")
	}
	opaque, err := canonicalPayloadJSON([]byte(document.Opaque))
	if err != nil || !bytes.Equal(opaque, []byte(document.Opaque)) {
		return bad("stored opaque text is not canonical JSON")
	}
	if _, present := fields["operation"]; present {
		operation, err := canonicalPayloadJSON([]byte(document.Operation))
		if err != nil || !bytes.Equal(operation, []byte(document.Operation)) {
			return bad("stored operation text is not canonical JSON")
		}
	}
	c := workContent(document.Fields)
	c.Opaque = json.RawMessage(document.Opaque)
	c.Operation = json.RawMessage(document.Operation)
	c, err = normalizeContent(c)
	if err != nil {
		return bad("invalid owned work meaning")
	}
	mode := "simple"
	if c.Complete {
		mode = "complete"
	}
	if c.ProjectID.String() != s.Metadata[ownedPrefix+"project"] || c.WorkID.String() != s.Metadata[ownedPrefix+"work"] || c.CreatedBy.String() != s.Metadata[ownedPrefix+"created-by"] || mode != s.Metadata[ownedPrefix+"mode"] {
		return bad("payload mapping disagrees with header")
	}
	if c.Title != s.Title || c.Description != s.Body || len(c.Criteria) != len(s.Criteria) {
		return bad("native text disagrees with owned payload")
	}
	for i, v := range c.Criteria {
		if v.ID != s.Criteria[i].ID || v.Text != s.Criteria[i].Text {
			return bad("native ordered criteria disagree with owned payload")
		}
	}
	return c, true, nil
}

// Payloads may use the full nesting supported by encoding/json. The wire DTO
// depth limit is intentionally not reused. Number lexemes are kept exactly;
// no float conversion, exponent expansion, or lossy string coercion occurs.
func canonicalPayloadJSON(raw []byte) ([]byte, error) {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return nil, errors.New("invalid JSON or Unicode")
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		start := i
		i++
		for i < len(raw) && raw[i] != '"' {
			if raw[i] == '\\' {
				i++
			}
			i++
		}
		var value string
		if err := decodeStrict(raw[start:i+1], &value); err != nil {
			return nil, err
		}
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	value, err := payloadValue(dec)
	if err != nil {
		return nil, err
	}
	if _, ok := value.(map[string]any); !ok {
		return nil, errors.New("object root required")
	}
	if _, err := dec.Token(); err != io.EOF {
		return nil, errors.New("trailing JSON")
	}
	return json.Marshal(value)
}
func payloadValue(dec *json.Decoder) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	delimiter, ok := token.(json.Delim)
	if !ok {
		return token, nil
	}
	switch delimiter {
	case '{':
		object := map[string]any{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return nil, err
			}
			name, ok := key.(string)
			if !ok {
				return nil, errors.New("invalid object key")
			}
			if _, exists := object[name]; exists {
				return nil, errors.New("duplicate object key")
			}
			value, err := payloadValue(dec)
			if err != nil {
				return nil, err
			}
			object[name] = value
		}
		_, err := dec.Token()
		return object, err
	case '[':
		values := []any{}
		for dec.More() {
			value, err := payloadValue(dec)
			if err != nil {
				return nil, err
			}
			values = append(values, value)
		}
		_, err := dec.Token()
		return values, err
	default:
		return nil, errors.New("unexpected delimiter")
	}
}
