# Makefile for jobber. Run `make` or `make help` to display valid targets

.DEFAULT_GOAL = help
O = out
VERSION ?= $(shell git describe --tags --dirty --always)

# --- CI -----------------------------------------------------------------------

REQUIRE_UPTODATE =

ci: clean check-uptodate all  ## Full clean, build and up-to-date checks for CI

all: build test lint  ## build, test and lint

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

lint:
	golangci-lint run

REQUIRE_UPTODATE += go.mod go.sum

.PHONY: tidy

# --- Protobuf -----------------------------------------------------------------

proto:  ## Generate Go pb and grpc bindings for .proto files
	protoc -I proto proto/jobexec.proto \
		--go_out=paths=source_relative:pb \
		--go-grpc_out=paths=source_relative:pb

REQUIRE_UPTODATE += pb

.PHONY: proto

# --- Certificates -------------------------------------------------------------

CERTDIR = certs
CERTSTRAP = certstrap --depot-path $(CERTDIR)

$(CERTDIR)/ca.key $(CERTDIR)/ca.crt: | $(CERTDIR) install-certstrap
	$(CERTSTRAP) init --common-name ca --expires "10 years" --curve P-256 --passphrase ""

$(CERTDIR)/server.key $(CERTDIR)/server.crt: | $(CERTDIR)/ca.key $(CERTDIR) install-certstrap
	$(CERTSTRAP) request-cert --common-name server --ip 0.0.0.0 --domain localhost --passphrase ""
	$(CERTSTRAP) sign server --expires "3 months" --CA ca

$(CERTDIR)/%.key $(CERTDIR)/%.crt: | $(CERTDIR)/ca.key $(CERTDIR) install-certstrap
	$(CERTSTRAP) request-cert --common-name $* --passphrase ""
	$(CERTSTRAP) sign $(USER) --expires "7 days" --CA ca

default-user-cert: | $(CERTDIR)/$(USER).key  ## Set user "$(USER)" as default user cert
	ln -nsf $(USER).key $(CERTDIR)/user.key
	ln -nsf $(USER).crt $(CERTDIR)/user.crt

$(CERTDIR):
	@mkdir -p $@

clean-certs::  ## Remove generated certificates
	rm -rf $(CERTDIR)

.PHONY: clean-certs default-user-cert


# --- Utilities ----------------------------------------------------------------
COLOUR_NORMAL = $(shell tput sgr0 2>/dev/null)
COLOUR_WHITE  = $(shell tput setaf 7 2>/dev/null)

install-certstrap: $(O)/bin/certstrap  ## install certstrap utility for generating certs
$(O)/bin/certstrap:
	go install github.com/square/certstrap@master

help:  ## Display this help message
	@echo 'Available targets:'
	@awk -F ':.*## ' 'NF == 2 && $$1 ~ /^[A-Za-z0-9%_-]+$$/ { printf "$(COLOUR_WHITE)%-25s$(COLOUR_NORMAL)%s\n", $$1, $$2}' $(MAKEFILE_LIST) | sort

$(O):
	@mkdir -p $@

.PHONY: help install-certstrap

define nl


endef

ifndef ACTIVE_HERMIT
$(eval $(subst \n,$(nl),$(shell bin/hermit env -r | sed 's/^\(.*\)$$/export \1\\n/')))
endif
