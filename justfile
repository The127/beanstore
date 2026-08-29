# list available recipes
default:
    @just --list

# build all modules
build:
    go build ./...
    cd client && go build ./...

# test all modules
test:
    go test ./...
    cd client && go test ./...

# test all modules with coverage profiles
cover:
    go test -coverprofile=coverage.out -covermode=atomic ./...
    cd client && go test -coverprofile=coverage.out -covermode=atomic ./...

# lint all modules
lint: lint-server lint-client

# lint the server module
lint-server:
    golangci-lint run ./...

# lint the client module
lint-client:
    cd client && golangci-lint run ./...

# format all modules
fmt:
    golangci-lint fmt ./...
    cd client && golangci-lint fmt ./...

# check that package doc comments live in doc.go
doccheck:
    bash scripts/check-doc-comments.sh

# check prose style: no em-dashes outside the license files
prose:
    #!/usr/bin/env bash
    emdash=$(printf '\342\200\224')
    if git grep -n "$emdash" -- ':!LICENSE' ':!client/LICENSE'; then
        echo "em-dashes found, replace them (see CLAUDE.md prose rules)"
        exit 1
    fi

# check for known vulnerabilities in reachable code
vuln:
    go run golang.org/x/vuln/cmd/govulncheck@latest ./...
    cd client && go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# one-time dev setup after cloning: local go workspace + git hooks
setup: hooks
    test -f go.work || go work init . ./client

# install the git hooks
hooks:
    lefthook install

# everything that must pass before a push
ci: lint prose doccheck build test vuln
