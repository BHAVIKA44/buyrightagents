.PHONY: build test vet fmt lint

build:
	go build ./cmd/buyrightagents

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd/buyrightagents/*.go

lint: fmt vet
