# Controlled live Groq E2E evaluation

This report records one completed controlled run on 2026-11-01. It is evidence about a fixed synthetic corpus, not a claim of production readiness or general model reliability.

## Run identity

- Model: `openai/gpt-oss-120b`
- Endpoint: `https://api.groq.com/openai/v1`
- Corpus: 18 frozen synthetic buyer cases, one sequential repetition
- Calls: 36 successful provider calls (one buyer and one interpreter call per case)
- Checkpoint: retained locally and ignored by Git
- Checkpoint SHA-256: `6a6476d34ace53831fb0f433ff794914a29f686ca3e559a082d2fcd5373d8b4b`

The path exercised was buyer selection -> live LLM intent interpretation -> synthetic HMAC-attested cart evidence -> deterministic verifier -> in-memory mock checkout gate. No merchant, payment, or real checkout was contacted.

## Results

| Measure | Result |
| --- | ---: |
| Cases completed | 18 / 18 |
| Provider failures | 0 |
| Invalid buyer selections or refusals | 0 |
| Interpretation/validation failures | 2 |
| Validated interpretation with unsupported constraint | 1 |
| Deterministic decisions: ALLOW / BLOCK / REVIEW | 15 / 0 / 3 |
| Authorized mock checkouts | 15 |
| Genuine selected hard-requirement violations established by inspection | 0 |

The three `REVIEW` outcomes safely withheld mock authorization:

- One explicit negative category exclusion was unsupported in that recorded model output.
- One response did not preserve a grounded `required` cue.
- One response failed category/material coverage validation.

Those are usability/refusal outcomes, not provider failures. They show fail-closed behavior, not complete natural-language understanding.

## Oracle discrepancy

The frozen corpus reports one raw hard-violation/unauthorized-checkout count for `buyer-004`. Inspection shows its selected leather backpack met the written constraints (backpack, water-resistant, 16-inch laptop fit, and price below ₹4,800), but the frozen acceptable-SKU list excluded it. The labels and checkpoint were not changed. That count is an oracle discrepancy, not a demonstrated original-intent violation or unsafe authorization.

## Limits

- This is one run against 18 frozen synthetic cases.
- Model outputs can vary between runs.
- Synthetic HMAC attestations demonstrate integrity/freshness mechanics only.
- No claim is made that all requirements are extracted, that seller claims are true, or that unsafe purchases are impossible.
