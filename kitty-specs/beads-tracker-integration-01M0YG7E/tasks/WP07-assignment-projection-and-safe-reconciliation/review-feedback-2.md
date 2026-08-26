**Issue 1 — opaque tracker revisions are ordered in the new evidence (blocking)**

`internal/execution/external_effect_test.go:283` and
`internal/scenario/wp07_assignment_authority_test.go:95,107` use `<=` to decide
whether a tracker revision advanced. That contradicts the provider contract in
`internal/tracker/provider.go:10-12`, which permits only zero and equality checks
and expressly forbids ordering or freshness inference. Content-derived and
high-bit revisions are valid opaque tokens and need not increase numerically, so
these assertions encode a false portability requirement in the acceptance
evidence.

Replace each ordering assertion with the actual portable predicate: require the
new revision to be nonzero and unequal to the preceding revision where the test
expects a provider mutation. Keep the value/marker assertions that prove the
semantic transition. No production saga change is requested.

All cycle-one blockers were otherwise closed on inspection. Focused correction
evidence passed for immutable terminal replay identity, fresh-only terminal
observations and restart persistence, high-bit SQLite revision round trips, 100
independent human-change races plus mutant/lock sensors under `-race`, the real
authority scenario, strong-fake compensation, the live Beads helper path, and
Inspect/Capabilities consistency.
