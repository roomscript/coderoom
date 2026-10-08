BINARY        := coderoom
INTEGRATION_TEST_TIMEOUT ?= 10m
CODEX_VERSION := $(shell grep -v '^\#' CODEX_VERSION | tr -d '[:space:]')

.PHONY: build
build:
	go build -o bin/$(BINARY) ./cmd/coderoom

.PHONY: lint
lint:
	golangci-lint run ./...

.PHONY: check-goleak
check-goleak:
	./scripts/check-goleak-testmain.sh

.PHONY: check-branding
check-branding:
	./scripts/check-branding.sh

.PHONY: test
test:
	go test ./...

.PHONY: test-race
test-race:
	CGO_ENABLED=1 go test -race ./...

.PHONY: test-integration
test-integration:
	go test -tags integration -p 1 -v -count=1 -timeout=$(INTEGRATION_TEST_TIMEOUT) ./...

.PHONY: test-all
test-all: lint test-race test-integration

.PHONY: pre-commit
pre-commit: check-branding check-goleak lint test-race

.PHONY: upgrade-codex
upgrade-codex:
	./scripts/upgrade-codex-latest.sh

.PHONY: install-hooks
install-hooks:
	ln -sf ../../scripts/pre-commit .git/hooks/pre-commit

.PHONY: commit
commit:
	git commit
	git push

.PHONY: release
release:
	@git diff --exit-code --quiet && git diff --cached --exit-code --quiet || \
		(echo "error: working tree is dirty — commit first"; exit 1)
	@VERSION=$$(./scripts/next-version.sh); \
	GITHUB_TOKEN=$$(gh auth token) && \
	echo "releasing $$VERSION"; \
	git tag $$VERSION && \
	git push origin $$VERSION && \
	GITHUB_TOKEN=$$GITHUB_TOKEN goreleaser release --clean

.PHONY: commit-release
commit-release: commit release

# Playground targets
.PHONY: $(shell grep -h '\.PHONY' playground/Makefile | sed 's/\.PHONY: //')
%:
	$(MAKE) -C playground $@
