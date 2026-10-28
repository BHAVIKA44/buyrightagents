package intent

import (
	"context"
	"encoding/json"
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
		{"hard cue softened", func(r *modelRequirement) { r.Severity = verification.Preference; r.SourceText = "must be vegan" }, "cannot be labelled as a preference"},
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
	if err == nil || len(result.Requirements) != 0 || !strings.Contains(err.Error(), "explicit intent cue") {
		t.Fatalf("dropped hard cue produced intent %#v, error %v", result, err)
	}
}

func TestRelativeDateResolutionAcrossBoundaries(t *testing.T) {
	tests := []struct {
		name, phrase, reference, want string
	}{
		{"week boundary", "tomorrow", "2026-10-30T23:30:00-07:00", "2026-10-31"},
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

func TestInterpretRejectsWrongRelativeDateAndInvalidReference(t *testing.T) {
	model := &fakeModel{content: `{"requirements":[{"attribute":"delivery_date","operator":"before_or_equal","expected":{"type":"date","value":"2026-10-30"},"severity":"hard","source_text":"by tomorrow"}],"clarification_questions":[],"unsupported_constraints":[]}`}
	result, err := (Interpreter{Client: model}).Interpret(context.Background(), "arrives by tomorrow", "2026-10-28T16:00:00+05:30")
	if err == nil || len(result.Requirements) != 0 || !strings.Contains(err.Error(), "does not match relative phrase") {
		t.Fatalf("wrong relative date returned %#v, error %v", result, err)
	}
	model.calls = 0
	if _, err := (Interpreter{Client: model}).Interpret(context.Background(), "vegan", "not-a-time"); err == nil || model.calls != 0 {
		t.Fatalf("invalid reference time reached model: calls=%d err=%v", model.calls, err)
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
