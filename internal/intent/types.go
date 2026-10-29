// Package intent converts natural-language shopping requests into structured
// requirements. It never evaluates evidence or makes purchase decisions.
package intent

import (
	"encoding/json"
	"fmt"
	"math/big"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bhavika44/buyrightagents/internal/verification"
)

type Requirement struct {
	ID          string                `json:"id"`
	Attribute   string                `json:"attribute"`
	Operator    verification.Operator `json:"operator"`
	Expected    verification.Value    `json:"expected"`
	Severity    verification.Severity `json:"severity"`
	SourceText  string                `json:"source_text"`
	SourceStart int                   `json:"source_start,omitempty"`
	SourceEnd   int                   `json:"source_end,omitempty"`
}

type ClarificationQuestion struct {
	Question   string `json:"question"`
	SourceText string `json:"source_text"`
}

type UnsupportedConstraint struct {
	SourceText string `json:"source_text"`
	Reason     string `json:"reason"`
}

type PurchaseIntent struct {
	Requirements           []Requirement           `json:"requirements"`
	ClarificationQuestions []ClarificationQuestion `json:"clarification_questions"`
	UnsupportedConstraints []UnsupportedConstraint `json:"unsupported_constraints"`
}

type attributeSpec struct {
	valueType verification.ValueType
	operators map[verification.Operator]bool
}

func attributeRegistry() map[string]attributeSpec {
	equality := map[verification.Operator]bool{verification.Equals: true, verification.NotEquals: true}
	numeric := map[verification.Operator]bool{
		verification.Equals: true, verification.NotEquals: true,
		verification.LessThan: true, verification.LessThanOrEqual: true,
		verification.GreaterThan: true, verification.GreaterThanOrEqual: true,
	}
	dates := map[verification.Operator]bool{
		verification.Equals: true, verification.NotEquals: true,
		verification.Before: true, verification.BeforeOrEqual: true,
		verification.After: true, verification.AfterOrEqual: true,
	}
	return map[string]attributeSpec{
		"product_category":  {verification.StringType, equality},
		"vegan":             {verification.BooleanType, equality},
		"water_resistant":   {verification.BooleanType, equality},
		"recycled_material": {verification.BooleanType, equality},
		"laptop_fit_inches": {verification.NumberType, numeric},
		"total_price_paise": {verification.NumberType, numeric},
		"delivery_date":     {verification.DateType, dates},
		"color":             {verification.StringType, equality},
		"material":          {verification.StringType, equality},
		"weight_grams":      {verification.NumberType, numeric},
	}
}

func validateRequirement(requirement Requirement, request string) error {
	spec, ok := attributeRegistry()[requirement.Attribute]
	if !ok {
		return fmt.Errorf("requirement attribute %q is unsupported", requirement.Attribute)
	}
	if requirement.Expected.Type != spec.valueType {
		return fmt.Errorf("attribute %q requires expected type %q, got %q", requirement.Attribute, spec.valueType, requirement.Expected.Type)
	}
	if !spec.operators[requirement.Operator] {
		return fmt.Errorf("operator %q is unsupported for attribute %q", requirement.Operator, requirement.Attribute)
	}
	if requirement.Severity != verification.Hard && requirement.Severity != verification.Preference {
		return fmt.Errorf("severity %q must be hard or preference", requirement.Severity)
	}
	if strings.TrimSpace(requirement.SourceText) == "" || !containsNormalized(request, requirement.SourceText) {
		return fmt.Errorf("source_text %q is not grounded in the original request", requirement.SourceText)
	}
	if err := validateExpected(requirement.Attribute, requirement.Expected); err != nil {
		return err
	}
	return nil
}

func validateExpected(attribute string, value verification.Value) error {
	switch value.Type {
	case verification.StringType:
		text, ok := value.Value.(string)
		if !ok {
			return fmt.Errorf("attribute %q expected value must be a string", attribute)
		}
		if attribute == "product_category" && normalizeCategory(text) == "" {
			return fmt.Errorf("product_category must not be empty")
		}
	case verification.BooleanType:
		if _, ok := value.Value.(bool); !ok {
			return fmt.Errorf("attribute %q expected value must be a boolean", attribute)
		}
	case verification.DateType:
		date, ok := value.Value.(string)
		if !ok || !validISODate(date) {
			return fmt.Errorf("attribute %q expected value must be a valid ISO date (YYYY-MM-DD)", attribute)
		}
	case verification.NumberType:
		rational, ok := numberRat(value.Value)
		if !ok {
			return fmt.Errorf("attribute %q expected value must be a finite number", attribute)
		}
		if attribute == "total_price_paise" && !rational.IsInt() {
			return fmt.Errorf("total_price_paise must be an integer number of paise")
		}
		if attribute == "total_price_paise" && !rational.Num().IsInt64() {
			return fmt.Errorf("total_price_paise must fit the verifier's signed 64-bit paise range")
		}
		if rational.Sign() < 0 {
			return fmt.Errorf("attribute %q cannot have a negative expected value", attribute)
		}
		if attribute == "laptop_fit_inches" && rational.Sign() == 0 {
			return fmt.Errorf("laptop_fit_inches must be greater than zero")
		}
	default:
		return fmt.Errorf("attribute %q has unsupported value type %q", attribute, value.Type)
	}
	return nil
}

func normalizeCategory(value string) string {
	return strings.Join(strings.Fields(strings.ToLower(strings.TrimSpace(value))), " ")
}

func numberRat(value any) (*big.Rat, bool) {
	var text string
	switch number := value.(type) {
	case json.Number:
		text = number.String()
	case interface{ String() string }:
		text = number.String()
	case float64:
		text = fmt.Sprintf("%.17g", number)
	case int:
		text = fmt.Sprintf("%d", number)
	case int64:
		text = fmt.Sprintf("%d", number)
	default:
		return nil, false
	}
	if _, err := strconv.ParseFloat(text, 64); err != nil {
		return nil, false
	}
	rational, ok := new(big.Rat).SetString(text)
	return rational, ok
}

func validISODate(value string) bool {
	if len(value) != len("2006-01-02") {
		return false
	}
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

// normalizeText lowercases and turns punctuation and runs of whitespace into
// one space. This allows source phrases to differ in case or punctuation while
// still requiring their words, in order, to appear in the original request.
func normalizeText(value string) string {
	var builder strings.Builder
	space := true
	for _, character := range value {
		if unicode.IsLetter(character) || unicode.IsNumber(character) {
			builder.WriteRune(unicode.ToLower(character))
			space = false
			continue
		}
		if !space {
			builder.WriteByte(' ')
			space = true
		}
	}
	return strings.TrimSpace(builder.String())
}

func containsNormalized(text, phrase string) bool {
	normalizedText, normalizedPhrase := normalizeText(text), normalizeText(phrase)
	if normalizedPhrase == "" {
		return false
	}
	return strings.Contains(" "+normalizedText+" ", " "+normalizedPhrase+" ")
}
