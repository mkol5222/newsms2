VERSION ?= $(shell date -u +%Y.%m.%d)-$(shell git rev-parse --short HEAD 2>/dev/null || echo dev)
PORT    ?= 60611
LDFLAGS := -s -w -X main.version=$(VERSION)

# lab gateway helper (admin password from macOS Keychain, nothing secret printed)
LAB     ?= ../lab/scripts/lab.sh
REMOTE  := /home/admin/newsms2

.PHONY: help build sha test vet run-local clean upload upload-install upload-check

help: ## show targets
	@grep -E '^[a-z-]+:.*## ' $(MAKEFILE_LIST) | awk -F':.*## ' '{printf "  make %-15s %s\n", $$1, $$2}'

build: ## static linux/amd64 binary for Gaia -> dist/newsms2
	mkdir -p dist
	CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags '$(LDFLAGS)' -o dist/newsms2 .
	@file dist/newsms2 2>/dev/null || true

sha: build ## sha256 to record with the published binary
	shasum -a 256 dist/newsms2

test: ## unit tests
	go test ./...

vet: ## go vet
	go vet ./...

run-local: ## relay on 127.0.0.1:$(PORT), logs + mockup deliveries in ./
	SMSDELIVERED_LOG=$(CURDIR)/smsdelivered.log go run . serve -conf /nonexistent \
	  -listen 127.0.0.1:$(PORT) -handler $(CURDIR)/sms-handler.sh -log $(CURDIR)/newsms2.log

upload: build ## copy dist/newsms2 to the lab gateway ($(REMOTE))
	$(LAB) scp dist/newsms2 $(REMOTE)

upload-install: upload ## upload + install + check on the lab gateway
	$(LAB) ssh 'chmod +x $(REMOTE) && $(REMOTE) install && $(REMOTE) check'

upload-check: ## run newsms2 check on the lab gateway
	$(LAB) ssh '$(REMOTE) check'

clean: ## remove build output and local logs
	rm -rf dist newsms2.log* smsdelivered.log*
