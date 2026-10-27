package nextrt

import "github.com/spec-kitty/go-kitty3/internal/invocation"

// AnalysisGoverningRoot, when set (an owned `next`: the repository root R
// while every other surface resolves from the owned checkout P), is the
// governing root the analyze-step currency check relativizes the mission's
// hash inputs against (next_cmd.analysis_currency_for(repo_root=R,
// owned=...)). Empty = the mission's own root.
var AnalysisGoverningRoot string

// OpRecordRoot, when set (an owned `next`), pins the checkout that keeps the
// Op records composition writes: the owned checkout P, whatever the working
// directory (#6682). Empty = the invoking checkout (resolve_op_record_root).
var OpRecordRoot string

// opRecordRoot is StepContractExecutor's record_root precedence: an
// explicit owned root wins over the invoking working directory.
func opRecordRoot(root string) string {
	if OpRecordRoot != "" {
		return OpRecordRoot
	}
	return invocation.ResolveRecordRoot(root, "")
}
