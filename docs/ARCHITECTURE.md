# Architecture

The project is CLI-first and deliberately small. The executable provides help, version, and `verify <input.json>` commands. The verification package evaluates structured requirements against explicitly typed evidence; it does not fetch evidence or call an LLM.

## Proposed direction

1. Capture the user's requirements and distinguish hard constraints from preferences. Structured requirements are accepted now; natural-language interpretation is not implemented.
2. Gather product, delivery, price, and cart evidence from sources. Evidence is supplied by the caller now; fetching and source authentication are not implemented.
3. Evaluate each requirement as `PASS`, `FAIL`, or `UNKNOWN`. This deterministic evaluation and the `ALLOW`, `BLOCK`, or `REVIEW` policy are implemented.
4. Bind the decision to an immutable representation or digest of the exact cart. This remains planned.
5. Permit checkout only under the policy for the result. Checkout enforcement and payments remain out of scope.

## Trust boundaries

- Natural-language interpretation may use an LLM later, but model output will be input to verification, not approval authority.
- Product descriptions, seller claims, agent summaries, and retrieved data are caller-supplied evidence with provenance; this prototype does not authenticate their truth.
- The evaluator applies deterministic comparisons and retains evidence IDs, source identifiers, timestamps, and per-evidence assessments in the report.
- The current evaluator does not bind a decision to an immutable cart snapshot. Cart snapshot binding is planned and any future checkout integration must revalidate changed carts.
- The current prototype does not enforce checkout or execute payments.

## Current implementation

Implemented: CLI help/version and `verify <input.json>`, typed scalar validation, exact attribute matching, evidence-based per-requirement outcomes, provenance reporting, and overall purchase policy.

Not implemented: shopping agent, MCP server, LLM integration, database, evidence retrieval/authentication, immutable cart snapshot binding, checkout enforcement, or payment integration. `ALLOW` describes only the result of this local evaluation and is not permission to execute a payment.
