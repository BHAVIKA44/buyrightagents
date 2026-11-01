package intent

import (
	"fmt"
	"math"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/bhavika44/buyrightagents/internal/verification"
)

// CoverageDiagnostic describes a deterministic, narrowly recognized request
// span that was omitted or semantically mismatched by a model response. Byte
// offsets refer to the original UTF-8 request and are diagnostic evidence, not
// a replacement for the model's grounded source_text.
type CoverageDiagnostic struct {
	Attribute   string                `json:"attribute"`
	SourceText  string                `json:"source_text"`
	SourceStart int                   `json:"source_start"`
	SourceEnd   int                   `json:"source_end"`
	Operator    verification.Operator `json:"operator,omitempty"`
	Expected    verification.Value    `json:"expected"`
	Severity    verification.Severity `json:"severity,omitempty"`
	Reason      string                `json:"reason"`
}

// CoverageError is returned only when a recognized supported constraint is
// neither represented correctly nor explicitly surfaced as unsupported or
// ambiguous. Callers must treat it as REVIEW, never as an empty intent.
type CoverageError struct {
	Diagnostics []CoverageDiagnostic `json:"diagnostics"`
}

func (err *CoverageError) Error() string {
	if err == nil || len(err.Diagnostics) == 0 {
		return "supported requirement coverage could not be established"
	}
	first := err.Diagnostics[0]
	return fmt.Sprintf("recognized supported requirement %q for attribute %q was omitted or mismatched: %s", first.SourceText, first.Attribute, first.Reason)
}

type coverageCandidate struct {
	attribute string
	source    string
	start     int
	end       int
	operator  verification.Operator
	expected  verification.Value
	severity  verification.Severity // empty means the request has no explicit severity cue.
}

var (
	negativeTotePattern      = regexp.MustCompile(`(?i)\bnot\s+a\s+tote\b`)
	priceCandidatePattern    = regexp.MustCompile(`(?i)\b(under|below|less\s+than|at\s+most|no\s+more\s+than|over|above|more\s+than|greater\s+than|at\s+least)\s+₹\s*([0-9][0-9,]*(?:\.[0-9]{1,2})?)`)
	weightCandidatePattern   = regexp.MustCompile(`(?i)\b(under|below|less\s+than|at\s+most|no\s+more\s+than|over|above|more\s+than|greater\s+than|at\s+least)\s+([0-9]+(?:\.[0-9]+)?)\s*(g|gram|grams|kg|kilogram|kilograms)\b`)
	deliveryCandidatePattern = regexp.MustCompile(`(?i)\b(before|earlier\s+than|by|no\s+later\s+than|on\s+or\s+before|after|later\s+than|exactly\s+on|on)\s+((?:\d{4}-\d{2}-\d{2})|tomorrow|(?:this\s+)?(?:sunday|monday|tuesday|wednesday|thursday|friday|saturday))\b`)
)

// validateSupportedHardConstraintCoverage reconciles a deliberately small
// deterministic inventory with the typed output. It is not a general NLP
// parser: a candidate exists only for an exact, contextual pattern whose
// operator/value can be determined without guessing. Ambiguity or unsupported
// output remains an explicit REVIEW route.
func validateSupportedHardConstraintCoverage(request string, reference time.Time, result PurchaseIntent) error {
	candidates := detectCoverageCandidates(request, reference)
	diagnostics := make([]CoverageDiagnostic, 0)
	for _, candidate := range candidates {
		if coverageCandidateAccountedFor(candidate, result) {
			continue
		}
		reason := "no matching typed requirement, clarification, or unsupported constraint"
		if candidateMentionedWithDifferentMeaning(candidate, result) {
			reason = "a grounded output mentions this span but has different attribute, operator, value, polarity, or explicit severity"
		}
		diagnostics = append(diagnostics, CoverageDiagnostic{
			Attribute: candidate.attribute, SourceText: candidate.source, SourceStart: candidate.start, SourceEnd: candidate.end,
			Operator: candidate.operator, Expected: candidate.expected, Severity: candidate.severity, Reason: reason,
		})
	}
	if len(diagnostics) > 0 {
		return &CoverageError{Diagnostics: diagnostics}
	}
	return nil
}

func detectCoverageCandidates(request string, reference time.Time) []coverageCandidate {
	clauses := unquotedConstraintClauses(request)
	result := make([]coverageCandidate, 0)
	for _, clause := range clauses {
		result = append(result, detectClauseCoverageCandidates(request, clause, reference)...)
	}
	return deduplicateCoverageCandidates(result)
}

func detectClauseCoverageCandidates(request, clause string, reference time.Time) []coverageCandidate {
	normalized := normalizeText(clause)
	if normalized == "" {
		return nil
	}
	tokens := strings.Fields(normalized)
	result := make([]coverageCandidate, 0)
	// A comma can place the exclusion in its own clause. Require shopping
	// context in the request while retaining the unquoted clause boundary.
	if hasShoppingContext(strings.Fields(normalizeText(request))) {
		// Cover this explicit exclusion only; do not infer a taxonomy or parse
		// general negation. Unsupported/ambiguous spans still require REVIEW.
		for _, source := range negativeTotePattern.FindAllString(clause, -1) {
			result = append(result, candidateForSource(request, coverageCandidate{
				attribute: "product_category", source: source, operator: verification.NotEquals,
				expected: verification.StringValue("tote"), severity: verification.Hard,
			}))
		}

	}
	if !hasShoppingContext(tokens) {
		return result
	}

	for _, item := range []struct {
		attribute string
		words     []string
		expected  bool
	}{
		{"vegan", []string{"vegan"}, true},
		{"water_resistant", []string{"water", "resistant"}, true},
		{"recycled_material", []string{"recycled"}, true},
	} {
		for start := 0; start+len(item.words) <= len(tokens); start++ {
			end := start + len(item.words)
			if !sameWords(tokens[start:end], item.words) || booleanOccurrenceIsIncidental(tokens, start, end) {
				continue
			}
			// A soft occurrence is useful to the interpreter, but cannot cause an
			// unsafe checkout when omitted. Do not turn it into a hard guardrail.
			if occurrenceHasAdjacentPreferenceCue(tokens, start, end) {
				continue
			}
			value := item.expected
			if negatesBooleanOccurrence(tokens, start) {
				value = !value
			}
			source := strings.Join(tokens[max(0, start-1):end], " ")
			result = append(result, candidateForSource(request, coverageCandidate{attribute: item.attribute, source: source, operator: verification.Equals, expected: verification.BooleanValue(value), severity: candidateSeverity(tokens, start, end)}))
		}
	}

	for _, category := range []string{"backpack", "bag"} {
		for index, token := range tokens {
			if token != category || occurrenceHasAdjacentIncidentalCue(tokens, index, index+1) {
				continue
			}
			result = append(result, candidateForSource(request, coverageCandidate{attribute: "product_category", source: category, operator: verification.Equals, expected: verification.StringValue(category), severity: candidateSeverity(tokens, index, index+1)}))
		}
	}

	for _, material := range []string{"canvas", "leather", "cotton", "polyester", "nylon", "wool", "silk"} {
		for index, token := range tokens {
			if token != material || occurrenceHasAdjacentIncidentalCue(tokens, index, index+1) || !nearProductCategory(tokens, index) {
				continue
			}
			result = append(result, candidateForSource(request, coverageCandidate{attribute: "material", source: material, operator: verification.Equals, expected: verification.StringValue(material), severity: candidateSeverity(tokens, index, index+1)}))
		}
	}

	for start := 0; start < len(tokens); start++ {
		if tokens[start] != "fit" && tokens[start] != "fits" {
			continue
		}
		for index := start + 1; index+2 < len(tokens) && index <= start+4; index++ {
			inches, err := strconv.ParseInt(tokens[index], 10, 64)
			if err != nil || (tokens[index+1] != "inch" && tokens[index+1] != "inches") || tokens[index+2] != "laptop" {
				continue
			}
			source := strings.Join(tokens[start:index+3], " ")
			result = append(result, candidateForSource(request, coverageCandidate{attribute: "laptop_fit_inches", source: source, operator: verification.GreaterThanOrEqual, expected: verification.NumberInt64Value(inches), severity: candidateSeverity(tokens, start, index+3)}))
			break
		}
	}

	for _, match := range priceCandidatePattern.FindAllStringSubmatch(clause, -1) {
		if len(match) != 3 {
			continue
		}
		paise, valueOK := parseINRPaise(match[2])
		operator, operatorOK := comparisonOperator(match[1])
		if !valueOK || !operatorOK {
			continue
		}
		result = append(result, candidateForSource(request, coverageCandidate{attribute: "total_price_paise", source: match[0], operator: operator, expected: verification.NumberInt64Value(paise), severity: sourceCandidateSeverity(tokens, match[0])}))
	}

	for _, match := range weightCandidatePattern.FindAllStringSubmatch(clause, -1) {
		if len(match) != 4 {
			continue
		}
		grams, ok := parseWeightGrams(match[2], match[3])
		operator, operatorOK := comparisonOperator(match[1])
		if !ok || !operatorOK {
			continue
		}
		result = append(result, candidateForSource(request, coverageCandidate{attribute: "weight_grams", source: match[0], operator: operator, expected: verification.NumberInt64Value(grams), severity: sourceCandidateSeverity(tokens, match[0])}))
	}

	if containsAnyNormalized(normalized, []string{"arrive", "arrives", "arrival", "delivery", "deliver"}) {
		for _, match := range deliveryCandidatePattern.FindAllStringSubmatch(clause, -1) {
			if len(match) != 3 {
				continue
			}
			source := match[0]
			operator, known := ResolveDeliveryDateOperator(source, request)
			if !known {
				continue
			}
			date, ok := coverageDate(match[2], reference)
			if !ok {
				continue
			}
			result = append(result, candidateForSource(request, coverageCandidate{attribute: "delivery_date", source: source, operator: operator, expected: verification.DateValue(date), severity: sourceCandidateSeverity(tokens, source)}))
		}
	}

	for _, color := range []string{"red", "blue", "black", "white", "green", "brown", "yellow", "pink", "purple", "orange", "grey", "gray"} {
		for index, token := range tokens {
			if token != color || occurrenceHasAdjacentIncidentalCue(tokens, index, index+1) || !nearProductCategory(tokens, index) {
				continue
			}
			result = append(result, candidateForSource(request, coverageCandidate{attribute: "color", source: color, operator: verification.Equals, expected: verification.StringValue(color), severity: candidateSeverity(tokens, index, index+1)}))
		}
	}
	return result
}

func unquotedConstraintClauses(request string) []string {
	var clauses []string
	var clause strings.Builder
	var quote rune
	characters := []rune(request)
	for index, character := range characters {
		if quote != 0 {
			if (quote == '"' && character == '"') || (quote == '\'' && character == '\'') || (quote == '“' && character == '”') {
				quote = 0
			}
			clause.WriteByte(' ')
			continue
		}
		if character == '"' || character == '“' || character == '\'' && startsSingleQuotedText(characters, index) {
			quote = character
			clause.WriteByte(' ')
			continue
		}
		if character == ',' && index > 0 && index+1 < len(characters) && unicode.IsDigit(characters[index-1]) && unicode.IsDigit(characters[index+1]) {
			clause.WriteRune(character)
			continue
		}
		if strings.ContainsRune(".!?,;:\n", character) {
			if text := strings.TrimSpace(clause.String()); text != "" {
				clauses = append(clauses, text)
			}
			clause.Reset()
			continue
		}
		clause.WriteRune(character)
	}
	if text := strings.TrimSpace(clause.String()); text != "" {
		clauses = append(clauses, text)
	}
	return clauses
}

func candidateForSource(request string, candidate coverageCandidate) coverageCandidate {
	if start, end, found := sourceLocation(request, candidate.source); found {
		candidate.start, candidate.end = start, end
	}
	return candidate
}

func sourceLocation(request, source string) (start, end int, found bool) {
	start = strings.Index(strings.ToLower(request), strings.ToLower(strings.TrimSpace(source)))
	if start < 0 {
		return 0, 0, false
	}
	return start, start + len(strings.TrimSpace(source)), true
}

func coverageCandidateAccountedFor(candidate coverageCandidate, result PurchaseIntent) bool {
	for _, requirement := range result.Requirements {
		if requirement.Attribute != candidate.attribute || requirement.Operator != candidate.operator || !coverageValuesEqual(requirement.Expected, candidate.expected) {
			continue
		}
		if candidate.severity != "" && requirement.Severity != candidate.severity {
			continue
		}
		if sourceSpansOverlap(candidate.source, requirement.SourceText) {
			return true
		}
	}
	for _, question := range result.ClarificationQuestions {
		if sourceSpansOverlap(candidate.source, question.SourceText) {
			return true
		}
	}
	for _, unsupported := range result.UnsupportedConstraints {
		if sourceSpansOverlap(candidate.source, unsupported.SourceText) {
			return true
		}
	}
	return false
}

func candidateMentionedWithDifferentMeaning(candidate coverageCandidate, result PurchaseIntent) bool {
	for _, requirement := range result.Requirements {
		if sourceSpansOverlap(candidate.source, requirement.SourceText) {
			return true
		}
	}
	return false
}

func coverageValuesEqual(left, right verification.Value) bool {
	if left.Type != right.Type {
		return false
	}
	switch left.Type {
	case verification.StringType, verification.DateType:
		leftText, leftOK := left.Value.(string)
		rightText, rightOK := right.Value.(string)
		if !leftOK || !rightOK {
			return false
		}
		if left.Type == verification.StringType {
			return normalizeCategory(leftText) == normalizeCategory(rightText)
		}
		return leftText == rightText
	case verification.BooleanType:
		leftValue, leftOK := left.Value.(bool)
		rightValue, rightOK := right.Value.(bool)
		return leftOK && rightOK && leftValue == rightValue
	case verification.NumberType:
		leftNumber, leftOK := numberRat(left.Value)
		rightNumber, rightOK := numberRat(right.Value)
		return leftOK && rightOK && leftNumber.Cmp(rightNumber) == 0
	default:
		return false
	}
}

func sourceSpansOverlap(left, right string) bool {
	left, right = normalizeText(left), normalizeText(right)
	return left != "" && right != "" && (containsNormalized(left, right) || containsNormalized(right, left))
}

func deduplicateCoverageCandidates(values []coverageCandidate) []coverageCandidate {
	seen := make(map[string]bool)
	result := make([]coverageCandidate, 0, len(values))
	for _, value := range values {
		key := strings.Join([]string{value.attribute, value.source, string(value.operator), valueKey(value.expected), string(value.severity)}, "\x1f")
		if seen[key] {
			continue
		}
		seen[key] = true
		result = append(result, value)
	}
	return result
}

func hasShoppingContext(tokens []string) bool {
	for _, token := range tokens {
		switch token {
		case "find", "buy", "need", "want", "looking", "look", "show", "get", "require", "requires", "required", "must":
			return true
		}
	}
	return false
}

// candidateSeverity is intentionally local to the identified span. Neutral
// shopping filters are treated as hard; an explicit preference cue adjacent to
// the same span keeps only that candidate soft. A mixed local hard/soft cue is
// left unspecified so a grounded clarification can account for it without
// inventing a severity.
func candidateSeverity(tokens []string, start, end int) verification.Severity {
	preference := occurrenceHasAdjacentPreferenceCue(tokens, start, end)
	hard := occurrenceHasAdjacentHardCue(tokens, start, end)
	if preference && hard {
		return ""
	}
	if preference {
		return verification.Preference
	}
	return verification.Hard
}

func sourceCandidateSeverity(tokens []string, source string) verification.Severity {
	words := strings.Fields(normalizeText(source))
	for start := 0; start+len(words) <= len(tokens); start++ {
		if sameWords(tokens[start:start+len(words)], words) {
			return candidateSeverity(tokens, start, start+len(words))
		}
	}
	return verification.Hard
}

func nearProductCategory(tokens []string, index int) bool {
	for _, other := range tokenWindow(index, index+1, len(tokens), 2) {
		if tokens[other] == "bag" || tokens[other] == "backpack" {
			return true
		}
	}
	return false
}

func containsAnyNormalized(text string, values []string) bool {
	for _, value := range values {
		if containsNormalized(text, value) {
			return true
		}
	}
	return false
}

func comparisonOperator(value string) (verification.Operator, bool) {
	switch normalizeText(value) {
	case "under", "below", "less than":
		return verification.LessThan, true
	case "at most", "no more than":
		return verification.LessThanOrEqual, true
	case "over", "above", "more than", "greater than":
		return verification.GreaterThan, true
	case "at least":
		return verification.GreaterThanOrEqual, true
	default:
		return "", false
	}
}

func parseINRPaise(value string) (int64, bool) {
	parts := strings.Split(strings.ReplaceAll(value, ",", ""), ".")
	if len(parts) > 2 {
		return 0, false
	}
	rupees, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil || rupees < 0 || rupees > math.MaxInt64/100 {
		return 0, false
	}
	paise := int64(0)
	if len(parts) == 2 {
		fraction := parts[1]
		if len(fraction) == 1 {
			fraction += "0"
		}
		if len(fraction) != 2 {
			return 0, false
		}
		paise, err = strconv.ParseInt(fraction, 10, 64)
		if err != nil {
			return 0, false
		}
	}
	return rupees*100 + paise, true
}

func parseWeightGrams(value, unit string) (int64, bool) {
	amount, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(amount) || math.IsInf(amount, 0) || amount < 0 {
		return 0, false
	}
	if strings.HasPrefix(strings.ToLower(unit), "k") {
		amount *= 1000
	}
	if math.Trunc(amount) != amount || amount > math.MaxInt64 {
		return 0, false
	}
	return int64(amount), true
}

func coverageDate(value string, reference time.Time) (string, bool) {
	value = strings.TrimSpace(value)
	if len(value) == len("2006-01-02") {
		if parsed, err := time.Parse("2006-01-02", value); err == nil && parsed.Format("2006-01-02") == value {
			return value, true
		}
	}
	return ResolveRelativeDate(value, reference)
}

func max(left, right int) int {
	if left > right {
		return left
	}
	return right
}
