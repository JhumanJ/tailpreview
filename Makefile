.PHONY: build test test-race vet fmt check

build:
	go build -o bin/tailpreview ./cmd/tailpreview

test:
	go test ./...

test-race:
	go test -race ./...

vet:
	go vet ./...

fmt:
	gofmt -w $$(find . -name '*.go' -not -path './vendor/*')

check: fmt vet test test-race build
