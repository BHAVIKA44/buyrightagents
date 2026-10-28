# BuyRightAgents

BuyRightAgents is an independent prototype exploring whether payment infrastructure could independently verify that a proposed agentic purchase satisfies the user's original requirements before checkout proceeds.

For example, given a request for a vegan backpack that fits a 16-inch laptop, costs less than ₹5,000 including delivery, and arrives before Friday, a future verifier would evaluate the actual product and cart using evidence. It would report `PASS`, `FAIL`, or `UNKNOWN` for each requirement. Hard violations should block checkout; unresolved hard requirements should require human review. Decisions must bind to the exact cart, and any cart change requires revalidation.

An LLM may help interpret requirements, but it must not approve its own purchase. This is an independent prototype exploring a proposed capability. It does not claim Razorpay currently provides this exact feature or that no other company is working on it.

## Status

Implemented: a minimal Go CLI with help and version commands.

Not implemented: shopping agent, MCP server, LLM integration, database, payment integration, and verification engine.

## Local setup

Install Go 1.22 or newer, then run:

```sh
go run ./cmd/buyrightagents help
go run ./cmd/buyrightagents version
go test ./...
go vet ./...
```

The initial project uses only the Go standard library.
