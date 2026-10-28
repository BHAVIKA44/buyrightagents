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
	response := `{"requirements":[{"attribute":"color","operator":"equals","expected":{"type":"string","value":"red"},"severity":"preference","source_text":"Ideally RED!"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"}],"clarification_questions":[],"unsupported_constraints":[]}`
	summary, err := EvaluateIntentCases(context.Background(), Interpreter{Client: mappedModel{response}}, []IntentCase{item})
	if err != nil {
		t.Fatal(err)
	}
	if !summary.AllExact() || summary.Constraints.TruePositive != 2 || summary.Constraints.FalsePositive != 0 || summary.Constraints.FalseNegative != 0 {
		t.Fatalf("IDs/order affected comparison: %#v", summary)
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
	response := `{"requirements":[],"clarification_questions":[],"unsupported_constraints":[{"source_text":"lifetime repairs","reason":"Service policy is not supported."}]}`
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
