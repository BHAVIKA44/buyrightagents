# Problem

BuyRightAgents explores a proposed capability for payment infrastructure providers: independently checking whether a proposed purchase satisfies the user's original requirements before checkout proceeds.

For example, a user may ask an agent to find a vegan backpack that fits a 16-inch laptop, costs less than ₹5,000 including delivery, and arrives before Friday. The verifier should evaluate the actual product and cart against those requirements using evidence, rather than trusting the agent's summary.

Each requirement should resolve to one of three outcomes:

- **PASS**: sufficient evidence supports the requirement.
- **FAIL**: evidence establishes a violation.
- **UNKNOWN**: evidence is insufficient or contradictory.

Hard violations should block checkout. An unresolved hard requirement should require human review. A decision must be bound to the exact cart being approved; changing the cart requires revalidation.

An LLM may help interpret natural-language requirements, but it must not be the authority that approves its own purchase. Verification needs independent evidence, explicit outcomes, and a binding between the decision and the cart.

This repository is an independent prototype exploring a proposed capability. It does not claim that Razorpay currently provides this exact feature, or that no other company is working on the problem.
