# Exact M2 qualification artifact recovered

Status: **artifact identity resolved; no current-branch qualification transfer**.
This recovery record is new development evidence, not a modification of original
admission, producer/review receipts or release records.

## Verified identity and provenance

Recovered executable: `/tmp/spec-kitty-recovered-m2/bin/gapdbd` (local convenience).

| Binding | Verified value |
|---|---|
| Binary SHA-256 | `54d3c80dd225a5e58f1488f0ce62fd6b95a6bb0ecf95b541e211b199555d8eaf` |
| Binary size / mode | 5,426,793 bytes / `0500` |
| GapDB source | `006e9d469d40fc5a6ce306a083ec96a98cc02e36` |
| GapDB source tree | `fe4bf39150a5511db76ba6c69de2a2c176abbd82` — archived candidate tuple |
| Module | `v0.0.0-20260907114832-006e9d469d40` |
| Existing release | `spec-kitty/spec-kitty-redesign`, `m2-gapdb-release-ready-v1` |
| Release asset | ID `551092948`, `wp17-qualification-416138f4-cycle2-final23.release.tar` |
| Release archive SHA-256 | `098fa8d3fae6f33e227ce290530fc172f820c961ffd326adb820a26aa44e2414` |
| Nested producer | `producer/producer.tar`, SHA-256 `250bad6ef3115b154794db61407736133ec0b1f5a5fc61117ec1267e77057fc2` |
| Producer manifest | `manifest.json`, SHA-256 `8d7731992a35b0ace4cdcd816651df302d4459c8af91d1a2c31cf5d3aeb285a0` |
| Binary archive member | Producer `sidecar/gapdbd`, bound by that manifest |
| Producer source / run | Commit `416138f490ad6d908fc445c12b91440ca66ef18c`, tree `2b68ee733305465d5acdfe8680f053b91d867a8d`; run `wp17-qualification-416138f4-cycle2-final23` |

All three archive/manifest hashes were independently recomputed and compared with
[immutable M2 adoption](../programs/evidence/m2-adoption.json), not just trusted
from downloaded metadata. Exact binary bytes/size were independently checked.
The archived candidate confirms the source/tree/module tuple. TAR inspection was
bounded; only explicitly selected regular-file members were copied, with no
uncontrolled extraction or symlink/path traversal.

`go version -m` (inspection, not executable launch) reports Go 1.26.7,
`-trimpath=true`, CGO disabled, linux/amd64, GOAMD64=v1, embedded VCS source
`006e9d469d40fc5a6ce306a083ec96a98cc02e36` and `vcs.modified=false`.
This does not establish an exact build command; recovery solved the artifact
requirement without speculative flag changes or hash overrides.

## Recovery route and earlier observations

Unauthenticated GitHub repository/release requests returned 404. Existing
already-authorized `gh` CLI access successfully retrieved the private release;
404 did not prove the artifact absent. No credentials, permissions or auth
configuration were changed or exposed.

The earlier bounded local searches and same-module `go install -trimpath`
reproduction remained unsuccessful. That different binary (`555ea12d...`,
5,431,089 bytes) was not used. Failed recovery observations are preserved rather
than relabeled. The actual recovered binary has not been executed by this
recovery work and is not automatically substituted for the distinct historical
M1 or production assertion-extension artifacts.

Local retained provenance: `/tmp/spec-kitty-recovered-m2/recovery.log`,
`recovered-binary-provenance.json`, release/producer inventories, archived
candidate, producer manifest and downloaded immutable archives. These paths are
not durable identities; the release asset and digest chain above identify them.

## Re-fetch and verify

Using existing authorized GitHub CLI access, download outside the repository:

```bash
gh api repos/spec-kitty/spec-kitty-redesign/releases/tags/m2-gapdb-release-ready-v1
gh api -H 'Accept: application/octet-stream' \
  repos/spec-kitty/spec-kitty-redesign/releases/assets/551092948 \
  > /absolute/external/recovery.release.tar
sha256sum /absolute/external/recovery.release.tar
```

Require the exact release SHA-256 above before inspecting. Verify producer and
manifest hashes against adoption and each selected binary against the manifest
before copying or executing; do not extract an untrusted archive wholesale.

## Remaining boundary

This closes **PARITY-QUALIFICATION-003's artifact availability/identity blocker**,
not namespace-host support, frozen proxy compatibility, complete archive/release
requalification, or M3/M4 admission. Current source changes still need their own
clean candidate and independent source-bound proof. See
[admission readiness](ADMISSION_READINESS.md) and [findings](QUALIFICATION_FINDINGS.md).
