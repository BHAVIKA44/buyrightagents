# Verify BuyRightAgents claims

> **Do not trust a README claim that this repository can prove locally.**

BuyRightAgents is a CLI prototype. This guide separates commands that work from a
fresh clone from historical results that require private artifacts or an
explicitly configured remote model.

## 60-second route: observe the three decisions

Prerequisite: Go 1.22 or newer.

```sh
git clone https://github.com/BHAVIKA44/buyrightagents.git
cd buyrightagents
make demo
```

The command runs the public `demo` CLI entirely offline. It evaluates the same
three fixtures as `verify`, derives the displayed total/outcome/reason from each
real report, and fails if a fixture no longer produces its expected decision:

| Fixture | Expected decision | What it proves |
| --- | --- | --- |
| [`examples/vegan-backpack.json`](../examples/vegan-backpack.json) | `ALLOW` | The verifier can derive cart total/delivery and accept correctly scoped hard evidence. |
| [`examples/non-vegan-backpack.json`](../examples/non-vegan-backpack.json) | `BLOCK` | A verified negative vegan claim establishes a hard failure. |
| [`examples/missing-vegan-evidence.json`](../examples/missing-vegan-evidence.json) | `REVIEW` | Missing evidence for a hard requirement cannot fall through to approval. |

For full JSON reports, run `go run ./cmd/buyrightagents verify <fixture.json>`.
Those reports contain the cart snapshot hash, the evidence used, and a `PASS`,
`FAIL`, or `UNKNOWN` assessment for each requirement. The public CLI tests
[`TestRunVerify`](../cmd/buyrightagents/main_test.go) and
[`TestRunDemoUsesActualFixtureResults`](../cmd/buyrightagents/main_test.go)
cover the fixtures and formatted experience.

The 60-second route is **verification only**: it does not call an LLM, create
mock HMAC evidence attestations, or execute a checkout. The gate paths are
tested separately below.

## Five-minute route: verify the engineering boundaries

```sh
go test -count=1 ./...
go test -race -count=1 ./...
go vet ./...
go build ./...
make eval
make adversarial-eval
```

`make eval` compares real verifier output with independently authored labels in
[`evals/cases.jsonl`](../evals/cases.jsonl). `make adversarial-eval` executes
fixed synthetic attacks through intent validation, evidence policy, the verifier,
and the mock gate. It regenerates its tracked JSON/report; inspect the diff if
you are running it in a working tree.

The commands above make **no live model calls**. CI runs the same command family;
see [`ci.yml`](../.github/workflows/ci.yml).

## Claim → source → test → command

| Claim | Primary implementation | Regression evidence | Reproduce |
| --- | --- | --- | --- |
| Model output cannot contain a purchase decision, cart, or evidence fields. | [`internal/intent/types.go`](../internal/intent/types.go), [`internal/intent/interpreter.go`](../internal/intent/interpreter.go) | [`TestModelJSONRejectsMalformedUnknownDuplicateAndDecisionFields`](../internal/intent/interpreter_test.go) | `go test ./internal/intent -run TestModelJSONRejectsMalformedUnknownDuplicateAndDecisionFields` |
| Defined hard omissions, wrong polarity, and certain operator/value changes fail closed. | [`internal/intent/coverage.go`](../internal/intent/coverage.go) | [`TestCoverageGuardDetectsOmittedCategoryMaterialAndLaptopFit`](../internal/intent/interpreter_test.go), [`TestWrongPolarityCannotBypassCoverageThroughIncidentalText`](../cmd/buyrightagents/purchase_check_test.go) | `go test ./internal/intent ./cmd/buyrightagents -run 'TestCoverageGuardDetectsOmittedCategoryMaterialAndLaptopFit|TestWrongPolarityCannotBypassCoverageThroughIncidentalText'` |
| Numeric and date contracts stay typed; cart price/delivery are derived, not caller-provided. | [`internal/verification/verification.go`](../internal/verification/verification.go) | [`TestStrictNumericBoundaries`](../internal/verification/verification_test.go), [`TestBuiltInCartRequirementContracts`](../internal/verification/verification_test.go), [`TestSnapshotDerivedTotalAndDelivery`](../internal/verification/verification_test.go) | `go test ./internal/verification` |
| Item evidence cannot move to another cart item/product/variant. | [`internal/verification/verification.go`](../internal/verification/verification.go) | [`TestEvidenceScopePreventsCrossItemMatching`](../internal/verification/verification_test.go) | `go test ./internal/verification -run TestEvidenceScopePreventsCrossItemMatching` |
| A hard failure wins over unrelated uncertainty; unresolved hard requirements remain `REVIEW`. | [`cmd/buyrightagents/purchase_check.go`](../cmd/buyrightagents/purchase_check.go) | [`TestPurchaseCheckHardFailureDecisionPrecedence`](../cmd/buyrightagents/purchase_check_test.go), [`TestPurchaseCheckDecisionPrecedencePreservesReviewAndAllow`](../cmd/buyrightagents/purchase_check_test.go) | `go test ./cmd/buyrightagents -run 'TestPurchaseCheckHardFailureDecisionPrecedence|TestPurchaseCheckDecisionPrecedencePreservesReviewAndAllow'` |
| Mock evidence is bound to the snapshot and cannot silently satisfy a different variant. | [`internal/evidence/mock_provider.go`](../internal/evidence/mock_provider.go) | [`TestAttestationCannotMoveAcrossSnapshotOrProductVariant`](../internal/evidence/mock_provider_test.go) | `go test ./internal/evidence -run TestAttestationCannotMoveAcrossSnapshotOrProductVariant` |
| The mock gate requires trusted evidence, rechecks freshness, binds the context, and consumes an approval once. | [`internal/checkout/control.go`](../internal/checkout/control.go) | [`TestGateDoesNotTrustCallerSuppliedAllowWithoutTrustedEvidence`](../internal/checkout/control_test.go), [`TestGateRechecksEvidenceFreshnessAtCheckout`](../internal/checkout/control_test.go), [`TestGateRejectsTamperingWithoutConsumingApproval`](../internal/checkout/control_test.go) | `go test ./internal/checkout` |
| The deterministic fixture corpus agrees with its authored labels. | [`internal/evaluation`](../internal/evaluation), [`evals/cases.jsonl`](../evals/cases.jsonl) | [`TestRunEvalReportsMetricsAndFailsOnMismatch`](../cmd/buyrightagents/main_test.go) | `make eval` |
| The fixed adversarial corpus reaches no unexpected authorization. | [`cmd/buyrightagents/adversarial.go`](../cmd/buyrightagents/adversarial.go), [`evals/adversarial-cases.jsonl`](../evals/adversarial-cases.jsonl) | [`TestAdversarialOmittedCategoryMaterialAndLaptopFitCannotAuthorizeCheckout`](../cmd/buyrightagents/adversarial_test.go) | `make adversarial-eval` |

## What these commands do not prove

They do not prove that a merchant's live cart, catalogue, or delivery promise is
true. They do not establish that an LLM correctly understands arbitrary shopping
language. They do not execute payment. They do not make the in-process HMAC a
real seller identity system. They establish only the tested deterministic and
mock-boundary behaviour.

## Optional remote and historical evidence

`interpret`, `check`, `mock-checkout`, and `eval-intent` use a configured
OpenAI-compatible model endpoint. They require a local environment configuration
and may incur API charges. They are intentionally not part of `go test`, `make
eval`, or the 60-second route.

The [controlled live run](LIVE_GROQ_E2E_EVALUATION.md) describes one frozen
18-case run. The [saved-Groq checkout report](LIVE_GROQ_CHECKOUT_EVALUATION.md)
is an offline replay of a private ignored checkpoint. A fresh clone cannot
reproduce that replay; `make saved-groq-checkout-eval` exits clearly without the
authorized private artifact. Do not treat either as a public benchmark or proof
of production reliability.

## Read next

- [Architecture](ARCHITECTURE.md): actual components and trust boundaries.
- [Engineering decisions](DECISIONS.md): contracts and limitations.
- [Offline deterministic evaluation](EVALUATION.md): corpus and metric details.
- [Adversarial evaluation](ADVERSARIAL_EVALUATION.md): fixed corpus results and
  attack classes.
