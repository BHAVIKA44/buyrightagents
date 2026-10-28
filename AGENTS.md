# Coding instructions

- Keep the project CLI-first and implement one requested increment at a time.
- Use Go and prefer the standard library; add dependencies only when needed and explain why.
- Keep unimplemented systems out of the codebase. The CLI supports help/version and local verification of structured input against a validated cart snapshot.
- Keep requirements, evidence, evaluation results, and cart snapshots as distinct concepts.
- Never let an LLM be the authority that approves its own purchase.
- Use `PASS`, `FAIL`, and `UNKNOWN` consistently; preserve evidence provenance in results.
- A local `ALLOW` decision is not payment permission. Reports include a hash of the supplied snapshot, but this does not authenticate seller claims or the merchant's live cart. The CLI does not execute checkout.
- Format Go code with `gofmt`. Run `go test ./...` and `go vet ./...` when Go is available.
- Do not claim integrations or verification behavior that has not been implemented.
- Keep `evals/cases.jsonl` labels authored independently from evaluator output; never rewrite labels from a run. Run `go run ./cmd/buyrightagents eval evals/cases.jsonl` (or `make eval`) after changing verifier behavior.
