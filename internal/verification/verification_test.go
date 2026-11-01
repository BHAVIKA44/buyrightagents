package verification

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func TestStringAndBooleanRequirements(t *testing.T) {
	tests := []struct {
		name     string
		required Value
		observed Value
		operator Operator
		want     Outcome
	}{
		{"string equality passes", StringValue("vegan"), StringValue("vegan"), Equals, Pass},
		{"string equality fails", StringValue("vegan"), StringValue("leather"), Equals, Fail},
		{"string inequality passes", StringValue("red"), StringValue("blue"), NotEquals, Pass},
		{"boolean true passes", BooleanValue(true), BooleanValue(true), Equals, Pass},
		{"boolean false fails", BooleanValue(true), BooleanValue(false), Equals, Fail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := evaluateOne(t, tt.required, tt.observed, tt.operator)
			if got := report.Results[0].Outcome; got != tt.want {
				t.Fatalf("outcome = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNumericBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name  string
		price float64
		want  Outcome
	}{
		{"exactly at limit", 500000, Pass},
		{"below limit", 499999, Pass},
		{"over limit", 500001, Fail},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report := evaluateOne(t, NumberValue(500000), NumberValue(tt.price), LessThanOrEqual)
			if got := report.Results[0].Outcome; got != tt.want {
				t.Fatalf("outcome = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestStrictNumericBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name     string
		operator Operator
		actual   int64
		want     Outcome
	}{
		{"less than below threshold", LessThan, 9, Pass},
		{"less than equality fails", LessThan, 10, Fail},
		{"greater than above threshold", GreaterThan, 11, Pass},
		{"greater than equality fails", GreaterThan, 10, Fail},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report := evaluateOne(t, NumberInt64Value(10), NumberInt64Value(tt.actual), tt.operator)
			if got := report.Results[0].Outcome; got != tt.want {
				t.Fatalf("outcome = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNumberComparisonPreservesJSONPrecision(t *testing.T) {
	report := evaluateOne(t,
		Value{Type: NumberType, Value: json.Number("9007199254740992")},
		Value{Type: NumberType, Value: json.Number("9007199254740993")},
		Equals,
	)
	if got := report.Results[0].Outcome; got != Fail {
		t.Fatalf("outcome = %s, want %s", got, Fail)
	}
	if got := compareNumbers(json.Number("1e3"), json.Number("1000.0")); got != 0 {
		t.Fatalf("equivalent exponent numbers compare as %d, want 0", got)
	}
}

func TestDateComparisons(t *testing.T) {
	for _, tt := range []struct {
		name     string
		operator Operator
		actual   string
		want     Outcome
	}{
		{"due date is inclusive", BeforeOrEqual, "2026-10-30", Pass},
		{"earlier date passes", BeforeOrEqual, "2026-10-29", Pass},
		{"later date fails", BeforeOrEqual, "2026-10-31", Fail},
		{"after or equal passes", AfterOrEqual, "2026-10-31", Pass},
		{"strict before passes earlier", Before, "2026-10-29", Pass},
		{"strict before rejects equality", Before, "2026-10-30", Fail},
		{"strict after passes later", After, "2026-10-31", Pass},
		{"strict after rejects equality", After, "2026-10-30", Fail},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report := evaluateOne(t, DateValue("2026-10-30"), DateValue(tt.actual), tt.operator)
			if got := report.Results[0].Outcome; got != tt.want {
				t.Fatalf("outcome = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestMissingAndContradictoryEvidence(t *testing.T) {
	requirement := req("r1", Hard, "vegan", Equals, BooleanValue(true))
	report, err := Evaluate(Input{CartSnapshot: testSnapshot(), Requirements: []Requirement{requirement}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Outcome != Unknown || report.Decision != Review {
		t.Fatalf("missing evidence report = %#v", report)
	}

	input := Input{
		CartSnapshot: testSnapshot(),
		Requirements: []Requirement{requirement},
		Evidence: []Evidence{
			evidence("e1", "vegan", BooleanValue(true)),
			evidence("e2", "vegan", BooleanValue(false)),
		},
	}
	report, err = Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Outcome != Unknown || report.Decision != Review {
		t.Fatalf("contradictory evidence report = %#v", report)
	}
	if got := len(report.Results[0].Assessments); got != 2 {
		t.Fatalf("assessment count = %d, want 2", got)
	}
}

func TestOverallDecisionPolicy(t *testing.T) {
	tests := []struct {
		name         string
		requirements []Requirement
		evidence     []Evidence
		want         Decision
	}{
		{
			name:         "hard failure blocks",
			requirements: []Requirement{req("hard", Hard, "vegan", Equals, BooleanValue(true))},
			evidence:     []Evidence{evidence("e1", "vegan", BooleanValue(false))},
			want:         Block,
		},
		{
			name:         "unknown hard requirement requires review",
			requirements: []Requirement{req("hard", Hard, "vegan", Equals, BooleanValue(true))},
			want:         Review,
		},
		{
			name: "failed preference does not block",
			requirements: []Requirement{
				req("hard", Hard, "vegan", Equals, BooleanValue(true)),
				req("soft", Preference, "color", Equals, StringValue("red")),
			},
			evidence: []Evidence{
				evidence("e1", "vegan", BooleanValue(true)),
				evidence("e2", "color", StringValue("blue")),
			},
			want: Allow,
		},
		{name: "empty requirements require review", want: Review},
		{
			name:         "preferences without hard requirements cannot allow",
			requirements: []Requirement{req("soft", Preference, "color", Equals, StringValue("red"))},
			evidence:     []Evidence{evidence("e1", "color", StringValue("red"))},
			want:         Review,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := Evaluate(Input{CartSnapshot: testSnapshot(), Requirements: tt.requirements, Evidence: tt.evidence})
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != tt.want {
				t.Fatalf("decision = %s, want %s", report.Decision, tt.want)
			}
		})
	}
}

func TestAssessmentPreservesEvidenceProvenance(t *testing.T) {
	item := evidence("evidence-1", "vegan", BooleanValue(true))
	report, err := Evaluate(Input{
		CartSnapshot: testSnapshot(),
		Requirements: []Requirement{req("r1", Hard, "vegan", Equals, BooleanValue(true))},
		Evidence:     []Evidence{item},
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment := report.Results[0].Assessments[0]
	result := report.Results[0]
	if result.Operator != Equals || result.Expected.Value != true {
		t.Fatalf("requirement comparator missing from report: %#v", result)
	}
	if assessment.EvidenceID != item.ID || assessment.Value.Value != true || assessment.SourceID != item.SourceID || assessment.ObservedAt != item.ObservedAt || assessment.Outcome != Pass {
		t.Fatalf("assessment lost provenance: %#v", assessment)
	}
}

func TestCartSnapshotHashIsStableAndNormalizesItemOrder(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Items = append(snapshot.Items, CartItem{
		ItemID: "item-2", ProductID: "product-2", VariantID: "variant-2", Quantity: 2, UnitPricePaise: 100,
	})
	reordered := snapshot
	reordered.Items = []CartItem{snapshot.Items[1], snapshot.Items[0]}

	first := snapshotHashForTest(t, snapshot)
	second := snapshotHashForTest(t, snapshot)
	third := snapshotHashForTest(t, reordered)
	if first != second || first != third {
		t.Fatalf("hashes differ for identical or reordered snapshots: %s %s %s", first, second, third)
	}
	if len(first) != 64 {
		t.Fatalf("SHA-256 hex length = %d, want 64", len(first))
	}
}

func TestCartSnapshotHashChangesWithPurchaseFields(t *testing.T) {
	base := testSnapshot()
	baseHash := snapshotHashForTest(t, base)
	tests := []struct {
		name   string
		mutate func(*CartSnapshot)
	}{
		{"cart identity", func(s *CartSnapshot) { s.CartID = "cart-2" }},
		{"capture timestamp", func(s *CartSnapshot) { s.CapturedAt = "2026-10-28T10:01:00Z" }},
		{"item identity", func(s *CartSnapshot) { s.Items[0].ItemID = "item-2" }},
		{"product", func(s *CartSnapshot) { s.Items[0].ProductID = "product-2" }},
		{"variant", func(s *CartSnapshot) { s.Items[0].VariantID = "variant-2" }},
		{"quantity", func(s *CartSnapshot) { s.Items[0].Quantity = 2 }},
		{"unit price", func(s *CartSnapshot) { s.Items[0].UnitPricePaise++ }},
		{"shipping", func(s *CartSnapshot) { s.ShippingPaise++ }},
		{"discount", func(s *CartSnapshot) { s.DiscountPaise = 1 }},
		{"delivery promise", func(s *CartSnapshot) { s.DeliveryDate = datePointer("2026-10-30") }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			changed := cloneSnapshot(base)
			tt.mutate(&changed)
			if got := snapshotHashForTest(t, changed); got == baseHash {
				t.Fatalf("hash did not change: %s", got)
			}
		})
	}
	unsupportedCurrency := cloneSnapshot(base)
	unsupportedCurrency.Currency = "USD"
	baseCanonicalHash, err := hashSnapshot(base)
	if err != nil {
		t.Fatal(err)
	}
	unsupportedCurrencyHash, err := hashSnapshot(unsupportedCurrency)
	if err != nil {
		t.Fatal(err)
	}
	if baseCanonicalHash == unsupportedCurrencyHash {
		t.Fatal("hash did not include currency")
	}
	if _, err := Evaluate(Input{CartSnapshot: unsupportedCurrency}); err == nil || !strings.Contains(err.Error(), "only INR") {
		t.Fatalf("unsupported currency error = %v", err)
	}
}

func TestCartSnapshotValidation(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(*CartSnapshot)
		want   string
	}{
		{"missing cart id", func(s *CartSnapshot) { s.CartID = "" }, "cart_id is required"},
		{"missing item list", func(s *CartSnapshot) { s.Items = nil }, "items list is required"},
		{"duplicate item id", func(s *CartSnapshot) { s.Items = append(s.Items, s.Items[0]) }, "duplicate cart item id"},
		{"missing item id", func(s *CartSnapshot) { s.Items[0].ItemID = "" }, "item_id is required"},
		{"missing product id", func(s *CartSnapshot) { s.Items[0].ProductID = "" }, "product_id is required"},
		{"missing variant id", func(s *CartSnapshot) { s.Items[0].VariantID = "" }, "variant_id is required"},
		{"zero quantity", func(s *CartSnapshot) { s.Items[0].Quantity = 0 }, "quantity must be positive"},
		{"negative unit price", func(s *CartSnapshot) { s.Items[0].UnitPricePaise = -1 }, "unit_price_paise must be non-negative"},
		{"negative shipping", func(s *CartSnapshot) { s.ShippingPaise = -1 }, "non-negative"},
		{"negative discount", func(s *CartSnapshot) { s.DiscountPaise = -1 }, "non-negative"},
		{"unsupported currency", func(s *CartSnapshot) { s.Currency = "USD" }, "only INR"},
		{"invalid capture timestamp", func(s *CartSnapshot) { s.CapturedAt = "yesterday" }, "invalid captured_at"},
		{"invalid delivery date", func(s *CartSnapshot) { s.DeliveryDate = datePointer("2026-02-30") }, "valid ISO calendar date"},
		{"discount exceeds gross", func(s *CartSnapshot) { s.DiscountPaise = 475001 }, "discount exceeds"},
		{"line total overflow", func(s *CartSnapshot) { s.Items[0].Quantity = 2; s.Items[0].UnitPricePaise = math.MaxInt64 }, "line total overflows"},
		{"subtotal overflow", func(s *CartSnapshot) {
			s.Items[0].UnitPricePaise = math.MaxInt64
			s.Items = append(s.Items, CartItem{ItemID: "item-2", ProductID: "product-2", VariantID: "variant-2", Quantity: 1, UnitPricePaise: 1})
		}, "subtotal overflows"},
		{"shipping total overflow", func(s *CartSnapshot) { s.Items[0].UnitPricePaise = math.MaxInt64; s.ShippingPaise = 1 }, "overflows int64"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			snapshot := cloneSnapshot(testSnapshot())
			tt.mutate(&snapshot)
			_, err := Evaluate(Input{CartSnapshot: snapshot})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestEvidenceScopePreventsCrossItemMatching(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Items = append(snapshot.Items, CartItem{ItemID: "item-2", ProductID: "product-2", VariantID: "variant-2", Quantity: 1, UnitPricePaise: 100})
	requirement := req("r2-vegan", Hard, "vegan", Equals, BooleanValue(true))
	requirement.SubjectID = "item-2"
	report, err := Evaluate(Input{CartSnapshot: snapshot, Requirements: []Requirement{requirement}, Evidence: []Evidence{evidence("e1", "vegan", BooleanValue(true))}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Outcome != Unknown || len(report.Results[0].Assessments) != 0 || report.Decision != Review {
		t.Fatalf("other item's evidence satisfied requirement: %#v", report)
	}
}

func TestScopeAndItemEvidenceValidation(t *testing.T) {
	base := Input{CartSnapshot: testSnapshot(), Requirements: []Requirement{req("r1", Hard, "vegan", Equals, BooleanValue(true))}, Evidence: []Evidence{evidence("e1", "vegan", BooleanValue(true))}}
	tests := []struct {
		name   string
		mutate func(*Input)
		want   string
	}{
		{"invalid requirement scope", func(in *Input) { in.Requirements[0].Scope = "order" }, "unsupported scope"},
		{"requirement wrong cart id", func(in *Input) { in.Requirements[0].Scope = CartScope; in.Requirements[0].SubjectID = "other-cart" }, "does not match cart_id"},
		{"requirement missing item", func(in *Input) { in.Requirements[0].SubjectID = "missing-item" }, "does not reference a cart item"},
		{"evidence missing item", func(in *Input) { in.Evidence[0].SubjectID = "missing-item" }, "does not reference a cart item"},
		{"evidence wrong product", func(in *Input) { in.Evidence[0].ProductID = "wrong-product" }, "does not match cart item"},
		{"evidence wrong variant", func(in *Input) { in.Evidence[0].VariantID = "wrong-variant" }, "does not match cart item"},
		{"cart scope evidence wrong id", func(in *Input) {
			in.Evidence[0].Scope = CartScope
			in.Evidence[0].SubjectID = "other-cart"
			in.Evidence[0].ProductID = ""
			in.Evidence[0].VariantID = ""
		}, "does not match cart_id"},
		{"cart evidence cannot override total", func(in *Input) {
			in.Evidence[0].Attribute = "total_price_paise"
			in.Evidence[0].Scope = CartScope
			in.Evidence[0].SubjectID = "cart-1"
			in.Evidence[0].ProductID = ""
			in.Evidence[0].VariantID = ""
			in.Evidence[0].Value = NumberValue(1)
		}, "cannot override cart snapshot"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := cloneInput(base)
			tt.mutate(&input)
			_, err := Evaluate(input)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestReservedDerivedAttributesRejectCallerEvidenceAtEveryScope(t *testing.T) {
	snapshot := testSnapshot()
	for _, attribute := range []string{"total_price_paise", "delivery_date"} {
		for _, scope := range []Scope{CartScope, ItemScope} {
			t.Run(string(scope)+"/"+attribute, func(t *testing.T) {
				subjectID := snapshot.CartID
				value := NumberInt64Value(475000)
				evidenceItem := Evidence{ID: "caller", Attribute: attribute, Scope: CartScope, SubjectID: subjectID, Value: value, SourceID: "caller", ObservedAt: "2026-10-28T09:55:00Z"}
				if attribute == "delivery_date" {
					evidenceItem.Value = DateValue("2026-10-29")
				}
				if scope == ItemScope {
					cartItem := snapshot.Items[0]
					evidenceItem.Scope = ItemScope
					evidenceItem.SubjectID = cartItem.ItemID
					evidenceItem.ProductID = cartItem.ProductID
					evidenceItem.VariantID = cartItem.VariantID
				}
				_, err := Evaluate(Input{CartSnapshot: snapshot, Evidence: []Evidence{evidenceItem}})
				if err == nil || !strings.Contains(err.Error(), "cannot override cart snapshot") {
					t.Fatalf("Evaluate error = %v, want reserved attribute rejection", err)
				}
			})
		}
	}
}

func TestSnapshotDerivedTotalAndDelivery(t *testing.T) {
	totalRequirement := Requirement{
		ID: "total", Attribute: "total_price_paise", Scope: CartScope, SubjectID: "cart-1",
		Operator: LessThanOrEqual, Expected: NumberInt64Value(475000), Severity: Hard,
	}
	report, err := Evaluate(Input{CartSnapshot: testSnapshot(), Requirements: []Requirement{totalRequirement}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != Allow || report.Results[0].Outcome != Pass || report.CartSnapshotSHA256 == "" {
		t.Fatalf("exact cart total did not pass: %#v", report)
	}
	assessment := report.Results[0].Assessments[0]
	if assessment.EvidenceID != "cart-snapshot:total-price-paise" || assessment.SourceID != "local-cart-snapshot" || assessment.ObservedAt != testSnapshot().CapturedAt || assessment.Value.Value != json.Number("475000") {
		t.Fatalf("derived total provenance missing: %#v", assessment)
	}
	if len(report.Evidence) != 2 || report.Evidence[0].ID != assessment.EvidenceID || report.Evidence[1].ID != "cart-snapshot:delivery-date" {
		t.Fatalf("derived evidence missing from report: %#v", report.Evidence)
	}

	overLimit := testSnapshot()
	overLimit.ShippingPaise++
	report, err = Evaluate(Input{CartSnapshot: overLimit, Requirements: []Requirement{totalRequirement}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != Block || report.Results[0].Outcome != Fail {
		t.Fatalf("above-limit total was not blocked: %#v", report)
	}

	discounted := testSnapshot()
	discounted.DiscountPaise = 50000
	discountRequirement := totalRequirement
	discountRequirement.Expected = NumberInt64Value(425000)
	report, err = Evaluate(Input{CartSnapshot: discounted, Requirements: []Requirement{discountRequirement}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != Allow || report.Evidence[0].Value.Value != json.Number("425000") {
		t.Fatalf("discount was not included in derived total: %#v", report)
	}
}

func TestMissingDeliveryPromiseIsUnknown(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.DeliveryDate = nil
	requirement := Requirement{
		ID: "delivery", Attribute: "delivery_date", Scope: CartScope, SubjectID: snapshot.CartID,
		Operator: BeforeOrEqual, Expected: DateValue("2026-10-30"), Severity: Hard,
	}
	report, err := Evaluate(Input{CartSnapshot: snapshot, Requirements: []Requirement{requirement}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Outcome != Unknown || report.Decision != Review || len(report.Results[0].Assessments) != 0 {
		t.Fatalf("missing delivery promise did not require review: %#v", report)
	}
}

func TestBuiltInCartRequirementContracts(t *testing.T) {
	snapshot := testSnapshot()
	tests := []struct {
		name        string
		attribute   string
		expected    Value
		scope       Scope
		operator    Operator
		wantMessage string
	}{
		{"total as string", "total_price_paise", StringValue("475000"), CartScope, Equals, "requires expected type \"number\""},
		{"total as boolean", "total_price_paise", BooleanValue(true), CartScope, Equals, "requires expected type \"number\""},
		{"total as date", "total_price_paise", DateValue("2026-10-30"), CartScope, Equals, "requires expected type \"number\""},
		{"delivery as number", "delivery_date", NumberInt64Value(20261030), CartScope, Equals, "requires expected type \"date\""},
		{"delivery as boolean", "delivery_date", BooleanValue(true), CartScope, Equals, "requires expected type \"date\""},
		{"delivery as string", "delivery_date", StringValue("2026-10-30"), CartScope, Equals, "requires expected type \"date\""},
		{"total at item scope", "total_price_paise", NumberInt64Value(475000), ItemScope, Equals, "must use cart scope"},
		{"delivery at item scope", "delivery_date", DateValue("2026-10-30"), ItemScope, Equals, "must use cart scope"},
		{"total with date operator", "total_price_paise", NumberInt64Value(475000), CartScope, BeforeOrEqual, "unsupported for built-in cart attribute"},
		{"delivery with numeric operator", "delivery_date", DateValue("2026-10-30"), CartScope, LessThanOrEqual, "unsupported for built-in cart attribute"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			subjectID := snapshot.CartID
			if tt.scope == ItemScope {
				subjectID = snapshot.Items[0].ItemID
			}
			requirement := Requirement{
				ID: "built-in", Attribute: tt.attribute, Scope: tt.scope, SubjectID: subjectID,
				Operator: tt.operator, Expected: tt.expected, Severity: Hard,
			}
			_, err := Evaluate(Input{CartSnapshot: snapshot, Requirements: []Requirement{requirement}})
			if err == nil || !strings.Contains(err.Error(), tt.wantMessage) {
				t.Fatalf("Evaluate error = %v, want substring %q", err, tt.wantMessage)
			}
		})
	}
}

func TestEvaluateRejectsEmptyCart(t *testing.T) {
	snapshot := testSnapshot()
	snapshot.Items = []CartItem{}
	report, err := Evaluate(Input{CartSnapshot: snapshot, Requirements: []Requirement{
		{ID: "total", Attribute: "total_price_paise", Scope: CartScope, SubjectID: snapshot.CartID, Operator: Equals, Expected: NumberInt64Value(0), Severity: Hard},
	}})
	if err == nil || !strings.Contains(err.Error(), "at least one item") {
		t.Fatalf("Evaluate error = %v, want empty-cart validation error", err)
	}
	if report.Decision == Allow {
		t.Fatal("empty cart produced ALLOW")
	}
}

func TestSampleCartTotalAndDecision(t *testing.T) {
	snapshot := testSnapshot()
	requirement := Requirement{
		ID: "total", Attribute: "total_price_paise", Scope: CartScope, SubjectID: snapshot.CartID,
		Operator: LessThanOrEqual, Expected: NumberInt64Value(500000), Severity: Hard,
	}
	report, err := Evaluate(Input{CartSnapshot: snapshot, Requirements: []Requirement{requirement}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Decision != Allow || report.Evidence[0].Value.Value != json.Number("475000") {
		t.Fatalf("sample cart verification = decision %s, total %#v; want ALLOW and 475000 paise", report.Decision, report.Evidence[0].Value.Value)
	}
}

func TestEvaluateRejectsInvalidInput(t *testing.T) {
	valid := Input{
		CartSnapshot: testSnapshot(),
		Requirements: []Requirement{req("r1", Hard, "price", LessThanOrEqual, NumberValue(500000))},
		Evidence:     []Evidence{evidence("e1", "price", NumberValue(500000))},
	}
	tests := []struct {
		name string
		edit func(*Input)
		want string
	}{
		{"duplicate requirement ids", func(in *Input) { in.Requirements = append(in.Requirements, in.Requirements[0]) }, "duplicate requirement id"},
		{"duplicate evidence ids", func(in *Input) { in.Evidence = append(in.Evidence, in.Evidence[0]) }, "duplicate evidence id"},
		{"missing requirement id", func(in *Input) { in.Requirements[0].ID = " " }, "id is required"},
		{"missing evidence id", func(in *Input) { in.Evidence[0].ID = "" }, "id is required"},
		{"invalid severity", func(in *Input) { in.Requirements[0].Severity = "optional" }, "invalid severity"},
		{"unsupported operator", func(in *Input) { in.Requirements[0].Operator = "contains" }, "unsupported operator"},
		{"operator with incompatible type", func(in *Input) { in.Requirements[0].Expected = StringValue("500000") }, "requires a number"},
		{"bad numeric Go value", func(in *Input) { in.Requirements[0].Expected = Value{Type: NumberType, Value: "500000"} }, "finite number"},
		{"mismatched evidence type", func(in *Input) { in.Evidence[0].Value = StringValue("500000") }, "expected \"number\""},
		{"malformed date", func(in *Input) {
			in.Requirements[0].Expected = DateValue("2026-02-30")
			in.Requirements[0].Operator = BeforeOrEqual
			in.Evidence[0].Value = DateValue("2026-02-28")
		}, "valid ISO calendar date"},
		{"missing provenance", func(in *Input) { in.Evidence[0].SourceID = "" }, "source_id is required"},
		{"invalid timestamp", func(in *Input) { in.Evidence[0].ObservedAt = "yesterday" }, "expected RFC3339 timestamp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := cloneInput(valid)
			tt.edit(&input)
			_, err := Evaluate(input)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func evaluateOne(t *testing.T, expected, actual Value, operator Operator) Report {
	t.Helper()
	attribute := "value"
	report, err := Evaluate(Input{
		CartSnapshot: testSnapshot(),
		Requirements: []Requirement{req("r1", Hard, attribute, operator, expected)},
		Evidence:     []Evidence{evidence("e1", attribute, actual)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func req(id string, severity Severity, attribute string, operator Operator, expected Value) Requirement {
	return Requirement{ID: id, Severity: severity, Attribute: attribute, Scope: ItemScope, SubjectID: "item-1", Operator: operator, Expected: expected}
}

func evidence(id, attribute string, value Value) Evidence {
	return Evidence{
		ID: id, Attribute: attribute, Scope: ItemScope, SubjectID: "item-1",
		ProductID: "product-1", VariantID: "variant-1", Value: value,
		SourceID: "catalogue:item-123", ObservedAt: "2026-10-28T10:00:00Z",
	}
}

func testSnapshot() CartSnapshot {
	return CartSnapshot{
		CartID: "cart-1", Currency: "INR", CapturedAt: "2026-10-28T10:00:00Z",
		Items:         []CartItem{{ItemID: "item-1", ProductID: "product-1", VariantID: "variant-1", Quantity: 1, UnitPricePaise: 450000}},
		ShippingPaise: 25000, DiscountPaise: 0, DeliveryDate: datePointer("2026-10-29"),
	}
}

func datePointer(value string) *string { return &value }

func cloneSnapshot(snapshot CartSnapshot) CartSnapshot {
	snapshot.Items = append([]CartItem(nil), snapshot.Items...)
	if snapshot.DeliveryDate != nil {
		date := *snapshot.DeliveryDate
		snapshot.DeliveryDate = &date
	}
	return snapshot
}

func snapshotHashForTest(t *testing.T, snapshot CartSnapshot) string {
	t.Helper()
	report, err := Evaluate(Input{CartSnapshot: snapshot})
	if err != nil {
		t.Fatal(err)
	}
	return report.CartSnapshotSHA256
}

func cloneInput(input Input) Input {
	return Input{
		CartSnapshot: cloneSnapshot(input.CartSnapshot),
		Requirements: append([]Requirement(nil), input.Requirements...),
		Evidence:     append([]Evidence(nil), input.Evidence...),
	}
}

func TestDirectionalBackpackSatisfiesBag(t *testing.T) {
	for _, tt := range []struct {
		name, expected, observed string
		missing, unrelated       bool
		want                     Outcome
		decision                 Decision
	}{
		{name: "backpack satisfies general bag", expected: "bag", observed: "backpack", want: Pass, decision: Allow},
		{name: "normalization retained", expected: " Bag ", observed: " BACKPACK ", want: Pass, decision: Allow},
		{name: "generic bag cannot satisfy backpack", expected: "backpack", observed: "bag", want: Fail, decision: Block},
		{name: "tote cannot satisfy backpack", expected: "backpack", observed: "tote", want: Fail, decision: Block},
		{name: "no extra taxonomy", expected: "bag", observed: "tote", want: Fail, decision: Block},
		{name: "no fuzzy matching", expected: "bag", observed: "backpack-style", want: Fail, decision: Block},
		{name: "missing category evidence", expected: "bag", missing: true, want: Unknown, decision: Review},
		{name: "unrelated evidence cannot establish category", expected: "bag", observed: "backpack", unrelated: true, want: Unknown, decision: Review},
	} {
		t.Run(tt.name, func(t *testing.T) {
			in := Input{CartSnapshot: testSnapshot(), Requirements: []Requirement{req("r1", Hard, "product_category", Equals, StringValue(tt.expected))}}
			if !tt.missing {
				e := evidence("e1", "product_category", StringValue(tt.observed))
				if tt.unrelated {
					e.Attribute = "description"
				}
				in.Evidence = []Evidence{e}
			}
			report, err := Evaluate(in)
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != tt.decision || report.Results[0].Outcome != tt.want {
				t.Fatalf("report=%+v", report)
			}
		})
	}
}
