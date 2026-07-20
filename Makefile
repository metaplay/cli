# Makefile for Metaplay CLI

ifeq ($(OS),Windows_NT)
  BIN_SUFFIX=.exe
else
  BIN_SUFFIX=
endif

# Keep in sync with the version pinned in .github/workflows/build.yaml.
GOLANGCI_LINT_VERSION ?= v2.12.2

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
	go build -o dist/metaplay$(BIN_SUFFIX) .

test:
	go test ./...
