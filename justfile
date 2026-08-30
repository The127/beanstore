# list available recipes
default:
    @just --list

# build all modules
build:
    go build ./...
    cd client && go build ./...
    cd lvm && go build ./...

# test all modules
test:
    go test ./...
    cd client && go test ./...
    cd lvm && go test ./...

# test all modules with coverage profiles
cover:
    go test -coverprofile=coverage.out -covermode=atomic ./...
    cd client && go test -coverprofile=coverage.out -covermode=atomic ./...
    cd lvm && go test -coverprofile=coverage.out -covermode=atomic ./...

# lint all modules
lint: lint-server lint-client lint-lvm

# lint the server module
lint-server:
    golangci-lint run ./...

# lint the client module
lint-client:
    cd client && golangci-lint run ./...

# lint the lvm module
lint-lvm:
    cd lvm && golangci-lint run ./...

# format all modules
fmt:
    golangci-lint fmt ./...
    cd client && golangci-lint fmt ./...
    cd lvm && golangci-lint fmt ./...

# regenerate grpc code from the protos
proto:
    buf generate

# lint the protos
proto-lint:
    buf lint

# check that the generated grpc code matches the protos
proto-check:
    buf generate
    git diff --exit-code -- client/gen

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

# run the lvm and daemon integration tests against loop device vgs (needs sudo)
test-integration:
    sudo -v
    cd lvm && go test -tags integration -run TestIntegration -count=1 -v ./...
    go test -tags integration -run TestIntegration -count=1 -v ./internal/e2e/

# tag and push a release: signed tags for all three modules
release version:
    #!/usr/bin/env bash
    set -euo pipefail
    test -z "$(git status --porcelain)" || { echo "working tree not clean"; exit 1; }
    git tag -s "client/v{{version}}" -m "client/v{{version}}"
    git tag -s "lvm/v{{version}}" -m "lvm/v{{version}}"
    git tag -s "v{{version}}" -m "v{{version}}"
    git push origin "client/v{{version}}" "lvm/v{{version}}" "v{{version}}"

# validate the goreleaser config
release-check:
    go run github.com/goreleaser/goreleaser/v2@latest check

# build the release artifacts locally without publishing
release-snapshot:
    go run github.com/goreleaser/goreleaser/v2@latest release --snapshot --clean

# check for known vulnerabilities in reachable code
vuln:
    go run golang.org/x/vuln/cmd/govulncheck@latest ./...
    cd client && go run golang.org/x/vuln/cmd/govulncheck@latest ./...
    cd lvm && go run golang.org/x/vuln/cmd/govulncheck@latest ./...

# one-time dev setup after cloning: local go workspace + git hooks
setup: hooks
    test -f go.work || go work init . ./client ./lvm

# install the git hooks
hooks:
    lefthook install

# everything that must pass before a push
ci: lint proto-lint proto-check prose doccheck build test vuln
