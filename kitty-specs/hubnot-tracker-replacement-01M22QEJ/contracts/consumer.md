# Native tracker consumer

Discoverable spec-kitty tracker ACTION --binding FILE [--input FILE|-], existing global output convention. Dispatch before control-root validation and any HostFactory call. Existing mission grammar and production.go unchanged.

Actions: inspect, inventory, read, create, update, define, definition, assign, comment, discussion, dependencies, link, unlink, ready. Define creates complete definition; definition reads it; update is simple catalog update. Do not advertise unsupported guarded definition edits, atomic claims or conditional compensation as working commands.

Strict input schema go-kitty.tracker-request/1 with action-specific portable request data. Inspect may omit input; inventory uses binding project. Explicit project must match. Reuse portable validations; separate DTO decoder from mission authority requests. Document field spellings once in provider_request.go tests/current docs. Unknown/duplicate keys, malformed UTF-8, trailing values or oversized input fail before mutation. Portable IDs do not expose native ref grammar.

Opaque metadata uses optional provider_metadata_json, a JSON-text string in request/result DTOs. Omission means the existing empty object; a present empty value or invalid/non-object JSON refuses. Validate duplicates, Unicode and syntax inside that text before neutral construction, retaining exact number lexemes and the full neutral nesting range without adding envelope nesting. Keep ordinary work fields structured. Encode neutral composites explicitly through accessors so private criteria, dependencies, discussion, diagnostics and canonical proof bytes are retained; direct marshaling of private-field values is not a complete result.

Versioned result: action, typed outcome, bounded diagnostic, portable result and observation proof. Preserve replay/applied/prior-mutation/ambiguous distinctions, existing exit meanings and JSON/text ergonomics. Native refs/signers may be provenance only.

Every successful command calls real adapter through neutral ports. Reachability tests kill removal of actual connection. HostFactory test fails if touched for tracker actions; old mission tests retain original path. Missing executable, wrong tuple and legacy binding produce actionable refusal without fallback. No automatic init, real project sync/publish or rebinding.

Discussion DTOs use the neutral attributed-discussion port for both append and observation. Expose signing_actor separately from author and operation_marker, retain complete proof via accessors, and preserve historical replay results without a post-append current read. Signing actor is transport provenance, never independent accountable identity or execution/review permission.

Assignment uses the existing assign action with request data mode=observe|project. Omitted mode means project. Observe accepts the immutable reference and matching project, rejects mutation-only fields and calls the actual neutral ObserveAssignment method, retaining operation attribution and its observation proof. Project calls the existing unguarded mutation method. Catalog read remains its own current catalog projection; do not substitute its plain assignee field for provenance-aware assignment observation or silently combine independent snapshots into one coherent proof. Help and strict action fixtures document both modes.
