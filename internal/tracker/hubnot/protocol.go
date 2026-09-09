package hubnot

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

type envelope struct {
	Schema     string          `json:"schema"`
	OK         *bool           `json:"ok"`
	Data       json.RawMessage `json:"data,omitempty"`
	Error      *wireError      `json:"error,omitempty"`
	Snapshot   string          `json:"snapshot,omitempty"`
	NextCursor string          `json:"next_cursor,omitempty"`
}
type wireError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func decodeEnvelope(raw []byte, schema string, exit int) (envelope, error) {
	var result envelope
	if err := decodeStrict(raw, &result); err != nil {
		return result, err
	}
	if result.Schema != schema || result.OK == nil {
		return result, errors.New("schema or outcome missing")
	}
	var fields map[string]json.RawMessage
	_ = json.Unmarshal(raw, &fields)
	_, errorPresent := fields["error"]
	if *result.OK {
		if exit != 0 || errorPresent || len(result.Data) == 0 || bytes.Equal(bytes.TrimSpace(result.Data), []byte("null")) {
			return result, errors.New("contradictory success")
		}
	} else {
		if exit == 0 || result.Error == nil || result.Error.Code == "" || result.Error.Message == "" || len(result.Data) != 0 || result.Snapshot != "" || result.NextCursor != "" {
			return result, errors.New("contradictory failure")
		}
	}
	if len(result.NextCursor) > 4096 {
		return result, errors.New("oversized cursor")
	}
	if result.Snapshot != "" && !validEventID(result.Snapshot) {
		return result, errors.New("invalid snapshot")
	}
	return result, nil
}

// Go's standard decoder repairs invalid Unicode; reject it before decoding.
// Token walking additionally rejects duplicate fields at every nesting level.
func decodeStrict(raw []byte, target any) error {
	if !utf8.Valid(raw) || !json.Valid(raw) {
		return errors.New("invalid JSON or UTF-8")
	}
	for i := 0; i < len(raw); i++ {
		if raw[i] != '"' {
			continue
		}
		i++
		for i < len(raw) && raw[i] != '"' {
			if raw[i] != '\\' {
				i++
				continue
			}
			i++
			if raw[i] == 'u' {
				v, _ := strconv.ParseUint(string(raw[i+1:i+5]), 16, 16)
				i += 4
				if v >= 0xd800 && v <= 0xdbff {
					if i+6 >= len(raw) || raw[i+1] != '\\' || raw[i+2] != 'u' {
						return errors.New("unpaired high surrogate")
					}
					low, _ := strconv.ParseUint(string(raw[i+3:i+7]), 16, 16)
					if low < 0xdc00 || low > 0xdfff {
						return errors.New("unpaired high surrogate")
					}
					i += 6
				} else if v >= 0xdc00 && v <= 0xdfff {
					return errors.New("unpaired low surrogate")
				}
			}
			i++
		}
	}
	scan := json.NewDecoder(bytes.NewReader(raw))
	scan.UseNumber()
	if err := scanValue(scan, 0); err != nil {
		return err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	dec.DisallowUnknownFields()
	if err := dec.Decode(target); err != nil {
		return err
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("trailing JSON")
	}
	return checkShape(raw, reflect.TypeOf(target).Elem())
}

// Require exact JSON field spellings and required fields, including nested DTOs.
func checkShape(raw []byte, t reflect.Type) error {
	if t == reflect.TypeFor[json.RawMessage]() {
		return nil
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		switch t.Kind() {
		case reflect.Pointer, reflect.Map, reflect.Slice, reflect.Interface:
			return nil
		}
		return errors.New("null scalar field")
	}
	if t.Kind() == reflect.Pointer {
		return checkShape(raw, t.Elem())
	}
	switch t.Kind() {
	case reflect.Struct:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		known := map[string]bool{}
		for i := 0; i < t.NumField(); i++ {
			field := t.Field(i)
			if !field.IsExported() {
				continue
			}
			tag := field.Tag.Get("json")
			name, options, _ := strings.Cut(tag, ",")
			if name == "-" {
				continue
			}
			if name == "" {
				name = field.Name
			}
			known[name] = true
			value, present := object[name]
			if !present {
				if options != "omitempty" {
					return fmt.Errorf("missing field %s", name)
				}
				continue
			}
			if err := checkShape(value, field.Type); err != nil {
				return err
			}
		}
		for key := range object {
			if !known[key] {
				return fmt.Errorf("unknown field %s", key)
			}
		}
	case reflect.Slice, reflect.Array:
		if t.Elem().Kind() == reflect.Uint8 {
			return nil
		}
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := checkShape(value, t.Elem()); err != nil {
				return err
			}
		}
	case reflect.Map:
		var object map[string]json.RawMessage
		if err := json.Unmarshal(raw, &object); err != nil {
			return err
		}
		for _, value := range object {
			if err := checkShape(value, t.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
func scanValue(dec *json.Decoder, depth int) error {
	if depth > 64 {
		return errors.New("JSON nesting limit exceeded")
	}
	token, err := dec.Token()
	if err != nil {
		return err
	}
	d, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch d {
	case '{':
		seen := map[string]bool{}
		for dec.More() {
			key, err := dec.Token()
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return errors.New("duplicate JSON field")
			}
			seen[name] = true
			if err := scanValue(dec, depth+1); err != nil {
				return err
			}
		}
	case '[':
		for dec.More() {
			if err := scanValue(dec, depth+1); err != nil {
				return err
			}
		}
	default:
		return errors.New("unexpected JSON delimiter")
	}
	_, err = dec.Token()
	return err
}

type issueCriterion struct {
	ID   string `json:"id"`
	Text string `json:"text"`
}
type issueRelation struct {
	Kind   string `json:"kind"`
	Target string `json:"target"`
}
type issueState struct {
	Title     string            `json:"title"`
	Body      string            `json:"body"`
	Status    string            `json:"status"`
	Criteria  []issueCriterion  `json:"criteria"`
	Labels    []string          `json:"labels"`
	Assignees []string          `json:"assignees"`
	Relations []issueRelation   `json:"relations"`
	Metadata  map[string]string `json:"metadata"`
}
type issueDetail struct {
	ID              string            `json:"id"`
	Creator         string            `json:"creator"`
	OpeningMetadata map[string]string `json:"opening_metadata"`
	State           *issueState       `json:"state"`
	Conflict        bool              `json:"conflict"`
	HeadCount       int               `json:"head_count"`
}
type issueHead struct {
	ID    string     `json:"id"`
	Actor string     `json:"actor"`
	State issueState `json:"state"`
}
type issueShow struct {
	ID            string      `json:"id"`
	Creator       string      `json:"creator"`
	State         *issueState `json:"state"`
	Conflict      bool        `json:"conflict"`
	HeadIDs       []string    `json:"head_ids"`
	HeadCount     int         `json:"head_count"`
	RevisionCount int         `json:"revision_count"`
	CommentCount  int         `json:"comment_count"`
}
type issueHistoryItem struct {
	ID        string      `json:"id"`
	Actor     string      `json:"actor"`
	Kind      string      `json:"kind"`
	Timestamp string      `json:"timestamp"`
	Parents   []string    `json:"parents"`
	State     *issueState `json:"state,omitempty"`
	Body      string      `json:"body,omitempty"`
	Intent    string      `json:"intent,omitempty"`
	Operation string      `json:"operation,omitempty"`
}
type issueEdge struct {
	Source    string `json:"source"`
	Target    string `json:"target"`
	Kind      string `json:"kind"`
	Revision  string `json:"revision"`
	Actor     string `json:"actor"`
	Ambiguous bool   `json:"ambiguous"`
}
type issueGraphItem struct {
	Type      string     `json:"type"`
	Edge      *issueEdge `json:"edge,omitempty"`
	Kind      string     `json:"kind,omitempty"`
	Component string     `json:"component,omitempty"`
	IssueID   string     `json:"issue_id,omitempty"`
}
type issueOperation struct {
	IssueID   string      `json:"issue_id"`
	EventID   string      `json:"event_id"`
	Actor     string      `json:"actor"`
	Operation string      `json:"operation"`
	Intent    string      `json:"intent"`
	Kind      string      `json:"kind"`
	Timestamp string      `json:"timestamp"`
	Parents   []string    `json:"parents"`
	State     *issueState `json:"state,omitempty"`
	Body      *string     `json:"body,omitempty"`
	Request   string      `json:"request"`
}
type issueMutation struct {
	IssueID   string `json:"issue_id"`
	EventID   string `json:"event_id"`
	Replayed  bool   `json:"replayed"`
	Conflict  bool   `json:"conflict"`
	HeadCount int    `json:"head_count"`
}
type page[T any] struct {
	Items      []T    `json:"items"`
	Total      int    `json:"total"`
	IssueID    string `json:"issue_id,omitempty"`
	Conflict   bool   `json:"conflict"`
	HeadCount  int    `json:"head_count,omitempty"`
	CycleCount int    `json:"cycle_count"`
}

func validateState(s issueState) error {
	text := func(v string, limit int, required bool) bool {
		return len(v) <= limit && utf8.ValidString(v) && (!required || strings.TrimSpace(v) != "")
	}
	identifier := func(v string, limit int) bool {
		return text(v, limit, true) && !strings.ContainsFunc(v, unicode.IsControl)
	}
	if !text(s.Title, 1024, true) || !text(s.Body, 65536, false) || (s.Status != "open" && s.Status != "closed") {
		return errors.New("invalid title/body/status")
	}
	if len(s.Criteria) > 64 || len(s.Labels) > 64 || len(s.Assignees) > 64 || len(s.Relations) > 128 || len(s.Metadata) > 64 {
		return errors.New("native issue collection limit exceeded")
	}
	seen := map[string]bool{}
	for _, c := range s.Criteria {
		if !identifier(c.ID, 64) || !text(c.Text, 4096, true) || seen[c.ID] {
			return errors.New("invalid or duplicate criterion")
		}
		seen[c.ID] = true
	}
	for _, set := range [][]string{s.Labels, s.Assignees} {
		seen = map[string]bool{}
		for _, v := range set {
			if !identifier(v, 256) || seen[v] {
				return errors.New("invalid or duplicate label/assignee")
			}
			seen[v] = true
		}
	}
	relations := map[issueRelation]bool{}
	for _, r := range s.Relations {
		if (r.Kind != "blocks" && r.Kind != "related" && r.Kind != "parent") || !validEventID(r.Target) || relations[r] {
			return errors.New("invalid or duplicate relation")
		}
		relations[r] = true
	}
	for k, v := range s.Metadata {
		prefix, name, ok := strings.Cut(k, "/")
		if !ok || !identifier(prefix, 128) || !identifier(name, 128) || !identifier(k, 128) || !text(v, 4096, false) {
			return errors.New("invalid namespaced metadata")
		}
	}
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	if len(raw) > maxMutationBytes {
		return fmt.Errorf("native state exceeds %d bytes", maxMutationBytes)
	}
	return nil
}
