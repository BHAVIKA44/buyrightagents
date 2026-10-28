# BuyRightAgents

BuyRightAgents is an independent prototype exploring whether payment infrastructure could independently verify that a proposed agentic purchase satisfies the user's original requirements before checkout proceeds.

For example, given a request for a vegan backpack that fits a 16-inch laptop, costs less than ₹5,000 including delivery, and arrives before Friday, a future verifier would evaluate the actual product and cart using evidence. It would report `PASS`, `FAIL`, or `UNKNOWN` for each requirement. Hard violations should block checkout; unresolved hard requirements should require human review. Decisions must bind to the exact cart, and any cart change requires revalidation.

An LLM may help interpret requirements, but it must not approve its own purchase. This is an independent prototype exploring a proposed capability. It does not claim Razorpay currently provides this exact feature or that no other company is working on it.

## Status

Implemented: a minimal Go CLI and deterministic verifier for structured requirements and typed evidence. The verifier reports `PASS`, `FAIL`, or `UNKNOWN` per requirement and applies the `ALLOW`, `BLOCK`, or `REVIEW` policy locally.

The evaluator does not bind a decision to an immutable cart snapshot and does not execute checkout or payments. Evidence is supplied as input; it is not fetched or independently authenticated.

Not implemented: shopping agent, MCP server, LLM integration, database, or payment integration.

## Verify requirements

Provide a JSON file containing `requirements` and `evidence`, then run:

```sh
go run ./cmd/buyrightagents verify examples/vegan-backpack.json
```

The included fictional backpack example evaluates to `ALLOW`. Its evidence is illustrative only and does not describe a real product. INR totals use paise (for example, `500000` paise is ₹5,000). Typed values use an explicit object such as `{"type":"number","value":500000}`.

## Local setup

Install Go 1.22 or newer, then run:

```sh
go run ./cmd/buyrightagents help
go run ./cmd/buyrightagents version
go test ./...
go vet ./...
```

The project uses only the Go standard library.
