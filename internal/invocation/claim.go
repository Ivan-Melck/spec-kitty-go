package invocation

// Additive (area L): Op-record reads for the claim seams
// (`agent action implement|review --invocation-id/--model`).

import (
	"fmt"
	"os"
	"strings"

	"github.com/spec-kitty/go-kitty3/internal/jsonx"
)

// ModelProvider is workflow._resolved_model_provider: the provider of a
// model in the packaged routing catalog.
func ModelProvider(modelID string) (string, error) {
	cat := loadCatalog()
	if cat == nil {
		return "", fmt.Errorf("Could not resolve dispatched model %s: the canonical routing catalog is unavailable", pyRepr(modelID))
	}
	for _, x := range cat.GetList("models") {
		if m, ok := x.(*jsonx.Object); ok && m.GetString("id") == modelID {
			return m.GetString("provider"), nil
		}
	}
	return "", fmt.Errorf("Could not resolve dispatched model %s: model is absent from the canonical routing catalog", pyRepr(modelID))
}

// ReadStartedForClaim is workflow._read_op_started_event: the validated
// started event of an Op record correlated with a claim.
func ReadStartedForClaim(root, id string) (*Started, error) {
	if err := ValidateID(id); err != nil {
		return nil, err
	}
	p, err := RecordPath(root, id)
	if err != nil {
		return nil, fmt.Errorf("Could not read Op record for invocation_id=%s: %v", pyRepr(id), err)
	}
	b, err := os.ReadFile(p)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("Op record not found for invocation_id=%s", pyRepr(id))
		}
		return nil, fmt.Errorf("Could not read Op record for invocation_id=%s: %v", pyRepr(id), err)
	}
	var lines []string
	for _, l := range strings.Split(string(b), "\n") {
		lines = append(lines, strings.TrimSuffix(l, "\r"))
	}
	if len(b) == 0 {
		return nil, fmt.Errorf("Op record is empty for invocation_id=%s", pyRepr(id))
	}
	o, err := decodeLine(lines[0])
	if err != nil || o == nil {
		return nil, fmt.Errorf("Could not read Op record for invocation_id=%s: %v", pyRepr(id), err)
	}
	if o.GetString("event") != "started" {
		return nil, fmt.Errorf("First Op record is not a started event for invocation_id=%s", pyRepr(id))
	}
	s, err := ParseStarted(o)
	if err != nil {
		return nil, fmt.Errorf("Could not read Op record for invocation_id=%s: %v", pyRepr(id), err)
	}
	if s.InvocationID != id {
		return nil, fmt.Errorf("Op record invocation_id %s does not match requested %s", pyRepr(s.InvocationID), pyRepr(id))
	}
	return s, nil
}

// PyRepr is Python repr() of a str (exported for claim-seam messages).
func PyRepr(s string) string { return pyRepr(s) }
