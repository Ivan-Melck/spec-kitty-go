package orchestrator

import (
	"bytes"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/cli"
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	orch "github.com/spec-kitty/go-kitty3/internal/orchestrator"
	"github.com/spec-kitty/go-kitty3/internal/pyjson"
)

// delegate runs a registered host command in-process (the way upstream
// calls the typer function under redirect_stdout): no group callbacks, its
// own argv, stdout captured. Returns the captured stdout and exit code.
func delegate(ctx *cli.Context, path []string, argv ...string) (string, int) {
	var buf bytes.Buffer
	target := rootCmd.Lookup(path...)
	if target == nil {
		return "", 1
	}
	cp := *target
	mini := &cli.Command{Name: "spec-kitty"}
	mini.Add(&cp)
	code := cli.Execute(mini, append([]string{cp.Name}, argv...), cli.IO{Stdin: ctx.Stdin, Stdout: &buf, Stderr: ctx.Stderr})
	return buf.String(), code
}

// extractJSONPayload is _extract_json_payload: the first stdout line that
// starts with "{" and parses as a JSON object.
func extractJSONPayload(raw string) *jsonx.Object {
	for _, line := range strings.Split(raw, "\n") {
		st := strings.TrimSpace(line)
		if !strings.HasPrefix(st, "{") {
			continue
		}
		v, err := pyjson.Loads(st)
		if err != nil {
			continue
		}
		if o, ok := v.(*jsonx.Object); ok {
			return o
		}
	}
	return nil
}

// pyStr is str(value) for a decoded JSON value.
func pyStr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return pyjson.Str(v)
}

// classifyDelegateError is _classify_delegate_error.
func classifyDelegateError(payload *jsonx.Object, raw, fallbackCode, fallbackMessage string) (string, string, *jsonx.Object) {
	if payload == nil {
		msg := strings.TrimSpace(raw)
		if msg == "" {
			msg = fallbackMessage
		}
		return fallbackCode, msg, jsonx.NewObject("raw_output", raw)
	}
	msg := fallbackMessage
	if v, ok := payload.Get("error"); ok && pyjson.Truthy(v) {
		msg = pyStr(v)
	} else if v, ok := payload.Get("message"); ok && pyjson.Truthy(v) {
		msg = pyStr(v)
	}
	if v, ok := payload.Get("error_code"); ok && pyjson.Truthy(v) {
		return pyStr(v), msg, payload
	}
	return fallbackCode, msg, payload
}

// setDefault is dict.setdefault.
func setDefault(o *jsonx.Object, key string, value any) {
	if !o.Has(key) {
		o.Set(key, value)
	}
}

var _ = orch.JSON
