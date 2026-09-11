# M2 production-authority adoption closeout

The exact M2 GapDB-only production-authority release tuple is adopted for the production-runtime program. This satisfies the M2 adoption prerequisite for M3 execution composition and M4 stack-neutral acceptance. Each successor still has its own remaining entry evidence and qualification gates.

## Human decision

The user approved the presented exact-tuple proposal in the active session: “Yes please, document looks great”. The preceding question explicitly requested M2 adoption so M3/M4, M5 API delivery, and the Nano adapter trials could proceed in order.

- Signer: `github:lynncoleart`
- Recorded approval reference: `conversation-approval:2026-09-11T03:07:01Z`
- Mechanism: `approved-local-reference`, following the M1 record convention; this is an attributable conversation approval, not a detached cryptographic signature.
- Canonical adoption-envelope SHA-256: `ccab7d53bf15b9bd6ec4942c54af0583d9312bd9050200c29260ea62228e78b7` (UTF-8 JSON, sorted keys, compact separators, no trailing newline).
- Machine-readable decision: [m2-adoption.json](m2-adoption.json).

## Exact admitted evidence

Release: [m2-gapdb-release-ready-v1](https://github.com/spec-kitty/spec-kitty-redesign/releases/tag/m2-gapdb-release-ready-v1).

| Field | Value |
|---|---|
| source_commit | `416138f490ad6d908fc445c12b91440ca66ef18c` |
| source_tree | `2b68ee733305465d5acdfe8680f053b91d867a8d` |
| run_id | `wp17-qualification-416138f4-cycle2-final23` |
| manifest_sha256 | `8d7731992a35b0ace4cdcd816651df302d4459c8af91d1a2c31cf5d3aeb285a0` |
| producer_archive_sha256 | `250bad6ef3115b154794db61407736133ec0b1f5a5fc61117ec1267e77057fc2` |
| review_receipt_sha256 | `a35f944548bd766887344192f59c36fd6002519d88588e07e3cdc48989510e50` |
| release_archive_sha256 | `098fa8d3fae6f33e227ce290530fc172f820c961ffd326adb820a26aa44e2414` |

Both published assets were freshly downloaded before approval. The verifier built from the qualified source recursively checked the release against externally supplied exact source/tree/manifest/producer/archive hashes and exited 0. It returned `review_eligible=true`, `release_ready=true`, `adopted=false`. The release archive was hashed again when recording this decision and matched the admitted tuple.

The frozen release receipt remains unchanged with `adopted=false`: it records the qualification outcome before this separate human decision. The adoption record does not rewrite release history or automatically qualify later commits, including the subsequent Hubnot adapter integration.

## Successor boundary

- M3 may build production execution/Hubnot/Git/workspace composition after resolving its remaining provider admission and frozen product-law inputs.
- M4 may build stack-neutral claims/evidence acceptance against the admitted durable substrate and ratified acceptance contract.
- M5 depends on qualified M3/M4 capabilities and delivers the complete API 2.0 surface; the current API document remains a semantic design, not a shipped transport.
- Nano's global Go Kitty mode remains unavailable until its real-host adapter and deterministic trials pass. No live-model trial is claimed.
- Dual write, retired-store fallback/import, and runtime backend selection remain forbidden. Consumers use production host capabilities; the M2 qualification campaign is never a runtime service.
