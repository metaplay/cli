# Makefile for Metaplay CLI

ifeq ($(OS),Windows_NT)
  BIN_SUFFIX=.exe
else
  BIN_SUFFIX=
endif

# The golangci-lint version comes from .tool-versions, shared with CI and updated by Renovate.
# $(file) needs make 4.2+ and works from cmd.exe; the cat fallback covers macOS's make 3.81.
TOOL_VERSIONS := $(file < .tool-versions)
ifeq ($(TOOL_VERSIONS),)
  TOOL_VERSIONS := $(shell cat .tool-versions)
endif
GOLANGCI_LINT_VERSION ?= v$(patsubst golangci-lint=%,%,$(filter golangci-lint=%,$(subst golangci-lint ,golangci-lint=,$(TOOL_VERSIONS))))
ifeq ($(GOLANGCI_LINT_VERSION),v)
  $(error Could not read the golangci-lint version from .tool-versions)
endif

.PHONY: all clean build lint test

all: lint build

build:
	go build -o dist/metaplay$(BIN_SUFFIX) .

lint:
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) run

clean:
	rm -rf dist

fix:
	go mod tidy
	go fix ./...
	go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@$(GOLANGCI_LINT_VERSION) fmt
	go build -o dist/metaplay$(BIN_SUFFIX) .

test:
	go test ./...
