# Coding instructions

- Keep the project CLI-first and implement one requested increment at a time.
- Use Go and prefer the standard library; add dependencies only when needed and explain why.
- Keep unimplemented systems out of the codebase. The current CLI is only help/version.
- Treat requirements, evidence, evaluation, and cart binding as distinct concepts as those features are added.
- Never let an LLM be the authority that approves its own purchase.
- Use `PASS`, `FAIL`, and `UNKNOWN` consistently; preserve evidence provenance.
- Format Go code with `gofmt`. Run `go test ./...` and `go vet ./...` when Go is available.
- Do not claim integrations or verification behavior that has not been implemented.
