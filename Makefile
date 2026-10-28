.PHONY: build test vet fmt lint

build:
	go build ./cmd/buyrightagents

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd/buyrightagents/*.go internal/verification/*.go

lint: fmt vet
