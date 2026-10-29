package evidence

import (
	"testing"
	"time"

	"github.com/bhavika44/buyrightagents/internal/verification"
)

func evidenceReference() time.Time { return time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC) }

func evidenceSnapshot(capturedAt string) verification.CartSnapshot {
	return verification.CartSnapshot{
		CartID: "cart-evidence", Currency: "INR", CapturedAt: capturedAt,
		Items: []verification.CartItem{{
			ItemID: "item-a", ProductID: "product-a", VariantID: "variant-a", Quantity: 1, UnitPricePaise: 400000,
		}},
		ShippingPaise: 25000,
	}
}

func veganRequirement() verification.Requirement {
	return verification.Requirement{
		ID: "vegan", Attribute: "vegan", Scope: verification.ItemScope, SubjectID: "item-a",
		Operator: verification.Equals, Expected: verification.BooleanValue(true), Severity: verification.Hard,
	}
}

func veganEvidence(value bool, observedAt string) verification.Evidence {
	return verification.Evidence{
		ID: "vegan-claim", Attribute: "vegan", Scope: verification.ItemScope, SubjectID: "item-a",
		ProductID: "product-a", VariantID: "variant-a", Value: verification.BooleanValue(value),
		SourceID: "synthetic-catalogue", ObservedAt: observedAt,
	}
}

func testProvider(t *testing.T, id string) *MockProvider {
	t.Helper()
	provider, err := NewMockProvider(id, []byte("evidence-provider-test-key-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func evaluateTrusted(t *testing.T, provider *MockProvider, snapshot verification.CartSnapshot, evidence []verification.Evidence) verification.Report {
	t.Helper()
	policy := provider.TrustPolicy(time.Hour, 5*time.Minute)
	policy.ReferenceTime = evidenceReference()
	report, err := verification.Evaluate(verification.Input{
		CartSnapshot: snapshot, Requirements: []verification.Requirement{veganRequirement()}, Evidence: evidence, EvidencePolicy: &policy,
	})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func TestTrustedMockEvidencePolicy(t *testing.T) {
	snapshot := evidenceSnapshot("2026-10-28T09:00:00Z")
	trusted := testProvider(t, "catalogue-a")
	other := testProvider(t, "catalogue-b")
	attest := func(t *testing.T, provider *MockProvider, claim verification.Evidence) verification.Evidence {
		t.Helper()
		result, err := provider.Attest(snapshot, claim)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}

	for _, test := range []struct {
		name         string
		evidence     []verification.Evidence
		wantDecision verification.Decision
		wantOutcome  verification.Outcome
		wantTrust    verification.EvidenceTrustOutcome
	}{
		{"fresh positive", []verification.Evidence{attest(t, trusted, veganEvidence(true, "2026-10-28T09:59:00Z"))}, verification.Allow, verification.Pass, verification.EvidenceTrusted},
		{"fresh negative", []verification.Evidence{attest(t, trusted, veganEvidence(false, "2026-10-28T09:59:00Z"))}, verification.Block, verification.Fail, verification.EvidenceTrusted},
		{"missing evidence", nil, verification.Review, verification.Unknown, ""},
		{"unknown provider", []verification.Evidence{attest(t, other, veganEvidence(true, "2026-10-28T09:59:00Z"))}, verification.Review, verification.Unknown, verification.EvidenceUnknownProvider},
		{"tampered value", func() []verification.Evidence {
			claim := attest(t, trusted, veganEvidence(true, "2026-10-28T09:59:00Z"))
			claim.Value = verification.BooleanValue(false)
			return []verification.Evidence{claim}
		}(), verification.Review, verification.Unknown, verification.EvidenceInvalidProof},
		{"tampered provider identifier", func() []verification.Evidence {
			claim := attest(t, trusted, veganEvidence(true, "2026-10-28T09:59:00Z"))
			claim.Attestation.ProviderID = "unknown-provider"
			return []verification.Evidence{claim}
		}(), verification.Review, verification.Unknown, verification.EvidenceUnknownProvider},
		{"tampered provenance", func() []verification.Evidence {
			claim := attest(t, trusted, veganEvidence(true, "2026-10-28T09:59:00Z"))
			claim.SourceID = "rewritten-source"
			return []verification.Evidence{claim}
		}(), verification.Review, verification.Unknown, verification.EvidenceInvalidProof},
		{"tampered attribute", func() []verification.Evidence {
			claim := attest(t, trusted, veganEvidence(true, "2026-10-28T09:59:00Z"))
			claim.Attribute = "water_resistant"
			return []verification.Evidence{claim}
		}(), verification.Review, verification.Unknown, verification.EvidenceInvalidProof},
		{"stale", []verification.Evidence{attest(t, trusted, veganEvidence(true, "2026-10-28T08:00:00Z"))}, verification.Review, verification.Unknown, verification.EvidenceStale},
		{"future dated", []verification.Evidence{attest(t, trusted, veganEvidence(true, "2026-10-28T10:06:00Z"))}, verification.Review, verification.Unknown, verification.EvidenceFutureDated},
		{"contradictory authenticated claims", []verification.Evidence{
			attest(t, trusted, veganEvidence(true, "2026-10-28T09:58:00Z")),
			func() verification.Evidence {
				claim := veganEvidence(false, "2026-10-28T09:59:00Z")
				claim.ID = "vegan-claim-negative"
				return attest(t, trusted, claim)
			}(),
		}, verification.Review, verification.Unknown, verification.EvidenceTrusted},
	} {
		t.Run(test.name, func(t *testing.T) {
			report := evaluateTrusted(t, trusted, snapshot, test.evidence)
			if report.Decision != test.wantDecision || report.Results[0].Outcome != test.wantOutcome {
				t.Fatalf("decision=%s outcome=%s, want %s/%s: %#v", report.Decision, report.Results[0].Outcome, test.wantDecision, test.wantOutcome, report)
			}
			if test.wantTrust != "" && (len(report.EvidenceTrust) != len(test.evidence) || report.EvidenceTrust[0].Outcome != test.wantTrust) {
				t.Fatalf("trust=%#v, want %s", report.EvidenceTrust, test.wantTrust)
			}
		})
	}
}

func TestAttestationCannotMoveAcrossSnapshotOrProductVariant(t *testing.T) {
	provider := testProvider(t, "catalogue-a")
	snapshot := evidenceSnapshot("2026-10-28T09:00:00Z")
	claim, err := provider.Attest(snapshot, veganEvidence(true, "2026-10-28T09:59:00Z"))
	if err != nil {
		t.Fatal(err)
	}

	t.Run("different snapshot", func(t *testing.T) {
		otherSnapshot := evidenceSnapshot("2026-10-28T09:01:00Z")
		report := evaluateTrusted(t, provider, otherSnapshot, []verification.Evidence{claim})
		if report.Decision != verification.Review || report.Results[0].Outcome != verification.Unknown || report.EvidenceTrust[0].Outcome != verification.EvidenceSnapshotMismatch {
			t.Fatalf("replayed evidence was usable: %#v", report)
		}
	})
	t.Run("changed variant", func(t *testing.T) {
		variantClaim := claim
		variantClaim.VariantID = "variant-b"
		if _, err := verification.Evaluate(verification.Input{CartSnapshot: snapshot, Requirements: []verification.Requirement{veganRequirement()}, Evidence: []verification.Evidence{variantClaim}}); err == nil {
			t.Fatal("product or variant mismatch was accepted")
		}
	})
	t.Run("different item product and variant", func(t *testing.T) {
		twoItemSnapshot := snapshot
		twoItemSnapshot.Items = append(twoItemSnapshot.Items, verification.CartItem{
			ItemID: "item-b", ProductID: "product-b", VariantID: "variant-b", Quantity: 1, UnitPricePaise: 100000,
		})
		claim, err := provider.Attest(twoItemSnapshot, veganEvidence(true, "2026-10-28T09:59:00Z"))
		if err != nil {
			t.Fatal(err)
		}
		claim.SubjectID, claim.ProductID, claim.VariantID = "item-b", "product-b", "variant-b"
		requirement := veganRequirement()
		requirement.SubjectID = "item-b"
		policy := provider.TrustPolicy(time.Hour, 5*time.Minute)
		policy.ReferenceTime = evidenceReference()
		report, err := verification.Evaluate(verification.Input{CartSnapshot: twoItemSnapshot, Requirements: []verification.Requirement{requirement}, Evidence: []verification.Evidence{claim}, EvidencePolicy: &policy})
		if err != nil {
			t.Fatal(err)
		}
		if report.Decision != verification.Review || report.Results[0].Outcome != verification.Unknown || report.EvidenceTrust[0].Outcome != verification.EvidenceInvalidProof {
			t.Fatalf("attestation moved to another item: %#v", report)
		}
	})
}
