// Package prereview ports the pre-review regression gate that `agent tasks
// move-task --to for_review` runs (upstream specify_cli.review.{baseline,
// scope_source, pre_review_gate, gate_budget, verdict_aggregation,
// gate_registry} and configured_command): the declared review.test_command
// scope source, the lazy base-branch baseline capture, the head run with
// timeout/heartbeat/lock, the baseline diff, the budget assessment and the
// verdict aggregation.
package prereview

import (
	"os"
	"path/filepath"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/yamlx"
)

// Config keys of the review: section.
const (
	KeyTestCommand          = "test_command"
	KeyTestOutputFormat     = "test_output_format"
	KeyBlock                = "fail_on_pre_review_regression"
	KeyPreReviewTestCommand = "pre_review_test_command"
	FrontmatterScopeKey     = "pre_review_test_scope"
)

// ReviewSection is a best-effort read of the review: section of
// .kittify/config.yaml (_mt_review_config_section): missing file, malformed
// YAML or absent section all degrade to an empty object.
func ReviewSection(root string) *jsonx.Object {
	b, err := os.ReadFile(filepath.Join(root, ".kittify", "config.yaml"))
	if err != nil {
		return jsonx.NewObject()
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return jsonx.NewObject()
	}
	if r := o.GetObject("review"); r != nil {
		return r
	}
	return jsonx.NewObject()
}

// Truthy is Python bool() of a decoded YAML value.
func Truthy(v any) bool { return truthy(v) }

func truthy(v any) bool {
	switch x := v.(type) {
	case nil:
		return false
	case bool:
		return x
	case string:
		return x != ""
	case int:
		return x != 0
	case int64:
		return x != 0
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case *jsonx.Object:
		return x.Len() > 0
	}
	return true
}

// BlockEnabled is review.fail_on_pre_review_regression.
func BlockEnabled(root string) bool {
	v, _ := ReviewSection(root).Get(KeyBlock)
	return truthy(v)
}

// TestCommandTemplate is baseline._get_test_command: the configured
// review.test_command (present reports whether the key exists at all).
func TestCommandTemplate(root string) (template string, present bool) {
	b, err := os.ReadFile(filepath.Join(root, ".kittify", "config.yaml"))
	if err != nil {
		return "", false
	}
	o, err := yamlx.ParseObject(b)
	if err != nil || o == nil {
		return "", false
	}
	r := o.GetObject("review")
	if r == nil || !r.Has(KeyTestCommand) {
		return "", false
	}
	v, _ := r.Get(KeyTestCommand)
	if !truthy(v) {
		return "", true
	}
	return pyStr(v), true
}

func pyStr(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case nil:
		return "None"
	}
	return jsonx.Dumps(v, jsonx.Options{})
}
