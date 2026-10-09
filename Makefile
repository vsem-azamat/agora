# One entry point for the checks CI runs. Every tool is pinned in tools/ and runs through
# `go tool`; nothing needs a global install besides Go, Node.js, Bash and Git.

GO_TOOL := go tool -modfile=tools/go.mod
BUF     := $(GO_TOOL) buf
LINT    := go tool -modfile=tools/lint/go.mod golangci-lint

# What `make breaking` compares proto/ against; CI points it at the pull request base.
BREAKING_AGAINST ?= .git\#branch=main

.PHONY: check fmt lint lint-go lint-proto breaking test generate generate-check repo

## check: everything CI runs
check: repo lint breaking test generate-check

## fmt: apply the Go and protobuf formatters
fmt:
	$(LINT) fmt
	$(BUF) format -w

## lint: Go linters and formatting, protobuf lint and formatting
lint: lint-go lint-proto

lint-go:
	$(LINT) run

lint-proto:
	$(BUF) lint
	$(BUF) format -d --exit-code

## breaking: proto/ has no breaking change against BREAKING_AGAINST
breaking:
	$(BUF) breaking --against "$(BREAKING_AGAINST)"

## test: unit and end-to-end tests with the race detector
test:
	go test -race ./...

## generate: regenerate gen/ from proto/
generate:
	$(BUF) generate

## generate-check: gen/ matches proto/
generate-check: generate
	@git diff --exit-code -- gen && test -z "$$(git status --porcelain -- gen)" || \
		{ echo "gen/ is out of date: run make generate and commit the result" >&2; exit 1; }

## repo: repository policy, documentation and spec checks
repo:
	scripts/repo-policy-check.sh
	node scripts/docs-check.mjs
	scripts/spec-layout-check.sh
	npx -y @fission-ai/openspec@1.12.0 validate --specs --no-interactive
