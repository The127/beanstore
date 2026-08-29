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

# install the git hooks
hooks:
    lefthook install

# everything that must pass before a push
ci: lint build test
