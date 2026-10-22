package charter

import (
	"github.com/spec-kitty/go-kitty3/internal/jsonx"
	"github.com/spec-kitty/go-kitty3/internal/pydantic"
)

// provenanceEntryModel is synthesize_pipeline.ProvenanceEntry (extra ignored).
var provenanceEntryModel = &pydModel{"ProvenanceEntry", []pydField{
	{name: "schema_version", kind: pkLiteral, literals: []string{"2"}},
	{name: "artifact_urn", kind: pkStr, required: true},
	{name: "artifact_kind", kind: pkLiteral, required: true, literals: []string{"directive", "tactic", "styleguide", "procedure", "agent_profile"}},
	{name: "artifact_slug", kind: pkStr, required: true},
	{name: "artifact_content_hash", kind: pkStr, required: true},
	{name: "inputs_hash", kind: pkStr, required: true},
	{name: "adapter_id", kind: pkStr, required: true},
	{name: "adapter_version", kind: pkStr, required: true},
	{name: "synthesizer_version", kind: pkStr, required: true, minLen: 1},
	{name: "source_section", kind: pkStr, required: true, nullable: true},
	{name: "source_urns", kind: pkStrList, required: true},
	{name: "source_input_ids", kind: pkStrList, required: true},
	{name: "generated_at", kind: pkStr, required: true},
	{name: "produced_at", kind: pkStr, required: true},
	{name: "corpus_snapshot_id", kind: pkStr, required: true},
	{name: "synthesis_run_id", kind: pkStr, required: true, minLen: 1},
	{name: "evidence_bundle_hash", kind: pkStr, nullable: true},
	{name: "adapter_notes", kind: pkStr, nullable: true},
}, false}

// ValidateProvenanceEntry is provenance.load_yaml's model_validate (with
// the _check_source_provenance after-validator); ruamel selects ruamel's
// loader types for input_type.
func ValidateProvenanceEntry(raw any, ruamel bool) error {
	if ve := validatePyd(provenanceEntryModel, raw, ruamel); ve != nil {
		return ve
	}
	o := raw.(*jsonx.Object)
	sec, _ := o.Get("source_section")
	s, _ := sec.(string)
	urns, _ := o.Get("source_urns")
	l, _ := urns.([]any)
	if s == "" && len(l) == 0 {
		return pydantic.New("ProvenanceEntry", pydantic.ErrorDetail{Type: pydantic.TypeValueError,
			Msg:   pydantic.ValueErrorMsg("ProvenanceEntry requires at least one of 'source_section' (non-empty) or 'source_urns' (non-empty list). Both are absent/empty."),
			Input: raw, InputType: typeName(raw, ruamel)})
	}
	return nil
}

func typeName(v any, ruamel bool) string {
	if ruamel {
		return pydantic.RuamelTypeName(v)
	}
	return pydantic.TypeName(v)
}
