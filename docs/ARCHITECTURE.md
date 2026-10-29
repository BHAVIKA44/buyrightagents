# Architecture

The project is CLI-first. The executable provides help, version, `interpret`, `verify`, `check`, `mock-checkout`, `eval`, and explicitly invoked `eval-intent` commands. Natural-language interpretation runs before a cart exists and cannot inspect evidence or make a purchase decision. The deterministic verification package evaluates structured requirements against explicitly typed evidence and a concrete cart snapshot.

## Proposed direction

1. Interpret a shopping request into typed intent, hard requirements, preferences, clarification questions, and unsupported constraints. The LLM-backed `interpret` command is implemented, uses an explicit RFC3339 reference time, and returns no evidence or decision.
2. Gather evidence for the requested constraints. Evidence retrieval and real-world source authentication are not implemented. `mock-checkout` accepts only HMAC-attested evidence from a configured in-process mock provider, with deterministic freshness checks.
3. Bind structured requirements to a validated local INR cart snapshot and item IDs, derive cart total and delivery facts, and evaluate each requirement as `PASS`, `FAIL`, or `UNKNOWN`. The deterministic evaluator applies the `ALLOW`, `BLOCK`, or `REVIEW` policy.
4. Hash a canonical snapshot representation into every report. `mock-checkout` additionally binds the request, normalized requirements, exact cart, evidence, reference time, composed decision, and its policy version in canonical form.
5. The in-memory mock checkout gate independently re-evaluates the supplied context and issues a one-time HMAC-protected approval only for a final `ALLOW`; it compares the current context before consuming that approval. This demonstrates the enforcement boundary but is not a real merchant checkout or payment action.

## Trust boundaries

- Intent interpretation uses an LLM, but its output is untrusted structured input. The LLM is never the evidence evaluator or purchase-decision authority.
- The intent model has no cart, item, product, variant, evidence, payment, or checkout fields. Cart and item scopes are assigned only after a concrete snapshot exists; `total_price_paise` and `delivery_date` remain cart-scoped in verification.
- Source phrases are checked against the original request after lowercasing and collapsing punctuation/whitespace to spaces. This grounding check prevents invented source spans but cannot prove that the model captured every semantic constraint.
- Explicit cues such as `must`, `non-negotiable`, `ideally`, and `prefer` must be preserved in a grounded source span and cannot be assigned the opposite severity. This is a conservative guard, not a proof of semantic completeness.
- Relative-date interpretation receives the caller-supplied timestamp, local calendar date, and UTC offset. Supported simple phrases are checked against deterministic date arithmetic; ambiguous wording should produce a clarification question.
- Intent attributes are `vegan`, `water_resistant`, `recycled_material`, `laptop_fit_inches`, `total_price_paise`, `delivery_date`, `color`, `material`, and `weight_grams`. Units are inches, integer INR paise, ISO calendar dates, strings, or grams as indicated by each name. Unsupported concepts remain explicit rather than being mapped to invented attributes.
- `BUYRIGHTAGENTS_LLM_API_KEY` and `BUYRIGHTAGENTS_LLM_MODEL` are required for interpretation; `BUYRIGHTAGENTS_LLM_BASE_URL` is optional. HTTP calls have a fixed timeout, response-size bound, context cancellation, and no automatic retries.
- Product descriptions, seller claims, agent summaries, and retrieved data are caller-supplied evidence with provenance. The ordinary verifier accepts them for diagnostic use; `mock-checkout` requires a valid mock-provider attestation before they can satisfy a requirement. This does not authenticate real-world truth.
- An attestation uses HMAC over provider ID, evidence ID/value/provenance, item/product/variant binding, and the canonical cart snapshot hash. The mock gate evaluates it against a gate-owned reference clock. Its defaults reject evidence older than 24 hours or more than five minutes in the future; invalid or untrusted evidence is excluded and leads to `UNKNOWN`, not a fabricated `FAIL`.
- Item-scoped evidence is tied to a real cart `item_id` and must match its `product_id` and `variant_id`. Requirements match evidence by attribute, scope, and subject ID. For carts with multiple items, product-level requirements are bound only by explicit `item_bindings` metadata; missing or ambiguous bindings lead to `REVIEW`. Cart-scoped requirements remain cart-wide.
- The built-in `total_price_paise` attribute is cart-scoped and numeric; `delivery_date` is cart-scoped and a validated ISO date. Their allowed operators are limited to compatible numeric or date comparisons, and caller evidence cannot supply either attribute.
- Empty cart snapshots are invalid. A report cannot approve a cart with zero items.
- The evaluator retains evidence values, IDs, source identifiers, timestamps, scopes, and per-evidence assessments in the report. Derived total and delivery evidence cite `local-cart-snapshot` and the capture timestamp.
- The SHA-256 hash covers cart ID, currency, capture timestamp, sorted item IDs/product IDs/variant IDs/quantities/unit prices, shipping, discount, and optional delivery promise. Item sorting by `item_id` makes item order irrelevant. All snapshot fields are represented in a fixed struct before JSON encoding.
- The hash binds a report to the supplied local snapshot. It does not authenticate that the snapshot matches a merchant's live cart or independently prove seller claims. The mock gate separately binds the full context, including the snapshot and evidence, and rejects any changed value or provenance.
- The mock gate's secret key and approval state are random, synchronized, and process-local. An approval is one-time and rejected if replayed; a rejected presentation does not consume the valid approval. It is neither persistent nor a real authorization credential.
- `ALLOW` describes the result of local evaluation. Only `mock-checkout` demonstrates an additional local enforcement step; it executes no payment and contacts no merchant.

## Current implementation

Implemented: LLM-backed `interpret`, deterministic `verify`, composed `check`, in-memory `mock-checkout`, mock-provider evidence attestations/freshness checks, explicit multi-item bindings, offline `eval`, separately invoked remote `eval-intent`, strict structured-output validation, strict/inclusive numeric and date operators, INR cart snapshot validation and total calculation, cart/item evidence scoping, derived cart facts, canonical snapshot hashing, provenance reporting, and the overall purchase policy.

The flow is separated into natural-language interpretation, evidence gathering, deterministic verification against a cart snapshot, and a mock checkout authorization layer. Real evidence gathering/authentication and any persistent or external checkout authorization are not implemented. `eval-intent` makes remote model calls and is never part of unit tests or `make eval`.
