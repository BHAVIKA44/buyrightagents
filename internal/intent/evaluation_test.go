package intent

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"

	"github.com/bhavika44/buyrightagents/internal/verification"
)

type mappedModel struct{ output string }

func (model mappedModel) Complete(context.Context, string, string) (string, error) {
	return model.output, nil
}

func TestIntentEvaluationIgnoresGeneratedIDsAndArrayOrder(t *testing.T) {
	item := IntentCase{
		ID: "case-1", Request: "Vegan, and ideally red.", ReferenceTime: "2026-10-28T10:00:00Z",
		ExpectedIntent: PurchaseIntent{
			Requirements: []Requirement{
				{ID: "expected-a", Attribute: "vegan", Operator: verification.Equals, Expected: verification.BooleanValue(true), Severity: verification.Hard, SourceText: "Vegan"},
				{ID: "expected-b", Attribute: "color", Operator: verification.Equals, Expected: verification.StringValue("red"), Severity: verification.Preference, SourceText: "ideally red"},
			},
			ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{},
		},
	}
	response := `{"requirements":[{"attribute":"color","operator":"equals","expected":{"type":"string","value":"red"},"severity":"preference","source_text":"  Ideally   RED!  "},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"Vegan, and"}],"clarification_questions":[],"unsupported_constraints":[]}`
	summary, err := EvaluateIntentCases(context.Background(), Interpreter{Client: mappedModel{response}}, []IntentCase{item})
	if err != nil {
		t.Fatal(err)
	}
	if !summary.AllExact() || summary.Constraints.TruePositive != 2 || summary.Constraints.FalsePositive != 0 || summary.Constraints.FalseNegative != 0 {
		t.Fatalf("IDs/order affected comparison: %#v", summary)
	}
}

func TestExactIntentComparisonRejectsNegatedConflictingAndEmptySourceSpans(t *testing.T) {
	tests := []struct {
		name      string
		attribute string
		expected  verification.Value
		wantSpan  string
		gotSpan   string
	}{
		{name: "negated vegan", attribute: "vegan", expected: verification.BooleanValue(true), wantSpan: "vegan", gotSpan: "not vegan"},
		{name: "negated water resistance", attribute: "water_resistant", expected: verification.BooleanValue(true), wantSpan: "water resistant", gotSpan: "not water resistant"},
		{name: "empty expected span", attribute: "vegan", expected: verification.BooleanValue(true), wantSpan: "", gotSpan: "vegan"},
		{name: "empty actual span", attribute: "vegan", expected: verification.BooleanValue(true), wantSpan: "vegan", gotSpan: ""},
		{name: "ungrounded unrelated span", attribute: "vegan", expected: verification.BooleanValue(true), wantSpan: "vegan", gotSpan: "leather"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			expected := PurchaseIntent{Requirements: []Requirement{{Attribute: test.attribute, Operator: verification.Equals, Expected: test.expected, Severity: verification.Hard, SourceText: test.wantSpan}}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}}
			actual := PurchaseIntent{Requirements: []Requirement{{Attribute: test.attribute, Operator: verification.Equals, Expected: test.expected, Severity: verification.Hard, SourceText: test.gotSpan}}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}}
			if exactIntentMatch(expected, actual) {
				t.Fatalf("conflicting or empty spans matched: expected %q, actual %q", test.wantSpan, test.gotSpan)
			}
		})
	}
}

func TestIntentEvaluationRejectsEmptyOrUngroundedModelSourceText(t *testing.T) {
	for _, sourceText := range []string{"", "leather"} {
		t.Run("source="+sourceText, func(t *testing.T) {
			response, err := json.Marshal(modelOutput{
				Requirements:           ptr([]modelRequirement{{Attribute: "vegan", Operator: verification.Equals, Expected: verification.BooleanValue(true), Severity: verification.Hard, SourceText: sourceText}}),
				ClarificationQuestions: ptr([]modelClarification{}),
				UnsupportedConstraints: ptr([]modelUnsupported{}),
			})
			if err != nil {
				t.Fatal(err)
			}
			item := IntentCase{ID: "source-check", Request: "vegan bag", ReferenceTime: "2026-10-28T10:00:00Z", ExpectedIntent: PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}}}
			if _, err := EvaluateIntentCases(context.Background(), Interpreter{Client: mappedModel{string(response)}}, []IntentCase{item}); err == nil {
				t.Fatalf("invalid source text %q was accepted", sourceText)
			}
		})
	}
}

func TestExactIntentComparisonAllowsGroundedSpanVariantsButKeepsSemanticDistinctions(t *testing.T) {
	expected := PurchaseIntent{
		Requirements:           []Requirement{{Attribute: "vegan", Operator: verification.Equals, Expected: verification.BooleanValue(true), Severity: verification.Hard, SourceText: "vegan"}},
		ClarificationQuestions: []ClarificationQuestion{{Question: "Is this hard or a preference?", SourceText: "color should be red"}},
		UnsupportedConstraints: []UnsupportedConstraint{},
	}
	actual := PurchaseIntent{
		Requirements:           []Requirement{{Attribute: "vegan", Operator: verification.Equals, Expected: verification.BooleanValue(true), Severity: verification.Hard, SourceText: "Find a vegan bag"}},
		ClarificationQuestions: []ClarificationQuestion{{Question: "Is the red color requirement a hard requirement or a preference?", SourceText: "The color should be red."}},
		UnsupportedConstraints: []UnsupportedConstraint{},
	}
	if !exactIntentMatch(expected, actual) {
		t.Fatal("grounded span and equivalent severity question wording should match")
	}

	actual.Requirements[0].Severity = verification.Preference
	if exactIntentMatch(expected, actual) {
		t.Fatal("hard versus preference distinction was ignored")
	}
	actual.Requirements[0].Severity = verification.Hard
	actual.ClarificationQuestions[0].Question = "Which color should the product be?"
	if exactIntentMatch(expected, actual) {
		t.Fatal("different clarification focus was ignored")
	}
}

func TestIntentEvaluationReportsMismatchesAndCounts(t *testing.T) {
	item := IntentCase{
		ID: "case-unsupported", Request: "vegan with lifetime repairs", ReferenceTime: "2026-10-28T10:00:00Z",
		ExpectedIntent: PurchaseIntent{
			Requirements:           []Requirement{{Attribute: "vegan", Operator: verification.Equals, Expected: verification.BooleanValue(true), Severity: verification.Hard, SourceText: "vegan"}},
			ClarificationQuestions: []ClarificationQuestion{{Question: "Do you mean every product?", SourceText: "vegan"}},
			UnsupportedConstraints: []UnsupportedConstraint{{SourceText: "lifetime repairs", Reason: "Service policy is not supported."}},
		},
	}
	response := `{"requirements":[],"clarification_questions":[],"unsupported_constraints":[{"source_text":"vegan","reason":"Incorrectly treated as unsupported for this mismatch fixture."},{"source_text":"lifetime repairs","reason":"Service policy is not supported."}]}`
	summary, err := EvaluateIntentCases(context.Background(), Interpreter{Client: mappedModel{response}}, []IntentCase{item})
	if err != nil {
		t.Fatal(err)
	}
	if summary.AllExact() || summary.ExactRequests != 0 || summary.ClarificationCorrect != 0 {
		t.Fatalf("mismatches not reported: %#v", summary)
	}
	if summary.Constraints.FalseNegative != 1 || summary.Unsupported.TruePositive != 1 || summary.Unsupported.FalseNegative != 0 {
		t.Fatalf("exact constraint/unsupported metrics incorrect: %#v", summary)
	}
	var output strings.Builder
	WriteIntentEvalSummary(&output, summary, "test-model", "mock.example")
	for _, want := range []string{"Intent evaluation cases run: 1", "test-model", "mock.example", "Mismatch case-unsupported", "expected:", "actual:"} {
		if !strings.Contains(output.String(), want) {
			t.Errorf("summary missing %q: %s", want, output.String())
		}
	}
}

func TestUnsupportedMetricMatchesGroundedSpanIndependentOfReasonWording(t *testing.T) {
	item := IntentCase{
		ID: "case-unsupported-paraphrase", Request: "A brand that guarantees lifetime repairs", ReferenceTime: "2026-10-28T10:00:00Z",
		ExpectedIntent: PurchaseIntent{
			Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{},
			UnsupportedConstraints: []UnsupportedConstraint{{SourceText: "guarantees lifetime repairs", Reason: "Brand policies are unsupported."}},
		},
	}
	actual := PurchaseIntent{
		Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{},
		UnsupportedConstraints: []UnsupportedConstraint{{SourceText: "A brand that guarantees lifetime repairs", Reason: "Service guarantees are outside the schema."}},
	}
	summary := IntentEvalSummary{}
	addIntentCaseResult(&summary, item, actual)
	if summary.Unsupported != (Counts{TruePositive: 1}) {
		t.Fatalf("unsupported metric counted a reason paraphrase as a detection miss: %#v", summary.Unsupported)
	}
}

func TestIntentEvaluationCorpusLoadsAtLeastFortyStaticCases(t *testing.T) {
	file, err := os.Open("../../evals/intent-cases.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cases, err := LoadIntentCases(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 40 {
		t.Fatalf("intent corpus has %d cases, want at least 40", len(cases))
	}
	seen := make(map[string]bool)
	for _, item := range cases {
		seen[item.ID] = true
	}
	if len(seen) != len(cases) {
		t.Fatalf("intent corpus has duplicate case IDs")
	}
}

func TestVersionedIntentCorpusPreservesOriginalAndClarifiesAmbiguousSeverityFixtures(t *testing.T) {
	load := func(path string) []IntentCase {
		file, err := os.Open(path)
		if err != nil {
			t.Fatal(err)
		}
		defer file.Close()
		cases, err := LoadIntentCases(file)
		if err != nil {
			t.Fatalf("load %s: %v", path, err)
		}
		return cases
	}
	original := load("../../evals/intent-cases.jsonl")
	corrected := load("../../evals/intent-cases-v2.jsonl")
	if len(original) != 50 || len(corrected) != 50 {
		t.Fatalf("corpus sizes original=%d corrected=%d, want 50 each", len(original), len(corrected))
	}
	for _, index := range []int{8, 12, 18, 22} {
		if len(original[index].ExpectedIntent.Requirements) != 1 || len(original[index].ExpectedIntent.ClarificationQuestions) != 0 {
			t.Fatalf("original fixture %s was not preserved", original[index].ID)
		}
		if len(corrected[index].ExpectedIntent.Requirements) != 0 || len(corrected[index].ExpectedIntent.ClarificationQuestions) != 1 {
			t.Fatalf("corrected fixture %s does not ask for severity clarification", corrected[index].ID)
		}
	}
}

func TestIntentCorpusRejectsDuplicateKeysAndMissingArrays(t *testing.T) {
	for _, text := range []string{
		`{"id":"x","id":"y","request":"vegan","reference_time":"2026-10-28T00:00:00Z","expected_intent":{"requirements":[],"clarification_questions":[],"unsupported_constraints":[]}}`,
		`{"id":"x","request":"vegan","reference_time":"2026-10-28T00:00:00Z","expected_intent":{"requirements":[],"clarification_questions":[]}}`,
	} {
		if _, err := LoadIntentCases(strings.NewReader(text)); err == nil {
			t.Fatalf("invalid intent case accepted: %s", text)
		}
	}
}

func TestMetricFormattingHandlesZeroDenominators(t *testing.T) {
	if got := metricRatio(0, 0); got != "n/a (0/0)" {
		t.Fatalf("zero ratio = %s", got)
	}
	precision, recall, f1 := precisionRecallF1(Counts{FalsePositive: 1, FalseNegative: 1})
	if precision != "0.0% (0/1)" || recall != "0.0% (0/1)" || f1 != "0.0%" {
		t.Fatalf("zero-score metrics = %q %q %q", precision, recall, f1)
	}
	if _, err := json.Marshal(PurchaseIntent{Requirements: []Requirement{}}); err != nil {
		t.Fatal(err)
	}
}
