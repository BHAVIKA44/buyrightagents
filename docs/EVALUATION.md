# Offline evaluation suite

`evals/cases.jsonl` is a line-delimited corpus of complete, synthetic verifier inputs with manually authored expected labels. Each non-empty line has a stable `id`, `category`, explanatory `description`, a full `input` using the same schema as `verify`, an `expected_decision`, and an `expected_outcomes` mapping that covers every requirement ID exactly once.

The current 67-case corpus contains these categories:

| Category | Minimum cases | Coverage |
| --- | ---: | --- |
| `baseline` | 8 | Consistent evidence and passing hard requirements |
| `hard_failure` | 8 | Hard violations and decision precedence over unknowns or preferences |
| `total_price` | 11 | Quantity, shipping, discounts, strict/inclusive thresholds, and boundary amounts |
| `delivery` | 10 | Strict/inclusive deadlines, earlier/later dates, equality, and missing promises |
| `missing_contradictory` | 8 | Missing, unrelated, or contradictory evidence |
| `item_scoping` | 10 | Multiple items and evidence that must stay attached to the right item |
| `preferences` | 6 | Passing, failing, and unknown preferences under different hard outcomes |
| `mixed` | 6 | Combined item claims and snapshot-derived price/delivery facts |

## Labels and review

Expected labels are authored from the documented verifier contract: matching evidence yields `PASS` or `FAIL` according to the requirement operator; absent or contradictory evidence yields `UNKNOWN`; a hard failure yields `BLOCK`; unresolved hard requirements yield `REVIEW`; and `ALLOW` requires at least one hard requirement with all hard requirements passing. Preferences do not override hard-constraint policy. Labels are explicit corpus data and are not rewritten by the command or copied from live `Evaluate` results. New cases should be reviewed against these rules before being committed.

The loader rejects malformed JSONL, blank lines, duplicate JSON keys, unknown fields, repeated case IDs, invalid labels, and expected-outcome mappings that omit or add requirement IDs. Any invalid case stops the run instead of being skipped. The command does not modify the corpus.

Run the suite with:

```sh
go run ./cmd/buyrightagents eval evals/cases.jsonl
make eval
```

## Metrics

All metrics compare the verifier's actual output with the authored labels. Percentages include their count numerator and denominator. A zero denominator is reported as `n/a (0/0)`.

| Metric | Calculation |
| --- | --- |
| Decision accuracy | Exact decision matches divided by all valid cases |
| Requirement outcome accuracy | Exact `PASS`/`FAIL`/`UNKNOWN` matches divided by all expected requirement outcomes |
| False-ALLOW rate | Expected non-`ALLOW` cases predicted `ALLOW`, divided by all expected non-`ALLOW` cases |
| Missed-BLOCK rate | Expected `BLOCK` cases predicted `ALLOW` or `REVIEW`, divided by all expected `BLOCK` cases |
| False-BLOCK rate | Cases predicted `BLOCK` when expected decision is not `BLOCK`, divided by all expected non-`BLOCK` cases |
| Review rate | Actual `REVIEW` decisions divided by all valid cases |
| Confusion matrix | Counts for each expected decision row and actual decision column |
| Per-category decision accuracy | Exact decision matches divided by valid cases in each category |

On mismatches, output identifies the case ID, category, expected and actual decision, plus each requirement outcome that disagrees. The command exits nonzero on any mismatch.

## Limits

This suite measures deterministic evaluator agreement on synthetic, explicitly labelled examples. It does not prove real-world purchase correctness or show that evidence is true. Evidence is caller-supplied, seller claims are not authenticated, and a local report or snapshot hash does not authenticate that the merchant's live cart matches the snapshot. A passing offline suite is not payment authorization or evidence of production readiness.

## Remote intent interpretation evaluation

`evals/intent-cases.jsonl` contains 50 static, manually authored natural-language requests, explicit reference timestamps, and expected intent structures. `buyrightagents eval-intent` explicitly sends each request to the configured remote LLM and may incur API charges. It is not run by `go test`, `make eval`, or ordinary validation. Automated tests exercise this comparison machinery with fake model clients; they never make live LLM calls.

Intent request-level matches normalize requirement ordering and ignore application-generated IDs. Constraint precision/recall/F1 use exact normalized attribute, operator, typed expected value, and severity matches, counted across cases (micro-averaged). Clarification detection accuracy compares whether each expected and actual case has at least one clarification. Unsupported constraints are matched by normalized source phrase and reason; their precision, recall, and F1 counts are reported separately. Zero denominators are printed as `n/a`. These metrics assess model output against synthetic labels, not real-world semantic correctness.
