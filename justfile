default:
    @just --list

# Build the CLI into dist/preview
build:
    go build -trimpath -o dist/preview ./cmd

test:
    go test ./...

vet:
    gofmt -l .
    go vet ./...


