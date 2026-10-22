// Package jsonschema is a Draft 7 validator reproducing python-jsonschema
// 4.26's iter_errors order and messages for the keywords the doctrine
// schemas use (type, enum, const, required, properties,
// additionalProperties, patternProperties, items, min/maxItems,
// min/maxLength, pattern, minimum/maximum, anyOf/oneOf/allOf/not, local $ref).
package jsonschema

import (
	"encoding/json"
	"fmt"
	"math"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// Error is one ValidationError: the instance path and the message.
type Error struct {
	Path    []any
	Message string
}

// PathString is ".".join(str(p) for p in error.path) or "root".
func (e Error) PathString() string {
	if len(e.Path) == 0 {
		return "root"
	}
	parts := make([]string, len(e.Path))
	for i, p := range e.Path {
		parts[i] = fmt.Sprint(p)
	}
	return strings.Join(parts, ".")
}

// Validator is Draft7Validator(schema).
type Validator struct{ root *jsonx.Object }

// New returns a validator for schema (a parsed schema document).
func New(schema *jsonx.Object) *Validator { return &Validator{root: schema} }

// IterErrors is validator.iter_errors(instance).
func (v *Validator) IterErrors(instance any) []Error {
	return v.descend(instance, v.root, nil)
}

func repr(x any) string { return pyjson.Repr(x) }

func (v *Validator) resolve(ref string) *jsonx.Object {
	if !strings.HasPrefix(ref, "#") {
		return nil
	}
	cur := any(v.root)
	for _, part := range strings.Split(strings.TrimPrefix(ref, "#"), "/") {
		if part == "" {
			continue
		}
		part = strings.ReplaceAll(strings.ReplaceAll(part, "~1", "/"), "~0", "~")
		o, ok := cur.(*jsonx.Object)
		if !ok {
			return nil
		}
		cur, _ = o.Get(part)
	}
	o, _ := cur.(*jsonx.Object)
	return o
}

func isType(x any, t string) bool {
	switch t {
	case "string":
		_, ok := x.(string)
		return ok
	case "boolean":
		_, ok := x.(bool)
		return ok
	case "null":
		return x == nil
	case "array":
		_, ok := x.([]any)
		return ok
	case "object":
		_, ok := x.(*jsonx.Object)
		return ok
	case "integer":
		switch n := x.(type) {
		case int, int64:
			return true
		case json.Number:
			_, err := n.Int64()
			return err == nil
		case float64:
			return n == math.Trunc(n) && !math.IsInf(n, 0)
		}
		return false
	case "number":
		switch x.(type) {
		case int, int64, float64, json.Number:
			return true
		}
		return false
	}
	return false
}

func num(x any) (float64, bool) {
	switch n := x.(type) {
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	case float64:
		return n, true
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	}
	return 0, false
}

func appendPath(p []any, x any) []any { return append(append([]any(nil), p...), x) }

func (v *Validator) descend(inst any, schema *jsonx.Object, path []any) []Error {
	if schema == nil {
		return nil
	}
	if ref, ok := schema.Get("$ref"); ok {
		if s, isStr := ref.(string); isStr {
			return v.descend(inst, v.resolve(s), path)
		}
	}
	var errs []Error
	add := func(msg string) { errs = append(errs, Error{append([]any(nil), path...), msg}) }
	for _, kv := range schema.Pairs() {
		switch kv.Key {
		case "type":
			var types []string
			switch t := kv.Value.(type) {
			case string:
				types = []string{t}
			case []any:
				for _, x := range t {
					types = append(types, fmt.Sprint(x))
				}
			}
			ok := false
			for _, t := range types {
				ok = ok || isType(inst, t)
			}
			if !ok {
				q := make([]string, len(types))
				for i, t := range types {
					q[i] = pyjson.ReprString(t)
				}
				add(fmt.Sprintf("%s is not of type %s", repr(inst), strings.Join(q, ", ")))
			}
		case "enum":
			l, _ := kv.Value.([]any)
			found := false
			for _, e := range l {
				found = found || pyjson.Equal(e, inst)
			}
			if !found {
				add(fmt.Sprintf("%s is not one of %s", repr(inst), repr(kv.Value)))
			}
		case "const":
			if !pyjson.Equal(kv.Value, inst) {
				add(fmt.Sprintf("%s was expected", repr(kv.Value)))
			}
		case "required":
			o, ok := inst.(*jsonx.Object)
			if !ok {
				continue
			}
			l, _ := kv.Value.([]any)
			for _, r := range l {
				if _, has := o.Get(fmt.Sprint(r)); !has {
					add(fmt.Sprintf("%s is a required property", repr(r)))
				}
			}
		case "properties":
			o, ok := inst.(*jsonx.Object)
			props, ok2 := kv.Value.(*jsonx.Object)
			if !ok || !ok2 {
				continue
			}
			for _, pkv := range props.Pairs() {
				if val, has := o.Get(pkv.Key); has {
					sub, _ := pkv.Value.(*jsonx.Object)
					errs = append(errs, v.descend(val, sub, appendPath(path, pkv.Key))...)
				}
			}
		case "patternProperties":
			o, ok := inst.(*jsonx.Object)
			pp, ok2 := kv.Value.(*jsonx.Object)
			if !ok || !ok2 {
				continue
			}
			for _, pkv := range pp.Pairs() {
				re, err := regexp.Compile(pkv.Key)
				if err != nil {
					continue
				}
				for _, ikv := range o.Pairs() {
					if re.MatchString(ikv.Key) {
						sub, _ := pkv.Value.(*jsonx.Object)
						errs = append(errs, v.descend(ikv.Value, sub, appendPath(path, ikv.Key))...)
					}
				}
			}
		case "additionalProperties":
			o, ok := inst.(*jsonx.Object)
			if !ok {
				continue
			}
			extras := additional(o, schema)
			switch ap := kv.Value.(type) {
			case *jsonx.Object:
				for _, k := range extras {
					val, _ := o.Get(k)
					errs = append(errs, v.descend(val, ap, appendPath(path, k))...)
				}
			case bool:
				if !ap && len(extras) > 0 {
					if _, hasPP := schema.Get("patternProperties"); hasPP {
						sorted := append([]string(nil), extras...)
						sort.Strings(sorted)
						q := make([]string, len(sorted))
						for i, k := range sorted {
							q[i] = pyjson.ReprString(k)
						}
						verb := "does"
						if len(q) > 1 {
							verb = "do"
						}
						pp, _ := schema.Get("patternProperties")
						var keys []string
						for _, pkv := range pp.(*jsonx.Object).Pairs() {
							keys = append(keys, pkv.Key)
						}
						sort.Strings(keys)
						var pats []string
						for _, k := range keys {
							pats = append(pats, pyjson.ReprString(k))
						}
						add(fmt.Sprintf("%s %s not match any of the regexes: %s", strings.Join(q, ", "), verb, strings.Join(pats, ", ")))
						continue
					}
					sorted := append([]string(nil), extras...)
					sort.Strings(sorted)
					q := make([]string, len(sorted))
					for i, k := range sorted {
						q[i] = pyjson.ReprString(k)
					}
					verb := "was"
					if len(q) > 1 {
						verb = "were"
					}
					add(fmt.Sprintf("Additional properties are not allowed (%s %s unexpected)", strings.Join(q, ", "), verb))
				}
			}
		case "items":
			l, ok := inst.([]any)
			if !ok {
				continue
			}
			switch it := kv.Value.(type) {
			case *jsonx.Object:
				for i, x := range l {
					errs = append(errs, v.descend(x, it, appendPath(path, i))...)
				}
			case []any:
				for i, x := range l {
					if i >= len(it) {
						break
					}
					sub, _ := it[i].(*jsonx.Object)
					errs = append(errs, v.descend(x, sub, appendPath(path, i))...)
				}
			case bool:
				if !it && len(l) > 0 {
					add(fmt.Sprintf("%s is not allowed for %s", repr(l[0]), repr(l)))
				}
			}
		case "minItems", "maxItems":
			l, ok := inst.([]any)
			n, isNum := num(kv.Value)
			if !ok || !isNum {
				continue
			}
			if kv.Key == "minItems" && float64(len(l)) < n {
				msg := "is too short"
				if n == 1 {
					msg = "should be non-empty"
				}
				add(repr(inst) + " " + msg)
			}
			if kv.Key == "maxItems" && float64(len(l)) > n {
				msg := "is too long"
				if n == 0 {
					msg = "is expected to be empty"
				}
				add(repr(inst) + " " + msg)
			}
		case "minLength", "maxLength":
			s, ok := inst.(string)
			n, isNum := num(kv.Value)
			if !ok || !isNum {
				continue
			}
			l := float64(len([]rune(s)))
			if kv.Key == "minLength" && l < n {
				msg := "is too short"
				if n == 1 {
					msg = "should be non-empty"
				}
				add(repr(inst) + " " + msg)
			}
			if kv.Key == "maxLength" && l > n {
				msg := "is too long"
				if n == 0 {
					msg = "is expected to be empty"
				}
				add(repr(inst) + " " + msg)
			}
		case "pattern":
			s, ok := inst.(string)
			p, ok2 := kv.Value.(string)
			if !ok || !ok2 {
				continue
			}
			if re, err := regexp.Compile(p); err == nil && !re.MatchString(s) {
				add(fmt.Sprintf("%s does not match %s", repr(inst), pyjson.ReprString(p)))
			}
		case "minimum", "maximum", "exclusiveMinimum", "exclusiveMaximum":
			if _, isBool := inst.(bool); isBool {
				continue
			}
			x, ok := num(inst)
			n, ok2 := num(kv.Value)
			if !ok || !ok2 {
				continue
			}
			switch {
			case kv.Key == "minimum" && x < n:
				add(fmt.Sprintf("%s is less than the minimum of %s", repr(inst), repr(kv.Value)))
			case kv.Key == "maximum" && x > n:
				add(fmt.Sprintf("%s is greater than the maximum of %s", repr(inst), repr(kv.Value)))
			case kv.Key == "exclusiveMinimum" && x <= n:
				add(fmt.Sprintf("%s is less than or equal to the minimum of %s", repr(inst), repr(kv.Value)))
			case kv.Key == "exclusiveMaximum" && x >= n:
				add(fmt.Sprintf("%s is greater than or equal to the maximum of %s", repr(inst), repr(kv.Value)))
			}
		case "anyOf", "oneOf", "allOf":
			l, _ := kv.Value.([]any)
			matched := 0
			var allErrs []Error
			for _, s := range l {
				sub, _ := s.(*jsonx.Object)
				e := v.descend(inst, sub, path)
				if len(e) == 0 {
					matched++
				}
				allErrs = append(allErrs, e...)
			}
			switch kv.Key {
			case "anyOf":
				if matched == 0 {
					add(repr(inst) + " is not valid under any of the given schemas")
				}
			case "oneOf":
				if matched == 0 {
					add(repr(inst) + " is not valid under any of the given schemas")
				} else if matched > 1 {
					add(repr(inst) + " is valid under each of " + strconv.Itoa(matched) + " given schemas")
				}
			case "allOf":
				errs = append(errs, allErrs...)
			}
		case "not":
			sub, _ := kv.Value.(*jsonx.Object)
			if len(v.descend(inst, sub, path)) == 0 {
				add(fmt.Sprintf("%s should not be valid under %s", repr(inst), repr(kv.Value)))
			}
		}
	}
	return errs
}

// additional is find_additional_properties (instance order).
func additional(o *jsonx.Object, schema *jsonx.Object) []string {
	props := schema.GetObject("properties")
	var pats []*regexp.Regexp
	if pp := schema.GetObject("patternProperties"); pp != nil {
		for _, kv := range pp.Pairs() {
			if re, err := regexp.Compile(kv.Key); err == nil {
				pats = append(pats, re)
			}
		}
	}
	var out []string
	for _, kv := range o.Pairs() {
		if props != nil {
			if _, ok := props.Get(kv.Key); ok {
				continue
			}
		}
		matched := false
		for _, re := range pats {
			matched = matched || re.MatchString(kv.Key)
		}
		if !matched {
			out = append(out, kv.Key)
		}
	}
	return out
}
