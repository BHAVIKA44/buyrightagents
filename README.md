# BuyRightAgents

BuyRightAgents is an independent prototype exploring whether payment infrastructure could independently verify that a proposed agentic purchase satisfies the user's original requirements before checkout proceeds.

For example, given a request for a vegan backpack that fits a 16-inch laptop, costs less than ₹5,000 including delivery, and arrives before Friday, the intent interpreter can normalize the request. The deterministic verifier then accepts structured requirements and caller-supplied evidence against a specific cart snapshot, reporting `PASS`, `FAIL`, or `UNKNOWN` for each requirement. Hard violations block the local decision; unresolved hard requirements require review. Any cart change requires revalidation.

An LLM may help interpret requirements, but it must not approve its own purchase. This is an independent prototype exploring a proposed capability. It does not claim Razorpay currently provides this exact feature or that no other company is working on it.

## Status

Implemented: a Go CLI, an LLM-backed natural-language intent interpreter, a deterministic verifier for structured requirements, typed evidence, and validated INR cart snapshots, and a deliberately in-memory mock checkout gate. The interpreter outputs requirements, clarification questions, and unsupported constraints; only deterministic code reports `PASS`, `FAIL`, or `UNKNOWN`, applies the `ALLOW`, `BLOCK`, or `REVIEW` policy, and may issue a mock checkout approval.

Grounded source text proves that an extracted requirement came from the request; by itself it cannot prove that the model extracted every requirement. As a narrow additional safeguard, the interpreter deterministically checks unquoted, non-preference mentions of the supported `vegan` and `water_resistant` boolean attributes. A matching hard requirement with the correct boolean value must be present, or the model must surface the phrase as unsupported or request clarification; otherwise interpretation fails and `check` returns `REVIEW`. This does not solve arbitrary natural-language completeness: it deliberately does not infer categories, numeric constraints, dates, quoted text, or contextual mentions. Omitted constraints outside this narrow guard remain a false-`ALLOW` risk until they are independently covered.

Every report includes a SHA-256 hash of a canonical snapshot representation. It covers cart identity, currency, capture time, item identities and product/variant IDs, quantities, unit prices, shipping, discount, and promised delivery date. Items are sorted by stable `item_id` before hashing, so equivalent item orderings have the same hash. The mock checkout gate separately hashes a canonical purchase context containing the original request, normalized requirements, cart, evidence and provenance, reference time, decision, and gate policy version. Any change requires fresh approval.

Requirements and evidence identify either the cart or a specific cart item. Item evidence must name the matching product and variant in the snapshot. In a multi-item cart, every product-level requirement must have an explicit `item_bindings` entry identifying its target item; an unqualified product requirement becomes `REVIEW` rather than being applied to an arbitrary item. Cart-wide total price and delivery requirements continue to cover the entire cart. The verifier derives total price and promised delivery evidence from the local snapshot; caller evidence cannot override those cart-level facts.

The hash binds the report to the supplied local snapshot; it does not authenticate that snapshot against a merchant's live cart or prove seller claims such as material or laptop fit. The mock checkout path additionally requires each caller-supplied product claim to carry an HMAC attestation from a configured mock provider. The attestation binds provider identity, evidence value and provenance, product/variant/item identity, and the canonical cart hash. The gate rejects missing, unknown, tampered, replayed, stale (older than 24 hours), and future-dated (more than 5 minutes) attestations by treating them as unusable evidence. Those defaults are configurable in code and evaluated against the gate's injected clock. This demonstrates integrity and freshness boundaries only; it does not prove a real merchant supplied truthful data.

Not implemented: shopping agent, MCP server, real merchant evidence retrieval/authentication, database, persistent or distributed checkout authorization, merchant integration, and payment integration.

## Verify requirements

Provide a JSON file containing `cart_snapshot`, `requirements`, and `evidence`, then run:

```sh
go run ./cmd/buyrightagents verify examples/vegan-backpack.json
```

The included fictional backpack example evaluates to `ALLOW`. Its item costs ₹4,500 plus ₹250 delivery, for ₹4,750 total. Seller claims and cart facts are illustrative only and do not describe a real product or live merchant cart. INR amounts use paise (`475000` is ₹4,750). Typed values use an explicit object such as `{"type":"number","value":500000}`. Unknown JSON fields and duplicate object keys are rejected.

## Offline adversarial evaluation

The manually labelled synthetic corpus in `evals/cases.jsonl` contains 67 independent scenarios across baseline, hard failures, total price, delivery, missing/contradictory evidence, item scoping, preferences, and mixed requirements. Run it with:

```sh
go run ./cmd/buyrightagents eval evals/cases.jsonl
make eval
```

The runner checks each decision and requirement outcome against its authored label, reports accuracy and safety-related rates with denominators, and exits nonzero for invalid cases or mismatches. See [`docs/EVALUATION.md`](docs/EVALUATION.md) for the format, metric definitions, and limitations. This suite measures agreement on synthetic cases; it does not establish real-world purchase correctness or authenticate seller claims.

`evals/adversarial-cases.jsonl` is a separate, version-controlled 109-scenario offline adversarial corpus. It uses deterministic model fixtures but runs the real intent parser and validator, evidence policy, verifier, mock approval gate, and checkout boundary. It also replays compatible saved structured-intent checkpoint results read-only. Run it with:

```sh
make adversarial-eval
# or:
go run ./cmd/buyrightagents adversarial-eval \
  --dataset evals/adversarial-cases.jsonl \
  --json reports/adversarial-results.json \
  --report docs/ADVERSARIAL_EVALUATION.md
```

The runner intentionally writes mismatches to the report instead of treating them as a successful security score. Fixture behavior exercises the product pipeline, not live-model extraction reliability. The generated report separates observed deterministic boundary results from unresolved model-omission risks; neither a passing corpus nor zero observed failures would establish zero risk.

## Local setup

Install Go 1.22 or newer, then run:

```sh
go run ./cmd/buyrightagents help
go run ./cmd/buyrightagents version
go test ./...
go vet ./...
```

The project uses only the Go standard library.

## Interpret a request

Natural-language interpretation calls a configured remote OpenAI-compatible Chat Completions API. It requires an explicit reference timestamp and never chooses the machine's current date implicitly:

```sh
export BUYRIGHTAGENTS_LLM_API_KEY="..."
export BUYRIGHTAGENTS_LLM_MODEL="your-model-id"
# Optional; defaults to https://api.openai.com/v1
export BUYRIGHTAGENTS_LLM_BASE_URL="https://api.openai.com/v1"
go run ./cmd/buyrightagents interpret --reference-time "2026-10-28T16:48:00+05:30" "Find a vegan bag under ₹5,000 and ideally red."
```

The interpreter has no cart or evidence inputs and cannot return a purchase decision. Its output is an untrusted interpretation for later use. Evidence gathering, deterministic verification against a specific cart snapshot, and the mock checkout gate remain separate steps. Any persistent or external checkout authorization is future work. API keys must be supplied through the environment and must never be committed.

## End-to-end purchase check demo

The `check` command composes the configured remote interpreter with the local deterministic verifier. It accepts a natural-language request and a supplied cart/evidence JSON file, plus an explicit RFC3339 reference timestamp:

```sh
go run ./cmd/buyrightagents check \
  --reference-time "2026-10-28T10:00:00Z" \
  --cart examples/demo-cart.json \
  "Find a vegan bag under ₹5,000"
```

The JSON result is shaped like this (the actual output includes typed expected values and evidence assessments):

```json
{
  "decision": "ALLOW",
  "cart_snapshot_sha256": "<snapshot checksum>",
  "requirements": [
    {"result": {"attribute": "product_category", "outcome": "PASS"}, "reason": "supplied evidence satisfies this requirement"},
    {"result": {"attribute": "vegan", "outcome": "PASS"}, "reason": "supplied evidence satisfies this requirement"},
    {"result": {"attribute": "total_price_paise", "outcome": "PASS"}, "reason": "supplied evidence satisfies this requirement"}
  ]
}
```

The synthetic cart has a ₹4,500 item, ₹250 shipping, and illustrative catalogue evidence for its category and vegan claim. Product category, vegan status, and total price are separate requirements; the verifier derives the ₹4,750 cart total from the snapshot, while category satisfaction comes only from supplied typed evidence. Category labels use case and whitespace normalization only. A successful response includes the decision, interpreted intent, per-requirement `PASS`/`FAIL`/`UNKNOWN` outcomes and evidence provenance, review reasons, and the supplied snapshot's hash. Model ambiguity or invalid intent, unsupported requirements, missing evidence, or conflicting hard constraints result in `REVIEW`; a detected hard violation results in `BLOCK`. `check` does not invoke checkout.

## Mock checkout-control-plane demo

`mock-checkout` runs the same interpretation and deterministic verification path as `check`, then lets the in-memory gate issue and immediately consume one bound mock approval only when the final decision is `ALLOW`. Unlike `check`, it requires HMAC-attested product evidence from a configured trusted mock provider. It never contacts a merchant or payment provider.

The provider identity and HMAC key are trusted process configuration, not cart data. This deliberately in-process symmetric-HMAC mock shares the key between the synthetic provider and gate; it must never be committed, printed, or supplied in cart JSON:

```sh
export BUYRIGHTAGENTS_MOCK_EVIDENCE_PROVIDER_ID="synthetic-catalogue"
export BUYRIGHTAGENTS_MOCK_EVIDENCE_HMAC_KEY="<provider-held-secret-at-least-16-bytes>"
```

An evidence record accepted by `mock-checkout` has this additional shape, where the provider creates the signature over the complete record and cart hash:

```json
"attestation": {
  "version": "mock-evidence-v1",
  "provider_id": "synthetic-catalogue",
  "cart_snapshot_sha256": "<canonical-cart-hash>",
  "signature": "<provider-hmac>"
}
```

The checked-in demo cart is deliberately unsigned, so it remains suitable for ordinary local `check` examples but cannot authorize mock checkout. The offline integration tests construct provider-issued fixtures without exposing any provider key.

```sh
go run ./cmd/buyrightagents mock-checkout \
  --reference-time "2026-10-28T10:00:00Z" \
  --cart /path/to/provider-attested-cart.json \
  "Find a vegan bag under ₹5,000"
```

With a valid interpretation, the JSON output has `purchase_check.decision: "ALLOW"`, an approval reference and bound-context checksum, then `checkout.authorized: true` with a `CHECKOUT_SUCCEEDED` audit record. It deliberately omits the HMAC credential from JSON. The approval is valid only inside that command's in-memory gate and is consumed by the simulated checkout; it cannot authorize a payment or survive a process restart.

The following simulates a price change after approval. It uses a fictional cart whose item price changed from ₹4,500 to ₹4,550, while still fitting the requested budget. The gate rejects it because the approved context no longer matches, so `checkout.authorized` is `false` and the audit outcome is `CHECKOUT_REJECTED`.

```sh
go run ./cmd/buyrightagents mock-checkout \
  --reference-time "2026-10-28T10:00:00Z" \
  --cart /path/to/provider-attested-cart.json \
  --checkout-cart /path/to/provider-attested-price-changed-cart.json \
  "Find a vegan bag under ₹5,000"
```

The mock authorization record binds the exact request, typed requirements, cart snapshot, evidence including provenance, reference time, composed decision, and gate policy version using canonical serialization. Gate state is synchronized so concurrent presentations of the same approval yield at most one simulated checkout success. Rejections do not consume an otherwise valid approval. The audit trail is an in-memory diagnostic slice, not an append-only or tamper-proof ledger.

### Multi-item bindings

For a cart with more than one item, add one explicit binding for each unique product-level attribute required by the request:

```json
"item_bindings": [
  {"attribute": "vegan", "item_id": "item-a"},
  {"attribute": "water_resistant", "item_id": "item-b"}
]
```

Each attribute may appear in the interpreted requirements only once for this narrow mapping scheme. Unknown items, duplicate bindings, attempts to bind cart-wide attributes, or ambiguous repeated attributes result in `REVIEW`. This is explicit cart selection metadata, not natural-language product planning.

The separately invoked `eval-intent` command sends the labelled intent corpus to the configured model and may incur API charges. It is not run by `go test`, `make eval`, or ordinary validation. The corpus contains static authored expectations; tests use fake clients and local mock servers. Every run requires a local checkpoint path; successful case results are appended and synced individually. If a request fails, resume with the same dataset, model, endpoint, and checkpoint to skip completed cases and continue:

```sh
go run ./cmd/buyrightagents eval-intent --checkpoint ./intent-eval.checkpoint.jsonl
go run ./cmd/buyrightagents eval-intent --checkpoint ./intent-eval.checkpoint.jsonl --resume
# Optional: change the 3-second default interval (0s disables pacing).
go run ./cmd/buyrightagents eval-intent --checkpoint ./intent-eval.checkpoint.jsonl --resume --request-interval 5s
# Select the corrected intent fixture version explicitly.
go run ./cmd/buyrightagents eval-intent --dataset evals/intent-cases-v2.jsonl --checkpoint ./intent-eval-v2.checkpoint.jsonl
```

The checkpoint records the model and endpoint, a SHA-256 dataset identity, case IDs, valid structured outputs, and terminal invalid-response outcomes. Invalid outcomes store a bounded diagnostic rather than the raw response and are skipped on resume. The checkpoint never contains the API key. Checkpoints are tied to the exact selected dataset; use the same `--dataset` value when resuming. `evals/intent-cases.jsonl` preserves the original fixtures, while `evals/intent-cases-v2.jsonl` corrects four ambiguous severity labels to expect clarification. Summaries separate response validity, exact accuracy across all outcomes, and exact accuracy among valid responses. Incomplete runs report partial metrics only. A 429 is retried at most once only when a reliable reset delay is 30 seconds or less; longer, daily, or missing reset information stops the run with a resume command.
