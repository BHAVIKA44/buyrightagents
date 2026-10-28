// Package evaluation loads manually labelled verifier scenarios and reports
// deterministic aggregate and per-case results.
package evaluation

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"

	"github.com/bhavika44/buyrightagents/internal/verification"
)

type Case struct {
	ID               string                          `json:"id"`
	Category         string                          `json:"category"`
	Description      string                          `json:"description"`
	Input            verification.Input              `json:"input"`
	ExpectedDecision verification.Decision           `json:"expected_decision"`
	ExpectedOutcomes map[string]verification.Outcome `json:"expected_outcomes"`
}

type CaseResult struct {
	Case               Case
	ActualDecision     verification.Decision
	ActualOutcomes     map[string]verification.Outcome
	DecisionMatches    bool
	MismatchedOutcomes map[string]OutcomeMismatch
}

type OutcomeMismatch struct {
	Expected verification.Outcome
	Actual   verification.Outcome
}

type CategoryMetric struct {
	Cases   int
	Correct int
}

type Summary struct {
	Cases               []CaseResult
	ValidCases          int
	DecisionMatches     int
	RequirementOutcomes int
	RequirementMatches  int
	ExpectedNonAllow    int
	FalseAllows         int
	ExpectedBlocks      int
	MissedBlocks        int
	ExpectedNonBlock    int
	FalseBlocks         int
	ActualReviews       int
	Confusion           map[verification.Decision]map[verification.Decision]int
	Categories          map[string]CategoryMetric
}

// LoadCases reads one complete, strictly decoded scenario from every JSONL
// line. Empty lines are invalid; no case is silently skipped.
func LoadCases(reader io.Reader) ([]Case, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var cases []Case
	ids := make(map[string]struct{})
	line := 0
	for scanner.Scan() {
		line++
		data := bytes.TrimSpace(scanner.Bytes())
		if len(data) == 0 {
			return nil, fmt.Errorf("line %d: empty JSONL lines are not allowed", line)
		}
		item, err := decodeCase(data)
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if strings.TrimSpace(item.ID) == "" {
			return nil, fmt.Errorf("line %d: case id is required", line)
		}
		if _, exists := ids[item.ID]; exists {
			return nil, fmt.Errorf("line %d: duplicate case id %q", line, item.ID)
		}
		ids[item.ID] = struct{}{}
		if strings.TrimSpace(item.Category) == "" {
			return nil, fmt.Errorf("line %d case %q: category is required", line, item.ID)
		}
		if strings.TrimSpace(item.Description) == "" {
			return nil, fmt.Errorf("line %d case %q: description is required", line, item.ID)
		}
		if item.ExpectedOutcomes == nil {
			return nil, fmt.Errorf("line %d case %q: expected_outcomes mapping is required", line, item.ID)
		}
		if !validDecision(item.ExpectedDecision) {
			return nil, fmt.Errorf("line %d case %q: invalid expected_decision %q", line, item.ID, item.ExpectedDecision)
		}
		cases = append(cases, item)
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read JSONL dataset: %w", err)
	}
	if len(cases) == 0 {
		return nil, errors.New("evaluation dataset is empty")
	}
	return cases, nil
}

func decodeCase(data []byte) (Case, error) {
	if err := rejectDuplicateKeys(data); err != nil {
		return Case{}, err
	}
	var wire struct {
		ID               *string                          `json:"id"`
		Category         *string                          `json:"category"`
		Description      *string                          `json:"description"`
		Input            json.RawMessage                  `json:"input"`
		ExpectedDecision *verification.Decision           `json:"expected_decision"`
		ExpectedOutcomes *map[string]verification.Outcome `json:"expected_outcomes"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&wire); err != nil {
		return Case{}, err
	}
	if err := requireEOF(decoder); err != nil {
		return Case{}, err
	}
	if wire.ID == nil || wire.Category == nil || wire.Description == nil || wire.ExpectedDecision == nil || wire.ExpectedOutcomes == nil || len(wire.Input) == 0 {
		return Case{}, errors.New("case requires id, category, description, input, expected_decision, and expected_outcomes")
	}
	input, err := decodeInput(wire.Input)
	if err != nil {
		return Case{}, fmt.Errorf("input: %w", err)
	}
	return Case{
		ID: *wire.ID, Category: *wire.Category, Description: *wire.Description,
		Input: input, ExpectedDecision: *wire.ExpectedDecision, ExpectedOutcomes: *wire.ExpectedOutcomes,
	}, nil
}

func decodeInput(data []byte) (verification.Input, error) {
	var wire struct {
		CartSnapshot json.RawMessage             `json:"cart_snapshot"`
		Requirements *[]verification.Requirement `json:"requirements"`
		Evidence     *[]verification.Evidence    `json:"evidence"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&wire); err != nil {
		return verification.Input{}, err
	}
	if err := requireEOF(decoder); err != nil {
		return verification.Input{}, err
	}
	if len(wire.CartSnapshot) == 0 || wire.Requirements == nil || wire.Evidence == nil {
		return verification.Input{}, errors.New("input requires cart_snapshot, requirements, and evidence")
	}
	var snapshot verification.CartSnapshot
	snapshotDecoder := json.NewDecoder(bytes.NewReader(wire.CartSnapshot))
	snapshotDecoder.DisallowUnknownFields()
	snapshotDecoder.UseNumber()
	if err := snapshotDecoder.Decode(&snapshot); err != nil {
		return verification.Input{}, fmt.Errorf("cart_snapshot: %w", err)
	}
	if err := requireEOF(snapshotDecoder); err != nil {
		return verification.Input{}, err
	}
	return verification.Input{CartSnapshot: snapshot, Requirements: *wire.Requirements, Evidence: *wire.Evidence}, nil
}

func requireEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("multiple JSON values are not allowed")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func rejectDuplicateKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	return requireEOF(decoder)
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	switch delim {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			token, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := token.(string)
			if !ok {
				return errors.New("JSON object key is not a string")
			}
			if _, exists := keys[key]; exists {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			keys[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return errors.New("malformed JSON object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return errors.New("malformed JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delim)
	}
	return nil
}

func validDecision(decision verification.Decision) bool {
	return decision == verification.Allow || decision == verification.Block || decision == verification.Review
}

func validOutcome(outcome verification.Outcome) bool {
	return outcome == verification.Pass || outcome == verification.Fail || outcome == verification.Unknown
}

// Run evaluates every valid case and compares all expected labels. Invalid
// inputs return an error; label mismatches remain in the summary for reporting.
func Run(cases []Case) (Summary, error) {
	if len(cases) == 0 {
		return Summary{}, errors.New("evaluation dataset is empty")
	}
	summary := Summary{
		Cases:      make([]CaseResult, 0, len(cases)),
		Confusion:  make(map[verification.Decision]map[verification.Decision]int),
		Categories: make(map[string]CategoryMetric),
	}
	ids := make(map[string]struct{}, len(cases))
	for _, item := range cases {
		if strings.TrimSpace(item.ID) == "" || strings.TrimSpace(item.Category) == "" {
			return Summary{}, errors.New("case id and category are required")
		}
		if _, exists := ids[item.ID]; exists {
			return Summary{}, fmt.Errorf("duplicate case id %q", item.ID)
		}
		ids[item.ID] = struct{}{}
		if !validDecision(item.ExpectedDecision) {
			return Summary{}, fmt.Errorf("case %q has invalid expected decision %q", item.ID, item.ExpectedDecision)
		}
		if item.ExpectedOutcomes == nil {
			return Summary{}, fmt.Errorf("case %q is missing expected_outcomes", item.ID)
		}
		requirementIDs := make(map[string]struct{}, len(item.Input.Requirements))
		for _, requirement := range item.Input.Requirements {
			requirementIDs[requirement.ID] = struct{}{}
		}
		for requirementID := range requirementIDs {
			outcome, exists := item.ExpectedOutcomes[requirementID]
			if !exists {
				return Summary{}, fmt.Errorf("case %q expected_outcomes is missing requirement %q", item.ID, requirementID)
			}
			if !validOutcome(outcome) {
				return Summary{}, fmt.Errorf("case %q requirement %q has invalid expected outcome %q", item.ID, requirementID, outcome)
			}
		}
		for requirementID := range item.ExpectedOutcomes {
			if _, exists := requirementIDs[requirementID]; !exists {
				return Summary{}, fmt.Errorf("case %q expected_outcomes has extra requirement %q", item.ID, requirementID)
			}
		}
		report, err := verification.Evaluate(item.Input)
		if err != nil {
			return Summary{}, fmt.Errorf("case %q (%s): evaluate input: %w", item.ID, item.Category, err)
		}
		result := CaseResult{
			Case: item, ActualDecision: report.Decision, ActualOutcomes: make(map[string]verification.Outcome),
			DecisionMatches:    report.Decision == item.ExpectedDecision,
			MismatchedOutcomes: make(map[string]OutcomeMismatch),
		}
		if result.DecisionMatches {
			summary.DecisionMatches++
		}
		for _, requirementResult := range report.Results {
			result.ActualOutcomes[requirementResult.RequirementID] = requirementResult.Outcome
			if item.ExpectedOutcomes[requirementResult.RequirementID] == requirementResult.Outcome {
				summary.RequirementMatches++
			} else {
				result.MismatchedOutcomes[requirementResult.RequirementID] = OutcomeMismatch{
					Expected: item.ExpectedOutcomes[requirementResult.RequirementID], Actual: requirementResult.Outcome,
				}
			}
			summary.RequirementOutcomes++
		}
		summary.ValidCases++
		if report.Decision == verification.Review {
			summary.ActualReviews++
		}
		if item.ExpectedDecision != verification.Allow {
			summary.ExpectedNonAllow++
			if report.Decision == verification.Allow {
				summary.FalseAllows++
			}
		}
		if item.ExpectedDecision == verification.Block {
			summary.ExpectedBlocks++
			if report.Decision != verification.Block {
				summary.MissedBlocks++
			}
		}
		if item.ExpectedDecision != verification.Block {
			summary.ExpectedNonBlock++
			if report.Decision == verification.Block {
				summary.FalseBlocks++
			}
		}
		if summary.Confusion[item.ExpectedDecision] == nil {
			summary.Confusion[item.ExpectedDecision] = make(map[verification.Decision]int)
		}
		summary.Confusion[item.ExpectedDecision][report.Decision]++
		category := summary.Categories[item.Category]
		category.Cases++
		if result.DecisionMatches {
			category.Correct++
		}
		summary.Categories[item.Category] = category
		summary.Cases = append(summary.Cases, result)
	}
	return summary, nil
}

func ratio(numerator, denominator int) string {
	if denominator == 0 {
		return fmt.Sprintf("n/a (%d/%d)", numerator, denominator)
	}
	return fmt.Sprintf("%.1f%% (%d/%d)", float64(numerator)*100/float64(denominator), numerator, denominator)
}

// WriteSummary prints metric definitions, confusion counts, category accuracy,
// and all label mismatches in deterministic order.
func WriteSummary(writer io.Writer, summary Summary) {
	fmt.Fprintf(writer, "Cases evaluated: %d\n", summary.ValidCases)
	fmt.Fprintf(writer, "Decision accuracy (exact decisions / valid cases): %s\n", ratio(summary.DecisionMatches, summary.ValidCases))
	fmt.Fprintf(writer, "Requirement outcome accuracy (exact outcomes / expected outcomes): %s\n", ratio(summary.RequirementMatches, summary.RequirementOutcomes))
	fmt.Fprintf(writer, "False-ALLOW rate (unexpected ALLOW / expected non-ALLOW cases): %s\n", ratio(summary.FalseAllows, summary.ExpectedNonAllow))
	fmt.Fprintf(writer, "Missed-BLOCK rate (expected BLOCK predicted ALLOW or REVIEW / expected BLOCK cases): %s\n", ratio(summary.MissedBlocks, summary.ExpectedBlocks))
	fmt.Fprintf(writer, "False-BLOCK rate (unexpected BLOCK / expected non-BLOCK cases): %s\n", ratio(summary.FalseBlocks, summary.ExpectedNonBlock))
	fmt.Fprintf(writer, "Review rate (actual REVIEW / valid cases): %s\n", ratio(summary.ActualReviews, summary.ValidCases))
	fmt.Fprintln(writer, "\nDecision confusion matrix (rows=expected, columns=actual):")
	fmt.Fprintln(writer, "expected\\actual  ALLOW  BLOCK  REVIEW")
	decisions := []verification.Decision{verification.Allow, verification.Block, verification.Review}
	for _, expected := range decisions {
		fmt.Fprintf(writer, "%-16s %5d %6d %7d\n", expected,
			summary.Confusion[expected][verification.Allow], summary.Confusion[expected][verification.Block], summary.Confusion[expected][verification.Review])
	}
	fmt.Fprintln(writer, "\nPer-category decision accuracy:")
	categories := make([]string, 0, len(summary.Categories))
	for category := range summary.Categories {
		categories = append(categories, category)
	}
	sort.Strings(categories)
	for _, category := range categories {
		metric := summary.Categories[category]
		fmt.Fprintf(writer, "  %s: %s\n", category, ratio(metric.Correct, metric.Cases))
	}

	mismatchCount := 0
	for _, result := range summary.Cases {
		if !result.DecisionMatches || len(result.MismatchedOutcomes) > 0 {
			mismatchCount++
		}
	}
	if mismatchCount == 0 {
		fmt.Fprintln(writer, "\nMismatches: none")
		return
	}
	fmt.Fprintf(writer, "\nMismatches (%d cases):\n", mismatchCount)
	for _, result := range summary.Cases {
		if result.DecisionMatches && len(result.MismatchedOutcomes) == 0 {
			continue
		}
		fmt.Fprintf(writer, "  %s [%s] expected=%s actual=%s\n", result.Case.ID, result.Case.Category, result.Case.ExpectedDecision, result.ActualDecision)
		keys := make([]string, 0, len(result.MismatchedOutcomes))
		for key := range result.MismatchedOutcomes {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			mismatch := result.MismatchedOutcomes[key]
			fmt.Fprintf(writer, "    requirement %s expected=%s actual=%s\n", key, mismatch.Expected, mismatch.Actual)
		}
	}
}
