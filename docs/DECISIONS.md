# Engineering decisions

## Go and the standard library

Use Go for the core implementation and CLI. The deterministic evaluator uses only the standard library; no external dependencies are needed.

## Keep each increment focused

The current scope is the CLI, structured verification input, deterministic evidence evaluation, local decision policy, and cart snapshot binding. Do not pre-create packages or integrations for unimplemented systems.

## Separate interpretation from approval

An LLM may help interpret user language later, but it cannot approve a purchase. The current evaluator accepts structured requirements and evidence, applies explicit operators, and returns `PASS`, `FAIL`, or `UNKNOWN` with evidence provenance.

## Use explicit values and policy

Typed values use explicit JSON type tags. Invalid or incomparable evidence is rejected instead of being coerced. Hard failures result in `BLOCK`; unresolved hard requirements result in `REVIEW`; `ALLOW` requires at least one hard requirement and all hard requirements passing. Preferences do not change the overall decision.

## Bind each report to a canonical snapshot

Hash a fixed struct representation of the full INR snapshot with item rows sorted by `item_id`. The report includes the SHA-256 digest. Snapshot changes require revalidation; a future checkout gate must compare the current snapshot hash with the report hash.

## Scope evidence to the cart or item

Requirements and evidence use explicit cart or item scope and subject IDs. Item evidence must match the cart item's product and variant. Cart total and promised delivery are derived from the snapshot, not caller evidence.

## Keep local evaluation separate from checkout

`ALLOW` reports only the result of this local evaluation. A snapshot hash binds the report to the caller-supplied data, but does not prove the live merchant cart matches it or authenticate seller claims. The current verifier does not execute checkout or payments.

## Prototype scope

BuyRightAgents is an independent prototype exploring a proposed capability. Documentation must not imply that Razorpay currently offers this exact feature or that no other company is pursuing it.
