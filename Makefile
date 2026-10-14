# One entry point for the checks CI runs. Every Go and protobuf tool is pinned in tools/ and
# runs through `go tool`; the web tools are pinned in web/package.json and run through pnpm.
# Nothing needs a global install besides Go, Node.js with pnpm, Bash and Git.

GO_TOOL := go tool -modfile=tools/go.mod
BUF     := $(GO_TOOL) buf
LINT    := go tool -modfile=tools/lint/go.mod golangci-lint
PNPM    := pnpm --dir web

# What `make breaking` compares proto/ against.
BREAKING_AGAINST ?= .git\#branch=main

# Installed web dependencies; reinstalled when package.json or the lockfile changes.
WEB_DEPS := web/node_modules/.modules.yaml

# clean-check: $(1) matches the commit, with no changed or untracked files; $(2) is the fix.
define clean-check
@git diff --exit-code -- $(1) && test -z "$$(git status --porcelain -- $(1))" || \
	{ echo "$(1) is out of date: run $(2) and commit the result" >&2; exit 1; }
endef

.PHONY: check fmt lint lint-go lint-proto lint-web breaking test test-go test-web test-deploy deploy \
	generate generate-check generate-web generate-check-web build-web build-check repo help

## check: everything CI runs but the release config (docs/development/checks.md)
check: repo lint test generate-check generate-check-web build-check

## fmt: apply the Go, protobuf and web formatters
fmt: $(WEB_DEPS)
	$(LINT) fmt
	$(BUF) format -w
	$(PNPM) format

## lint: Go, protobuf and web lint and formatting
lint: lint-go lint-proto lint-web

## lint-go: Go linters and formatting
lint-go:
	$(LINT) run

## lint-proto: protobuf lint and formatting
lint-proto:
	$(BUF) lint
	$(BUF) format -d --exit-code

## lint-web: web lint and formatting (Biome)
lint-web: $(WEB_DEPS)
	$(PNPM) lint

## breaking: what proto/ breaks against BREAKING_AGAINST, for the pull request to say (not part of check)
breaking:
	$(BUF) breaking --against "$(BREAKING_AGAINST)"

## test: Go and web tests
test: test-go test-web test-deploy

## test-go: unit and end-to-end tests with the race detector, with the web app built in
test-go: build-web
	go test -race ./...

## test-deploy: make deploy on a clone of HEAD, without a service
test-deploy:
	scripts/deploy-test.sh

## test-web: web app tests (Vitest)
test-web: $(WEB_DEPS)
	$(PNPM) test

## generate: regenerate gen/ from proto/
generate:
	$(BUF) generate

## generate-check: gen/ matches proto/
generate-check: generate
	$(call clean-check,gen,make generate)

## generate-web: regenerate web/src/gen/ from proto/
generate-web: $(WEB_DEPS)
	$(PNPM) generate

## generate-check-web: web/src/gen/ matches proto/
generate-check-web: generate-web
	$(call clean-check,web/src/gen,make generate-web)

## build-web: type-check web/ and build it into internal/web/dist/, which the binary embeds
build-web: $(WEB_DEPS)
	$(PNPM) build

## build-check: the web app builds and writes back internal/web/dist/.keep, the one tracked file there
build-check: build-web
	$(call clean-check,internal/web/dist,make build-web)

## repo: repository policy, documentation and spec checks
repo:
	scripts/repo-policy-check.sh
	node scripts/docs-check.mjs
	scripts/spec-layout-check.sh
	npx -y @fission-ai/openspec@1.12.0 validate --specs --no-interactive

## deploy: pull, check, build and install this checkout, restart the hub and verify it (see docs/development/deploying.md)
deploy:
	scripts/deploy.sh

## help: list the targets
help:
	@awk '/^## /{l=substr($$0,4); i=index(l,": "); printf "  %-20s %s\n", substr(l,1,i-1), substr(l,i+2)}' $(MAKEFILE_LIST)

$(WEB_DEPS): web/package.json web/pnpm-lock.yaml
	$(PNPM) install --frozen-lockfile
	@touch $@
