GO ?= go
BINARY ?= upskill
CMD ?= ./cmd/upskill
BIN_DIR ?= bin
DIST_DIR ?= dist
COVERAGE_PROFILE ?= coverage.out
COVERAGE_HTML ?= coverage.html
PLATFORMS ?= darwin/amd64 darwin/arm64 linux/amd64 linux/arm64 windows/amd64

.DEFAULT_GOAL := build

.PHONY: help fmt vet test coverage coverage-html build build-all clean

help:
	@printf '%s\n' 'Targets:'
	@printf '  %-14s %s\n' 'fmt' 'Format Go sources'
	@printf '  %-14s %s\n' 'vet' 'Run go vet'
	@printf '  %-14s %s\n' 'test' 'Run tests without cache'
	@printf '  %-14s %s\n' 'coverage' 'Run tests and print coverage summary'
	@printf '  %-14s %s\n' 'coverage-html' 'Generate HTML coverage report'
	@printf '  %-14s %s\n' 'build' 'Build local binary into bin/'
	@printf '  %-14s %s\n' 'build-all' 'Build release binaries into dist/'
	@printf '  %-14s %s\n' 'clean' 'Remove generated build and coverage artifacts'

fmt:
	$(GO)fmt -w cmd internal

vet:
	$(GO) vet ./...

test:
	$(GO) test ./... -count=1

coverage:
	$(GO) test ./... -count=1 -coverprofile=$(COVERAGE_PROFILE) -covermode=count
	$(GO) tool cover -func=$(COVERAGE_PROFILE)

coverage-html: coverage
	$(GO) tool cover -html=$(COVERAGE_PROFILE) -o $(COVERAGE_HTML)

build:
	@mkdir -p $(BIN_DIR)
	$(GO) build -o $(BIN_DIR)/$(BINARY) $(CMD)

build-all:
	@mkdir -p $(DIST_DIR)
	@for target in $(PLATFORMS); do \
		os=$${target%/*}; \
		arch=$${target#*/}; \
		out="$(DIST_DIR)/$(BINARY)-$${os}-$${arch}"; \
		if [ "$$os" = "windows" ]; then out="$$out.exe"; fi; \
		printf 'building %s\n' "$$out"; \
		GOOS=$$os GOARCH=$$arch $(GO) build -o "$$out" $(CMD); \
	done

clean:
	rm -rf $(BIN_DIR) $(DIST_DIR) $(COVERAGE_PROFILE) $(COVERAGE_HTML)
