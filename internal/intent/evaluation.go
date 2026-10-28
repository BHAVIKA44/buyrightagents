package intent

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/bhavika44/buyrightagents/internal/verification"
)

type IntentCase struct {
	ID             string         `json:"id"`
	Request        string         `json:"request"`
	ReferenceTime  string         `json:"reference_time"`
	ExpectedIntent PurchaseIntent `json:"expected_intent"`
}

type IntentCaseResult struct {
	Case            IntentCase
	Actual          PurchaseIntent
	RequestExact    bool
	ClarificationOK bool
}

type Counts struct {
	TruePositive  int
	FalsePositive int
	FalseNegative int
}

type IntentEvalSummary struct {
	Results              []IntentCaseResult
	CasesRun             int
	ExactRequests        int
	ClarificationCorrect int
	Constraints          Counts
	Unsupported          Counts
}

func (summary IntentEvalSummary) AllExact() bool {
	if summary.CasesRun == 0 || summary.ExactRequests != summary.CasesRun {
		return false
	}
	for _, result := range summary.Results {
		if !result.RequestExact || !result.ClarificationOK {
			return false
		}
	}
	return true
}

func LoadIntentCases(reader io.Reader) ([]IntentCase, error) {
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	var cases []IntentCase
	ids := make(map[string]struct{})
	line := 0
	for scanner.Scan() {
		line++
		data := bytes.TrimSpace(scanner.Bytes())
		if len(data) == 0 {
			return nil, fmt.Errorf("line %d: blank JSONL line", line)
		}
		if err := rejectDuplicateJSONKeys(data); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		var wire struct {
			ID             *string         `json:"id"`
			Request        *string         `json:"request"`
			ReferenceTime  *string         `json:"reference_time"`
			ExpectedIntent json.RawMessage `json:"expected_intent"`
		}
		decoder := json.NewDecoder(bytes.NewReader(data))
		decoder.DisallowUnknownFields()
		decoder.UseNumber()
		if err := decoder.Decode(&wire); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if err := requireJSONEOF(decoder); err != nil {
			return nil, fmt.Errorf("line %d: %w", line, err)
		}
		if wire.ID == nil || strings.TrimSpace(*wire.ID) == "" || wire.Request == nil || strings.TrimSpace(*wire.Request) == "" || wire.ReferenceTime == nil || len(wire.ExpectedIntent) == 0 {
			return nil, fmt.Errorf("line %d: id, request, reference_time, and expected_intent are required", line)
		}
		if _, exists := ids[*wire.ID]; exists {
			return nil, fmt.Errorf("line %d: duplicate intent case id %q", line, *wire.ID)
		}
		ids[*wire.ID] = struct{}{}
		reference, err := time.Parse(time.RFC3339Nano, *wire.ReferenceTime)
		if err != nil {
			return nil, fmt.Errorf("line %d case %q: invalid reference_time: %w", line, *wire.ID, err)
		}
		var expected PurchaseIntent
		intentDecoder := json.NewDecoder(bytes.NewReader(wire.ExpectedIntent))
		intentDecoder.DisallowUnknownFields()
		intentDecoder.UseNumber()
		if err := intentDecoder.Decode(&expected); err != nil {
			return nil, fmt.Errorf("line %d case %q expected_intent: %w", line, *wire.ID, err)
		}
		if err := requireJSONEOF(intentDecoder); err != nil {
			return nil, fmt.Errorf("line %d case %q expected_intent: %w", line, *wire.ID, err)
		}
		if expected.Requirements == nil || expected.ClarificationQuestions == nil || expected.UnsupportedConstraints == nil {
			return nil, fmt.Errorf("line %d case %q: expected_intent requires all three arrays", line, *wire.ID)
		}
		modelOutput := toModelOutput(expected)
		if _, err := validateOutput(modelOutput, *wire.Request, reference); err != nil {
			return nil, fmt.Errorf("line %d case %q expected_intent invalid: %w", line, *wire.ID, err)
		}
		cases = append(cases, IntentCase{ID: *wire.ID, Request: *wire.Request, ReferenceTime: *wire.ReferenceTime, ExpectedIntent: expected})
	}
	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("read intent case corpus: %w", err)
	}
	if len(cases) == 0 {
		return nil, errors.New("intent case corpus is empty")
	}
	return cases, nil
}

func requireJSONEOF(decoder *json.Decoder) error {
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return errors.New("multiple JSON values are not allowed")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func toModelOutput(intent PurchaseIntent) modelOutput {
	requirements := make([]modelRequirement, len(intent.Requirements))
	for index, requirement := range intent.Requirements {
		requirements[index] = modelRequirement{
			Attribute: requirement.Attribute, Operator: requirement.Operator,
			Expected: requirement.Expected, Severity: requirement.Severity, SourceText: requirement.SourceText,
		}
	}
	clarifications := make([]modelClarification, len(intent.ClarificationQuestions))
	for index, question := range intent.ClarificationQuestions {
		clarifications[index] = modelClarification{Question: question.Question, SourceText: question.SourceText}
	}
	unsupported := make([]modelUnsupported, len(intent.UnsupportedConstraints))
	for index, constraint := range intent.UnsupportedConstraints {
		unsupported[index] = modelUnsupported{SourceText: constraint.SourceText, Reason: constraint.Reason}
	}
	return modelOutput{Requirements: &requirements, ClarificationQuestions: &clarifications, UnsupportedConstraints: &unsupported}
}

func EvaluateIntentCases(ctx context.Context, interpreter Interpreter, cases []IntentCase) (IntentEvalSummary, error) {
	if len(cases) == 0 {
		return IntentEvalSummary{}, errors.New("intent case corpus is empty")
	}
	summary := IntentEvalSummary{Results: make([]IntentCaseResult, 0, len(cases))}
	for _, item := range cases {
		actual, err := interpreter.Interpret(ctx, item.Request, item.ReferenceTime)
		if err != nil {
			return IntentEvalSummary{}, fmt.Errorf("intent case %q: %w", item.ID, err)
		}
		exact := exactIntentMatch(item.ExpectedIntent, actual)
		clarificationOK := (len(item.ExpectedIntent.ClarificationQuestions) > 0) == (len(actual.ClarificationQuestions) > 0)
		if exact {
			summary.ExactRequests++
		}
		if clarificationOK {
			summary.ClarificationCorrect++
		}
		addCounts(&summary.Constraints, requirementKeys(item.ExpectedIntent.Requirements), requirementKeys(actual.Requirements))
		addCounts(&summary.Unsupported, unsupportedKeys(item.ExpectedIntent.UnsupportedConstraints), unsupportedKeys(actual.UnsupportedConstraints))
		summary.CasesRun++
		summary.Results = append(summary.Results, IntentCaseResult{Case: item, Actual: actual, RequestExact: exact, ClarificationOK: clarificationOK})
	}
	return summary, nil
}

func exactIntentMatch(expected, actual PurchaseIntent) bool {
	return sameStrings(fullRequirementKeys(expected.Requirements), fullRequirementKeys(actual.Requirements)) &&
		sameStrings(clarificationKeys(expected.ClarificationQuestions), clarificationKeys(actual.ClarificationQuestions)) &&
		sameStrings(unsupportedKeys(expected.UnsupportedConstraints), unsupportedKeys(actual.UnsupportedConstraints))
}

func requirementKeys(requirements []Requirement) []string {
	keys := make([]string, len(requirements))
	for index, requirement := range requirements {
		keys[index] = strings.Join([]string{requirement.Attribute, string(requirement.Operator), valueKey(requirement.Expected), string(requirement.Severity)}, "\x1f")
	}
	sort.Strings(keys)
	return keys
}

func fullRequirementKeys(requirements []Requirement) []string {
	keys := make([]string, len(requirements))
	for index, requirement := range requirements {
		keys[index] = strings.Join([]string{requirement.Attribute, string(requirement.Operator), valueKey(requirement.Expected), string(requirement.Severity), normalizeText(requirement.SourceText)}, "\x1f")
	}
	sort.Strings(keys)
	return keys
}

func valueKey(value verification.Value) string {
	if value.Type == verification.NumberType {
		if rational, ok := numberRat(value.Value); ok {
			return string(value.Type) + ":" + rational.RatString()
		}
	}
	encoded, _ := json.Marshal(value.Value)
	return string(value.Type) + ":" + string(encoded)
}

func clarificationKeys(questions []ClarificationQuestion) []string {
	keys := make([]string, len(questions))
	for index, question := range questions {
		keys[index] = normalizeText(question.Question) + "\x1f" + normalizeText(question.SourceText)
	}
	sort.Strings(keys)
	return keys
}

func unsupportedKeys(constraints []UnsupportedConstraint) []string {
	keys := make([]string, len(constraints))
	for index, constraint := range constraints {
		keys[index] = normalizeText(constraint.SourceText) + "\x1f" + normalizeText(constraint.Reason)
	}
	sort.Strings(keys)
	return keys
}

func sameStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}

func addCounts(counts *Counts, expected, actual []string) {
	remaining := make(map[string]int, len(actual))
	for _, key := range actual {
		remaining[key]++
	}
	for _, key := range expected {
		if remaining[key] > 0 {
			counts.TruePositive++
			remaining[key]--
		} else {
			counts.FalseNegative++
		}
	}
	for _, count := range remaining {
		counts.FalsePositive += count
	}
}

func metricRatio(numerator, denominator int) string {
	if denominator == 0 {
		return fmt.Sprintf("n/a (%d/%d)", numerator, denominator)
	}
	return fmt.Sprintf("%.1f%% (%d/%d)", 100*float64(numerator)/float64(denominator), numerator, denominator)
}

func precisionRecallF1(counts Counts) (precision, recall, f1 string) {
	precisionDenominator := counts.TruePositive + counts.FalsePositive
	recallDenominator := counts.TruePositive + counts.FalseNegative
	precision = metricRatio(counts.TruePositive, precisionDenominator)
	recall = metricRatio(counts.TruePositive, recallDenominator)
	if precisionDenominator == 0 && recallDenominator == 0 {
		return precision, recall, "n/a"
	}
	if counts.TruePositive == 0 {
		return precision, recall, "0.0%"
	}
	p := float64(counts.TruePositive) / float64(precisionDenominator)
	r := float64(counts.TruePositive) / float64(recallDenominator)
	f1 = fmt.Sprintf("%.1f%%", 200*p*r/(p+r))
	return
}

func WriteIntentEvalSummary(writer io.Writer, summary IntentEvalSummary, model, endpointHost string) {
	fmt.Fprintf(writer, "Intent evaluation cases run: %d\nModel: %s\nEndpoint host: %s\n", summary.CasesRun, model, endpointHost)
	fmt.Fprintf(writer, "Exact request-level interpretation matches: %s\n", metricRatio(summary.ExactRequests, summary.CasesRun))
	fmt.Fprintf(writer, "Clarification detection accuracy: %s\n", metricRatio(summary.ClarificationCorrect, summary.CasesRun))
	precision, recall, f1 := precisionRecallF1(summary.Constraints)
	fmt.Fprintf(writer, "Constraint exact-match precision / recall / F1: %s / %s / %s (TP=%d FP=%d FN=%d)\n", precision, recall, f1, summary.Constraints.TruePositive, summary.Constraints.FalsePositive, summary.Constraints.FalseNegative)
	precision, recall, f1 = precisionRecallF1(summary.Unsupported)
	fmt.Fprintf(writer, "Unsupported-constraint exact-match precision / recall / F1: %s / %s / %s (TP=%d FP=%d FN=%d)\n", precision, recall, f1, summary.Unsupported.TruePositive, summary.Unsupported.FalsePositive, summary.Unsupported.FalseNegative)
	for _, result := range summary.Results {
		if result.RequestExact && result.ClarificationOK {
			continue
		}
		expected, _ := json.MarshalIndent(result.Case.ExpectedIntent, "  ", "  ")
		actual, _ := json.MarshalIndent(result.Actual, "  ", "  ")
		fmt.Fprintf(writer, "\nMismatch %s\n  request: %s\n  expected: %s\n  actual: %s\n", result.Case.ID, result.Case.Request, expected, actual)
	}
}
