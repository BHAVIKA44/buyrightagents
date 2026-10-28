# Engineering decisions

## Go and the standard library

Use Go for the core implementation and CLI. The deterministic evaluator uses only the standard library; no external dependencies are needed.

## Keep each increment focused

The current scope is the CLI, structured verification input, a deterministic evidence evaluator, and its local decision policy. Do not pre-create packages or integrations for unimplemented systems.

## Separate interpretation from approval

An LLM may help interpret user language later, but it cannot approve a purchase. The current evaluator accepts structured requirements and evidence, applies explicit operators, and returns `PASS`, `FAIL`, or `UNKNOWN` with evidence provenance.

## Use explicit values and policy

Typed values use explicit JSON type tags. Invalid or incomparable evidence is rejected instead of being coerced. Hard failures result in `BLOCK`; unresolved hard requirements result in `REVIEW`; `ALLOW` requires at least one hard requirement and all hard requirements passing. Preferences do not change the overall decision.

## Keep local evaluation separate from checkout

`ALLOW` reports only the result of this local evaluation. The current verifier does not bind its report to an immutable cart snapshot and does not execute checkout or payments. Cart snapshot binding and revalidation after cart changes remain future work.

## Prototype scope

BuyRightAgents is an independent prototype exploring a proposed capability. Documentation must not imply that Razorpay currently offers this exact feature or that no other company is pursuing it.
