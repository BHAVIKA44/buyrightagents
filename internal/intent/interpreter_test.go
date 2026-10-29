package intent

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/bhavika44/buyrightagents/internal/verification"
)

type fakeModel struct {
	content string
	err     error
	calls   int
	system  string
	user    string
}

type sequenceModel struct {
	outputs []string
	errors  []error
	calls   int
	systems []string
	users   []string
}

func (model *sequenceModel) Complete(_ context.Context, system, user string) (string, error) {
	index := model.calls
	model.calls++
	model.systems = append(model.systems, system)
	model.users = append(model.users, user)
	if index < len(model.errors) && model.errors[index] != nil {
		return "", model.errors[index]
	}
	if index >= len(model.outputs) {
		return "", errors.New("unexpected extra model call")
	}
	return model.outputs[index], nil
}

func (fake *fakeModel) Complete(_ context.Context, system, user string) (string, error) {
	fake.calls++
	fake.system, fake.user = system, user
	return fake.content, fake.err
}

func TestInterpretBuildsTypedIntentAndApplicationIDs(t *testing.T) {
	model := &fakeModel{content: `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan is non-negotiable"},{"attribute":"color","operator":"equals","expected":{"type":"string","value":"red"},"severity":"preference","source_text":"Red would be nice"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000 including delivery"}],"clarification_questions":[],"unsupported_constraints":[]}`}
	result, err := (Interpreter{Client: model}).Interpret(context.Background(), "Vegan is non-negotiable. Red would be nice. Under ₹5,000 including delivery.", "2026-10-28T16:48:00+05:30")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.Requirements) != 3 || !strings.HasPrefix(result.Requirements[0].ID, "requirement-") || result.Requirements[0].ID == result.Requirements[1].ID {
		t.Fatalf("unexpected application IDs or requirement count: %#v", result.Requirements)
	}
	if result.Requirements[0].Severity != verification.Hard || result.Requirements[1].Severity != verification.Preference {
		t.Fatalf("hard/preference semantics lost: %#v", result.Requirements)
	}
	if result.Requirements[2].Expected.Value != json.Number("500000") || result.Requirements[2].Operator != verification.LessThan {
		t.Fatalf("strict INR price was not represented in integer paise: %#v", result.Requirements[2])
	}
	if !strings.Contains(model.user, "Reference local date: 2026-10-28") || !strings.Contains(model.user, "Reference UTC offset: +05:30") || !strings.Contains(model.user, "2026-10-28T16:48:00+05:30") {
		t.Fatalf("explicit reference time missing from model prompt: %s", model.user)
	}
	if strings.Contains(model.system, "ALLOW") == false || strings.Contains(model.system, "make checkout decisions") == false {
		t.Fatalf("system prompt does not prohibit purchase decisions: %s", model.system)
	}
}

func TestRequirementIDsAreStableAcrossModelOrdering(t *testing.T) {
	first := &fakeModel{content: `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"color","operator":"equals","expected":{"type":"string","value":"red"},"severity":"preference","source_text":"red would be nice"}],"clarification_questions":[],"unsupported_constraints":[]}`}
	second := &fakeModel{content: `{"requirements":[{"attribute":"color","operator":"equals","expected":{"type":"string","value":"red"},"severity":"preference","source_text":"red would be nice"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"}],"clarification_questions":[],"unsupported_constraints":[]}`}
	a, err := (Interpreter{Client: first}).Interpret(context.Background(), "vegan, red would be nice", "2026-10-28T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	b, err := (Interpreter{Client: second}).Interpret(context.Background(), "vegan, red would be nice", "2026-10-28T00:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]string{}
	for _, requirement := range a.Requirements {
		ids[requirement.Attribute] = requirement.ID
	}
	for _, requirement := range b.Requirements {
		if ids[requirement.Attribute] != requirement.ID {
			t.Fatalf("ID for %s changed with array ordering: %s != %s", requirement.Attribute, ids[requirement.Attribute], requirement.ID)
		}
	}
}

func TestInterpretAmbiguityAndUnsupportedConstraints(t *testing.T) {
	model := &fakeModel{content: `{"requirements":[],"clarification_questions":[{"question":"Do you mean cotton only, or would a blend be acceptable?","source_text":"cotton if possible"}],"unsupported_constraints":[{"source_text":"a brand with lifetime repairs","reason":"Brand service policy is not a supported attribute."}]}`}
	result, err := (Interpreter{Client: model}).Interpret(context.Background(), "Cotton if possible. I also want a brand with lifetime repairs.", "2026-10-28T10:00:00Z")
	if err != nil {
		t.Fatal(err)
	}
	if len(result.ClarificationQuestions) != 1 || len(result.UnsupportedConstraints) != 1 || len(result.Requirements) != 0 {
		t.Fatalf("ambiguity or unsupported constraint was lost: %#v", result)
	}
}

func TestAllSupportedAttributesAndCompatibleOperators(t *testing.T) {
	cases := []struct {
		attribute string
		operator  verification.Operator
		value     verification.Value
	}{
		{"vegan", verification.Equals, verification.BooleanValue(true)},
		{"water_resistant", verification.NotEquals, verification.BooleanValue(false)},
		{"recycled_material", verification.Equals, verification.BooleanValue(true)},
		{"laptop_fit_inches", verification.GreaterThanOrEqual, verification.NumberInt64Value(16)},
		{"total_price_paise", verification.LessThan, verification.NumberInt64Value(500000)},
		{"delivery_date", verification.Before, verification.DateValue("2026-10-30")},
		{"color", verification.Equals, verification.StringValue("red")},
		{"material", verification.NotEquals, verification.StringValue("polyester")},
		{"weight_grams", verification.LessThanOrEqual, verification.NumberInt64Value(750)},
	}
	for _, test := range cases {
		t.Run(test.attribute, func(t *testing.T) {
			requirement := Requirement{
				ID: "requirement-001", Attribute: test.attribute, Operator: test.operator,
				Expected: test.value, Severity: verification.Hard, SourceText: "the stated shopping constraint",
			}
			if err := validateRequirement(requirement, "The stated shopping constraint."); err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestCompleteAttributeOperatorCompatibilityMatrix(t *testing.T) {
	all := []verification.Operator{
		verification.Equals, verification.NotEquals, verification.LessThan, verification.LessThanOrEqual,
		verification.GreaterThan, verification.GreaterThanOrEqual, verification.Before,
		verification.BeforeOrEqual, verification.After, verification.AfterOrEqual,
	}
	booleanEquality := []verification.Operator{verification.Equals, verification.NotEquals}
	stringEquality := []verification.Operator{verification.Equals, verification.NotEquals}
	numeric := []verification.Operator{verification.Equals, verification.NotEquals, verification.LessThan, verification.LessThanOrEqual, verification.GreaterThan, verification.GreaterThanOrEqual}
	dates := []verification.Operator{verification.Equals, verification.NotEquals, verification.Before, verification.BeforeOrEqual, verification.After, verification.AfterOrEqual}
	attributes := []struct {
		name    string
		value   verification.Value
		allowed []verification.Operator
	}{
		{"vegan", verification.BooleanValue(true), booleanEquality},
		{"water_resistant", verification.BooleanValue(true), booleanEquality},
		{"recycled_material", verification.BooleanValue(true), booleanEquality},
		{"laptop_fit_inches", verification.NumberInt64Value(16), numeric},
		{"total_price_paise", verification.NumberInt64Value(500000), numeric},
		{"delivery_date", verification.DateValue("2026-10-30"), dates},
		{"color", verification.StringValue("red"), stringEquality},
		{"material", verification.StringValue("cotton"), stringEquality},
		{"weight_grams", verification.NumberInt64Value(750), numeric},
	}
	for _, attribute := range attributes {
		allowed := make(map[verification.Operator]bool)
		for _, operator := range attribute.allowed {
			allowed[operator] = true
		}
		for _, operator := range all {
			t.Run(attribute.name+"/"+string(operator), func(t *testing.T) {
				candidate := Requirement{Attribute: attribute.name, Operator: operator, Expected: attribute.value, Severity: verification.Hard, SourceText: "constraint"}
				err := validateRequirement(candidate, "constraint")
				if allowed[operator] && err != nil {
					t.Fatalf("compatible operator rejected: %v", err)
				}
				if !allowed[operator] && err == nil {
					t.Fatal("incompatible operator accepted")
				}
			})
		}
	}
}

func TestValidateOutputRejectsUnsafeOrUnsupportedValues(t *testing.T) {
	valid := modelRequirement{Attribute: "vegan", Operator: verification.Equals, Expected: verification.BooleanValue(true), Severity: verification.Hard, SourceText: "vegan"}
	tests := []struct {
		name string
		edit func(*modelRequirement)
		want string
	}{
		{"unknown attribute", func(r *modelRequirement) { r.Attribute = "carbon_neutral" }, "unsupported"},
		{"wrong type", func(r *modelRequirement) { r.Expected = verification.StringValue("yes") }, "requires expected type"},
		{"incompatible operator", func(r *modelRequirement) { r.Operator = verification.LessThan }, "unsupported for attribute"},
		{"invalid severity", func(r *modelRequirement) { r.Severity = "must" }, "must be hard or preference"},
		{"ungrounded source", func(r *modelRequirement) { r.SourceText = "guaranteed leather" }, "not grounded"},
		{"fractional paise", func(r *modelRequirement) {
			r.Attribute = "total_price_paise"
			r.Operator = verification.LessThan
			r.Expected = verification.Value{Type: verification.NumberType, Value: json.Number("500000.5")}
			r.SourceText = "under five thousand"
		}, "integer number of paise"},
		{"paise outside verifier range", func(r *modelRequirement) {
			r.Attribute = "total_price_paise"
			r.Operator = verification.LessThan
			r.Expected = verification.Value{Type: verification.NumberType, Value: json.Number("9223372036854775808")}
			r.SourceText = "under five thousand"
		}, "signed 64-bit paise range"},
		{"invalid date", func(r *modelRequirement) {
			r.Attribute = "delivery_date"
			r.Operator = verification.Before
			r.Expected = verification.DateValue("2026-02-30")
			r.SourceText = "before Friday"
		}, "valid ISO date"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			candidate := valid
			test.edit(&candidate)
			req := []modelRequirement{candidate}
			output := modelOutput{Requirements: &req, ClarificationQuestions: ptr([]modelClarification{}), UnsupportedConstraints: ptr([]modelUnsupported{})}
			_, err := validateOutput(output, "must be vegan, under five thousand, before Friday", time.Date(2026, 10, 28, 0, 0, 0, 0, time.UTC))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("validation error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestModelJSONRejectsMalformedUnknownDuplicateAndDecisionFields(t *testing.T) {
	for _, test := range []struct{ name, content, want string }{
		{"malformed", `{"requirements":`, "EOF"},
		{"duplicate nested key", `{"requirements":[{"attribute":"vegan","attribute":"color"}],"clarification_questions":[],"unsupported_constraints":[]}`, "duplicate JSON object key"},
		{"unknown field", `{"requirements":[],"clarification_questions":[],"unsupported_constraints":[],"extra":true}`, "unknown field"},
		{"decision injection", `{"requirements":[],"clarification_questions":[],"unsupported_constraints":[],"decision":"ALLOW"}`, "unknown field"},
		{"missing output arrays", `{"requirements":[]}`, "arrays are required"},
	} {
		t.Run(test.name, func(t *testing.T) {
			_, err := decodeModelOutput(test.content)
			if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(test.want)) {
				t.Fatalf("decode error = %v, want substring %q", err, test.want)
			}
		})
	}
}

func TestInterpretReturnsNoPartialIntentOnInvalidOutput(t *testing.T) {
	model := &fakeModel{content: `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"unsupported_new_field","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"backpack"}],"clarification_questions":[],"unsupported_constraints":[]}`}
	result, err := (Interpreter{Client: model}).Interpret(context.Background(), "vegan backpack", "2026-10-28T00:00:00Z")
	if err == nil || len(result.Requirements) != 0 {
		t.Fatalf("invalid output returned partial intent %#v with error %v", result, err)
	}
}

func TestInterpretRejectsSilentlyDroppedExplicitHardCue(t *testing.T) {
	model := &fakeModel{content: `{"requirements":[{"attribute":"color","operator":"equals","expected":{"type":"string","value":"red"},"severity":"preference","source_text":"ideally red"}],"clarification_questions":[],"unsupported_constraints":[]}`}
	result, err := (Interpreter{Client: model}).Interpret(context.Background(), "Must be vegan, ideally red.", "2026-10-28T00:00:00Z")
	if err == nil || len(result.Requirements) != 0 || !strings.Contains(err.Error(), "attribute \"vegan\"") {
		t.Fatalf("dropped hard cue produced intent %#v, error %v", result, err)
	}
}

func TestCoverageCandidateAcceptsMatchingCategory(t *testing.T) {
	response := `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	if got, err := (Interpreter{Client: &fakeModel{content: response}}).Interpret(context.Background(), "Find a vegan bag under ₹5,000", "2026-10-28T00:00:00Z"); err != nil {
		t.Fatalf("matching category should be covered: intent=%#v err=%v", got, err)
	}
}

func TestCoverageGuardUsesPositionAwareSeverity(t *testing.T) {
	tests := []struct {
		name, request, output string
		wantCoverage          bool
	}{
		{
			name:         "neutral category cannot be downgraded to preference",
			request:      "Find a vegan bag under ₹5,000.",
			output:       `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"preference","source_text":"bag"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`,
			wantCoverage: true,
		},
		{
			name:    "preference cue stays local to color",
			request: "Find a vegan bag, ideally red, under ₹5,000.",
			output:  `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"color","operator":"equals","expected":{"type":"string","value":"red"},"severity":"preference","source_text":"ideally red"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			result, err := (Interpreter{Client: &fakeModel{content: test.output}}).Interpret(context.Background(), test.request, "2026-10-28T10:00:00Z")
			var coverage *CoverageError
			if errors.As(err, &coverage) != test.wantCoverage {
				t.Fatalf("intent=%#v error=%v; coverage=%t want %t", result, err, errors.As(err, &coverage), test.wantCoverage)
			}
		})
	}
}

func TestCoverageGuardDetectsOmittedCategoryMaterialAndLaptopFit(t *testing.T) {
	tests := []struct {
		name, request, output, attribute string
	}{
		{
			name: "category", request: "Find a vegan bag under ₹5,000.",
			output:    `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`,
			attribute: "product_category",
		},
		{
			name: "material", request: "Find a canvas bag under ₹5,000.",
			output:    `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`,
			attribute: "material",
		},
		{
			name: "laptop fit", request: "Find a bag that fits a 16 inch laptop under ₹5,000.",
			output:    `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`,
			attribute: "laptop_fit_inches",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, _, err := (Interpreter{Client: &fakeModel{content: test.output}}).InterpretWithMetadata(context.Background(), test.request, "2026-10-28T10:00:00Z")
			var coverage *CoverageError
			if !errors.As(err, &coverage) {
				t.Fatalf("error=%v; want CoverageError", err)
			}
			for _, diagnostic := range coverage.Diagnostics {
				if diagnostic.Attribute == test.attribute && diagnostic.SourceText != "" && diagnostic.SourceEnd > diagnostic.SourceStart {
					return
				}
			}
			t.Fatalf("missing %q diagnostic: %#v", test.attribute, coverage.Diagnostics)
		})
	}
}

func TestCoverageGuardRejectsIncorrectValuesUnitsPolarityAndOperators(t *testing.T) {
	tests := []struct {
		name, request, output, attribute string
	}{
		{"price value", "Find a bag under ₹5,000.", `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":400000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`, "total_price_paise"},
		{"price operator", "Find a bag under ₹5,000.", `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"total_price_paise","operator":"less_than_or_equal","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`, "total_price_paise"},
		{"weight unit", "Find a bag under 1 kg.", `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"weight_grams","operator":"less_than","expected":{"type":"number","value":1},"severity":"hard","source_text":"under 1 kg"}],"clarification_questions":[],"unsupported_constraints":[]}`, "weight_grams"},
		{"negated boolean", "Find a not vegan bag.", `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"not vegan"}],"clarification_questions":[],"unsupported_constraints":[]}`, "vegan"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			_, err := (Interpreter{Client: &fakeModel{content: test.output}}).Interpret(context.Background(), test.request, "2026-10-28T10:00:00Z")
			var coverage *CoverageError
			if !errors.As(err, &coverage) {
				t.Fatalf("error=%v; want CoverageError", err)
			}
			found := false
			for _, diagnostic := range coverage.Diagnostics {
				found = found || diagnostic.Attribute == test.attribute
			}
			if !found {
				t.Fatalf("diagnostics=%#v; want %q", coverage.Diagnostics, test.attribute)
			}
		})
	}
}

func TestTargetedRepairRevalidatesOmittedRequirement(t *testing.T) {
	partial := `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	complete := `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	model := &sequenceModel{outputs: []string{partial, complete}}
	result, metadata, err := (Interpreter{Client: model, Repair: RepairPolicy{MaxAttempts: 1}}).InterpretWithMetadata(context.Background(), "Find a vegan bag under ₹5,000.", "2026-10-28T10:00:00Z")
	if err != nil || len(result.Requirements) != 3 || model.calls != 2 || metadata.RepairCalls != 1 || !metadata.RepairSucceeded || len(metadata.CoverageDiagnostics) == 0 || metadata.TokenUsageAvailable {
		t.Fatalf("repair result=%#v metadata=%#v calls=%d err=%v", result, metadata, model.calls, err)
	}
	if !strings.Contains(model.systems[1], "repairing") || !strings.Contains(model.users[1], "product_category") || !strings.Contains(model.users[1], "Find a vegan bag") {
		t.Fatalf("repair call lacks bounded diagnostic context: system=%q user=%q", model.systems[1], model.users[1])
	}
}

func TestTargetedRepairFailureReturnsNoIntent(t *testing.T) {
	partial := `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	model := &sequenceModel{outputs: []string{partial, partial}}
	result, metadata, err := (Interpreter{Client: model, Repair: RepairPolicy{MaxAttempts: 1}}).InterpretWithMetadata(context.Background(), "Find a vegan bag under ₹5,000.", "2026-10-28T10:00:00Z")
	if err == nil || len(result.Requirements) != 0 || model.calls != 2 || metadata.RepairCalls != 1 || metadata.RepairSucceeded {
		t.Fatalf("failed repair produced unsafe output=%#v metadata=%#v calls=%d err=%v", result, metadata, model.calls, err)
	}
	var coverage *CoverageError
	if !errors.As(err, &coverage) || len(coverage.Diagnostics) == 0 {
		t.Fatalf("failed repair did not preserve diagnostics: %v", err)
	}
}

func TestInterpretGuardsConfidentlyDetectedBooleanConstraintCoverage(t *testing.T) {
	priceOnly := `{"requirements":[{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	backpackAndPrice := `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"backpack"},"severity":"hard","source_text":"backpack"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	bagAndPrice := `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"bag"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	backpackWrongVegan := `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"backpack"},"severity":"hard","source_text":"backpack"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":false},"severity":"hard","source_text":"vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	for _, test := range []struct {
		name, request, output string
		wantErr               string
		wantCount             int
	}{
		{"omitted vegan", "Find a vegan backpack under ₹5,000", backpackAndPrice, "attribute \"vegan\"", 0},
		{"wrong vegan polarity", "Find a vegan backpack under ₹5,000", backpackWrongVegan, "attribute \"vegan\"", 0},
		{"omitted water resistance", "Find a water-resistant laptop bag under ₹5,000", bagAndPrice, "attribute \"water_resistant\"", 0},
		{"not necessarily is not a hard vegan requirement", "Not necessarily vegan, but under ₹5,000", priceOnly, "", 1},
		{"quoted word is not a vegan requirement", "Find a backpack under ₹5,000; do not use the word \"vegan\" in its description", backpackAndPrice, "", 2},
		{"single-quoted word is not a vegan requirement", "Find a backpack under ₹5,000; do not use the word 'vegan' in its description", backpackAndPrice, "", 2},
		{"incidental description mention is not a vegan requirement", "A description says vegan, but find a backpack under ₹5,000", backpackAndPrice, "", 2},
	} {
		t.Run(test.name, func(t *testing.T) {
			got, err := (Interpreter{Client: &fakeModel{content: test.output}}).Interpret(context.Background(), test.request, "2026-10-28T00:00:00Z")
			if test.wantErr == "" {
				if err != nil || len(got.Requirements) != test.wantCount {
					t.Fatalf("unexpected hard boolean constraint: intent=%#v err=%v", got, err)
				}
				return
			}
			if err == nil || len(got.Requirements) != 0 || !strings.Contains(err.Error(), test.wantErr) {
				t.Fatalf("intent=%#v err=%v; want %q", got, err, test.wantErr)
			}
		})
	}
}

func TestInterpretPreservesExplicitBooleanNegationInCoverage(t *testing.T) {
	request := "Find a not vegan backpack under ₹5,000"
	valid := `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"backpack"},"severity":"hard","source_text":"backpack"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":false},"severity":"hard","source_text":"not vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	intent, err := (Interpreter{Client: &fakeModel{content: valid}}).Interpret(context.Background(), request, "2026-10-28T00:00:00Z")
	if err != nil || len(intent.Requirements) != 3 || intent.Requirements[1].Expected.Value != false || intent.Requirements[1].Severity != verification.Hard {
		t.Fatalf("negated requirement was not preserved: intent=%#v err=%v", intent, err)
	}

	wrongPolarity := strings.Replace(valid, `"value":false`, `"value":true`, 1)
	if intent, err := (Interpreter{Client: &fakeModel{content: wrongPolarity}}).Interpret(context.Background(), request, "2026-10-28T00:00:00Z"); err == nil || len(intent.Requirements) != 0 || !strings.Contains(err.Error(), `attribute "vegan"`) {
		t.Fatalf("wrong negated polarity was accepted: intent=%#v err=%v", intent, err)
	}
}

func TestRelativeDateResolutionAcrossBoundaries(t *testing.T) {
	tests := []struct {
		name, phrase, reference, want string
	}{
		{"week boundary", "tomorrow", "2026-10-30T23:30:00-07:00", "2026-10-31"},
		{"same instant before local midnight", "tomorrow", "2026-10-28T23:30:00-07:00", "2026-10-29"},
		{"same instant after UTC midnight", "tomorrow", "2026-10-29T06:30:00Z", "2026-10-30"},
		{"year boundary", "tomorrow", "2026-12-31T23:59:00-05:00", "2027-01-01"},
		{"weekday wraps into next week", "this Friday", "2026-10-31T10:00:00+05:30", "2026-11-06"},
		{"within two weeks crosses year", "within two weeks", "2026-12-25T08:00:00Z", "2027-01-08"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			reference, err := time.Parse(time.RFC3339, test.reference)
			if err != nil {
				t.Fatal(err)
			}
			actual, found := ResolveRelativeDate(test.phrase, reference)
			if !found || actual != test.want {
				t.Fatalf("ResolveRelativeDate = %q, %v; want %q, true", actual, found, test.want)
			}
		})
	}
}

func TestInterpretCanonicalizesSupportedRelativeDateAndRejectsAmbiguousDate(t *testing.T) {
	model := &fakeModel{content: `{"requirements":[{"attribute":"delivery_date","operator":"before_or_equal","expected":{"type":"date","value":"2026-10-30"},"severity":"hard","source_text":"by tomorrow"}],"clarification_questions":[],"unsupported_constraints":[]}`}
	result, err := (Interpreter{Client: model}).Interpret(context.Background(), "arrives by tomorrow", "2026-10-28T16:00:00+05:30")
	if err != nil || len(result.Requirements) != 1 || result.Requirements[0].Expected.Value != "2026-10-29" {
		t.Fatalf("relative date was not canonicalized from reference time: result=%#v error=%v", result, err)
	}

	ambiguous := &fakeModel{content: `{"requirements":[{"attribute":"delivery_date","operator":"before_or_equal","expected":{"type":"date","value":"2026-10-30"},"severity":"hard","source_text":"by Friday or Saturday"}],"clarification_questions":[],"unsupported_constraints":[]}`}
	if result, err := (Interpreter{Client: ambiguous}).Interpret(context.Background(), "arrive by Friday or Saturday", "2026-10-28T10:00:00Z"); err == nil || len(result.Requirements) != 0 || !strings.Contains(err.Error(), "request clarification") {
		t.Fatalf("ambiguous relative date was guessed: result=%#v error=%v", result, err)
	}

	model.calls = 0
	if _, err := (Interpreter{Client: model}).Interpret(context.Background(), "vegan", "not-a-time"); err == nil || model.calls != 0 {
		t.Fatalf("invalid reference time reached model: calls=%d err=%v", model.calls, err)
	}
}

func TestInterpretDerivesDeliveryOperatorFromGroundedWording(t *testing.T) {
	tests := []struct {
		name, request, source, modelOperator, modelDate, reference string
		want                                                       verification.Operator
	}{
		{"intent-022 strict deadline despite numeric model operator", "Delivery must be before Friday.", "must be before Friday", "less_than", "2026-10-30", "2026-10-28T10:00:00Z", verification.Before},
		{"intent-029 after date despite numeric model operator", "Arrive after Friday.", "after Friday", "greater_than", "2026-10-30", "2026-10-28T10:00:00Z", verification.After},
		{"intent-024 inclusive named arrival date", "It must arrive this Friday.", "must arrive this Friday", "equals", "2026-10-30", "2026-10-28T10:00:00Z", verification.BeforeOrEqual},
		{"intent-026 inclusive tomorrow boundary", "Must arrive tomorrow.", "Must arrive tomorrow", "equals", "2027-01-01", "2026-12-31T23:59:00-05:00", verification.BeforeOrEqual},
		{"explicit exact date", "Must arrive exactly on 2026-10-30.", "must arrive exactly on 2026-10-30", "before_or_equal", "2026-10-30", "2026-10-28T10:00:00Z", verification.Equals},
		{"strict versus inclusive", "Must arrive no later than Friday.", "must arrive no later than Friday", "before", "2026-10-30", "2026-10-28T10:00:00Z", verification.BeforeOrEqual},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			response := `{"requirements":[{"attribute":"delivery_date","operator":"` + test.modelOperator + `","expected":{"type":"date","value":"` + test.modelDate + `"},"severity":"hard","source_text":"` + test.source + `"}],"clarification_questions":[],"unsupported_constraints":[]}`
			result, err := (Interpreter{Client: &fakeModel{content: response}}).Interpret(context.Background(), test.request, test.reference)
			if err != nil || len(result.Requirements) != 1 || result.Requirements[0].Operator != test.want {
				t.Fatalf("intent=%#v err=%v; want operator %q", result, err, test.want)
			}
		})
	}
}

func TestInterpretRejectsAmbiguousDeliveryOperatorMeaning(t *testing.T) {
	for _, source := range []string{"around Friday", "Friday or Saturday"} {
		t.Run(source, func(t *testing.T) {
			response := `{"requirements":[{"attribute":"delivery_date","operator":"equals","expected":{"type":"date","value":"2026-10-30"},"severity":"hard","source_text":"` + source + `"}],"clarification_questions":[],"unsupported_constraints":[]}`
			if got, err := (Interpreter{Client: &fakeModel{content: response}}).Interpret(context.Background(), "arrive "+source, "2026-10-28T10:00:00Z"); err == nil || len(got.Requirements) != 0 || !strings.Contains(err.Error(), "request clarification") {
				t.Fatalf("ambiguous date meaning accepted: intent=%#v err=%v", got, err)
			}
		})
	}
}

func TestSavedSeverityAndConflictPatternsNormalizeConservatively(t *testing.T) {
	tests := []struct {
		name, request, output                string
		wantRequirements, wantClarifications int
	}{
		{"should price is ambiguous", "It should cost ₹1,000 or more all in.", `{"requirements":[{"attribute":"total_price_paise","operator":"greater_than_or_equal","expected":{"type":"number","value":100000},"severity":"hard","source_text":"It should cost ₹1,000 or more all in."}],"clarification_questions":[],"unsupported_constraints":[]}`, 0, 1},
		{"should delivery is ambiguous", "It should arrive tomorrow.", `{"requirements":[{"attribute":"delivery_date","operator":"before_or_equal","expected":{"type":"date","value":"2026-10-29"},"severity":"hard","source_text":"It should arrive tomorrow."}],"clarification_questions":[],"unsupported_constraints":[]}`, 0, 1},
		{"opposite boolean requirements become clarification", "It should be vegan and it must not be vegan.", `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"should be vegan"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":false},"severity":"hard","source_text":"must not be vegan"}],"clarification_questions":[],"unsupported_constraints":[]}`, 0, 1},
		{"soft but uncertain severity remains preference", "I would prefer vegan, but it may not be essential.", `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"preference","source_text":"I would prefer vegan, but it may not be essential."}],"clarification_questions":[],"unsupported_constraints":[]}`, 1, 0},
		{"would be good remains preference", "A red one would be good.", `{"requirements":[{"attribute":"color","operator":"equals","expected":{"type":"string","value":"red"},"severity":"preference","source_text":"A red one would be good"}],"clarification_questions":[],"unsupported_constraints":[]}`, 1, 0},
		{"neutral deadline remains hard", "Delivery within two weeks.", `{"requirements":[{"attribute":"delivery_date","operator":"before_or_equal","expected":{"type":"date","value":"2026-11-11"},"severity":"hard","source_text":"within two weeks"}],"clarification_questions":[],"unsupported_constraints":[]}`, 1, 0},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := (Interpreter{Client: &fakeModel{content: test.output}}).Interpret(context.Background(), test.request, "2026-10-28T10:00:00Z")
			if err != nil || len(got.Requirements) != test.wantRequirements || len(got.ClarificationQuestions) != test.wantClarifications {
				t.Fatalf("intent=%#v err=%v", got, err)
			}
		})
	}
}

func TestInterpretPreservesExplicitSeverityAndClarifiesConflicts(t *testing.T) {
	tests := []struct {
		name, request, output                string
		wantSeverity                         verification.Severity
		wantRequirements, wantClarifications int
	}{
		{"prefer may not be essential", "I prefer vegan; it may not be essential.", `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"I prefer vegan; it may not be essential"}],"clarification_questions":[],"unsupported_constraints":[]}`, verification.Preference, 1, 0},
		{"would be good", "A red one would be good.", `{"requirements":[{"attribute":"color","operator":"equals","expected":{"type":"string","value":"red"},"severity":"hard","source_text":"A red one would be good"}],"clarification_questions":[],"unsupported_constraints":[]}`, verification.Preference, 1, 0},
		{"required", "Vegan is required.", `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"preference","source_text":"Vegan is required"}],"clarification_questions":[],"unsupported_constraints":[]}`, verification.Hard, 1, 0},
		{"mixed hard and soft", "Vegan is required but ideally red.", `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"Vegan is required but ideally red"}],"clarification_questions":[],"unsupported_constraints":[]}`, "", 0, 1},
		{"opposing hard requirements", "Vegan must be true and vegan must be false.", `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"Vegan must be true"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":false},"severity":"hard","source_text":"vegan must be false"}],"clarification_questions":[],"unsupported_constraints":[]}`, "", 0, 1},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := (Interpreter{Client: &fakeModel{content: test.output}}).Interpret(context.Background(), test.request, "2026-10-28T10:00:00Z")
			if err != nil || len(got.Requirements) != test.wantRequirements || len(got.ClarificationQuestions) != test.wantClarifications {
				t.Fatalf("intent=%#v err=%v", got, err)
			}
			if test.wantRequirements == 1 && got.Requirements[0].Severity != test.wantSeverity {
				t.Fatalf("severity=%q want %q", got.Requirements[0].Severity, test.wantSeverity)
			}
		})
	}
}

func TestInterpretRejectsUnsupportedRelativeDeliveryDates(t *testing.T) {
	for _, phrase := range []string{"next week", "in 3 days", "this weekend", "soon"} {
		t.Run(phrase, func(t *testing.T) {
			model := &fakeModel{content: `{"requirements":[{"attribute":"delivery_date","operator":"before_or_equal","expected":{"type":"date","value":"2026-11-01"},"severity":"hard","source_text":"` + phrase + `"}],"clarification_questions":[],"unsupported_constraints":[]}`}
			if result, err := (Interpreter{Client: model}).Interpret(context.Background(), "arrive "+phrase, "2026-10-28T10:00:00Z"); err == nil || len(result.Requirements) != 0 || !strings.Contains(err.Error(), "request clarification") {
				t.Fatalf("unsupported relative date was guessed: result=%#v error=%v", result, err)
			}
		})
	}
}

func TestInterpretRequiresClientAndRequest(t *testing.T) {
	if _, err := (Interpreter{}).Interpret(context.Background(), "vegan", "2026-10-28T00:00:00Z"); err == nil {
		t.Fatal("missing client was accepted")
	}
	if _, err := (Interpreter{Client: &fakeModel{}}).Interpret(context.Background(), " ", "2026-10-28T00:00:00Z"); err == nil {
		t.Fatal("empty request was accepted")
	}
}

func ptr[T any](value T) *T { return &value }
