package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/bhavika44/buyrightagents/internal/intent"
	"github.com/bhavika44/buyrightagents/internal/verification"
)

const demoReferenceTime = "2026-10-28T10:00:00Z"

type fixedIntentClient struct {
	response string
	user     string
	err      error
}

func (client *fixedIntentClient) Complete(_ context.Context, _, user string) (string, error) {
	client.user = user
	return client.response, client.err
}

func testCart(includeEvidence bool, vegan bool, unitPrice int64, color string) cartEvidenceFile {
	evidence := []verification.Evidence{}
	if includeEvidence {
		evidence = append(evidence, verification.Evidence{ID: "category-proof", Attribute: "product_category", Scope: verification.ItemScope, SubjectID: "item-1", ProductID: "product-1", VariantID: "variant-1", Value: verification.StringValue("  bAg  "), SourceID: "synthetic-catalogue", ObservedAt: "2026-10-28T09:00:00Z"})
		evidence = append(evidence, verification.Evidence{
			ID: "vegan-proof", Attribute: "vegan", Scope: verification.ItemScope, SubjectID: "item-1",
			ProductID: "product-1", VariantID: "variant-1", Value: verification.BooleanValue(vegan),
			SourceID: "synthetic-catalogue", ObservedAt: "2026-10-28T09:00:00Z",
		})
		if color != "" {
			evidence = append(evidence, verification.Evidence{
				ID: "color-proof", Attribute: "color", Scope: verification.ItemScope, SubjectID: "item-1",
				ProductID: "product-1", VariantID: "variant-1", Value: verification.StringValue(color),
				SourceID: "synthetic-catalogue", ObservedAt: "2026-10-28T09:00:00Z",
			})
		}
	}
	return cartEvidenceFile{
		CartSnapshot: verification.CartSnapshot{
			CartID: "cart-1", Currency: "INR", CapturedAt: "2026-10-28T09:00:00Z",
			Items:         []verification.CartItem{{ItemID: "item-1", ProductID: "product-1", VariantID: "variant-1", Quantity: 1, UnitPricePaise: unitPrice}},
			ShippingPaise: 25000, DiscountPaise: 0,
		},
		Evidence: &evidence,
	}
}

func multiItemCart(evidence []verification.Evidence, bindings []itemRequirementBinding) cartEvidenceFile {
	return cartEvidenceFile{
		CartSnapshot: verification.CartSnapshot{
			CartID: "multi-cart", Currency: "INR", CapturedAt: "2026-10-28T09:00:00Z",
			Items: []verification.CartItem{
				{ItemID: "item-a", ProductID: "product-bag", VariantID: "variant-red", Quantity: 1, UnitPricePaise: 220000},
				{ItemID: "item-b", ProductID: "product-bag", VariantID: "variant-black", Quantity: 1, UnitPricePaise: 260000},
			},
			ShippingPaise: 25000,
		},
		Evidence: &evidence, ItemBindings: bindings,
	}
}

func itemEvidence(id, attribute, itemID, productID, variantID string, value verification.Value) verification.Evidence {
	return verification.Evidence{
		ID: id, Attribute: attribute, Scope: verification.ItemScope, SubjectID: itemID,
		ProductID: productID, VariantID: variantID, Value: value,
		SourceID: "synthetic-catalogue", ObservedAt: "2026-10-28T09:00:00Z",
	}
}

func testInterpreter(response string) intent.Interpreter {
	return intent.Interpreter{Client: &fixedIntentClient{response: response}}
}

func veganAndPriceResponse() string {
	return `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":" BAG "},"severity":"hard","source_text":"bag"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
}

func veganBackpackAndPriceResponse() string {
	return `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"backpack"},"severity":"hard","source_text":"backpack"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
}

func veganAndPriceWithoutCategoryResponse() string {
	return `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
}

func priceOnlyResponse() string {
	return `{"requirements":[{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
}

func wrongVeganAndPriceResponse() string {
	return `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":false},"severity":"hard","source_text":"vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
}

func TestCheckCommandAllowsVerifiedVeganBagAndUsesReferenceTimestamp(t *testing.T) {
	client := &fixedIntentClient{response: veganAndPriceResponse()}
	cartPath := filepath.Join(t.TempDir(), "cart.json")
	data, err := os.ReadFile(filepath.Join("..", "..", "examples", "demo-cart.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cartPath, data, 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"check", "--cart", cartPath, "--reference-time", demoReferenceTime, "Find a vegan bag under ₹5,000"}
	if code := checkCommand(args[1:], &stdout, &stderr, client); code != 0 {
		t.Fatalf("check command code=%d stderr=%s", code, stderr.String())
	}
	var got PurchaseCheckResult
	if err := json.Unmarshal(stdout.Bytes(), &got); err != nil {
		t.Fatalf("decode check output: %v\n%s", err, stdout.String())
	}
	if got.Decision != verification.Allow || len(got.Requirements) != 3 || got.CartSnapshotSHA256 == "" || got.ReferenceTime != demoReferenceTime || len(got.Intent.Requirements) != 3 || got.Intent.Requirements[0].Expected.Value != "bag" {
		t.Fatalf("check result=%#v", got)
	}
	if got.Intent.Requirements[0].Attribute != "product_category" || got.Intent.Requirements[0].Severity != verification.Hard || got.Intent.Requirements[0].Expected.Type != verification.StringType || got.Intent.Requirements[1].Attribute != "vegan" || got.Intent.Requirements[1].Severity != verification.Hard || got.Intent.Requirements[1].Expected.Type != verification.BooleanType || got.Intent.Requirements[2].Attribute != "total_price_paise" || got.Intent.Requirements[2].Severity != verification.Hard || got.Intent.Requirements[2].Expected.Type != verification.NumberType || fmt.Sprint(got.Intent.Requirements[2].Expected.Value) != "500000" {
		t.Fatalf("request did not produce distinct typed category, vegan, and price requirements: %#v", got.Intent.Requirements)
	}
	if got.Requirements[0].Result.Outcome != verification.Pass || got.Requirements[1].Result.Outcome != verification.Pass || got.Requirements[2].Result.Outcome != verification.Pass {
		t.Fatalf("requirement outcomes=%#v", got.Requirements)
	}
	if !strings.Contains(client.user, "Reference timestamp: "+demoReferenceTime) {
		t.Fatalf("interpreter did not receive explicit reference time: %q", client.user)
	}
}

func TestDemoCartFixtureDecodesAndVerifiesOffline(t *testing.T) {
	file, err := os.Open(filepath.Join("..", "..", "examples", "demo-cart.json"))
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cart, err := decodeCartEvidence(file)
	if err != nil {
		t.Fatal(err)
	}
	report, err := verification.Evaluate(verification.Input{CartSnapshot: cart.CartSnapshot, Evidence: *cart.Evidence})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != verification.Review || report.CartSnapshotSHA256 == "" || len(report.Evidence) != 4 || report.Evidence[2].Value.Value != json.Number("475000") {
		t.Fatalf("demo cart preflight=%#v", report)
	}
}

func TestPurchaseCheckBlocksDemonstratedHardViolations(t *testing.T) {
	request := "Find a vegan bag under ₹5,000"
	for _, test := range []struct {
		name     string
		cart     cartEvidenceFile
		wantFail string
	}{
		{name: "not vegan", cart: testCart(true, false, 450000, ""), wantFail: "vegan"},
		{name: "over budget", cart: testCart(true, true, 510000, ""), wantFail: "total_price_paise"},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := evaluatePurchase(context.Background(), testInterpreter(veganAndPriceResponse()), request, demoReferenceTime, test.cart)
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != verification.Block {
				t.Fatalf("decision=%s, want BLOCK", result.Decision)
			}
			found := false
			for _, requirement := range result.Requirements {
				if requirement.Result.Attribute == test.wantFail && requirement.Result.Outcome == verification.Fail && requirement.Result.Severity == verification.Hard {
					found = true
				}
			}
			if !found {
				t.Fatalf("missing demonstrated hard failure for %s: %#v", test.wantFail, result.Requirements)
			}
		})
	}
}

func TestPurchaseCheckReviewsOmittedExplicitSupportedHardConstraints(t *testing.T) {
	tests := []struct {
		name, request, omittedAttribute string
	}{
		{"vegan", "Find a vegan backpack under ₹5,000", "vegan"},
		{"water resistance", "Find a water-resistant laptop bag under ₹5,000", "water_resistant"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := evaluatePurchase(context.Background(), testInterpreter(priceOnlyResponse()), test.request, demoReferenceTime, testCart(true, false, 450000, ""))
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != verification.Review || result.Intent != nil || !strings.Contains(strings.Join(result.ReviewReasons, " "), "omitted") || !strings.Contains(strings.Join(result.ReviewReasons, " "), test.omittedAttribute) {
				t.Fatalf("omitted %s produced unsafe result %#v", test.omittedAttribute, result)
			}
		})
	}
}

func TestConstraintCoverageDoesNotSuppressVeganForUnrelatedDescription(t *testing.T) {
	result, err := evaluatePurchase(context.Background(), testInterpreter(priceOnlyResponse()), "Find a vegan backpack with a detailed description under ₹5,000", demoReferenceTime, testCart(true, false, 450000, ""))
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != verification.Review || result.Intent != nil || !strings.Contains(strings.Join(result.ReviewReasons, " "), "attribute \"vegan\" was omitted") {
		t.Fatalf("unrelated description suppressed vegan coverage: %#v", result)
	}
}

func TestWrongPolarityCannotBypassCoverageThroughIncidentalText(t *testing.T) {
	result, err := evaluatePurchase(context.Background(), testInterpreter(wrongVeganAndPriceResponse()), "Find a vegan backpack with a detailed description under ₹5,000", demoReferenceTime, testCart(true, false, 450000, ""))
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != verification.Review || result.Intent != nil || !strings.Contains(strings.Join(result.ReviewReasons, " "), "attribute \"vegan\" was omitted") {
		t.Fatalf("wrong vegan polarity reached the verifier: %#v", result)
	}
}

func TestConstraintCoverageDoesNotTreatPreferenceAsHardBecausePriceIsRequired(t *testing.T) {
	response := `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"preference","source_text":"Prefer a vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"price is required to be under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	result, err := evaluatePurchase(context.Background(), testInterpreter(response), "Prefer a vegan bag but the price is required to be under ₹5,000", demoReferenceTime, testCart(true, false, 450000, ""))
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != verification.Allow || result.Intent == nil || len(result.Requirements) != 3 || result.Requirements[1].Result.Attribute != "vegan" || result.Requirements[1].Result.Severity != verification.Preference || result.Requirements[1].Result.Outcome != verification.Fail || result.Requirements[2].Result.Attribute != "total_price_paise" || result.Requirements[2].Result.Outcome != verification.Pass {
		t.Fatalf("unrelated hard price cue changed vegan preference semantics: %#v", result)
	}
}

func TestPurchaseCheckCompletenessGuardPreservesExplicitReviewRoutes(t *testing.T) {
	for _, response := range []string{
		`{"requirements":[],"clarification_questions":[{"question":"Should vegan be hard or a preference?","source_text":"Find a vegan backpack"}],"unsupported_constraints":[]}`,
		`{"requirements":[],"clarification_questions":[],"unsupported_constraints":[{"source_text":"Find a vegan backpack","reason":"This fixture treats the request as unsupported."}]}`,
	} {
		result, err := evaluatePurchase(context.Background(), testInterpreter(response), "Find a vegan backpack", demoReferenceTime, testCart(true, false, 450000, ""))
		if err != nil {
			t.Fatal(err)
		}
		if result.Decision != verification.Review || result.Intent == nil || len(result.ReviewReasons) == 0 {
			t.Fatalf("explicit review route was allowed: %#v", result)
		}
	}
}

func TestPurchaseCheckHardFailureDecisionPrecedence(t *testing.T) {
	tests := []struct {
		name, request, response string
		cart                    cartEvidenceFile
	}{
		{
			name: "budget failure with missing vegan evidence", request: "Find a vegan bag under ₹5,000", response: veganAndPriceResponse(),
			cart: testCart(false, true, 510000, ""),
		},
		{
			name: "budget failure with unrelated contradictory weights", request: "Keep the total under ₹5,000. Weight must be under 500 grams and over 700 grams.", response: `{"requirements":[{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"total under ₹5,000"},{"attribute":"weight_grams","operator":"less_than","expected":{"type":"number","value":500},"severity":"hard","source_text":"Weight must be under 500 grams"},{"attribute":"weight_grams","operator":"greater_than","expected":{"type":"number","value":700},"severity":"hard","source_text":"over 700 grams"}],"clarification_questions":[],"unsupported_constraints":[]}`,
			cart: testCart(false, true, 510000, ""),
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := evaluatePurchase(context.Background(), testInterpreter(test.response), test.request, demoReferenceTime, test.cart)
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != verification.Block {
				t.Fatalf("confirmed independent hard price failure was masked: %#v", result)
			}
			for _, requirement := range result.Requirements {
				if requirement.Result.Attribute == "total_price_paise" && requirement.Result.Outcome == verification.Fail && requirement.Result.Severity == verification.Hard {
					return
				}
			}
			t.Fatalf("missing confirmed price failure: %#v", result.Requirements)
		})
	}
}

func TestPurchaseCheckConflictedHardFailuresRemainReviewWithoutIndependentFailure(t *testing.T) {
	response := `{"requirements":[{"attribute":"weight_grams","operator":"less_than","expected":{"type":"number","value":500},"severity":"hard","source_text":"Weight must be under 500 grams"},{"attribute":"weight_grams","operator":"greater_than","expected":{"type":"number","value":700},"severity":"hard","source_text":"over 700 grams"}],"clarification_questions":[],"unsupported_constraints":[]}`
	cart := testCart(false, true, 450000, "")
	evidence := append(*cart.Evidence, verification.Evidence{
		ID: "weight-proof", Attribute: "weight_grams", Scope: verification.ItemScope, SubjectID: "item-1",
		ProductID: "product-1", VariantID: "variant-1", Value: verification.NumberInt64Value(600),
		SourceID: "synthetic-catalogue", ObservedAt: "2026-10-28T09:00:00Z",
	})
	cart.Evidence = &evidence
	result, err := evaluatePurchase(context.Background(), testInterpreter(response), "Weight must be under 500 grams and over 700 grams.", demoReferenceTime, cart)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != verification.Review || len(result.Requirements) != 2 || result.Requirements[0].Result.Outcome != verification.Fail || result.Requirements[1].Result.Outcome != verification.Fail || !strings.Contains(strings.Join(result.ReviewReasons, " "), "conflict") {
		t.Fatalf("conflicted failures fabricated a block: %#v", result)
	}
}

func TestPurchaseCheckDecisionPrecedencePreservesReviewAndAllow(t *testing.T) {
	tests := []struct {
		name, request, response string
		cart                    cartEvidenceFile
		want                    verification.Decision
	}{
		{
			name: "passing price with unknown hard vegan", request: "Find a vegan bag under ₹5,000", response: veganAndPriceResponse(),
			cart: testCart(false, true, 450000, ""), want: verification.Review,
		},
		{
			name: "contradictory booleans without independent failure", request: "vegan and not vegan", response: `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":false},"severity":"hard","source_text":"not vegan"}],"clarification_questions":[],"unsupported_constraints":[]}`,
			cart: testCart(true, true, 450000, ""), want: verification.Review,
		},
		{
			name: "all hard requirements pass", request: "Find a vegan bag under ₹5,000", response: veganAndPriceResponse(),
			cart: testCart(true, true, 450000, ""), want: verification.Allow,
		},
		{
			name: "preference failure is non-blocking", request: "Prefer red, but total must be under ₹5,000", response: `{"requirements":[{"attribute":"color","operator":"equals","expected":{"type":"string","value":"red"},"severity":"preference","source_text":"Prefer red"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"total must be under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`,
			cart: testCart(true, true, 450000, "blue"), want: verification.Allow,
		},
		{
			name: "ambiguous intent remains review", request: "Maybe red", response: `{"requirements":[],"clarification_questions":[{"question":"Is red a hard requirement or a preference?","source_text":"Maybe red"}],"unsupported_constraints":[]}`,
			cart: testCart(true, true, 450000, "blue"), want: verification.Review,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := evaluatePurchase(context.Background(), testInterpreter(test.response), test.request, demoReferenceTime, test.cart)
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != test.want {
				t.Fatalf("decision=%s want %s: %#v", result.Decision, test.want, result)
			}
		})
	}
}

func TestPurchaseCheckCompletenessGuardPreservesKnownIntentSemantics(t *testing.T) {
	tests := []struct {
		name, request, response string
		cart                    cartEvidenceFile
		wantDecision            verification.Decision
		wantRequirements        int
		wantVeganSeverity       verification.Severity
		wantVeganOutcome        verification.Outcome
	}{
		{
			name: "all extracted hard constraints allow", request: "Find a vegan bag under ₹5,000", response: veganAndPriceResponse(),
			cart: testCart(true, true, 450000, ""), wantDecision: verification.Allow, wantRequirements: 3,
			wantVeganSeverity: verification.Hard, wantVeganOutcome: verification.Pass,
		},
		{
			name: "verified vegan failure blocks", request: "Find a vegan bag under ₹5,000", response: veganAndPriceResponse(),
			cart: testCart(true, false, 450000, ""), wantDecision: verification.Block, wantRequirements: 3,
			wantVeganSeverity: verification.Hard, wantVeganOutcome: verification.Fail,
		},
		{
			name: "explicit preference remains a preference", request: "Prefer a vegan backpack under ₹5,000", response: `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"preference","source_text":"Prefer a vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`,
			cart: testCart(true, false, 450000, ""), wantDecision: verification.Allow, wantRequirements: 2,
			wantVeganSeverity: verification.Preference, wantVeganOutcome: verification.Fail,
		},
		{
			name: "not necessarily vegan does not invent a hard constraint", request: "Not necessarily vegan, but under ₹5,000", response: priceOnlyResponse(),
			cart: testCart(true, false, 450000, ""), wantDecision: verification.Allow, wantRequirements: 1,
		},
		{
			name: "backpack category is preserved", request: "Find a backpack under ₹5,000", response: `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"backpack"},"severity":"hard","source_text":"backpack"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`,
			cart: testCart(true, false, 450000, ""), wantDecision: verification.Block, wantRequirements: 2,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := evaluatePurchase(context.Background(), testInterpreter(test.response), test.request, demoReferenceTime, test.cart)
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != test.wantDecision || result.Intent == nil || len(result.Intent.Requirements) != test.wantRequirements || len(result.Requirements) != test.wantRequirements {
				t.Fatalf("decision or normalized requirements changed: %#v", result)
			}
			if test.wantVeganSeverity == "" {
				return
			}
			for _, requirement := range result.Requirements {
				if requirement.Result.Attribute == "vegan" {
					if requirement.Result.Severity != test.wantVeganSeverity || requirement.Result.Outcome != test.wantVeganOutcome {
						t.Fatalf("vegan requirement=%#v", requirement)
					}
					return
				}
			}
			t.Fatalf("missing normalized vegan requirement: %#v", result.Intent.Requirements)
		})
	}
}

func TestPurchaseCheckCategoryMismatchBlocks(t *testing.T) {
	cart := testCart(true, true, 450000, "")
	(*cart.Evidence)[0].Value = verification.StringValue("suitcase")
	result, err := evaluatePurchase(context.Background(), testInterpreter(veganAndPriceResponse()), "Find a vegan bag under ₹5,000", demoReferenceTime, cart)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != verification.Block || result.Requirements[0].Result.Outcome != verification.Fail {
		t.Fatalf("known category mismatch did not block: %#v", result)
	}
}

func TestPurchaseCheckMissingCategoryEvidenceReviews(t *testing.T) {
	cart := testCart(true, true, 450000, "")
	evidence := make([]verification.Evidence, 0, len(*cart.Evidence)-1)
	for _, item := range *cart.Evidence {
		if item.Attribute != "product_category" {
			evidence = append(evidence, item)
		}
	}
	cart.Evidence = &evidence
	result, err := evaluatePurchase(context.Background(), testInterpreter(veganAndPriceResponse()), "Find a vegan bag under ₹5,000", demoReferenceTime, cart)
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != verification.Review || result.Requirements[0].Result.Outcome != verification.Unknown || result.Requirements[1].Result.Outcome != verification.Pass || result.Requirements[2].Result.Outcome != verification.Pass {
		t.Fatalf("missing category evidence did not lead to review: %#v", result)
	}
}

func TestPurchaseCheckUsesResolvedDeliverySemantics(t *testing.T) {
	tests := []struct {
		name, request, source, modelOperator, modelDate, reference, cartDate string
		wantOperator                                                         verification.Operator
		wantOutcome                                                          verification.Outcome
		wantDecision                                                         verification.Decision
	}{
		{"intent-022", "Delivery must be before Friday.", "must be before Friday", "less_than", "2026-10-30", "2026-10-28T10:00:00Z", "2026-10-30", verification.Before, verification.Fail, verification.Block},
		{"intent-029", "Arrive after Friday.", "after Friday", "greater_than", "2026-10-30", "2026-10-28T10:00:00Z", "2026-10-31", verification.After, verification.Pass, verification.Allow},
		{"intent-024", "It must arrive this Friday.", "must arrive this Friday", "equals", "2026-10-30", "2026-10-28T10:00:00Z", "2026-10-30", verification.BeforeOrEqual, verification.Pass, verification.Allow},
		{"intent-026", "Must arrive tomorrow.", "Must arrive tomorrow", "equals", "2027-01-01", "2026-12-31T23:59:00-05:00", "2027-01-01", verification.BeforeOrEqual, verification.Pass, verification.Allow},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := `{"requirements":[{"attribute":"delivery_date","operator":"` + test.modelOperator + `","expected":{"type":"date","value":"` + test.modelDate + `"},"severity":"hard","source_text":"` + test.source + `"}],"clarification_questions":[],"unsupported_constraints":[]}`
			cart := testCart(true, true, 450000, "")
			cart.CartSnapshot.DeliveryDate = &test.cartDate
			result, err := evaluatePurchase(context.Background(), testInterpreter(response), test.request, test.reference, cart)
			if err != nil || len(result.Requirements) != 1 || result.Requirements[0].Result.Operator != test.wantOperator || result.Requirements[0].Result.Outcome != test.wantOutcome || result.Decision != test.wantDecision {
				t.Fatalf("check=%#v err=%v", result, err)
			}
		})
	}
}

func TestPurchaseCheckReviewsClarificationUnsupportedAndInvalidIntent(t *testing.T) {
	tests := []struct {
		name, request, output, wantReason string
	}{
		{"clarification", "Maybe red", `{"requirements":[],"clarification_questions":[{"question":"Is red a hard requirement or preference?","source_text":"Maybe red"}],"unsupported_constraints":[]}`, "clarification required"},
		{"explicit unsupported constraint", "lifetime repairs", `{"requirements":[],"clarification_questions":[],"unsupported_constraints":[{"source_text":"lifetime repairs","reason":"Service policy is unsupported."}]}`, "unsupported requirement"},
		{"unsupported attribute", "brand X", `{"requirements":[{"attribute":"brand","operator":"equals","expected":{"type":"string","value":"X"},"severity":"hard","source_text":"brand X"}],"clarification_questions":[],"unsupported_constraints":[]}`, "intent interpretation failed"},
		{"ambiguous delivery operator", "arrive around Friday", `{"requirements":[{"attribute":"delivery_date","operator":"less_than","expected":{"type":"date","value":"2026-10-30"},"severity":"hard","source_text":"around Friday"}],"clarification_questions":[],"unsupported_constraints":[]}`, "request clarification"},
		{"malformed model output", "vegan bag", `{`, "intent interpretation failed"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := evaluatePurchase(context.Background(), testInterpreter(test.output), test.request, demoReferenceTime, testCart(true, true, 450000, ""))
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != verification.Review || result.CartSnapshotSHA256 == "" || len(result.ReviewReasons) == 0 || !strings.Contains(strings.Join(result.ReviewReasons, " "), test.wantReason) {
				t.Fatalf("review result=%#v", result)
			}
		})
	}
}

func TestPurchaseCheckReviewsMissingEvidenceAndConflictingHardRequirements(t *testing.T) {
	missing, err := evaluatePurchase(context.Background(), testInterpreter(veganAndPriceResponse()), "Find a vegan bag under ₹5,000", demoReferenceTime, testCart(false, true, 450000, ""))
	if err != nil {
		t.Fatal(err)
	}
	if missing.Decision != verification.Review || len(missing.Requirements) != 3 || missing.Requirements[0].Result.Outcome != verification.Unknown || missing.Requirements[1].Result.Outcome != verification.Unknown || missing.Requirements[2].Result.Outcome != verification.Pass {
		t.Fatalf("missing evidence result=%#v", missing)
	}

	conflictRequest := "vegan and not vegan"
	conflictOutput := `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":false},"severity":"hard","source_text":"not vegan"}],"clarification_questions":[],"unsupported_constraints":[]}`
	conflict, err := evaluatePurchase(context.Background(), testInterpreter(conflictOutput), conflictRequest, demoReferenceTime, testCart(true, true, 450000, ""))
	if err != nil {
		t.Fatal(err)
	}
	if conflict.Decision != verification.Review || len(conflict.Requirements) != 0 || conflict.Intent == nil || len(conflict.Intent.ClarificationQuestions) != 1 {
		t.Fatalf("conflicting requirements were not routed to review: %#v", conflict)
	}
}

func TestPurchaseCheckPreferenceMismatchDoesNotBlock(t *testing.T) {
	response := `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"color","operator":"equals","expected":{"type":"string","value":"red"},"severity":"preference","source_text":"ideally red"}],"clarification_questions":[],"unsupported_constraints":[]}`
	result, err := evaluatePurchase(context.Background(), testInterpreter(response), "Find a vegan bag, ideally red", demoReferenceTime, testCart(true, true, 450000, "blue"))
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != verification.Allow || len(result.Requirements) != 3 || result.Requirements[0].Result.Outcome != verification.Pass || result.Requirements[1].Result.Outcome != verification.Pass || result.Requirements[2].Result.Outcome != verification.Fail || result.Requirements[2].Result.Severity != verification.Preference || !strings.Contains(result.Requirements[2].Reason, "not a hard violation") {
		t.Fatalf("preference mismatch affected hard decision: %#v", result)
	}
}

func TestPurchaseCheckMultiItemBindingPolicy(t *testing.T) {
	veganResponse := `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"}],"clarification_questions":[],"unsupported_constraints":[]}`
	bothResponse := `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"water_resistant","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"water-resistant"}],"clarification_questions":[],"unsupported_constraints":[]}`
	priceResponse := `{"requirements":[{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"total under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`

	for _, test := range []struct {
		name, request, response string
		cart                    cartEvidenceFile
		want                    verification.Decision
		wantSubject             string
	}{
		{
			name: "explicit target uses matching item evidence", request: "Find a vegan bag", response: veganResponse,
			cart: multiItemCart([]verification.Evidence{
				itemEvidence("category-a", "product_category", "item-a", "product-bag", "variant-red", verification.StringValue("bag")),
				itemEvidence("vegan-a", "vegan", "item-a", "product-bag", "variant-red", verification.BooleanValue(true)),
				itemEvidence("vegan-b", "vegan", "item-b", "product-bag", "variant-black", verification.BooleanValue(false)),
			}, []itemRequirementBinding{{Attribute: "product_category", ItemID: "item-a"}, {Attribute: "vegan", ItemID: "item-a"}}), want: verification.Allow, wantSubject: "item-a",
		},
		{
			name: "cart price includes every item", request: "Keep the total under ₹5,000", response: priceResponse,
			cart: multiItemCart(nil, nil), want: verification.Block, wantSubject: "multi-cart",
		},
		{
			name: "evidence on wrong item is unknown", request: "Find a vegan bag", response: veganResponse,
			cart: multiItemCart([]verification.Evidence{
				itemEvidence("category-a", "product_category", "item-a", "product-bag", "variant-red", verification.StringValue("bag")),
				itemEvidence("vegan-b", "vegan", "item-b", "product-bag", "variant-black", verification.BooleanValue(true)),
			}, []itemRequirementBinding{{Attribute: "product_category", ItemID: "item-a"}, {Attribute: "vegan", ItemID: "item-a"}}), want: verification.Review, wantSubject: "item-a",
		},
		{
			name: "distinct variants support different product requirements", request: "Find a vegan bag and a water-resistant bag", response: bothResponse,
			cart: multiItemCart([]verification.Evidence{
				itemEvidence("category-a", "product_category", "item-a", "product-bag", "variant-red", verification.StringValue("bag")),
				itemEvidence("vegan-a", "vegan", "item-a", "product-bag", "variant-red", verification.BooleanValue(true)),
				itemEvidence("water-b", "water_resistant", "item-b", "product-bag", "variant-black", verification.BooleanValue(true)),
			}, []itemRequirementBinding{{Attribute: "product_category", ItemID: "item-a"}, {Attribute: "vegan", ItemID: "item-a"}, {Attribute: "water_resistant", ItemID: "item-b"}}), want: verification.Allow,
		},
		{
			name: "unqualified multi item requirement reviews", request: "Find a vegan bag", response: veganResponse,
			cart: multiItemCart([]verification.Evidence{
				itemEvidence("category-a", "product_category", "item-a", "product-bag", "variant-red", verification.StringValue("bag")),
				itemEvidence("vegan-a", "vegan", "item-a", "product-bag", "variant-red", verification.BooleanValue(true)),
			}, nil), want: verification.Review,
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			result, err := evaluatePurchase(context.Background(), testInterpreter(test.response), test.request, demoReferenceTime, test.cart)
			if err != nil {
				t.Fatal(err)
			}
			if result.Decision != test.want {
				t.Fatalf("decision=%s want %s: %#v", result.Decision, test.want, result)
			}
			if test.wantSubject != "" && len(result.Requirements) > 0 && result.Requirements[0].Result.SubjectID != test.wantSubject {
				t.Fatalf("requirement bound to %q, want %q", result.Requirements[0].Result.SubjectID, test.wantSubject)
			}
			if test.want == verification.Review && test.name == "unqualified multi item requirement reviews" && !strings.Contains(strings.Join(result.ReviewReasons, " "), "item_bindings") {
				t.Fatalf("ambiguous multi-item request lacked actionable review reason: %#v", result)
			}
		})
	}
}

func TestPurchaseCheckSingleItemBindingRegression(t *testing.T) {
	result, err := evaluatePurchase(context.Background(), testInterpreter(veganAndPriceResponse()), "Find a vegan bag under ₹5,000", demoReferenceTime, testCart(true, true, 450000, ""))
	if err != nil {
		t.Fatal(err)
	}
	if result.Decision != verification.Allow || len(result.Requirements) != 3 || result.Requirements[0].Result.SubjectID != "item-1" {
		t.Fatalf("single-item mapping regressed: %#v", result)
	}
}
