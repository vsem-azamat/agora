# One entry point for the checks CI runs. Every Go and protobuf tool is pinned in tools/ and
# runs through `go tool`; the web tools are pinned in web/package.json and run through pnpm.
# Nothing needs a global install besides Go, Node.js with pnpm, Bash and Git.

GO_TOOL := go tool -modfile=tools/go.mod
BUF     := $(GO_TOOL) buf
LINT    := go tool -modfile=tools/lint/go.mod golangci-lint
PNPM    := pnpm --dir web

# What `make breaking` compares proto/ against; CI points it at the pull request base.
BREAKING_AGAINST ?= .git\#branch=main

# Installed web dependencies; reinstalled when package.json or the lockfile changes.
WEB_DEPS := web/node_modules/.modules.yaml

# clean-check: $(1) matches the commit, with no changed or untracked files; $(2) is the fix.
define clean-check
@git diff --exit-code -- $(1) && test -z "$$(git status --porcelain -- $(1))" || \
	{ echo "$(1) is out of date: run $(2) and commit the result" >&2; exit 1; }
endef

.PHONY: check fmt lint lint-go lint-proto lint-web breaking test test-go test-web \
	generate generate-check generate-web generate-check-web build-web build-check repo help

## check: everything CI runs
check: repo lint breaking test generate-check generate-check-web build-check

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

## breaking: proto/ has no breaking change against BREAKING_AGAINST
breaking:
	$(BUF) breaking --against "$(BREAKING_AGAINST)"

## test: Go and web tests
test: test-go test-web

## test-go: unit and end-to-end tests with the race detector
test-go:
	go test -race ./...

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

## build-web: type-check web/ and build it into internal/web/dist/
build-web: $(WEB_DEPS)
	$(PNPM) build

## build-check: internal/web/dist/ matches web/
build-check: build-web
	$(call clean-check,internal/web/dist,make build-web)

## repo: repository policy, documentation and spec checks
repo:
	scripts/repo-policy-check.sh
	node scripts/docs-check.mjs
	scripts/spec-layout-check.sh
	npx -y @fission-ai/openspec@1.12.0 validate --specs --no-interactive

## help: list the targets
help:
	@awk '/^## /{l=substr($$0,4); i=index(l,": "); printf "  %-20s %s\n", substr(l,1,i-1), substr(l,i+2)}' $(MAKEFILE_LIST)

$(WEB_DEPS): web/package.json web/pnpm-lock.yaml
	$(PNPM) install --frozen-lockfile
	@touch $@
