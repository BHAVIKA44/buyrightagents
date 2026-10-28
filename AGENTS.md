# Coding instructions

- Keep the project CLI-first and implement one requested increment at a time.
- Use Go and prefer the standard library; add dependencies only when needed and explain why.
- Keep unimplemented systems out of the codebase. The CLI supports help/version and local verification of structured input.
- Treat requirements, evidence, evaluation, and cart binding as distinct concepts as those features are added.
- Never let an LLM be the authority that approves its own purchase.
- Use `PASS`, `FAIL`, and `UNKNOWN` consistently; preserve evidence provenance in results.
- A local `ALLOW` decision is not payment permission. Current evaluation does not bind results to immutable carts or execute checkout.
- Format Go code with `gofmt`. Run `go test ./...` and `go vet ./...` when Go is available.
- Do not claim integrations or verification behavior that has not been implemented.
