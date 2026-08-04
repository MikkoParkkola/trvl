GOTOOLCHAIN ?= go1.26.5
GO ?= go
GO_RUN = GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO)
GOLANGCI_LINT_VERSION ?= v2.12.2
GOSEC_VERSION ?= v2.28.0

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION) -X github.com/MikkoParkkola/trvl/mcp.serverVersion=$(VERSION)"

.PHONY: build test test-proof test-coverage test-live-integrations test-live-probes lint repo-hygiene security-gosec dod distribution-metrics clean cross install safe-clean force-clean

build:
	@mkdir -p bin
	$(GO_RUN) build $(LDFLAGS) -o bin/trvl ./cmd/trvl

test:
	$(GO_RUN) test ./...

test-proof:
	$(GO_RUN) test -v -count=1 -race ./...

test-coverage:
	$(GO_RUN) test -p=1 -race -coverprofile coverage.out ./...
	@coverage_report="$$( $(GO_RUN) tool cover -func=coverage.out )" && \
		printf '%s\n' "$$coverage_report" | tail -1

test-live-integrations:
	TRVL_TEST_LIVE_INTEGRATIONS=1 $(GO_RUN) test -v -count=1 ./...

test-live-probes:
	TRVL_TEST_LIVE_PROBES=1 $(GO_RUN) test -v -count=1 ./... -run Probe

repo-hygiene:
	scripts/ci/check-workflow-hygiene.sh
	scripts/ci/check-language-hygiene.sh
	scripts/ci/check-file-size.sh
	scripts/ci/check-release-metadata.sh
	scripts/ci/check-home-isolation.sh

lint: repo-hygiene
	$(GO_RUN) vet ./...
	@if ! command -v golangci-lint >/dev/null 2>&1; then \
		echo "golangci-lint not installed. Install with: GOTOOLCHAIN=$(GOTOOLCHAIN) go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION)" >&2; \
		exit 1; \
	fi
	GOTOOLCHAIN=$(GOTOOLCHAIN) golangci-lint run ./...
	@if ! command -v staticcheck >/dev/null 2>&1; then \
		echo "staticcheck not installed. Install with: GOTOOLCHAIN=$(GOTOOLCHAIN) go install honnef.co/go/tools/cmd/staticcheck@v0.7.0" >&2; \
		exit 1; \
	fi
	GOTOOLCHAIN=$(GOTOOLCHAIN) staticcheck ./...
	@if ! command -v govulncheck >/dev/null 2>&1; then \
		echo "govulncheck not installed. Install with: GOTOOLCHAIN=$(GOTOOLCHAIN) go install golang.org/x/vuln/cmd/govulncheck@v1.4.0" >&2; \
		exit 1; \
	fi
	GOTOOLCHAIN=$(GOTOOLCHAIN) govulncheck ./...

# Local mirror of the CI `gosec` job. Same scan scope, same pinned version and
# same baseline, so a clean run here means a clean run there.
security-gosec:
	.github/scripts/gosec-gate_test.sh
	@if ! command -v gosec >/dev/null 2>&1; then \
		echo "gosec not installed. Install with: GOTOOLCHAIN=$(GOTOOLCHAIN) go install github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION)" >&2; \
		exit 1; \
	fi
	@mkdir -p bin
	@GOTOOLCHAIN=$(GOTOOLCHAIN) gosec -quiet -fmt json -out bin/gosec-report.json ./... || true
	@test -s bin/gosec-report.json
	.github/scripts/gosec-gate.sh bin/gosec-report.json

# Everything CI gates on, in one command, to be run BEFORE committing.
#
# The individual targets already existed; what did not was a single name for
# "would CI accept this". Splitting the question across four commands meant
# running three of them and pushing, which is how a merge shipped with two files
# over the 800-line ceiling and 26 unchecked error returns -- each caught by CI
# minutes later, after a 23-minute Windows job had already started.
#
# Ordered cheapest-first so an obvious failure stops the run early. gosec needs
# $(GOPATH)/bin on PATH; the target says so rather than failing obscurely.
#
# Known gap worth stating: scripts/ci/check-file-size.sh inspects TRACKED files
# only, so a newly added file reports clean until it is `git add`ed. Stage first,
# then run this.
dod: lint security-gosec test
	@echo
	@echo "dod: lint, gosec and tests all clean -- safe to commit."

distribution-metrics:
	$(GO_RUN) run ./cmd/distribution-metrics

clean:
	rm -f bin/trvl
	rm -f coverage.out
	rm -rf dist/

install:
	$(GO_RUN) build $(LDFLAGS) -o ~/.local/bin/trvl ./cmd/trvl

safe-clean: install
	rm -f bin/trvl
	rm -f coverage.out
	rm -rf dist/

force-clean:
	rm -f bin/trvl
	rm -f coverage.out
	rm -rf dist/

cross:
	@mkdir -p dist
	GOOS=linux  GOARCH=amd64 $(GO_RUN) build $(LDFLAGS) -o dist/trvl-linux-amd64  ./cmd/trvl
	GOOS=linux  GOARCH=arm64 $(GO_RUN) build $(LDFLAGS) -o dist/trvl-linux-arm64  ./cmd/trvl
	GOOS=darwin GOARCH=amd64 $(GO_RUN) build $(LDFLAGS) -o dist/trvl-darwin-amd64 ./cmd/trvl
	GOOS=darwin GOARCH=arm64 $(GO_RUN) build $(LDFLAGS) -o dist/trvl-darwin-arm64 ./cmd/trvl
