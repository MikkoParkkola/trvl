GOTOOLCHAIN ?= go1.26.4
GO ?= go
GO_RUN = GOTOOLCHAIN=$(GOTOOLCHAIN) $(GO)

VERSION ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo dev)
LDFLAGS := -ldflags "-s -w -X main.Version=$(VERSION) -X github.com/MikkoParkkola/trvl/mcp.serverVersion=$(VERSION)"

.PHONY: build test test-proof test-coverage test-live-integrations test-live-probes lint distribution-metrics clean cross install safe-clean force-clean

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
	TRVL_TEST_LIVE_PROBES=1 $(GO_RUN) test -v -count=1 ./internal/flights ./internal/hotels -run Probe

lint:
	$(GO_RUN) vet ./...
	@if command -v staticcheck >/dev/null 2>&1; then \
		GOTOOLCHAIN=$(GOTOOLCHAIN) staticcheck ./...; \
	else \
		echo "staticcheck not installed, skipping"; \
	fi
	@if command -v govulncheck >/dev/null 2>&1; then \
		GOTOOLCHAIN=$(GOTOOLCHAIN) govulncheck ./...; \
	else \
		echo "govulncheck not installed, skipping"; \
	fi

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
