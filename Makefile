SHELL := /bin/sh

GO ?= go
GOFMT ?= gofmt
STATICCHECK ?= staticcheck
GOVULNCHECK ?= govulncheck
GO_TEST_PARALLEL ?= 2
ARTIFACT_DIR ?= $(shell mktemp -d /tmp/go-kitty3-campaign.XXXXXX)
BUILD_DIR ?= $(shell mktemp -d /tmp/go-kitty3-build.XXXXXX)

.DEFAULT_GOAL := help
.NOTPARALLEL:

.PHONY: help tools preflight contract acceptance scope race static dependencies quality fault soak scenario-full full-campaign fmt-check build

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

scope: ## Run architecture, reachability, and rejecting-mutant guards.
	$(GO) run ./internal/testcampaign/cmd/campaign -profile architecture -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m
	$(GO) run ./internal/testcampaign/cmd/campaign -profile scope -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m

race: ## Run the authority-critical packages under the race detector.
	$(GO) test -race -p=1 -count=1 ./internal/controlstore/sqlite ./internal/execution ./internal/testcampaign

static: ## Run vet and staticcheck over all packages.
	@command -v $(STATICCHECK) >/dev/null 2>&1 || { echo "required tool missing: $(STATICCHECK) (install honnef.co/go/tools/cmd/staticcheck@v0.7.0)"; exit 1; }
	$(GO) vet ./...
	$(STATICCHECK) ./...

dependencies: ## Verify module bytes and scan reachable vulnerabilities.
	@command -v $(GOVULNCHECK) >/dev/null 2>&1 || { echo "required tool missing: $(GOVULNCHECK) (install golang.org/x/vuln/cmd/govulncheck@v1.6.0)"; exit 1; }
	$(GO) mod verify
	@git diff --exit-code -- go.mod go.sum
	$(GOVULNCHECK) ./...

quality: preflight contract race static dependencies ## Run the bounded pull-request campaign.
	$(GO) run ./internal/testcampaign/cmd/campaign -campaign quality -artifact-dir "$(ARTIFACT_DIR)" -timeout 8m
	@printf 'quality campaign passed; evidence=%s\n' "$(ARTIFACT_DIR)"

fault: ## Run crash, recovery, stale-writer, and zombie profiles.
	$(GO) run ./internal/testcampaign/cmd/campaign -profile fault -artifact-dir "$(ARTIFACT_DIR)" -timeout 30m

soak: ## Run the repeated contention and serialization profile once.
	$(GO) run ./internal/testcampaign/cmd/campaign -profile soak -artifact-dir "$(ARTIFACT_DIR)" -timeout 45m

scenario-full: ## Run every scenario with an exact top-level JSON census.
	$(GO) run ./internal/testcampaign/cmd/campaign -profile full-scenario -artifact-dir "$(ARTIFACT_DIR)" -timeout 60m

full-campaign: quality ## Run quality plus the exhaustive scenario/fault/soak campaign.
	$(GO) run ./internal/testcampaign/cmd/campaign -campaign full_campaign -artifact-dir "$(ARTIFACT_DIR)" -timeout 60m
	@printf 'full campaign passed; evidence=%s\n' "$(ARTIFACT_DIR)"
