package evaluation

import (
	"bytes"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/bhavika44/buyrightagents/internal/verification"
)

const matchingLine = `{"id":"case-1","category":"baseline","description":"A hard claim passes.","input":{"cart_snapshot":{"cart_id":"cart-1","currency":"INR","captured_at":"2026-10-28T10:00:00Z","items":[{"item_id":"item-1","product_id":"product-1","variant_id":"variant-1","quantity":1,"unit_price_paise":1000}],"shipping_paise":0,"discount_paise":0},"requirements":[{"id":"r1","attribute":"vegan","scope":"item","subject_id":"item-1","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard"}],"evidence":[{"id":"e1","attribute":"vegan","scope":"item","subject_id":"item-1","product_id":"product-1","variant_id":"variant-1","value":{"type":"boolean","value":true},"source_id":"fictional-source","observed_at":"2026-10-28T09:55:00Z"}]},"expected_decision":"ALLOW","expected_outcomes":{"r1":"PASS"}}`

func loadTestCases(t *testing.T, text string) []Case {
	t.Helper()
	cases, err := LoadCases(strings.NewReader(text))
	if err != nil {
		t.Fatal(err)
	}
	return cases
}

func cloneCase(item Case) Case {
	item.Input.Requirements = append([]verification.Requirement(nil), item.Input.Requirements...)
	item.Input.Evidence = append([]verification.Evidence(nil), item.Input.Evidence...)
	outcomes := make(map[string]verification.Outcome, len(item.ExpectedOutcomes))
	for id, outcome := range item.ExpectedOutcomes {
		outcomes[id] = outcome
	}
	item.ExpectedOutcomes = outcomes
	return item
}

func TestMatchingScenarioPassesHarness(t *testing.T) {
	summary, err := Run(loadTestCases(t, matchingLine))
	if err != nil {
		t.Fatal(err)
	}
	if summary.DecisionMatches != 1 || summary.RequirementMatches != 1 || summary.ValidCases != 1 {
		t.Fatalf("unexpected summary: %#v", summary)
	}
	if summary.Cases[0].ActualDecision != verification.Allow || summary.Cases[0].ActualOutcomes["r1"] != verification.Pass {
		t.Fatalf("unexpected result: %#v", summary.Cases[0])
	}
}

func TestHarnessRepresentsEachDecisionAndOutcome(t *testing.T) {
	cases := []Case{
		loadTestCases(t, matchingLine)[0],
	}
	block := cloneCase(cases[0])
	block.ID, block.Input.Requirements[0].ID, block.Input.Evidence[0].ID = "block", "rb", "eb"
	block.Input.Evidence[0].Value = verification.BooleanValue(false)
	block.ExpectedDecision, block.ExpectedOutcomes = verification.Block, map[string]verification.Outcome{"rb": verification.Fail}
	review := cloneCase(cases[0])
	review.ID, review.Input.Requirements[0].ID = "review", "rr"
	review.Input.Evidence = nil
	review.ExpectedDecision, review.ExpectedOutcomes = verification.Review, map[string]verification.Outcome{"rr": verification.Unknown}
	cases = append(cases, block, review)
	summary, err := Run(cases)
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []verification.Decision{verification.Allow, verification.Block, verification.Review} {
		if summary.Confusion[expected][expected] != 1 {
			t.Fatalf("expected decision %s not represented: %#v", expected, summary.Confusion)
		}
	}
	seen := map[verification.Outcome]bool{}
	for _, result := range summary.Cases {
		for _, outcome := range result.ActualOutcomes {
			seen[outcome] = true
		}
	}
	for _, outcome := range []verification.Outcome{verification.Pass, verification.Fail, verification.Unknown} {
		if !seen[outcome] {
			t.Fatalf("expected requirement outcome %s not represented", outcome)
		}
	}
}

func TestDecisionAndRequirementMismatchesAreDetected(t *testing.T) {
	t.Run("decision mismatch", func(t *testing.T) {
		item := loadTestCases(t, matchingLine)[0]
		item.ExpectedDecision = verification.Block
		summary, err := Run([]Case{item})
		if err != nil {
			t.Fatal(err)
		}
		if summary.Cases[0].DecisionMatches || summary.DecisionMatches != 0 {
			t.Fatalf("decision mismatch was not detected: %#v", summary.Cases[0])
		}
	})
	t.Run("outcome mismatch", func(t *testing.T) {
		item := loadTestCases(t, matchingLine)[0]
		item.ExpectedOutcomes["r1"] = verification.Fail
		summary, err := Run([]Case{item})
		if err != nil {
			t.Fatal(err)
		}
		if mismatch, ok := summary.Cases[0].MismatchedOutcomes["r1"]; !ok || mismatch.Expected != verification.Fail || mismatch.Actual != verification.Pass {
			t.Fatalf("requirement mismatch was not detected: %#v", summary.Cases[0])
		}
	})
}

func TestLoadCasesRejectsMalformedSchemas(t *testing.T) {
	tests := []struct {
		name string
		text string
		want string
	}{
		{"unknown case field", strings.TrimSuffix(matchingLine, "}") + `,"unexpected":true}`, "unknown field"},
		{"missing case field", `{"id":"x"}`, "requires id, category"},
		{"duplicate JSON key", strings.Replace(matchingLine, `"id":"case-1"`, `"id":"case-1","id":"case-2"`, 1), "duplicate JSON object key"},
		{"duplicate nested JSON key", strings.Replace(matchingLine, `"cart_id":"cart-1"`, `"cart_id":"cart-1","cart_id":"cart-2"`, 1), "duplicate JSON object key"},
		{"unknown input field", strings.Replace(matchingLine, `"cart_id":"cart-1"`, `"cart_id":"cart-1","rogue":true`, 1), "unknown field"},
		{"malformed JSONL", `{"id":`, "EOF"},
		{"blank line", matchingLine + "\n\n", "line 2: empty JSONL lines"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := LoadCases(strings.NewReader(tt.text))
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestLoadCasesRejectsDuplicateIDsAndEmptyDataset(t *testing.T) {
	if _, err := LoadCases(strings.NewReader(matchingLine + "\n" + matchingLine)); err == nil || !strings.Contains(err.Error(), "duplicate case id") {
		t.Fatalf("duplicate ID error = %v", err)
	}
	if _, err := LoadCases(strings.NewReader("  \n")); err == nil || !strings.Contains(err.Error(), "empty JSONL lines") {
		t.Fatalf("blank dataset error = %v", err)
	}
	if _, err := LoadCases(strings.NewReader("")); err == nil || !strings.Contains(err.Error(), "dataset is empty") {
		t.Fatalf("empty dataset error = %v", err)
	}
}

func TestExpectedOutcomesMustExactlyMatchRequirements(t *testing.T) {
	for _, tt := range []struct {
		name     string
		outcomes map[string]verification.Outcome
		want     string
	}{
		{"missing", map[string]verification.Outcome{}, "missing requirement \"r1\""},
		{"extra", map[string]verification.Outcome{"r1": verification.Pass, "extra": verification.Pass}, "extra requirement \"extra\""},
		{"invalid outcome", map[string]verification.Outcome{"r1": "MAYBE"}, "invalid expected outcome"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			item := loadTestCases(t, matchingLine)[0]
			item.ExpectedOutcomes = tt.outcomes
			_, err := Run([]Case{item})
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("Run error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func TestCommittedCorpusLoadsAndMatchesLabels(t *testing.T) {
	file, err := os.Open("../../evals/cases.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer file.Close()
	cases, err := LoadCases(file)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) < 64 {
		t.Fatalf("corpus has %d cases, want at least 64", len(cases))
	}
	counts := map[string]int{}
	for _, item := range cases {
		counts[item.Category]++
	}
	for category, minimum := range map[string]int{
		"baseline": 8, "hard_failure": 8, "total_price": 10, "delivery": 8,
		"missing_contradictory": 8, "item_scoping": 10, "preferences": 6, "mixed": 6,
	} {
		if counts[category] < minimum {
			t.Errorf("category %s has %d cases, want at least %d", category, counts[category], minimum)
		}
	}
	summary, err := Run(cases)
	if err != nil {
		t.Fatal(err)
	}
	if summary.ValidCases != len(cases) || summary.DecisionMatches != len(cases) || summary.RequirementMatches != summary.RequirementOutcomes {
		var output bytes.Buffer
		WriteSummary(&output, summary)
		t.Fatalf("committed labels differ from verifier results:\n%s", output.String())
	}
}

func TestWriteSummaryHandlesZeroDenominators(t *testing.T) {
	var output bytes.Buffer
	WriteSummary(&output, Summary{})
	if !strings.Contains(output.String(), "n/a (0/0)") || strings.Contains(output.String(), "NaN") {
		t.Fatalf("zero denominators are not rendered clearly: %s", output.String())
	}
}

func Exampleratio() {
	fmt.Println(ratio(2, 4))
	fmt.Println(ratio(0, 0))
	// Output:
	// 50.0% (2/4)
	// n/a (0/0)
}
