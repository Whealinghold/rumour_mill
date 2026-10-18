default: test

test:
    go test ./...

test-race:
    go test -race ./...

lint:
    go vet ./...
