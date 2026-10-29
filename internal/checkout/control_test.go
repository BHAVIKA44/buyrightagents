package checkout

import (
	"encoding/json"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/bhavika44/buyrightagents/internal/evidence"
	"github.com/bhavika44/buyrightagents/internal/verification"
)

var testClock = func() time.Time { return time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC) }

func newTestGate(t *testing.T) (*Gate, *evidence.MockProvider) {
	t.Helper()
	provider, err := evidence.NewMockProvider("test-catalogue", []byte("mock-evidence-test-key-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	gate, err := NewGateWithEvidencePolicy([]byte("mock-checkout-test-key-0123456789"), testClock, provider.TrustPolicy(24*time.Hour, 5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return gate, provider
}

func allowedContext(t *testing.T, provider *evidence.MockProvider) PurchaseContext {
	t.Helper()
	context := PurchaseContext{
		Request: "Find a vegan bag under ₹5,000", ReferenceTime: "2026-10-28T10:00:00Z", Decision: verification.Allow,
		CartSnapshot: verification.CartSnapshot{
			CartID: "cart-1", Currency: "INR", CapturedAt: "2026-10-28T09:00:00Z",
			Items:         []verification.CartItem{{ItemID: "item-1", ProductID: "product-1", VariantID: "variant-1", Quantity: 1, UnitPricePaise: 450000}},
			ShippingPaise: 25000,
		},
		Requirements: []verification.Requirement{
			{ID: "price", Attribute: "total_price_paise", Scope: verification.CartScope, SubjectID: "cart-1", Operator: verification.LessThan, Expected: verification.NumberInt64Value(500000), Severity: verification.Hard},
			{ID: "vegan", Attribute: "vegan", Scope: verification.ItemScope, SubjectID: "item-1", Operator: verification.Equals, Expected: verification.BooleanValue(true), Severity: verification.Hard},
		},
		Evidence: []verification.Evidence{{
			ID: "vegan-proof", Attribute: "vegan", Scope: verification.ItemScope, SubjectID: "item-1", ProductID: "product-1", VariantID: "variant-1",
			Value: verification.BooleanValue(true), SourceID: "synthetic-catalogue", ObservedAt: "2026-10-28T09:00:00Z",
		}},
	}
	attested, err := provider.Attest(context.CartSnapshot, context.Evidence[0])
	if err != nil {
		t.Fatal(err)
	}
	context.Evidence[0] = attested
	return context
}

func newAllowedFixture(t *testing.T) (*Gate, PurchaseContext) {
	t.Helper()
	gate, provider := newTestGate(t)
	return gate, allowedContext(t, provider)
}

func cloneContext(context PurchaseContext) PurchaseContext {
	copy := context
	copy.Requirements = append([]verification.Requirement(nil), context.Requirements...)
	copy.CartSnapshot.Items = append([]verification.CartItem(nil), context.CartSnapshot.Items...)
	copy.Evidence = append([]verification.Evidence(nil), context.Evidence...)
	return copy
}

func TestGateAuthorizesOneTimeBoundCheckout(t *testing.T) {
	gate, context := newAllowedFixture(t)
	approval, err := gate.Issue(context)
	if err != nil {
		t.Fatal(err)
	}
	if approval.Reference == "" || approval.BindingSHA256 == "" || approval.Credential == "" || approval.PolicyVersion != policyVersion {
		t.Fatalf("invalid approval %#v", approval)
	}
	encoded, err := json.Marshal(approval)
	if err != nil || strings.Contains(string(encoded), approval.Credential) {
		t.Fatalf("approval JSON leaked credential: %s err=%v", encoded, err)
	}
	first := gate.Checkout(approval, context)
	if !first.Authorized || first.Audit.Outcome != CheckoutSucceeded || first.Audit.Timestamp != "2026-10-28T10:00:00Z" {
		t.Fatalf("authorized checkout failed: %#v", first)
	}
	second := gate.Checkout(approval, context)
	if second.Authorized || !strings.Contains(second.Reason, "already been consumed") || second.Audit.Outcome != CheckoutRejected {
		t.Fatalf("replay was authorized: %#v", second)
	}
}

func TestGateRejectsNonAllowAndMalformedContexts(t *testing.T) {
	tests := []struct {
		name string
		edit func(*PurchaseContext)
	}{
		{"hard budget failure", func(context *PurchaseContext) {
			context.CartSnapshot.Items[0].UnitPricePaise = 510000
			context.Decision = verification.Block
		}},
		{"missing hard evidence", func(context *PurchaseContext) { context.Evidence = nil; context.Decision = verification.Review }},
		{"declared review", func(context *PurchaseContext) { context.Decision = verification.Review }},
		{"malformed cart", func(context *PurchaseContext) { context.CartSnapshot.Items = nil }},
		{"invalid requirement", func(context *PurchaseContext) {
			context.Requirements[0].Expected = verification.StringValue("not a paise amount")
		}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gate, context := newAllowedFixture(t)
			test.edit(&context)
			if approval, err := gate.Issue(context); err == nil || approval.Reference != "" {
				t.Fatalf("non-allow or malformed context issued approval %#v", approval)
			}
			audit := gate.AuditTrail()
			if len(audit) != 1 || audit[0].Outcome != ApprovalRejected {
				t.Fatalf("rejection was not audited: %#v", audit)
			}
		})
	}
}

func TestGateAuditSeparatesComposedAndVerifierDecisions(t *testing.T) {
	gate, context := newAllowedFixture(t)
	context.Decision = verification.Allow
	context.CartSnapshot.Items[0].UnitPricePaise = 510000
	if _, err := gate.Issue(context); err == nil {
		t.Fatal("a forged ALLOW decision issued approval")
	}
	audit := gate.AuditTrail()
	if len(audit) != 1 || audit[0].Decision != verification.Allow || audit[0].VerifierDecision != verification.Block || audit[0].Outcome != ApprovalRejected {
		t.Fatalf("audit did not explain decision disagreement: %#v", audit)
	}
}

func TestGateDoesNotTrustCallerSuppliedAllowWithoutTrustedEvidence(t *testing.T) {
	provider, err := evidence.NewMockProvider("untrusted-catalogue", []byte("untrusted-evidence-key-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	context := allowedContext(t, provider)
	gate, err := NewGate([]byte("mock-checkout-test-key-0123456789"), testClock)
	if err != nil {
		t.Fatal(err)
	}
	if approval, err := gate.Issue(context); err == nil || approval.Reference != "" {
		t.Fatalf("caller-supplied ALLOW with an untrusted provider issued approval %#v", approval)
	}
	audit := gate.AuditTrail()
	if len(audit) != 1 || audit[0].VerifierDecision != verification.Review || audit[0].Outcome != ApprovalRejected {
		t.Fatalf("untrusted evidence did not force safe review: %#v", audit)
	}
}

func TestGateRechecksEvidenceFreshnessAtCheckout(t *testing.T) {
	current := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	provider := testMockProviderForFreshness(t)
	gate, err := NewGateWithEvidencePolicy([]byte("mock-checkout-test-key-0123456789"), func() time.Time { return current }, provider.TrustPolicy(time.Hour, 5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	context := allowedContext(t, provider)
	approval, err := gate.Issue(context)
	if err != nil {
		t.Fatalf("fresh evidence did not issue approval: %v", err)
	}
	current = current.Add(time.Hour + time.Minute)
	result := gate.Checkout(approval, context)
	if result.Authorized || result.Audit.VerifierDecision != verification.Review || !strings.Contains(result.Reason, "not an ALLOW") {
		t.Fatalf("stale evidence was accepted at checkout: %#v", result)
	}
}

func testMockProviderForFreshness(t *testing.T) *evidence.MockProvider {
	t.Helper()
	provider, err := evidence.NewMockProvider("test-catalogue", []byte("mock-evidence-test-key-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func TestGateCanonicalBindingIgnoresRequirementAndEvidenceOrder(t *testing.T) {
	gate, provider := newTestGate(t)
	context := allowedContext(t, provider)
	colorEvidence := verification.Evidence{
		ID: "color-proof", Attribute: "color", Scope: verification.ItemScope, SubjectID: "item-1", ProductID: "product-1", VariantID: "variant-1",
		Value: verification.StringValue("black"), SourceID: "synthetic-catalogue", ObservedAt: "2026-10-28T09:00:00Z",
	}
	attested, err := provider.Attest(context.CartSnapshot, colorEvidence)
	if err != nil {
		t.Fatal(err)
	}
	context.Evidence = append(context.Evidence, attested)
	approval, err := gate.Issue(context)
	if err != nil {
		t.Fatal(err)
	}
	attempt := cloneContext(context)
	attempt.Requirements[0], attempt.Requirements[1] = attempt.Requirements[1], attempt.Requirements[0]
	attempt.Evidence[0], attempt.Evidence[1] = attempt.Evidence[1], attempt.Evidence[0]
	if result := gate.Checkout(approval, attempt); !result.Authorized {
		t.Fatalf("canonical ordering changed a semantically identical context: %#v", result)
	}
}

func TestGateRejectsTamperingWithoutConsumingApproval(t *testing.T) {
	tests := []struct {
		name           string
		mutateContext  func(*PurchaseContext)
		mutateApproval func(*Approval)
	}{
		{"price", func(context *PurchaseContext) { context.CartSnapshot.Items[0].UnitPricePaise = 455000 }, nil},
		{"quantity", func(context *PurchaseContext) { context.CartSnapshot.Items[0].Quantity = 2 }, nil},
		{"product", func(context *PurchaseContext) {
			context.CartSnapshot.Items[0].ProductID = "product-2"
			context.Evidence[0].ProductID = "product-2"
		}, nil},
		{"variant", func(context *PurchaseContext) {
			context.CartSnapshot.Items[0].VariantID = "variant-2"
			context.Evidence[0].VariantID = "variant-2"
		}, nil},
		{"evidence value", func(context *PurchaseContext) { context.Evidence[0].Value = verification.BooleanValue(false) }, nil},
		{"evidence source", func(context *PurchaseContext) { context.Evidence[0].SourceID = "different-source" }, nil},
		{"request", func(context *PurchaseContext) { context.Request = "Find a vegan bag under ₹4,000" }, nil},
		{"requirements", func(context *PurchaseContext) {
			context.Requirements[0].Expected = verification.NumberInt64Value(600000)
		}, nil},
		{"reference time", func(context *PurchaseContext) { context.ReferenceTime = "2026-10-29T10:00:00Z" }, nil},
		{"credential", nil, func(approval *Approval) { approval.Credential = "altered" }},
		{"policy version", nil, func(approval *Approval) { approval.PolicyVersion = "altered" }},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			gate, original := newAllowedFixture(t)
			approval, err := gate.Issue(original)
			if err != nil {
				t.Fatal(err)
			}
			attempt, presented := cloneContext(original), approval
			if test.mutateContext != nil {
				test.mutateContext(&attempt)
			}
			if test.mutateApproval != nil {
				test.mutateApproval(&presented)
			}
			if result := gate.Checkout(presented, attempt); result.Authorized || result.Audit.Outcome != CheckoutRejected {
				t.Fatalf("tampered checkout authorized: %#v", result)
			}
			if result := gate.Checkout(approval, original); !result.Authorized {
				t.Fatalf("rejection consumed valid approval: %#v", result)
			}
		})
	}
}

func TestGateRejectsUnknownApproval(t *testing.T) {
	gate, context := newAllowedFixture(t)
	result := gate.Checkout(Approval{Reference: "mock-approval-unknown", BindingSHA256: "unknown", PolicyVersion: policyVersion, Credential: "fabricated"}, context)
	if result.Authorized || !strings.Contains(result.Reason, "unknown approval") {
		t.Fatalf("unknown approval authorized: %#v", result)
	}
}

func TestGateAllowsSecondFreshApprovalAndConsumesOnlyOneConcurrentCheckout(t *testing.T) {
	gate, context := newAllowedFixture(t)
	first, err := gate.Issue(context)
	if err != nil {
		t.Fatal(err)
	}
	if result := gate.Checkout(first, context); !result.Authorized {
		t.Fatalf("first approval was not usable: %#v", result)
	}
	second, err := gate.Issue(context)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	results := make(chan CheckoutResult, 8)
	for range 8 {
		group.Add(1)
		go func() {
			defer group.Done()
			results <- gate.Checkout(second, context)
		}()
	}
	group.Wait()
	close(results)
	successes := 0
	for result := range results {
		if result.Authorized {
			successes++
		}
	}
	if successes != 1 {
		t.Fatalf("concurrent reuse authorized %d checkouts", successes)
	}
}
