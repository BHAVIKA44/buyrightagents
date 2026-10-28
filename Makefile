.PHONY: build test vet fmt lint eval

build:
	go build ./cmd/buyrightagents

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd/buyrightagents/*.go internal/evaluation/*.go internal/intent/*.go internal/verification/*.go

lint: fmt vet

eval:
	go run ./cmd/buyrightagents eval evals/cases.jsonl
