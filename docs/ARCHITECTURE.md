# Architecture

The project is CLI-first. The executable provides help, version, `interpret`, `verify`, `eval`, and explicitly invoked `eval-intent` commands. Natural-language interpretation runs before a cart exists and cannot inspect evidence or make a purchase decision. The deterministic verification package evaluates structured requirements against explicitly typed evidence and a concrete cart snapshot.

## Proposed direction

1. Interpret a shopping request into typed intent, hard requirements, preferences, clarification questions, and unsupported constraints. The LLM-backed `interpret` command is implemented, uses an explicit RFC3339 reference time, and returns no evidence or decision.
2. Gather evidence for the requested constraints. Evidence retrieval and source authentication are not implemented; current `verify` inputs provide evidence explicitly.
3. Bind structured requirements to a validated local INR cart snapshot and item IDs, derive cart total and delivery facts, and evaluate each requirement as `PASS`, `FAIL`, or `UNKNOWN`. The deterministic evaluator applies the `ALLOW`, `BLOCK`, or `REVIEW` policy.
4. Hash a canonical snapshot representation into every report. This binding is implemented; persistent approvals and checkout enforcement are not.
5. Any future checkout authorization system must independently enforce policy and compare the live cart with the approved snapshot. No checkout gate or payment action exists here.

## Trust boundaries

- Intent interpretation uses an LLM, but its output is untrusted structured input. The LLM is never the evidence evaluator or purchase-decision authority.
- The intent model has no cart, item, product, variant, evidence, payment, or checkout fields. Cart and item scopes are assigned only after a concrete snapshot exists; `total_price_paise` and `delivery_date` remain cart-scoped in verification.
- Source phrases are checked against the original request after lowercasing and collapsing punctuation/whitespace to spaces. This grounding check prevents invented source spans but cannot prove that the model captured every semantic constraint.
- Explicit cues such as `must`, `non-negotiable`, `ideally`, and `prefer` must be preserved in a grounded source span and cannot be assigned the opposite severity. This is a conservative guard, not a proof of semantic completeness.
- Relative-date interpretation receives the caller-supplied timestamp, local calendar date, and UTC offset. Supported simple phrases are checked against deterministic date arithmetic; ambiguous wording should produce a clarification question.
- Intent attributes are `vegan`, `water_resistant`, `recycled_material`, `laptop_fit_inches`, `total_price_paise`, `delivery_date`, `color`, `material`, and `weight_grams`. Units are inches, integer INR paise, ISO calendar dates, strings, or grams as indicated by each name. Unsupported concepts remain explicit rather than being mapped to invented attributes.
- `BUYRIGHTAGENTS_LLM_API_KEY` and `BUYRIGHTAGENTS_LLM_MODEL` are required for interpretation; `BUYRIGHTAGENTS_LLM_BASE_URL` is optional. HTTP calls have a fixed timeout, response-size bound, context cancellation, and no automatic retries.
- Product descriptions, seller claims, agent summaries, and retrieved data are caller-supplied evidence with provenance; this prototype does not authenticate their truth.
- Item-scoped evidence is tied to a real cart `item_id` and must match its `product_id` and `variant_id`. Requirements match evidence by attribute, scope, and subject ID.
- The built-in `total_price_paise` attribute is cart-scoped and numeric; `delivery_date` is cart-scoped and a validated ISO date. Their allowed operators are limited to compatible numeric or date comparisons, and caller evidence cannot supply either attribute.
- Empty cart snapshots are invalid. A report cannot approve a cart with zero items.
- The evaluator retains evidence values, IDs, source identifiers, timestamps, scopes, and per-evidence assessments in the report. Derived total and delivery evidence cite `local-cart-snapshot` and the capture timestamp.
- The SHA-256 hash covers cart ID, currency, capture timestamp, sorted item IDs/product IDs/variant IDs/quantities/unit prices, shipping, discount, and optional delivery promise. Item sorting by `item_id` makes item order irrelevant. All snapshot fields are represented in a fixed struct before JSON encoding.
- The hash binds a report to the supplied local snapshot. It does not authenticate that the snapshot matches a merchant's live cart or independently prove seller claims. A future checkout gate must compare hashes and revalidate changed carts.
- `ALLOW` describes only the result of local evaluation. The current prototype does not enforce checkout or execute payments.

## Current implementation

Implemented: LLM-backed `interpret`, deterministic `verify`, offline `eval`, separately invoked remote `eval-intent`, strict structured-output validation, strict/inclusive numeric and date operators, INR cart snapshot validation and total calculation, cart/item evidence scoping, derived cart facts, canonical snapshot hashing, provenance reporting, and the overall purchase policy.

The flow is separated into natural-language interpretation, evidence gathering, deterministic verification against a cart snapshot, and a future checkout authorization layer. Only interpretation and deterministic verification exist; evidence gathering/authentication and checkout authorization are not implemented. `eval-intent` makes remote model calls and is never part of unit tests or `make eval`.
