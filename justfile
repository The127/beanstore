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
ci: lint build test vuln
