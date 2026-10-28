# Initial architecture

The project is CLI-first and deliberately small. The initial executable only identifies itself and provides help and version commands. Domain and verification logic will be designed in later steps.

## Proposed direction

1. Capture the user's requirements and distinguish hard constraints from preferences.
2. Gather product, delivery, price, and cart evidence from trusted sources.
3. Evaluate each requirement as `PASS`, `FAIL`, or `UNKNOWN`.
4. Bind the decision to an immutable representation or digest of the exact cart.
5. Permit checkout only under the policy for the result; hard failures block, and unresolved hard requirements require human review.

These are design goals, not implemented capabilities.

## Trust boundaries

- Natural-language interpretation may use an LLM, but model output is an input to verification, not approval authority.
- Product descriptions, seller claims, agent summaries, and retrieved data are evidence with provenance and varying trust; they are not automatically true.
- The verifier should evaluate evidence independently and retain enough provenance to explain a result.
- The cart binding prevents a decision for one cart from authorizing a changed cart.
- Checkout/payment execution is outside the current prototype and requires an explicit integration design and authorization boundary.

## Current implementation

There is no shopping agent, MCP server, LLM integration, database, payment integration, or verification engine yet.
