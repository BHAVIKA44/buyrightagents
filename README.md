# BuyRightAgents

BuyRightAgents is an independent prototype exploring whether payment infrastructure could independently verify that a proposed agentic purchase satisfies the user's original requirements before checkout proceeds.

For example, given a request for a vegan backpack that fits a 16-inch laptop, costs less than ₹5,000 including delivery, and arrives before Friday, a future verifier would evaluate the actual product and cart using evidence. It would report `PASS`, `FAIL`, or `UNKNOWN` for each requirement. Hard violations should block checkout; unresolved hard requirements should require human review. Decisions must bind to the exact cart, and any cart change requires revalidation.

An LLM may help interpret requirements, but it must not approve its own purchase. This is an independent prototype exploring a proposed capability. It does not claim Razorpay currently provides this exact feature or that no other company is working on it.

## Status

Implemented: a Go CLI and deterministic verifier for structured requirements, typed evidence, and validated INR cart snapshots. The verifier reports `PASS`, `FAIL`, or `UNKNOWN` per requirement and applies the `ALLOW`, `BLOCK`, or `REVIEW` policy locally.

Every report includes a SHA-256 hash of a canonical snapshot representation. It covers cart identity, currency, capture time, item identities and product/variant IDs, quantities, unit prices, shipping, discount, and promised delivery date. Items are sorted by stable `item_id` before hashing, so equivalent item orderings have the same hash. Any snapshot change, including a changed delivery promise or cart identity, requires a fresh evaluation. A future checkout gate must compare the current snapshot hash with the report hash.

Requirements and evidence identify either the cart or a specific cart item. Item evidence must name the matching product and variant in the snapshot. The verifier derives total price and promised delivery evidence from the local snapshot; caller evidence cannot override those cart-level facts.

The hash binds the report to the supplied local snapshot; it does not authenticate that snapshot against a merchant's live cart or prove seller claims such as material or laptop fit. Evidence is supplied by the caller and is not independently authenticated. `ALLOW` is only the result of local evaluation; it does not execute checkout or authorize a payment.

Not implemented: shopping agent, MCP server, LLM integration, database, or payment integration.

## Verify requirements

Provide a JSON file containing `cart_snapshot`, `requirements`, and `evidence`, then run:

```sh
go run ./cmd/buyrightagents verify examples/vegan-backpack.json
```

The included fictional backpack example evaluates to `ALLOW`. Its item costs ₹4,500 plus ₹250 delivery, for ₹4,750 total. Seller claims and cart facts are illustrative only and do not describe a real product or live merchant cart. INR amounts use paise (`475000` is ₹4,750). Typed values use an explicit object such as `{"type":"number","value":500000}`. Unknown JSON fields and duplicate object keys are rejected.

## Offline adversarial evaluation

The manually labelled synthetic corpus in `evals/cases.jsonl` contains 64 independent scenarios across baseline, hard failures, total price, delivery, missing/contradictory evidence, item scoping, preferences, and mixed requirements. Run it with:

```sh
go run ./cmd/buyrightagents eval evals/cases.jsonl
make eval
```

The runner checks each decision and requirement outcome against its authored label, reports accuracy and safety-related rates with denominators, and exits nonzero for invalid cases or mismatches. See [`docs/EVALUATION.md`](docs/EVALUATION.md) for the format, metric definitions, and limitations. This suite measures agreement on synthetic cases; it does not establish real-world purchase correctness or authenticate seller claims.

## Local setup

Install Go 1.22 or newer, then run:

```sh
go run ./cmd/buyrightagents help
go run ./cmd/buyrightagents version
go test ./...
go vet ./...
```

The project uses only the Go standard library.
