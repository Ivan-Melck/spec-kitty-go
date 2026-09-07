SHELL := /bin/sh

GO ?= go
GOFMT ?= gofmt
STATICCHECK ?= staticcheck
GOVULNCHECK ?= govulncheck
GO_TEST_PARALLEL ?= 2
ARTIFACT_DIR := $(if $(ARTIFACT_DIR),$(ARTIFACT_DIR),$(shell mktemp -d /tmp/go-kitty3-campaign.XXXXXX))
BUILD_DIR := $(if $(BUILD_DIR),$(BUILD_DIR),$(shell mktemp -d /tmp/go-kitty3-build.XXXXXX))
PROVIDER_TMP_DIR := $(if $(PROVIDER_TMP_DIR),$(PROVIDER_TMP_DIR),$(shell mktemp -d /tmp/go-kitty3-provider.XXXXXX))
QUALIFIED_BEADS_DIGEST := 0c9990691b3b574d4772c0a9fed260769d68af57eb8072ee2a60ff0b619c7bb6
BEADS_BIN ?=
BEADS_INSTALL_RESULT ?=
OFFLINE_ENFORCED ?=
M2_GAPDBD ?= /tmp/gapdb-m2-reviewed.RTOAmn/gapdbd
M2_RUN_ID ?= m2-$$(git rev-parse --short=12 HEAD)-$$(date -u +%Y%m%dT%H%M%SZ)
M2_EVIDENCE_ROOT ?= $(ARTIFACT_DIR)/m2
M2_PRODUCT_BINARY ?= $(BUILD_DIR)/spec-kitty
M2_QUALIFIER_BINARY ?= $(BUILD_DIR)/spk-m2-qualification
M2_BUILD_SOURCE ?= $(BUILD_DIR)/candidate-source
M2_ACTOR_ID ?=
M2_PROCESS_ID ?=
M2_PROFILE_REVISION ?=
M2_IMPLEMENTATION_CONTRIBUTORS ?=
M2_PROFILE_DOCUMENT ?= $(CURDIR)/docs/programs/evidence/m2-producer-profile.json

.DEFAULT_GOAL := help
.NOTPARALLEL:

.PHONY: help tools preflight contract acceptance mission-contract mission-acceptance mission-adversarial phase3-publication scope race race-m1 static dependencies quality-core quality fault soak scenario-full full-campaign fmt-check build beads-contract beads-qualification beads-all m2-contract m2-capabilities m2-recovery m2-race-smoke m2-race m2-fault m2-mutants-smoke m2-mutants m2-absence-source m2-absence m2-sbom m2-build-candidate m2-performance m2-short m2-qualification m2-verify-evidence

help: ## Show the canonical local and CI validation targets.
	@awk 'BEGIN {FS = ":.*## "; printf "Usage: make <target>\n\nTargets:\n"} /^[a-zA-Z0-9_-]+:.*## / {printf "  %-15s %s\n", $$1, $$2}' $(MAKEFILE_LIST)

fmt-check: ## Reject unformatted Go source.
	@command -v $(GOFMT) >/dev/null 2>&1 || { echo "required tool missing: $(GOFMT)"; exit 1; }; \
	files=$$(find cmd internal -type f -name '*.go' -print); \
	unformatted=$$(if test -n "$$files"; then $(GOFMT) -l $$files; fi); \
	if test -n "$$unformatted"; then printf '%s\n' "$$unformatted"; exit 1; fi

build: ## Compile every package and command without running tests.
	$(GO) test -run '^$$' ./...
	@mkdir -p "$(BUILD_DIR)"
	$(GO) build -trimpath -o "$(BUILD_DIR)/spk-phase1-helper" ./cmd/spk-phase1-helper
	CGO_ENABLED=0 $(GO) build -trimpath -o "$(BUILD_DIR)/spec-kitty" ./cmd/spec-kitty
	CGO_ENABLED=0 $(GO) build -trimpath -o "$(BUILD_DIR)/spk-m2-qualification" ./cmd/spk-m2-qualification

tools:
	@command -v $(GO) >/dev/null 2>&1 || { echo "required tool missing: $(GO)"; exit 1; }
	@command -v $(GOFMT) >/dev/null 2>&1 || { echo "required tool missing: $(GOFMT)"; exit 1; }
	@command -v git >/dev/null 2>&1 || { echo "required tool missing: git"; exit 1; }

preflight: tools fmt-check build ## Check required local tools, formatting, and buildability.
	@printf 'preflight passed: go=%s artifacts=%s\n' "$$($(GO) version)" "$(ARTIFACT_DIR)"

contract: ## Run unit and portable contract packages, excluding expensive scenarios.
	@packages="$$( $(GO) list ./internal/... | awk '$$0 !~ /\/scenario$$/' )"; \
	test -n "$$packages" || { echo "no contract packages discovered"; exit 1; }; \
	$(GO) test -p=$(GO_TEST_PARALLEL) -count=1 $$packages

acceptance: ## Run the bounded acceptance selector with an exact JSON census.
	$(GO) run ./internal/testcampaign/cmd/campaign -profile acceptance -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m

mission-contract: ## Run the bounded Phase 3 contract and universal-invariant census.
	$(GO) run ./internal/testcampaign/cmd/campaign -profile mission-contract -artifact-dir "$(ARTIFACT_DIR)" -timeout 4m

mission-acceptance: ## Run bounded Phase 3 normalization, projection, governance, and pure-query trials.
	$(GO) run ./internal/testcampaign/cmd/campaign -profile mission-acceptance -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m

mission-adversarial: ## Run the slow Phase 3 drift, process-race, and crash census.
	$(GO) run ./internal/testcampaign/cmd/campaign -profile mission-adversarial -artifact-dir "$(ARTIFACT_DIR)" -timeout 20m

phase3-publication: ## Validate a clean Phase 3 payload and docs-only attestation checkout.
	@test -n "$(PAYLOAD_COMMIT)" || { echo 'PAYLOAD_COMMIT is required' >&2; exit 1; }
	@test -n "$(ATTESTATION_COMMIT)" || { echo 'ATTESTATION_COMMIT is required' >&2; exit 1; }
	$(GO) run ./internal/testcampaign/cmd/campaign \
		-publication-root "$(CURDIR)" \
		-payload-commit "$(PAYLOAD_COMMIT)" \
		-attestation-commit "$(ATTESTATION_COMMIT)"

scope: ## Run architecture, reachability, and rejecting-mutant guards.
	$(GO) run ./internal/testcampaign/cmd/campaign -profile architecture -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m
	$(GO) run ./internal/testcampaign/cmd/campaign -profile scope -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m

race-m1: ## Run the bounded concurrency-sensitive M1 package census under the race detector.
	$(GO) test -race -p=1 -count=1 -timeout=4m \
		-run '^(TestOperationAmbiguousReconciliation|TestServiceAggregateConcurrencyIsPerHeadNotGlobal|TestEpochBindingRefusesPredecessorAndLeaseExpiryDoesNotRotate|TestPlaybackFullHistoryIsDeterministicAndZeroIO|TestReducerRepeatedHistoryIsByteIdentical|TestCASCompetingUpdatesHaveExactlyOneWinner|TestLifecyclePidfdObservationAndSignalingSeams|TestRetainedClaimAndOwnerRestartAreRaceFree|TestEvidenceSemanticMutantsCannotMintPass|TestProgressSnapshotIsAtomicReadOnlyDiagnostic|TestRunWithRuntimeBindsOrderedTerminalArtifactsToUmbrellaRun|TestRestartTwice|TestBarrierUsesAcknowledgementRatherThanSleep|TestExactNineteenMutantsBindDistinctNamedProductGuards|TestStatusReportsActiveProgressWithoutMintingReadiness)$$' \
		./internal/controljournal/... \
		./internal/controlstore/gapdb \
		./internal/sidecar/gapdb \
		./internal/provingground \
		./internal/testcampaign/m1/... \
		./cmd/spk-m1-proving-ground

race: race-m1 ## Run the authority-critical packages under the race detector.
	$(GO) test -race -p=1 -count=1 ./internal/mission ./internal/artifact ./internal/workgraph ./internal/governancecontext ./internal/tracker/fake ./internal/controlstore/gapdb ./internal/productionruntime ./internal/sidecar/gapdb ./internal/execution ./internal/helpercmd ./internal/testcampaign

static: ## Run vet and staticcheck over all packages.
	@command -v $(STATICCHECK) >/dev/null 2>&1 || { echo "required tool missing: $(STATICCHECK) (install honnef.co/go/tools/cmd/staticcheck@v0.7.0)"; exit 1; }
	$(GO) vet ./...
	$(STATICCHECK) ./...

dependencies: ## Verify module bytes and scan reachable vulnerabilities.
	@command -v $(GOVULNCHECK) >/dev/null 2>&1 || { echo "required tool missing: $(GOVULNCHECK) (install golang.org/x/vuln/cmd/govulncheck@v1.6.0)"; exit 1; }
	$(GO) mod verify
	@git diff --exit-code -- go.mod go.sum
	$(GOVULNCHECK) ./...

beads-contract: ## Run the exact portable and adversarial provider campaign without Beads or network.
	@mkdir -p "$(ARTIFACT_DIR)"
	env -u BEADS_BIN -u BEADS_SERVER_ID -u BEADS_SERVER_ENDPOINT -u DOLT_SERVER_HOST -u DOLT_SERVER_PORT -u DOLT_CREDENTIALS_PATH GOPROXY=off GOSUMDB=off $(GO) run ./internal/testcampaign/cmd/campaign -campaign provider_bounded -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m

quality-core: preflight contract mission-contract mission-acceptance race static dependencies ## Run the bounded core pull-request campaign; provider qualification is a separate CI job.
	$(GO) run ./internal/testcampaign/cmd/campaign -campaign quality -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m
	@printf 'core quality campaign passed; evidence=%s\n' "$(ARTIFACT_DIR)"

quality: beads-contract quality-core ## Run the complete local pull-request gate aggregate.
	@printf 'quality aggregate passed; evidence=%s\n' "$(ARTIFACT_DIR)"

beads-qualification: ## Run real pinned Beads qualification only inside an enforced offline boundary.
	@set -eu; mkdir -p "$(ARTIFACT_DIR)" "$(PROVIDER_TMP_DIR)"; \
	fallback="$(ARTIFACT_DIR)/beads-qualification.non-admissible.json"; \
	evidence="$(ARTIFACT_DIR)/beads-qualification.json"; \
	started=$$(date -u +%Y-%m-%dT%H:%M:%SZ); \
	commit=$$(git rev-parse HEAD); tree=$$(git rev-parse 'HEAD^{tree}'); \
	goos=$$($(GO) env GOOS); goarch=$$($(GO) env GOARCH); \
	write_fallback() { finished=$$(date -u +%Y-%m-%dT%H:%M:%SZ); temporary="$$fallback.tmp"; rm -f "$$temporary"; printf '{"schema_version":1,"source_commit":"%s","source_tree":"%s","provider_version":"1.2.2","provider_digest_sha256":"%s","protocol_schema":1,"storage_mode":"embedded_serialized","host":{"goos":"%s","goarch":"%s","runner":"local"},"started_at":"%s","finished_at":"%s","scenarios":{"expected":11,"started":0,"terminal":0,"passed":0,"failed":0},"authority_audits":0,"authority_invariant_failures":0,"issues":[{"kind":"prerequisite_missing","message":"verified offline Beads prerequisite unavailable"}],"verdict":"non_admissible"}\n' "$$commit" "$$tree" "$(QUALIFIED_BEADS_DIGEST)" "$$goos" "$$goarch" "$$started" "$$finished" > "$$temporary"; chmod 600 "$$temporary"; mv -f "$$temporary" "$$fallback"; }; \
	write_fallback; \
	case "$(BEADS_BIN)" in /*) ;; *) echo 'BEADS_BIN must be an absolute verified path' >&2; exit 1;; esac; \
	case "$(BEADS_INSTALL_RESULT)" in /*) ;; *) echo 'BEADS_INSTALL_RESULT must be an absolute installer JSON path' >&2; exit 1;; esac; \
	test "$(OFFLINE_ENFORCED)" = linux-network-namespace || { echo 'real qualification requires OFFLINE_ENFORCED=linux-network-namespace' >&2; exit 1; }; \
	current_net=$$(readlink /proc/self/ns/net); host_net=$$(readlink /proc/1/ns/net); test "$$current_net" != "$$host_net" || { echo 'real qualification is not inside an isolated network namespace' >&2; exit 1; }; \
	command -v jq >/dev/null 2>&1 || { echo 'jq is required to verify installer identity' >&2; exit 1; }; \
	test -x "$(BEADS_BIN)" || { echo 'verified BEADS_BIN is not executable' >&2; exit 1; }; \
	jq -e --arg path "$(BEADS_BIN)" --arg digest "$(QUALIFIED_BEADS_DIGEST)" --arg goos "$$goos" --arg goarch "$$goarch" 'type == "object" and (keys | length == 9) and .schema_version == 1 and .version == "1.2.2" and .digest_sha256 == $$digest and .executable_path == $$path and .source_version == "v1.2.2" and .source_commit == "6c124203e771433a3550c348771a5b5e27fd3c21" and .goos == $$goos and .goarch == $$goarch and .build_method == "go-install-cgo-gms_pure_go-trimpath-nobuildid-go1.26.4"' "$(BEADS_INSTALL_RESULT)" >/dev/null || { echo 'installer identity is not the qualified tuple' >&2; exit 1; }; \
	observed=$$(sha256sum -- "$(BEADS_BIN)" | awk '{print $$1}'); \
	test "$$observed" = "$(QUALIFIED_BEADS_DIGEST)" || { echo 'BEADS_BIN digest changed before qualification' >&2; exit 1; }; \
	go_env="$(PROVIDER_TMP_DIR)/offline-go.env"; printf 'GOPROXY=off\nGOSUMDB=off\n' > "$$go_env"; chmod 600 "$$go_env"; \
	rm -f "$$evidence"; \
	set +e; TMPDIR="$(PROVIDER_TMP_DIR)" GOENV="$$go_env" GOPROXY=off GOSUMDB=off BEADS_BIN="$(BEADS_BIN)" BEADS_DIGEST_SHA256="$(QUALIFIED_BEADS_DIGEST)" $(GO) run ./internal/testcampaign/cmd/beads-qualification -beads-bin "$(BEADS_BIN)" -digest "$(QUALIFIED_BEADS_DIGEST)" -evidence "$$evidence" -repository "$$(pwd)" -timeout 20m; status=$$?; set -e; \
	if test -f "$$evidence"; then rm -f "$$fallback"; fi; \
	exit $$status

beads-all: beads-contract beads-qualification ## Run bounded provider evidence, then explicit real qualification.

fault: ## Run crash, recovery, stale-writer, and zombie profiles.
	$(GO) run ./internal/testcampaign/cmd/campaign -profile fault -artifact-dir "$(ARTIFACT_DIR)" -timeout 30m

soak: ## Run the repeated contention and serialization profile once.
	$(GO) run ./internal/testcampaign/cmd/campaign -profile soak -artifact-dir "$(ARTIFACT_DIR)" -timeout 45m

scenario-full: ## Run every scenario with an exact top-level JSON census.
	$(GO) run ./internal/testcampaign/cmd/campaign -profile full-scenario -artifact-dir "$(ARTIFACT_DIR)" -timeout 60m

full-campaign: quality ## Run quality plus the exhaustive scenario/fault/soak campaign.
	$(GO) run ./internal/testcampaign/cmd/campaign -campaign full_campaign -artifact-dir "$(ARTIFACT_DIR)" -timeout 60m
	$(GO) run ./internal/testcampaign/cmd/campaign -campaign mission_full -artifact-dir "$(ARTIFACT_DIR)" -timeout 20m
	@printf 'full campaign passed; evidence=%s\n' "$(ARTIFACT_DIR)"

m2-contract: ## Verify frozen M2 contracts, family registry, manifest, census, and source binding.
	$(GO) test -count=1 ./internal/testcampaign/m2 ./internal/architecture \
		-run '^(TestManifest|TestFrozenCensus|TestM2Contract|TestM2DependencyRestoreDeadline|TestM2DependencyRestoreTimeUse)'

m2-capabilities: ## Strictly run capability/readiness contracts, including the admitted-sidecar real-host concurrency scenario.
	@test -x "$(M2_GAPDBD)" || { echo 'm2-capabilities requires executable M2_GAPDBD' >&2; exit 1; }
	@test "$$(sha256sum "$(M2_GAPDBD)" | awk '{print $$1}')" = 4571ee2724e4b8194c8dda057adc471fcc1bcedc592e7c47a78282a9ef6388d2 || { echo 'M2_GAPDBD is not the exact qualification candidate' >&2; exit 1; }
	SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE="$(M2_GAPDBD)" $(GO) run ./cmd/spk-m2-qualification short --repository "$(CURDIR)" --group capabilities-full --timeout 8m

m2-recovery: ## Run the checkpoint-plus-10k recovery qualification and deadline laws.
	$(GO) test -count=1 ./internal/controljournal/v2/restore ./internal/controljournal/v2/playback \
		-run '^(TestQualificationFixtureCheckpointPlus10000Operations30Runs|TestRestoreDeadline|TestPlaybackFullHistoryIsDeterministicAndZeroIO)'

m2-race-smoke: ## Run the sidecar-free bounded PR concurrency slice under the race detector.
	$(GO) test -race -p=1 -count=1 -timeout=8m \
		./internal/controljournal/application ./internal/integration ./internal/testcampaign/m2/iterationreceipt \
		-run '^(TestServiceAggregateConcurrencyIsPerHeadNotGlobal|TestIntegrationTargetAuthorityAdmitsOneOfOneHundredContendersV2|TestRecordBindsExecutingIterationFacts)$$'

m2-race: ## Run bounded M2 authority/restart/concurrency smoke under the race detector.
	SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE="$(M2_GAPDBD)" $(GO) test -race -p=1 -count=1 -timeout=20m \
		./internal/scenario ./internal/productionruntime ./internal/testcampaign/m2 \
		-run '^(TestTwoItemMissionRestartAfterEachDurableBoundary|TestConcurrentIndependentAuthoritySmoke|TestConcurrentSameAggregateConflictSmoke|TestProductionHostRealSidecarRestartRebuildsMemoryAndRotatesEpoch|TestManifestCensusVerifyRoundTripIsCanonicalAndSourceBound|TestFrozenCensusRejectsDeletionReorderAndSubstitution)$$'

m2-fault: ## Run exact 100-trial fault, ambiguity, tracker, concurrency, and restart campaigns.
	SPEC_KITTY_WP14_AUTHORITY_EXECUTABLE="$(M2_GAPDBD)" SPEC_KITTY_WP13_GAPDBD="$(M2_GAPDBD)" \
	SPEC_KITTY_WP14_CAMPAIGN=100 SPEC_KITTY_WP14_SOURCE_REVISION="$$(git rev-parse HEAD)" \
	$(GO) test -count=1 -timeout=60m ./internal/scenario \
		-run '^(TestZombieOwnerReplacementCampaign100|TestConcurrentIndependentAuthorityCampaign100|TestConcurrentSameAggregateConflictCampaign100|TestCrashAmbiguousDishonestUniversalCampaign100)$$'

m2-mutants: m2-mutants-smoke ## Kill the full M2 mutant census, including exact admitted-sidecar production-runtime mutants.
	@test -x "$(M2_GAPDBD)" || { echo 'm2-mutants requires executable M2_GAPDBD' >&2; exit 1; }
	@test "$$(sha256sum "$(M2_GAPDBD)" | awk '{print $$1}')" = 4571ee2724e4b8194c8dda057adc471fcc1bcedc592e7c47a78282a9ef6388d2 || { echo 'M2_GAPDBD is not the exact qualification candidate' >&2; exit 1; }
	SPEC_KITTY_WP13_GAPDBD="$(M2_GAPDBD)" $(GO) run ./cmd/spk-m2-qualification short --repository "$(CURDIR)" --group mutants-full --timeout 8m

m2-mutants-smoke: ## Strictly prove the sidecar-free PR mutant census with no missing or skipped selector.
	$(GO) run ./cmd/spk-m2-qualification short --repository "$(CURDIR)" --group mutants --timeout 8m

m2-absence: ## Qualify source/module/binary/install/help/runtime SQLite absence from a clean archive.
	@mkdir -p "$(M2_EVIDENCE_ROOT)"
	SPEC_KITTY_WP15_GAPDBD="$(M2_GAPDBD)" scripts/m2-absence/inspect.sh "$(M2_EVIDENCE_ROOT)/absence"

m2-absence-source: ## Run the sidecar-free structural source and module-graph absence gate.
	$(GO) test -count=1 ./internal/architecture \
		-run '^(TestM2SQLiteAbsence|TestM2GateSourcesDoNotShelterExecutableBehavior|TestM2DependencyPolicyAcceptsOnlyBoundedProviderComposition)$$'

m2-sbom: ## Generate deterministic CycloneDX twice and require byte identity.
	@mkdir -p "$(M2_EVIDENCE_ROOT)"
	$(GO) run ./cmd/spk-m2-qualification sbom --repository "$(CURDIR)" --output "$(M2_EVIDENCE_ROOT)/sbom-1.cdx.json"
	$(GO) run ./cmd/spk-m2-qualification sbom --repository "$(CURDIR)" --output "$(M2_EVIDENCE_ROOT)/sbom-2.cdx.json"
	cmp "$(M2_EVIDENCE_ROOT)/sbom-1.cdx.json" "$(M2_EVIDENCE_ROOT)/sbom-2.cdx.json"

m2-build-candidate: ## Build exact-source M2 binaries from a clean standalone clone.
	@mkdir -p "$(BUILD_DIR)"
	@test -z "$$(git -C "$(CURDIR)" status --porcelain=v1 --untracked-files=all)" || { echo 'M2 candidate source is not clean' >&2; exit 1; }
	@test ! -e "$(M2_BUILD_SOURCE)" || { echo 'M2_BUILD_SOURCE already exists; use a fresh BUILD_DIR' >&2; exit 1; }
	@umask 077; git clone --quiet --no-hardlinks --no-checkout "$(CURDIR)" "$(M2_BUILD_SOURCE)"
	@candidate="$$(git -C "$(CURDIR)" rev-parse HEAD)"; \
	tree="$$(git -C "$(CURDIR)" rev-parse 'HEAD^{tree}')"; \
	git -C "$(M2_BUILD_SOURCE)" checkout --quiet --detach "$$candidate"; \
	test -d "$(M2_BUILD_SOURCE)/.git"; \
	test "$$(git -C "$(M2_BUILD_SOURCE)" rev-parse HEAD)" = "$$candidate"; \
	test "$$(git -C "$(M2_BUILD_SOURCE)" rev-parse 'HEAD^{tree}')" = "$$tree"; \
	test -z "$$(git -C "$(M2_BUILD_SOURCE)" status --porcelain=v1 --untracked-files=all)"; \
	printf 'm2 candidate build source: commit=%s tree=%s\n' "$$candidate" "$$tree"
	@toolchain="$$(cd "$(M2_BUILD_SOURCE)" && $(GO) version | awk '{print $$3}')"; \
	test "$$toolchain" = go1.26.7 || { echo "M2 candidate build requires go1.26.7, got $$toolchain" >&2; exit 1; }
	@cd "$(M2_BUILD_SOURCE)" && CGO_ENABLED=0 $(GO) build -trimpath -o "$(abspath $(M2_PRODUCT_BINARY))" ./cmd/spec-kitty
	@cd "$(M2_BUILD_SOURCE)" && CGO_ENABLED=0 $(GO) build -trimpath -o "$(abspath $(M2_QUALIFIER_BINARY))" ./cmd/spk-m2-qualification
	@candidate="$$(git -C "$(CURDIR)" rev-parse HEAD)"; \
	for binary in "$(abspath $(M2_PRODUCT_BINARY))" "$(abspath $(M2_QUALIFIER_BINARY))"; do \
		metadata="$$(go version -m "$$binary")"; \
		printf '%s\n' "$$metadata" | grep -Fq "vcs.revision=$$candidate"; \
		printf '%s\n' "$$metadata" | grep -Fq 'vcs.modified=false'; \
		printf '%s\n' "$$metadata" | grep -Fq -- '-trimpath=true'; \
		printf '%s\n' "$$metadata" | grep -Fq 'CGO_ENABLED=0'; \
	done

m2-performance: ## Run independently observed recovery/query/durable-mutation qualification.
	@mkdir -p "$(M2_EVIDENCE_ROOT)" "$(BUILD_DIR)"
	@$(MAKE) --no-print-directory m2-build-candidate
	"$(M2_QUALIFIER_BINARY)" measure --repository "$(CURDIR)" --sidecar "$(M2_GAPDBD)" --product-binary "$(M2_PRODUCT_BINARY)" --run-id "$(M2_RUN_ID)" --output "$(M2_EVIDENCE_ROOT)/performance.json"

m2-short: m2-mutants-smoke ## Run the strict sidecar-free PR contracts, structural absence, bounded race, mutant, static, and module gates.
	$(GO) run ./cmd/spk-m2-qualification short --repository "$(CURDIR)" --group core --timeout 8m
	$(GO) test -run '^$$' ./...
	$(GO) vet ./...
	@command -v $(STATICCHECK) >/dev/null 2>&1 || { echo "required tool missing: $(STATICCHECK)"; exit 1; }
	$(STATICCHECK) ./...
	$(GO) mod verify
	@git diff --exit-code -- go.mod go.sum

m2-qualification: ## Produce one immutable technical-pass bundle eligible for independent review.
	@test -n "$(M2_ACTOR_ID)" || { echo 'M2_ACTOR_ID is required' >&2; exit 1; }
	@test -n "$(M2_PROCESS_ID)" || { echo 'M2_PROCESS_ID is required' >&2; exit 1; }
	@test -n "$(M2_PROFILE_REVISION)" || { echo 'M2_PROFILE_REVISION is required' >&2; exit 1; }
	@test -n "$(M2_IMPLEMENTATION_CONTRIBUTORS)" || { echo 'M2_IMPLEMENTATION_CONTRIBUTORS is required' >&2; exit 1; }
	@mkdir -p "$(M2_EVIDENCE_ROOT)" "$(BUILD_DIR)"
	@$(MAKE) --no-print-directory m2-build-candidate
	"$(M2_QUALIFIER_BINARY)" run --repository "$(CURDIR)" --output "$(M2_EVIDENCE_ROOT)" \
		--sidecar "$(M2_GAPDBD)" --product-binary "$(M2_PRODUCT_BINARY)" --run-id "$(M2_RUN_ID)" \
		--actor-id "$(M2_ACTOR_ID)" --process-id "$(M2_PROCESS_ID)" \
		--implementation-contributors "$(M2_IMPLEMENTATION_CONTRIBUTORS)" \
		--profile-document "$(M2_PROFILE_DOCUMENT)" --profile-revision "$(M2_PROFILE_REVISION)" --timeout 60m

m2-verify-evidence: ## Independently recompute one immutable M2 producer bundle against trusted identities.
	@test -n "$(M2_EVIDENCE)" || { echo 'M2_EVIDENCE is required' >&2; exit 1; }
	@test -n "$(M2_EXPECTED_COMMIT)" -a -n "$(M2_EXPECTED_TREE)" -a -n "$(M2_EXPECTED_MANIFEST_SHA256)" -a -n "$(M2_EXPECTED_ARCHIVE_SHA256)" || { echo 'M2 trusted commit/tree/manifest/archive identities are required' >&2; exit 1; }
	$(GO) run ./cmd/spk-m2-qualification verify --evidence "$(M2_EVIDENCE)/evidence" \
		--archive "$(M2_EVIDENCE)/producer.tar" --archive-receipt "$(M2_EVIDENCE)/producer.sha256.json" \
		--expected-commit "$(M2_EXPECTED_COMMIT)" --expected-tree "$(M2_EXPECTED_TREE)" \
		--expected-manifest-sha256 "$(M2_EXPECTED_MANIFEST_SHA256)" --expected-archive-sha256 "$(M2_EXPECTED_ARCHIVE_SHA256)"
