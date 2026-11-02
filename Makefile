.PHONY: build test vet fmt lint eval adversarial-eval saved-groq-checkout-eval demo

build:
	go build ./cmd/buyrightagents

test:
	go test ./...

vet:
	go vet ./...

fmt:
	gofmt -w cmd/buyrightagents/*.go internal/checkout/*.go internal/evaluation/*.go internal/evidence/*.go internal/intent/*.go internal/verification/*.go

lint: fmt vet

eval:
	go run ./cmd/buyrightagents eval evals/cases.jsonl

adversarial-eval:
	go run ./cmd/buyrightagents adversarial-eval --dataset evals/adversarial-cases.jsonl --json reports/adversarial-results.json --report docs/ADVERSARIAL_EVALUATION.md

saved-groq-checkout-eval:
	@test -f reports/live-groq-gpt-oss-120b-v2-2026-10-29.checkpoint.jsonl || { echo "saved-Groq replay requires the separately retained private checkpoint; see docs/LIVE_GROQ_CHECKOUT_EVALUATION.md" >&2; exit 2; }
	go run ./cmd/buyrightagents saved-groq-checkout-eval --dataset evals/intent-cases-v2.jsonl --checkpoint reports/live-groq-gpt-oss-120b-v2-2026-10-29.checkpoint.jsonl --manifest reports/live-groq-gpt-oss-120b-v2-2026-10-29.manifest.json --json reports/live-groq-gpt-oss-120b-v2-2026-10-29.checkout-results.json --report docs/LIVE_GROQ_CHECKOUT_EVALUATION.md

demo:
	go run ./cmd/buyrightagents demo
