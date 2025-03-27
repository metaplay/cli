# Makefile for Metaplay CLI

ifeq ($(OS),Windows_NT)
  BIN_SUFFIX=.exe
else
  BIN_SUFFIX=
endif

.PHONY: all clean build test

all: build

build:
	go build -o dist/metaplay$(BIN_SUFFIX) .

clean:
	rm -rf dist

test:
	go test ./...
