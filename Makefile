SHELL := /bin/sh

GO ?= go
GOFMT ?= gofmt
STATICCHECK ?= staticcheck
GOVULNCHECK ?= govulncheck
GO_TEST_PARALLEL ?= 2
ARTIFACT_DIR ?= $(shell mktemp -d /tmp/go-kitty3-campaign.XXXXXX)
BUILD_DIR ?= $(shell mktemp -d /tmp/go-kitty3-build.XXXXXX)
PROVIDER_TMP_DIR ?= $(shell mktemp -d /tmp/go-kitty3-provider.XXXXXX)
QUALIFIED_BEADS_DIGEST := 0c9990691b3b574d4772c0a9fed260769d68af57eb8072ee2a60ff0b619c7bb6
BEADS_BIN ?=
BEADS_INSTALL_RESULT ?=
OFFLINE_ENFORCED ?=

.DEFAULT_GOAL := help
.NOTPARALLEL:

.PHONY: help tools preflight contract acceptance mission-contract mission-acceptance mission-adversarial phase3-publication scope race static dependencies quality fault soak scenario-full full-campaign fmt-check build beads-contract beads-qualification beads-all

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

race: ## Run the authority-critical packages under the race detector.
	$(GO) test -race -p=1 -count=1 ./internal/mission ./internal/artifact ./internal/workgraph ./internal/governancecontext ./internal/tracker/fake ./internal/controlstore/sqlite ./internal/execution ./internal/helpercmd ./internal/testcampaign

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
	env -u BEADS_BIN -u BEADS_SERVER_ID -u BEADS_SERVER_ENDPOINT -u DOLT_SERVER_HOST -u DOLT_SERVER_PORT -u DOLT_CREDENTIALS_PATH GOPROXY=off GOSUMDB=off $(GO) run ./internal/testcampaign/cmd/campaign -campaign provider_bounded -artifact-dir "$(ARTIFACT_DIR)" -timeout 9m

quality: preflight contract beads-contract mission-contract mission-acceptance race static dependencies ## Run the bounded pull-request campaign.
	$(GO) run ./internal/testcampaign/cmd/campaign -campaign quality -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m
	@printf 'quality campaign passed; evidence=%s\n' "$(ARTIFACT_DIR)"

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
