# Makefile for jobber. Run `make` or `make help` to display valid targets

.DEFAULT_GOAL = help

# --- CI -----------------------------------------------------------------------

ci: clean check-uptodate all  ## Full clean, build and up-to-date checks for CI

all:  ## Nothing yet. Will be build, test, check coverage and lint

check-uptodate:  ## Check that committed generated files are up-to-date

clean::  ## Remove generated files not to be committed

.PHONY: all check-uptodate ci clean

# --- Protobuf -----------------------------------------------------------------

proto:  ## Generate Go pb and grpc bindings for .proto files
	protoc -I proto proto/jobexec.proto \
		--go_out=paths=source_relative:pb \
		--go-grpc_out=paths=source_relative:pb

.PHONY: proto

# --- Utilities ----------------------------------------------------------------
COLOUR_NORMAL = $(shell tput sgr0 2>/dev/null)
COLOUR_WHITE  = $(shell tput setaf 7 2>/dev/null)

help:  ## Display this help message
	@echo 'Available targets:'
	@awk -F ':.*## ' 'NF == 2 && $$1 ~ /^[A-Za-z0-9%_-]+$$/ { printf "$(COLOUR_WHITE)%-25s$(COLOUR_NORMAL)%s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort

.PHONY: help

define nl


endef

ifndef ACTIVE_HERMIT
$(eval $(subst \n,$(nl),$(shell bin/hermit env -r | sed 's/^\(.*\)$$/export \1\\n/')))
endif
