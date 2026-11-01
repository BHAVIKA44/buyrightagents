# BuyRightAgents

An agent can find a plausible product and still buy the wrong thing.

“Vegan” can disappear from a summary. “Under ₹5,000” can become inclusive. A product claim can be attached to the wrong cart item. A checkout decision can outlive the cart it was supposed to approve. These are authorization problems, not merely retrieval problems.

BuyRightAgents is a Go prototype for placing an independent verification boundary between a shopping agent's proposal and a checkout decision. It converts untrusted natural-language interpretation into typed requirements, evaluates those requirements against a concrete cart and evidence, and produces a deterministic `ALLOW`, `BLOCK`, or `REVIEW` result.

> **An agent may propose a cart; only evidence bound to that cart may authorize it.**

This independent prototype uses synthetic carts, a mock evidence provider, and an in-memory mock checkout gate. It does not contact merchants, execute payments, or authenticate real seller claims.

## Why this matters now

The authorization rails for delegated commerce are becoming more concrete. For example, [NPCI's UPI Circle](https://www.npci.org.in/product/upi-circle) describes delegated payment authority with transaction limits and notes that AI/software-driven IoT payments are being piloted with a limited user group. That is a useful step toward answering **who may initiate a payment** and **within what monetary limit**.

It does not, by itself, answer the shopping question that comes earlier: **does this exact proposed cart still satisfy what the user asked for?** A delegated limit cannot tell whether the selected backpack is vegan, fits a 16-inch laptop, costs less than a strict threshold after delivery, or violates a negative constraint. A shopping agent can stay within its budget and still buy the wrong product.

BuyRightAgents explores that missing semantic-verification layer. It is not affiliated with NPCI or UPI, does not integrate with either, and does not claim to represent their current capabilities.

## The boundary this project explores

An LLM is useful for translating a request such as “find a vegan backpack under ₹5,000” into a structured representation. It is not a safe authority for deciding that its own proposed purchase satisfies that request. BuyRightAgents keeps those responsibilities separate:

```text
  ORIGINAL USER REQUEST                         untrusted language
             |
             v
  OPENAI-COMPATIBLE LLM INTERPRETER             extracts typed intent only
             |
             v
  GROUNDING + COVERAGE VALIDATION               rejects invalid / recognized omissions
             |
             v
  TYPED REQUIREMENTS                            no cart, evidence, or decision fields
             |
             +-------------------+
             |                   |
             v                   v
  CART SNAPSHOT           MOCK-ATTESTED EVIDENCE
  (price + delivery)      (item/product/variant scoped)
             \                   /
              v                 v
       DETERMINISTIC REQUIREMENT EVALUATOR      PASS / FAIL / UNKNOWN
                         |
                         v
                 ALLOW / BLOCK / REVIEW
                         |
                         v
          CART-BOUND, ONE-TIME MOCK CHECKOUT GATE
```

The LLM never receives a checkout-authority field. Its output is strict-decoded as untrusted data and checked against grounded source text from the original request. The verifier—not the model—evaluates evidence and determines the decision.

Grounding helps prevent invented constraints, and narrow coverage checks catch selected recognizable omissions. Neither proves arbitrary language was interpreted completely. If the system cannot establish a hard requirement, it returns `REVIEW`.

## A concrete verification path

The checked-in [deterministic ALLOW fixture](examples/vegan-backpack.json) demonstrates the evidence and policy path offline. It represents a synthetic cart for a request with these hard constraints:

| Requirement | Typed comparison | Evidence source | Outcome |
| --- | --- | --- | --- |
| Vegan material | item `vegan == true` | product evidence scoped to the selected item, product, and variant | `PASS` |
| Laptop fit | item `laptop_fit_inches >= 16` | product evidence scoped to that same item | `PASS` |
| Total cost | cart `total_price_paise <= 500000` | derived from item price, shipping, and discount in the cart | `PASS` |
| Delivery deadline | cart `delivery_date <= 2026-10-30` | derived from the cart's promised delivery date | `PASS` |

The cart total is ₹4,750 including delivery, so the local verifier returns `ALLOW`. The sentence “vegan backpack” is illustrative of the product scenario; this particular fixture intentionally demonstrates the four typed checks above rather than a recorded LLM transcript.

Three outcomes keep the decision rule legible:

| Decision | Meaning |
| --- | --- |
| `ALLOW` | Every applicable hard requirement passed with accepted evidence. |
| `BLOCK` | At least one independently established hard requirement failed. |
| `REVIEW` | A hard requirement is unresolved, the intent is ambiguous/invalid, or a constraint is unsupported. |

A preference can be reported as unsatisfied without independently blocking a cart. A confirmed hard failure takes precedence over unrelated unknowns; it cannot be silently downgraded to `REVIEW`.

## Engineering decisions that matter

### 1. LLM output is data, not authority

The [intent schema](internal/intent/types.go) admits requirements, clarifications, and unsupported constraints—not a cart, evidence, payment, or purchase decision. [Interpreter validation](internal/intent/interpreter.go) rejects malformed, duplicate, unknown, and unsupported output before it can influence verification. Confidence is not evidence.

### 2. A price claim is not a cart total

`total_price_paise` and `delivery_date` are cart-scoped attributes in the [verifier](internal/verification/verification.go). Their evidence comes from the validated snapshot; product evidence cannot override them. Numeric comparisons are exact and distinguish strict from inclusive operators. An empty cart can never produce `ALLOW`.

### 3. Evidence belongs to an item, not a description

Item evidence must match the selected cart item's ID, product ID, and variant ID. This prevents a true claim about one product approving another. Multi-item product requirements need explicit bindings; missing or ambiguous bindings return `REVIEW`. See the [verifier](internal/verification/verification.go) and [purchase-check tests](cmd/buyrightagents/purchase_check_test.go).

### 4. Integrity needs a concrete object to bind

Every report includes a canonical SHA-256 cart hash. The mock checkout path accepts product evidence only when its identity, value, provenance, item binding, and cart hash have an in-process HMAC attestation. The gate re-evaluates the bound context with its own clock and issues a one-time approval only for `ALLOW`. See [mock evidence](internal/evidence/mock_provider.go) and the [checkout gate](internal/checkout/control.go).

This is intentionally a prototype trust boundary—not a merchant identity system. It demonstrates what must be bound and rechecked; it does not prove that a real seller claim is true.

### 5. Missing meaning is a safety result, not a default

The [coverage validator](internal/intent/coverage.go) catches a defined set of recognizable required concepts—price, delivery, material, laptop fit, category, and selected boolean claims—when model output omits or changes them. It also preserves grounded hard-versus-preference signals. This reduces known omission paths without claiming natural-language completeness.

### 6. Category matching stays intentionally small

The implementation supports typed categories and the unambiguous negative phrase `backpack, not a tote`. It does not claim a general taxonomy or fuzzy matching. Its only directional compatibility rule is that observed `backpack` may satisfy requested `bag`; the inverse is not assumed.

## What broke, and what it taught us

The project records unfavorable results as engineering evidence rather than smoothing them into a headline metric.

| What failed | Root cause and response | What remains unresolved |
| --- | --- | --- |
| Recognized hard constraints could be omitted by model output. | Coverage validation now rejects recognized omissions or polarity/operator changes instead of letting them fall through to approval. | The guard is not a complete natural-language understanding system. Unrecognized omissions remain possible. |
| A frozen live-E2E oracle classified one selected leather backpack as unacceptable even though it satisfied the written constraints. | The acceptable-SKU list, not the verified request semantics, caused the discrepancy. The frozen data and raw count were retained and documented. | Synthetic oracle design still requires independent review. |
| Three cases in the controlled live run did not reach authorization. | Two interpretations failed validation and one included an unsupported constraint, so the system returned `REVIEW`. | This is a usability cost of failing closed; it is not evidence that the model understands every request. |

The result is not “the model is reliable.” It is that known uncertainty and validation failures do not receive mock authorization.

## Evaluation evidence

The numbers below measure different things and should not be combined into a single success rate.

| Evaluation | What it measures | Result | What it does **not** establish |
| --- | --- | --- | --- |
| [Deterministic verifier corpus](docs/EVALUATION.md) | Agreement with synthetic verifier labels | **67/67** decisions; **88/88** requirement outcomes | Seller truth, live-cart integrity, or general safety |
| [Adversarial regression corpus](docs/ADVERSARIAL_EVALUATION.md) | Fixed malformed/malicious fixtures through the full mock path | **109/109** executed; **0** unexpected `ALLOW`; **0** unauthorized mock checkouts | Independent live-model attacks or zero risk outside this corpus |
| [Controlled live Groq E2E run](docs/LIVE_GROQ_E2E_EVALUATION.md) | One buyer-selection + interpreter path with synthetic evidence | **18/18** cases; **36** calls; **15** `ALLOW`; **3** `REVIEW`; **15** mock checkouts | A benchmark or reliable behavior across models/configurations |
| [Historical saved-Groq replay](docs/LIVE_GROQ_CHECKOUT_EVALUATION.md) | Retained model outputs through the checkout path | **48** replayed; **2** invalid safely rejected; **60** scenarios; **0** unauthorized mock checkouts | Publicly reproducible model results—the checkpoint is private |

In the 18-case run, no genuine selected hard-requirement violation was established as authorized. That is a bounded observation from one frozen corpus. It does not prove unsafe authorization is impossible. The run also had three `REVIEW` outcomes, a concrete reminder that a safe system can still be inconvenient when interpretation is uncertain.

## Quickstart

Requires Go 1.22 or newer. The module uses the Go standard library only.

```sh
git clone https://github.com/BHAVIKA44/buyrightagents.git
cd buyrightagents

go run ./cmd/buyrightagents help
go run ./cmd/buyrightagents version
```

### Fully reproducible offline examples

```sh
# Synthetic cart with verified vegan, laptop-fit, price, and delivery evidence.
go run ./cmd/buyrightagents verify examples/vegan-backpack.json
# => ALLOW

# Same type of cart with accepted evidence that it is not vegan.
go run ./cmd/buyrightagents verify examples/non-vegan-backpack.json
# => BLOCK

go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build ./...
make eval
make adversarial-eval
```

`make adversarial-eval` regenerates its checked-in synthetic results/report. It makes no live model request.

### Optional remote interpretation

Only use this if you choose to provide your own OpenAI-compatible API key. Never commit or print it.

```sh
export BUYRIGHTAGENTS_LLM_API_KEY="..."
export BUYRIGHTAGENTS_LLM_BASE_URL="https://api.groq.com/openai/v1"
export BUYRIGHTAGENTS_LLM_MODEL="openai/gpt-oss-120b"

go run ./cmd/buyrightagents interpret \
  --reference-time "2026-10-28T16:48:00+05:30" \
  "Find a vegan bag under ₹5,000 and ideally red."
```

`interpret` returns an untrusted typed intent, not a purchase decision. `check`, `mock-checkout`, and `eval-intent` also use the configured remote model; the latter may incur API charges. Historical saved-Groq replay needs a private ignored checkpoint and cannot be reproduced from a fresh clone; see [its documentation](docs/LIVE_GROQ_CHECKOUT_EVALUATION.md).

## Limits and non-claims

- Natural-language interpretation remains incomplete. Grounding and coverage checks catch defined classes of error, not every omitted or misunderstood constraint.
- `REVIEW` is intentional fail-closed behavior, but can be an incorrect refusal of a compliant synthetic cart.
- Products, carts, evidence, and the provider HMAC are synthetic. There is no evidence retrieval, seller authentication, or live merchant-cart verification.
- Checkout approval is in memory, process-local, and mock-only. No payment authorization or merchant integration exists.
- Live evaluation is limited to one fixed 18-case run on one model/configuration. Historical model checkpoints are private and are not a public benchmark.

For implementation detail, start with [Architecture](docs/ARCHITECTURE.md), [Engineering decisions](docs/DECISIONS.md), [the problem statement](docs/PROBLEM.md), and the linked evaluation reports.
