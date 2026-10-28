# Engineering decisions

## Go for the core

Use Go for the core implementation and CLI. The standard library is sufficient for the initial command, so there are no external dependencies.

## Keep the first increment small

Start with a help/version CLI and reserve `internal/` for later domain and verification work. Do not pre-create packages for unimplemented components.

## Separate interpretation from approval

An LLM may interpret user language, but it cannot approve a purchase. Requirement outcomes must be grounded in evidence and represented as `PASS`, `FAIL`, or `UNKNOWN`.

## Bind decisions to carts

A verification decision must refer to the exact cart reviewed. Any cart change requires fresh validation.

## Prototype scope

BuyRightAgents is an independent prototype exploring a proposed capability. Documentation must not imply that Razorpay currently offers this exact feature or that no other company is pursuing it.
