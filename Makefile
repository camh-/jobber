# Makefile for jobber. Run `make` or `make help` to display valid targets

.DEFAULT_GOAL = help
O = out
VERSION ?= $(shell git describe --tags --dirty --always)

# --- CI -----------------------------------------------------------------------

REQUIRE_UPTODATE =

ci: clean check-uptodate all  ## Full clean, build and up-to-date checks for CI

all:  ## Nothing yet. Will be build, test, check coverage and lint

check-uptodate: proto tidy  ## Check that committed generated files are up-to-date
	test -z "$$(git status --porcelain -- $(REQUIRE_UPTODATE))" || { git diff -- $(REQUIRE_UPTODATE); git status $(REQUIRE_UPTODATE); false; }

clean::  ## Remove generated files not to be committed
	-rm -rf $(O)

.PHONY: all check-uptodate ci clean

# --- Go -----------------------------------------------------------------------

CMDS = .
GO_LDFLAGS = -X main.version=$(VERSION)

build: | $(O)
	go build -o $(O) -ldflags='$(GO_LDFLAGS)' $(CMDS)

test: | $(O)
	go test -race ./...

tidy:  ## Tidy go modules with "go mod tidy"
	go mod tidy

REQUIRE_UPTODATE += go.mod go.sum

.PHONY: tidy

# --- Protobuf -----------------------------------------------------------------

proto:  ## Generate Go pb and grpc bindings for .proto files
	protoc -I proto proto/jobexec.proto \
		--go_out=paths=source_relative:pb \
		--go-grpc_out=paths=source_relative:pb

REQUIRE_UPTODATE += pb

.PHONY: proto

# --- Utilities ----------------------------------------------------------------
COLOUR_NORMAL = $(shell tput sgr0 2>/dev/null)
COLOUR_WHITE  = $(shell tput setaf 7 2>/dev/null)

help:  ## Display this help message
	@echo 'Available targets:'
	@awk -F ':.*## ' 'NF == 2 && $$1 ~ /^[A-Za-z0-9%_-]+$$/ { printf "$(COLOUR_WHITE)%-25s$(COLOUR_NORMAL)%s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort

$(O):
	@mkdir -p $@

.PHONY: help

define nl


endef

ifndef ACTIVE_HERMIT
$(eval $(subst \n,$(nl),$(shell bin/hermit env -r | sed 's/^\(.*\)$$/export \1\\n/')))
endif
