# BuyRightAgents

BuyRightAgents is a Go CLI prototype for an important agentic-commerce question: before an AI agent proceeds with a purchase, can an independent system check the proposed cart against the user's original request?

It is an independent prototype, not a Razorpay product. It does not execute a payment, contact a merchant, or authenticate a live seller claim.

## What it does

```text
user request
  -> untrusted LLM intent interpretation
  -> validated typed requirements
  -> cart snapshot + typed evidence
  -> deterministic PASS / FAIL / UNKNOWN verification
  -> ALLOW / BLOCK / REVIEW
  -> optional, in-memory mock checkout gate
```

The language model may extract requirements, but it never evaluates evidence, chooses a purchase decision, or authorizes checkout. A deterministic verifier evaluates hard constraints and preferences against a concrete cart. A hard `FAIL` produces `BLOCK`; unresolved hard evidence produces `REVIEW`; `ALLOW` requires all applicable hard requirements to pass.

## Trust boundaries

- **Intent:** Groq or another OpenAI-compatible endpoint is used only by the optional interpreter. Its structured output is validated and grounded in the original request.
- **Evidence:** Product claims are separate from requirements. Item evidence is scoped to a cart item, product, and variant; cart price and delivery facts are derived from the snapshot.
- **Cart binding:** Reports include a canonical SHA-256 cart-snapshot hash. Changing the cart requires revalidation.
- **Mock checkout:** The optional in-memory gate accepts only HMAC-attested mock evidence, binds the request, requirements, cart, evidence, reference time, and policy version, and consumes an approval once. Tampering, replay, stale claims, and changed carts are rejected.

`ALLOW` is a local verification result. It is never payment permission.

## Requirements and evidence

Requirements are typed and independently evaluated as `PASS`, `FAIL`, or `UNKNOWN`.

- Hard requirements decide `ALLOW`, `BLOCK`, or `REVIEW`.
- Preferences are reported but do not independently block a compliant cart.
- Missing, conflicting, untrusted, or wrongly scoped evidence fails closed to `UNKNOWN`/`REVIEW`.
- The prototype supports typed category, boolean, string, numeric, price, delivery-date, material, colour, laptop-fit, and weight requirements. Explicit `backpack, not a tote` exclusions are represented as a separate category `not_equals` requirement when unambiguous.

Grounded text and narrow coverage checks reduce some extraction failures, but they do not prove complete interpretation of arbitrary natural language. Language-model omissions outside recognized checks remain possible.

## Install and run

Prerequisite: Go 1.22 or newer. The module uses only the Go standard library.

```sh
git clone https://github.com/BHAVIKA44/buyrightagents.git
cd buyrightagents
go run ./cmd/buyrightagents help
go run ./cmd/buyrightagents version
```

### Deterministic positive example

This needs no API key or network access:

```sh
go run ./cmd/buyrightagents verify examples/vegan-backpack.json
```

The fictional cart totals ₹4,750 and produces `ALLOW` for vegan material, a 16-inch laptop fit, total price, and delivery deadline.

### Deterministic negative example

```sh
go run ./cmd/buyrightagents verify examples/non-vegan-backpack.json
```

Its hard vegan requirement has evidence of `false`, so it produces `BLOCK`.

### Optional remote interpretation

Only this command family calls a configured remote model:

```sh
export BUYRIGHTAGENTS_LLM_API_KEY="..."
export BUYRIGHTAGENTS_LLM_BASE_URL="https://api.groq.com/openai/v1"
export BUYRIGHTAGENTS_LLM_MODEL="openai/gpt-oss-120b"

go run ./cmd/buyrightagents interpret \
  --reference-time "2026-10-28T16:48:00+05:30" \
  "Find a vegan bag under ₹5,000 and ideally red."
```

Never commit or print an API key. The returned intent is not a purchase decision.

`check` composes remote interpretation with a supplied synthetic cart/evidence file. `mock-checkout` additionally needs provider-issued HMAC attestations; no signed cart fixture is checked in because the provider credential must remain private. Its full authorization and tamper/replay behavior is reproducible offline through the integration tests below.

## Offline validation

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build ./...
make eval
make adversarial-eval
```

- `make eval` runs the 67-case deterministic verifier corpus.
- `make adversarial-eval` runs 109 deterministic adversarial scenarios through intent validation, evidence policy, verification, and the mock approval gate.
- `go test ./...` includes mock-checkout authorization, changed-cart, replay, attestation, and tampering tests with fake model clients; it makes no remote calls.

The historical saved-Groq checkout replay is intentionally **optional**. It requires a separately retained private checkpoint that is ignored by Git; see [docs/LIVE_GROQ_CHECKOUT_EVALUATION.md](docs/LIVE_GROQ_CHECKOUT_EVALUATION.md). A fresh clone can run every command above without it.

## Measured evaluation evidence

The offline deterministic suite currently matches **67/67 decisions** and **88/88 requirement outcomes**. The adversarial suite executes **109/109** synthetic scenarios with zero observed unexpected `ALLOW` decisions or unauthorized mock checkouts. These are regression results, not proof of real-world safety.

One controlled live Groq run used `openai/gpt-oss-120b` through `https://api.groq.com/openai/v1` across 18 frozen synthetic buyer cases. It made 36 successful provider calls (one buyer and one interpreter call per case). Fifteen cases reached `ALLOW` and authorized the mock checkout; three conservatively returned `REVIEW`. No genuine selected hard-requirement violation was established as authorized. One raw oracle count is a documented frozen-label discrepancy: a leather backpack satisfied the written request but was excluded by the frozen acceptable-SKU list. See [docs/LIVE_GROQ_E2E_EVALUATION.md](docs/LIVE_GROQ_E2E_EVALUATION.md).

This is a single frozen-corpus observation. It does not establish general model reliability, zero unsafe purchases, real merchant verification, or production readiness.

## Known limitations

- Merchant data, evidence retrieval, seller identity, carts, checkout, and payment are mocked.
- The evidence HMAC demonstrates a prototype trust boundary, not real merchant authenticity.
- The model can return invalid, incomplete, unsupported, or ambiguous intent; BuyRightAgents fails closed to `REVIEW`, but cannot guarantee language understanding completeness.
- The mock approval state is process-local and non-persistent.
- There is no shopping agent, MCP server, database, real merchant integration, or payment integration.

See [docs/ARCHITECTURE.md](docs/ARCHITECTURE.md), [docs/EVALUATION.md](docs/EVALUATION.md), and [docs/PROBLEM.md](docs/PROBLEM.md) for design details and boundaries.
