# Architecture

The project is CLI-first and deliberately small. The executable provides help, version, and `verify <input.json>` commands. The verification package evaluates structured requirements against explicitly typed evidence; it does not fetch evidence or call an LLM.

## Proposed direction

1. Capture the user's requirements and distinguish hard constraints from preferences. Structured requirements are accepted now; natural-language interpretation is not implemented.
2. Accept a local INR cart snapshot and caller-supplied product evidence. Evidence retrieval and source authentication are not implemented.
3. Derive cart total and promised delivery evidence from the snapshot, then evaluate each requirement as `PASS`, `FAIL`, or `UNKNOWN`. The deterministic evaluation and `ALLOW`, `BLOCK`, or `REVIEW` policy are implemented.
4. Hash a canonical snapshot representation into every report. This binding is implemented; persistent approvals and checkout enforcement are not.
5. A future checkout gate must compare the current cart hash against the report hash and revalidate after any mutation. Checkout enforcement and payments remain out of scope.

## Trust boundaries

- Natural-language interpretation may use an LLM later, but model output will be input to verification, not approval authority.
- Product descriptions, seller claims, agent summaries, and retrieved data are caller-supplied evidence with provenance; this prototype does not authenticate their truth.
- Item-scoped evidence is tied to a real cart `item_id` and must match its `product_id` and `variant_id`. Requirements match evidence by attribute, scope, and subject ID.
- The built-in `total_price_paise` attribute is cart-scoped and numeric; `delivery_date` is cart-scoped and a validated ISO date. Their allowed operators are limited to compatible numeric or date comparisons, and caller evidence cannot supply either attribute.
- Empty cart snapshots are invalid. A report cannot approve a cart with zero items.
- The evaluator retains evidence values, IDs, source identifiers, timestamps, scopes, and per-evidence assessments in the report. Derived total and delivery evidence cite `local-cart-snapshot` and the capture timestamp.
- The SHA-256 hash covers cart ID, currency, capture timestamp, sorted item IDs/product IDs/variant IDs/quantities/unit prices, shipping, discount, and optional delivery promise. Item sorting by `item_id` makes item order irrelevant. All snapshot fields are represented in a fixed struct before JSON encoding.
- The hash binds a report to the supplied local snapshot. It does not authenticate that the snapshot matches a merchant's live cart or independently prove seller claims. A future checkout gate must compare hashes and revalidate changed carts.
- `ALLOW` describes only the result of local evaluation. The current prototype does not enforce checkout or execute payments.

## Current implementation

Implemented: CLI help/version and `verify <input.json>`, strict JSON validation including duplicate-key rejection, typed scalar validation, INR cart snapshot validation and total calculation, cart/item evidence scoping, derived cart facts, canonical snapshot hashing, provenance reporting, and the overall purchase policy.

Not implemented: shopping agent, MCP server, LLM integration, database, evidence retrieval/authentication, persistent approvals, checkout enforcement, or payment integration.
