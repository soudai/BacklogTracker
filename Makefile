SHELL := /bin/bash

APP := backlog-tracker
BIN_DIR := bin
BINARY := $(BIN_DIR)/$(APP)
TMPDIR ?= $(CURDIR)/.tmp
GOCACHE ?= $(CURDIR)/.gocache
GOFLAGS ?= -buildvcs=false
GO_ENV := TMPDIR=$(TMPDIR) GOCACHE=$(GOCACHE) GOFLAGS='$(GOFLAGS)'
GO := $(GO_ENV) go

.PHONY: help prepare build test vet fmt fmt-check ci clean run-init run-period-summary run-account-report

help:
	@echo "Targets:"
	@echo "  make build               Build $(BINARY)"
	@echo "  make test                Run go test ./..."
	@echo "  make vet                 Run go vet ./..."
	@echo "  make fmt                 Format tracked Go files"
	@echo "  make fmt-check           Fail if formatting is required"
	@echo "  make ci                  Run fmt-check, vet, and test"
	@echo "  make clean               Remove local build artifacts"
	@echo "  make run-init ARGS='...' Run init command"
	@echo "  make run-period-summary ARGS='...'"
	@echo "  make run-account-report ARGS='...'"

prepare:
	@mkdir -p $(BIN_DIR) $(TMPDIR) $(GOCACHE)

build: prepare
	$(GO) build -o $(BINARY) ./cmd/backlog-tracker

test: prepare
	$(GO) test ./...

vet: prepare
	$(GO) vet ./...

fmt:
	@gofmt -w $$(git ls-files '*.go')

fmt-check:
	@unformatted="$$(gofmt -l $$(git ls-files '*.go'))"; \
	if [[ -n "$$unformatted" ]]; then \
		echo "The following files are not formatted:"; \
		echo "$$unformatted"; \
		exit 1; \
	fi

ci: fmt-check vet test

clean:
	@rm -rf $(BIN_DIR) $(TMPDIR) $(GOCACHE)

run-init: build
	$(BINARY) init $(ARGS)

run-period-summary: build
	$(BINARY) period-summary $(ARGS)

run-account-report: build
	$(BINARY) account-report $(ARGS)
