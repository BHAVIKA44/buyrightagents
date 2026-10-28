// Package verification evaluates structured purchase requirements against
// explicitly typed evidence. It does not fetch or authenticate evidence.
package verification

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
)

type ValueType string

const (
	StringType  ValueType = "string"
	NumberType  ValueType = "number"
	BooleanType ValueType = "boolean"
	DateType    ValueType = "date"
)

// Value uses an explicit {"type": ..., "value": ...} JSON representation.
// Its value must have the corresponding Go type when constructed directly.
type Value struct {
	Type  ValueType `json:"type"`
	Value any       `json:"value"`
}

func StringValue(value string) Value { return Value{Type: StringType, Value: value} }
func NumberValue(value float64) Value {
	return Value{Type: NumberType, Value: json.Number(strconv.FormatFloat(value, 'g', -1, 64))}
}
func BooleanValue(value bool) Value { return Value{Type: BooleanType, Value: value} }
func DateValue(value string) Value  { return Value{Type: DateType, Value: value} }

type Severity string

const (
	Hard       Severity = "hard"
	Preference Severity = "preference"
)

type Operator string

const (
	Equals             Operator = "equals"
	NotEquals          Operator = "not_equals"
	LessThanOrEqual    Operator = "less_than_or_equal"
	GreaterThanOrEqual Operator = "greater_than_or_equal"
	BeforeOrEqual      Operator = "before_or_equal"
	AfterOrEqual       Operator = "after_or_equal"
)

type Outcome string

const (
	Pass    Outcome = "PASS"
	Fail    Outcome = "FAIL"
	Unknown Outcome = "UNKNOWN"
)

type Decision string

const (
	Allow  Decision = "ALLOW"
	Block  Decision = "BLOCK"
	Review Decision = "REVIEW"
)

type Requirement struct {
	ID        string   `json:"id"`
	Attribute string   `json:"attribute"`
	Operator  Operator `json:"operator"`
	Expected  Value    `json:"expected"`
	Severity  Severity `json:"severity"`
}

type Evidence struct {
	ID         string `json:"id"`
	Attribute  string `json:"attribute"`
	Value      Value  `json:"value"`
	SourceID   string `json:"source_id"`
	ObservedAt string `json:"observed_at"`
}

type Input struct {
	Requirements []Requirement `json:"requirements"`
	Evidence     []Evidence    `json:"evidence"`
}

type EvidenceAssessment struct {
	EvidenceID string  `json:"evidence_id"`
	Value      Value   `json:"value"`
	SourceID   string  `json:"source_id"`
	ObservedAt string  `json:"observed_at"`
	Outcome    Outcome `json:"outcome"`
}

type RequirementResult struct {
	RequirementID string               `json:"requirement_id"`
	Attribute     string               `json:"attribute"`
	Operator      Operator             `json:"operator"`
	Expected      Value                `json:"expected"`
	Severity      Severity             `json:"severity"`
	Outcome       Outcome              `json:"outcome"`
	Assessments   []EvidenceAssessment `json:"assessments"`
}

type Report struct {
	Decision Decision            `json:"decision"`
	Results  []RequirementResult `json:"results"`
}

// Evaluate validates the complete input and evaluates each requirement using
// exact attribute matching. Conflicting evidence yields UNKNOWN.
func Evaluate(input Input) (Report, error) {
	if err := validateInput(input); err != nil {
		return Report{}, err
	}

	report := Report{Results: make([]RequirementResult, 0, len(input.Requirements))}
	hardCount := 0
	hardFailed := false
	hardUnknown := false

	for _, requirement := range input.Requirements {
		result := RequirementResult{
			RequirementID: requirement.ID,
			Attribute:     requirement.Attribute,
			Operator:      requirement.Operator,
			Expected:      requirement.Expected,
			Severity:      requirement.Severity,
			Outcome:       Unknown,
			Assessments:   make([]EvidenceAssessment, 0),
		}
		allPass, allFail := true, true
		matched := false
		for _, evidence := range input.Evidence {
			if evidence.Attribute != requirement.Attribute {
				continue
			}
			matched = true
			assessmentOutcome := compare(requirement.Operator, requirement.Expected, evidence.Value)
			result.Assessments = append(result.Assessments, EvidenceAssessment{
				EvidenceID: evidence.ID,
				Value:      evidence.Value,
				SourceID:   evidence.SourceID,
				ObservedAt: evidence.ObservedAt,
				Outcome:    assessmentOutcome,
			})
			if assessmentOutcome != Pass {
				allPass = false
			}
			if assessmentOutcome != Fail {
				allFail = false
			}
		}
		if matched {
			switch {
			case allPass:
				result.Outcome = Pass
			case allFail:
				result.Outcome = Fail
			}
		}
		report.Results = append(report.Results, result)

		if requirement.Severity == Hard {
			hardCount++
			switch result.Outcome {
			case Fail:
				hardFailed = true
			case Unknown:
				hardUnknown = true
			}
		}
	}

	switch {
	case hardFailed:
		report.Decision = Block
	case hardUnknown || hardCount == 0:
		report.Decision = Review
	default:
		report.Decision = Allow
	}
	return report, nil
}

func validateInput(input Input) error {
	requirementIDs := make(map[string]struct{}, len(input.Requirements))
	for _, requirement := range input.Requirements {
		if err := requireText("requirement", requirement.ID, "id"); err != nil {
			return err
		}
		if _, exists := requirementIDs[requirement.ID]; exists {
			return fmt.Errorf("duplicate requirement id %q", requirement.ID)
		}
		requirementIDs[requirement.ID] = struct{}{}
		if err := requireText("requirement", requirement.Attribute, "attribute"); err != nil {
			return err
		}
		if requirement.Severity != Hard && requirement.Severity != Preference {
			return fmt.Errorf("requirement %q has invalid severity %q", requirement.ID, requirement.Severity)
		}
		if err := validateValue(fmt.Sprintf("requirement %q expected value", requirement.ID), requirement.Expected); err != nil {
			return err
		}
		if err := validateOperator(requirement.ID, requirement.Operator, requirement.Expected.Type); err != nil {
			return err
		}
	}

	evidenceIDs := make(map[string]struct{}, len(input.Evidence))
	for _, evidence := range input.Evidence {
		if err := requireText("evidence", evidence.ID, "id"); err != nil {
			return err
		}
		if _, exists := evidenceIDs[evidence.ID]; exists {
			return fmt.Errorf("duplicate evidence id %q", evidence.ID)
		}
		evidenceIDs[evidence.ID] = struct{}{}
		if err := requireText("evidence", evidence.Attribute, "attribute"); err != nil {
			return err
		}
		if err := validateValue(fmt.Sprintf("evidence %q value", evidence.ID), evidence.Value); err != nil {
			return err
		}
		if err := requireText(fmt.Sprintf("evidence %q", evidence.ID), evidence.SourceID, "source_id"); err != nil {
			return err
		}
		if _, err := time.Parse(time.RFC3339Nano, evidence.ObservedAt); err != nil {
			return fmt.Errorf("evidence %q has invalid observed_at %q: expected RFC3339 timestamp", evidence.ID, evidence.ObservedAt)
		}
	}

	for _, requirement := range input.Requirements {
		for _, evidence := range input.Evidence {
			if evidence.Attribute == requirement.Attribute && evidence.Value.Type != requirement.Expected.Type {
				return typeMismatchError(requirement, evidence)
			}
		}
	}
	return nil
}

func typeMismatchError(requirement Requirement, evidence Evidence) error {
	return fmt.Errorf("evidence %q for requirement %q has type %q; expected %q", evidence.ID, requirement.ID, evidence.Value.Type, requirement.Expected.Type)
}

func requireText(kind, value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s %s is required", kind, field)
	}
	return nil
}

func validateValue(label string, value Value) error {
	switch value.Type {
	case StringType:
		if _, ok := value.Value.(string); !ok {
			return fmt.Errorf("%s has type %q but value must be a string", label, value.Type)
		}
	case NumberType:
		if _, ok := parseNumber(value.Value); !ok {
			return fmt.Errorf("%s has type %q but value must be a finite number", label, value.Type)
		}
	case BooleanType:
		if _, ok := value.Value.(bool); !ok {
			return fmt.Errorf("%s has type %q but value must be a boolean", label, value.Type)
		}
	case DateType:
		date, ok := value.Value.(string)
		if !ok || !isISODate(date) {
			return fmt.Errorf("%s must be a valid ISO calendar date (YYYY-MM-DD)", label)
		}
	default:
		return fmt.Errorf("%s has unsupported value type %q", label, value.Type)
	}
	return nil
}

func isISODate(value string) bool {
	if len(value) != len("2006-01-02") {
		return false
	}
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func validateOperator(requirementID string, operator Operator, valueType ValueType) error {
	switch operator {
	case Equals, NotEquals:
		return nil
	case LessThanOrEqual, GreaterThanOrEqual:
		if valueType != NumberType {
			return fmt.Errorf("requirement %q operator %q requires a number value", requirementID, operator)
		}
	case BeforeOrEqual, AfterOrEqual:
		if valueType != DateType {
			return fmt.Errorf("requirement %q operator %q requires a date value", requirementID, operator)
		}
	default:
		return fmt.Errorf("requirement %q has unsupported operator %q", requirementID, operator)
	}
	return nil
}

func compare(operator Operator, expected, actual Value) Outcome {
	var satisfied bool
	switch operator {
	case Equals:
		satisfied = equalValues(expected, actual)
	case NotEquals:
		satisfied = !equalValues(expected, actual)
	case LessThanOrEqual:
		satisfied = compareNumbers(actual.Value, expected.Value) <= 0
	case GreaterThanOrEqual:
		satisfied = compareNumbers(actual.Value, expected.Value) >= 0
	case BeforeOrEqual:
		satisfied = actual.Value.(string) <= expected.Value.(string)
	case AfterOrEqual:
		satisfied = actual.Value.(string) >= expected.Value.(string)
	}
	if satisfied {
		return Pass
	}
	return Fail
}

func equalValues(left, right Value) bool {
	switch left.Type {
	case StringType, DateType:
		return left.Value.(string) == right.Value.(string)
	case NumberType:
		return compareNumbers(left.Value, right.Value) == 0
	case BooleanType:
		return left.Value.(bool) == right.Value.(bool)
	default:
		return false
	}
}

func compareNumbers(left, right any) int {
	leftNumber, _ := parseNumber(left)
	rightNumber, _ := parseNumber(right)
	return leftNumber.Cmp(rightNumber)
}

func parseNumber(value any) (*big.Rat, bool) {
	var text string
	switch number := value.(type) {
	case json.Number:
		text = number.String()
	case float64:
		text = strconv.FormatFloat(number, 'g', -1, 64)
	default:
		return nil, false
	}
	if _, err := strconv.ParseFloat(text, 64); err != nil {
		return nil, false
	}
	result, ok := new(big.Rat).SetString(text)
	return result, ok
}
