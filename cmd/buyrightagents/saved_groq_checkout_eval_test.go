package main

import (
	"testing"

	"github.com/bhavika44/buyrightagents/internal/intent"
	"github.com/bhavika44/buyrightagents/internal/verification"
)

func TestSavedGroqCartUsesSatisfyingEvidenceForStrictNumbers(t *testing.T) {
	expected := intent.PurchaseIntent{Requirements: []intent.Requirement{{
		Attribute: "weight_grams", Operator: verification.LessThan,
		Expected: verification.NumberInt64Value(500), Severity: verification.Hard,
	}}}
	cart, err := savedGroqCart("strict-weight", "Weight under 0.5 kg.", expected, expected, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range *cart.Evidence {
		if claim.Attribute == "weight_grams" {
			value, ok := savedGroqNumber(claim.Value)
			if !ok || value != 499 {
				t.Fatalf("strict-less-than fixture value=%v, want 499", claim.Value.Value)
			}
			return
		}
	}
	t.Fatal("weight claim missing")
}

func TestSavedGroqCartAddsGroundedCategoryEvidence(t *testing.T) {
	expected := intent.PurchaseIntent{Requirements: []intent.Requirement{{
		Attribute: "vegan", Operator: verification.Equals,
		Expected: verification.BooleanValue(true), Severity: verification.Hard,
	}}}
	actual := intent.PurchaseIntent{Requirements: []intent.Requirement{
		{Attribute: "product_category", Operator: verification.Equals, Expected: verification.StringValue("bag"), Severity: verification.Hard},
		expected.Requirements[0],
	}}
	cart, err := savedGroqCart("grounded-category", "Find a vegan bag.", expected, actual, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range *cart.Evidence {
		if claim.Attribute == "product_category" {
			if claim.Value.Value != "bag" {
				t.Fatalf("category claim=%v, want bag", claim.Value.Value)
			}
			return
		}
	}
	t.Fatal("grounded category claim missing")
}

func TestSavedGroqCartDoesNotAddInventedCategoryEvidence(t *testing.T) {
	expected := intent.PurchaseIntent{Requirements: []intent.Requirement{{
		Attribute: "vegan", Operator: verification.Equals,
		Expected: verification.BooleanValue(true), Severity: verification.Hard,
	}}}
	actual := intent.PurchaseIntent{Requirements: []intent.Requirement{
		{Attribute: "product_category", Operator: verification.Equals, Expected: verification.StringValue("tote"), Severity: verification.Hard},
		expected.Requirements[0],
	}}
	cart, err := savedGroqCart("invented-category", "Find a vegan bag.", expected, actual, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, claim := range *cart.Evidence {
		if claim.Attribute == "product_category" {
			t.Fatalf("invented category evidence=%v", claim.Value.Value)
		}
	}
}
