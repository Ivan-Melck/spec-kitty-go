//go:build spec_kitty_expiry_host_qualification && linux && amd64

package productionruntime

import (
	"fmt"
	"strings"
	"testing"
)

// Both real W5 source tops compose their function contract with OnlyExtraction.
// Each negative must fail EACH composition, not merely some unrelated checker.
// Every mutant starts from the same SHA-bound actual-selected production bytes.
// Syntactic source negatives only: never compilation or behavioural mutation.
func expiryBootstrapW5Freshness(source string) error {
	if err := expiryCompatFreshness(source); err != nil { return err }
	return expiryCompatOnlyExtraction(source)
}
func expiryBootstrapW5Cleanup(source string) error {
	if err := expiryCompatCleanup(source); err != nil { return err }
	return expiryCompatOnlyExtraction(source)
}
func expiryBootstrapRejectBothW5(t *testing.T, source string) {
	t.Helper()
	expiryCompatMutantRejected(t, source, expiryBootstrapW5Freshness)
	expiryCompatMutantRejected(t, source, expiryBootstrapW5Cleanup)
}

func TestExpiryBootstrapFourHelperCompatibilityMutants(t *testing.T) {
	base := expiryCompatReadSelected(t)
	if err := expiryBootstrapW5Freshness(base); err != nil { t.Fatal("selected freshness positive", err) }
	if err := expiryBootstrapW5Cleanup(base); err != nil { t.Fatal("selected cleanup positive", err) }
	groups := [][]string{
		{`p.DatabaseID == ""`, "p.DataDirectory != controlRoot", "p.SupervisorRunID != supervisor", "p.ExecutedImage.SHA256 != identity.ExecutableSHA256",
			"p.Observation.Status.Lifecycle != gapdbapi.LifecycleReady", "p.Observation.Status.OwnerPID != p.Process.PID", "p.Observation.Status.DatabaseID != p.DatabaseID"},
		{"p.Observation.Status.CurrentRevision != 0", "p.Observation.Status.DurableThroughRevision != 0", "p.Observation.Status.RecordCount != 0"},
	}
	for group, atoms := range groups {
		for i, atom := range atoms {
			t.Run(fmt.Sprintf("atom_%d_%d_omit", group, i), func(t *testing.T) {
				expiryBootstrapRejectBothW5(t, expiryCompatReplaceOnce(t, base, atom, "false"))
			})
			t.Run(fmt.Sprintf("atom_%d_%d_invert", group, i), func(t *testing.T) {
				next := strings.Replace(atom, " != ", " == ", 1)
				if next == atom { next = strings.Replace(atom, " == ", " != ", 1) }
				expiryBootstrapRejectBothW5(t, expiryCompatReplaceOnce(t, base, atom, next))
			})
			if i+1 < len(atoms) {
				t.Run(fmt.Sprintf("ordered_OR_%d_%d", group, i), func(t *testing.T) {
					changed := expiryCompatReplaceOnce(t, base, atom, "expiryBootstrapTemporaryAtom")
					changed = expiryCompatReplaceOnce(t, changed, atoms[i+1], atom)
					changed = expiryCompatReplaceOnce(t, changed, "expiryBootstrapTemporaryAtom", atoms[i+1])
					expiryBootstrapRejectBothW5(t, changed)
				})
			}
		}
	}
	bootstrapCall := "expiryBootstrapRefused(p, l.controlRoot, supervisor, r.identity)"
	zeroCall := "expiryBootstrapNotZero(initial, p)"
	bootstrapBlock := "if "+bootstrapCall+" {\n\t\treturn nil, nil, expiryFailure(\"expiry_bootstrap\")\n\t}"
	zeroBlock := "if "+zeroCall+" {\n\t\treturn nil, nil, expiryFailure(\"expiry_bootstrap_not_zero\")\n\t}"
	for _, call := range []struct{name, expression string; args []string}{
		{"bootstrap", bootstrapCall, []string{"p", "l.controlRoot", "supervisor", "r.identity"}},
		{"zero", zeroCall, []string{"initial", "p"}},
	} {
		for i := range call.args {
			t.Run(fmt.Sprintf("%s_arg_omit_%d", call.name, i), func(t *testing.T) {
				args := append([]string(nil), call.args...); args = append(args[:i], args[i+1:]...)
				fun := strings.SplitN(call.expression, "(", 2)[0]
				expiryBootstrapRejectBothW5(t, expiryCompatReplaceOnce(t, base, call.expression, fun+"("+strings.Join(args, ", ")+")"))
			})
			if i+1 < len(call.args) {
				t.Run(fmt.Sprintf("%s_arg_swap_%d", call.name, i), func(t *testing.T) {
					args := append([]string(nil), call.args...); args[i], args[i+1] = args[i+1], args[i]
					fun := strings.SplitN(call.expression, "(", 2)[0]
					expiryBootstrapRejectBothW5(t, expiryCompatReplaceOnce(t, base, call.expression, fun+"("+strings.Join(args, ", ")+")"))
				})
			}
		}
	}
	for _, change := range []struct{name, old, next string}{
		{"gate_omitted", "return initial && (", "return ("},
		{"gate_inverted", "return initial && (", "return !initial && ("},
		{"gate_OR", "return initial && (", "return initial || ("},
		{"gate_literal_initial", zeroCall, "expiryBootstrapNotZero(true, p)"},
		{"gate_literal_successor", zeroCall, "expiryBootstrapNotZero(false, p)"},
		{"controller_kind", "initial := c.kind == expiryControlFresh", "initial := c.kind != expiryControlFresh"},
		{"wrong_bootstrap_error", `expiryFailure("expiry_bootstrap")`, `expiryFailure("expiry_restart_freshness")`},
		{"wrong_zero_error", `expiryFailure("expiry_bootstrap_not_zero")`, `expiryFailure("expiry_bootstrap")`},
		{"bootstrap_call_omitted", bootstrapBlock, ""},
		{"zero_call_omitted", zeroBlock, ""},
		{"bootstrap_call_duplicated", "if "+bootstrapCall+" {", "if "+bootstrapCall+" || "+bootstrapCall+" {"},
		{"zero_call_duplicated", "if "+zeroCall+" {", "if "+zeroCall+" || "+zeroCall+" {"},
		{"bootstrap_error_ignored", bootstrapBlock, "_ = "+bootstrapCall},
		{"zero_error_ignored", zeroBlock, "_ = "+zeroCall},
		{"bootstrap_inverted", "if "+bootstrapCall+" {", "if !"+bootstrapCall+" {"},
		{"zero_inverted", "if "+zeroCall+" {", "if !"+zeroCall+" {"},
		{"predicate_order", bootstrapBlock+"\n\t"+zeroBlock, zeroBlock+"\n\t"+bootstrapBlock},
		{"bootstrap_inline", "if "+bootstrapCall+" {", expiryBootstrapDraftInline},
		{"zero_inline", "if "+zeroCall+" {", expiryBootstrapDraftInlineZero},
		{"bootstrap_missing_definition", expiryCompatBootstrapHelper, ""},
		{"zero_missing_definition", expiryCompatBootstrapZeroHelper, ""},
		{"bootstrap_duplicate_definition", expiryCompatBootstrapHelper, expiryCompatBootstrapHelper+"\n"+expiryCompatBootstrapHelper},
		{"zero_duplicate_definition", expiryCompatBootstrapZeroHelper, expiryCompatBootstrapZeroHelper+"\n"+expiryCompatBootstrapZeroHelper},
		{"bootstrap_wrong_signature", "controlRoot, supervisor string, identity sidecargapdb.ExpiryTechnicalIdentity) bool", "supervisor, controlRoot string, identity sidecargapdb.ExpiryTechnicalIdentity) bool"},
		{"zero_wrong_signature", "func expiryBootstrapNotZero(initial bool, p sidecargapdb.SidecarProvenance) bool", "func expiryBootstrapNotZero(p sidecargapdb.SidecarProvenance, initial bool) bool"},
		{"OR_to_AND", `return p.DatabaseID == "" ||`, `return p.DatabaseID == "" &&`},
		{"extra_disjunct", `return p.DatabaseID == "" ||`, `return true || p.DatabaseID == "" ||`},
	} {
		t.Run(change.name, func(t *testing.T) { expiryBootstrapRejectBothW5(t, expiryCompatReplaceOnce(t, base, change.old, change.next)) })
	}
	// Remove from the original stage, THEN insert at a different unique stage.
	// Undefined identifiers in some negatives are intentional: parse-only proof.
	for _, block := range []struct{name, text string}{{"bootstrap", bootstrapBlock}, {"zero", zeroBlock}} {
		for _, stage := range []struct{name, anchor string}{
			{"before_provenance", "p := lifecycle.Provenance()"},
			{"after_DialOwned", "database := journalv2.DatabaseID(p.DatabaseID)"},
			{"after_authenticated_Gets", "binding := AuthorityBindingV2{"},
			{"after_freshness", "if predecessorRun != nil {"},
		} {
			t.Run(block.name+"_moved_"+stage.name, func(t *testing.T) {
				changed := expiryCompatReplaceOnce(t, base, block.text, "")
				changed = expiryCompatReplaceOnce(t, changed, stage.anchor, block.text+"\n\t"+stage.anchor)
				expiryBootstrapRejectBothW5(t, changed)
			})
		}
	}
	for _, helper := range []string{expiryCompatBootstrapHelper, expiryCompatBootstrapZeroHelper, expiryCompatFreshnessHelper, expiryCompatCleanupHelper} {
		t.Run("definition_order/"+strings.SplitN(helper, "(", 2)[0], func(t *testing.T) {
			changed := expiryCompatReplaceOnce(t, base, helper, "")
			expiryBootstrapRejectBothW5(t, changed+"\n"+helper+"\n")
		})
	}
	for _, extra := range []string{
		"\nfunc expiryExtraBootstrapUse() { expiryBootstrapRefused(p, root, supervisor, identity) }\n",
		"\nfunc expiryExtraZeroUse() { expiryBootstrapNotZero(true, p) }\n",
	} { expiryBootstrapRejectBothW5(t, base+extra) }
}
