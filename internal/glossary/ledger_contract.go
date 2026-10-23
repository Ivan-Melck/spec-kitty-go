package glossary

import (
	_ "embed"
	"fmt"
	"math"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/spec-kitty/go-kitty3/internal/clock"
	"github.com/spec-kitty/go-kitty3/internal/jsonschema"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pydantic"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// These producer contracts are generated from spec-kitty-events 11.0.0,
// installed with the pinned RC8 reference. Model property order is significant.
//
//go:embed ledger_contracts.json
var ledgerContractsJSON []byte
var ledgerContracts, _ = jsonx.DecodeObject(ledgerContractsJSON)

type LedgerError struct{ Message string }

func (e *LedgerError) Error() string { return e.Message }

func ledgerError(message string) error { return &LedgerError{message} }

func nativeRecord(value any, where string) error {
	switch v := value.(type) {
	case float64:
		if math.IsNaN(v) || math.IsInf(v, 0) {
			return ledgerError(where + ": non-finite float is not allowed")
		}
	case jsonx.Float:
		return nativeRecord(float64(v), where)
	case *jsonx.Object:
		if v != nil {
			for _, pair := range v.Pairs() {
				if err := nativeRecord(pair.Value, where+"."+pair.Key); err != nil {
					return err
				}
			}
		}
	case []any:
		for i, item := range v {
			if err := nativeRecord(item, fmt.Sprintf("%s[%d]", where, i)); err != nil {
				return err
			}
		}
	}
	return nil
}

// ValidateEvent performs both model and strict schema validation before any
// path is opened. Flat identity keys are retained in the ledger, not in the
// payload validation, exactly as persistence.build_record specifies.
func ValidateEvent(event *jsonx.Object) error {
	if event == nil {
		return ledgerError("record has no string 'event_type'")
	}
	if err := nativeRecord(event, "$"); err != nil {
		return err
	}
	typ, ok := event.Get("event_type")
	name, isString := typ.(string)
	if !ok || !isString {
		return ledgerError("record has no string 'event_type'")
	}
	contract := ledgerContracts.GetObject(name)
	if contract == nil {
		return ledgerError("unknown or unregistered event type " + pyjson.ReprString(name))
	}
	payload := event.Clone()
	payload.Delete("event_type")
	for _, key := range []string{"event_id", "timestamp"} {
		if value, exists := payload.Get(key); exists {
			if _, ok := value.(string); !ok {
				return ledgerError("identity key " + pyjson.ReprString(key) + " must be a string")
			}
			payload.Delete(key)
		}
	}
	// Convert the renderer's explicit float wrapper to JSON's native number
	// representation before using the shared schema validator.
	raw, err := jsonx.Marshal(payload, jsonx.Line)
	if err != nil {
		return err
	}
	payload, err = jsonx.DecodeObject(raw)
	if err != nil {
		return err
	}
	model := contract.GetObject("model")
	details := modelViolations(model, model, payload, "", name+"Payload")
	schemaErrors := jsonschema.New(contract.GetObject("schema")).IterErrors(payload)
	sort.SliceStable(schemaErrors, func(i, j int) bool {
		return schemaJSONPath(schemaErrors[i].Path) < schemaJSONPath(schemaErrors[j].Path)
	})
	for _, issue := range schemaErrors {
		details = append(details, schemaJSONPath(issue.Path)+": "+issue.Message)
	}
	if len(details) > 0 {
		return ledgerError("invalid " + name + ": " + strings.Join(details, "; "))
	}
	return nil
}

func schemaJSONPath(path []any) string {
	out := "$"
	for _, key := range path {
		if i, ok := key.(int); ok {
			out += fmt.Sprintf("[%d]", i)
		} else {
			out += "." + fmt.Sprint(key)
		}
	}
	return out
}

func modelViolations(root, schema *jsonx.Object, value any, path, model string) []string {
	if schema == nil {
		return nil
	}
	if ref := schema.GetString("$ref"); ref != "" {
		name := strings.TrimPrefix(ref, "#/$defs/")
		return modelViolations(root, root.GetObject("$defs").GetObject(name), value, path, name)
	}
	var errors []string
	add := func(msg string) { errors = append(errors, path+": "+msg) }
	if options := schema.GetList("enum"); options != nil {
		match := false
		for _, option := range options {
			match = match || pyjson.Repr(option) == pyjson.Repr(value)
		}
		if !match {
			add("Input should be " + pydantic.ExpectedOr(options))
		}
		return errors
	}
	switch schema.GetString("type") {
	case "object":
		object, ok := value.(*jsonx.Object)
		if !ok || object == nil {
			add(pydantic.ModelTypeMsg(model))
			return errors
		}
		properties := schema.GetObject("properties")
		required := map[string]bool{}
		for _, key := range schema.GetList("required") {
			required[fmt.Sprint(key)] = true
		}
		for _, pair := range properties.Pairs() {
			child := pair.Key
			if path != "" {
				child = path + "." + child
			}
			field, exists := object.Get(pair.Key)
			if !exists {
				if required[pair.Key] {
					errors = append(errors, child+": Field required")
				}
				continue
			}
			fieldSchema, _ := pair.Value.(*jsonx.Object)
			errors = append(errors, modelViolations(root, fieldSchema, field, child, "")...)
		}
		if extra, exists := schema.Get("additionalProperties"); exists && extra == false {
			for _, key := range object.Keys() {
				if !properties.Has(key) {
					field := key
					if path != "" {
						field = path + "." + field
					}
					errors = append(errors, field+": Extra inputs are not permitted")
				}
			}
		}
	case "string":
		text, ok := value.(string)
		if !ok {
			add(pydantic.MsgStringType)
			return errors
		}
		if n, ok := schema.GetInt("minLength"); ok && utf8.RuneCountInString(text) < n {
			add(pydantic.StringTooShortMsg(n))
			return errors
		}
		if path == "provenance.timestamp" {
			if _, err := clock.ParseISO(text); err != nil {
				add("Value error, provenance timestamp is not ISO-8601: " + pyjson.ReprString(text))
			}
		}
	case "number":
		n, ok := modelNumber(value)
		if !ok {
			if _, text := value.(string); text {
				add("Input should be a valid number, unable to parse string as a number")
			} else {
				add(pydantic.MsgFloatType)
			}
			return errors
		}
		for _, limit := range []string{"maximum", "minimum"} {
			if raw, exists := schema.Get(limit); exists {
				boundary, _ := modelNumber(raw)
				if limit == "maximum" && !(n <= boundary) {
					add(pydantic.LessEqualMsg(boundary))
					return errors
				}
				if limit == "minimum" && !(n >= boundary) {
					add(pydantic.GreaterEqualMsg(boundary))
					return errors
				}
			}
		}
	}
	return errors
}

func modelNumber(value any) (float64, bool) {
	switch n := value.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case jsonx.Float:
		return float64(n), true
	case bool:
		if n {
			return 1, true
		}
		return 0, true
	case string:
		f, err := strconv.ParseFloat(strings.TrimSpace(n), 64)
		return f, err == nil
	}
	return 0, false
}
