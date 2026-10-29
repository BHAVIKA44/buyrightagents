package intent

import (
	"regexp"
	"strings"
	"time"

	"github.com/bhavika44/buyrightagents/internal/verification"
)

var exactDatePattern = regexp.MustCompile(`\b(?:exactly\s+)?on\s+(?:\d{4}-\d{2}-\d{2}|tomorrow|(?:this\s+)?(?:sunday|monday|tuesday|wednesday|thursday|friday|saturday))\b`)

// ResolveDeliveryDateOperator maps clear deadline language to date operators.
// It deliberately returns false for wording whose exact-date/deadline meaning
// cannot be established from the grounded phrase and request.
func ResolveDeliveryDateOperator(source, request string) (verification.Operator, bool) {
	if strings.TrimSpace(source) == "" || !containsNormalized(request, source) {
		return "", false
	}
	text := normalizeText(source)
	switch {
	case containsNormalized(text, "on or before"), containsNormalized(text, "no later than"), containsNormalized(text, "by "), containsNormalized(text, "within "):
		return verification.BeforeOrEqual, true
	case containsNormalized(text, "before "), containsNormalized(text, "earlier than"):
		return verification.Before, true
	case containsNormalized(text, "after "), containsNormalized(text, "later than"):
		return verification.After, true
	case exactDatePattern.MatchString(strings.ToLower(source)):
		return verification.Equals, true
	case containsNormalized(text, "arrive this "), containsNormalized(text, "arrives this "), containsNormalized(text, "arrive tomorrow"), containsNormalized(text, "arrives tomorrow"), containsNormalized(text, "must arrive tomorrow"):
		return verification.BeforeOrEqual, true
	default:
		return "", false
	}
}

var unsupportedRelativeDatePattern = regexp.MustCompile(`\b(?:next week|next month|this weekend|within a week|within one week|in \d+ days?|in \d+ weeks?|in a few days?|in a few weeks?|soon|as soon as possible)\b`)

// ResolveRelativeDate resolves the supported, unambiguous relative phrases
// against the supplied reference time in that timestamp's local offset. A
// weekday means its next occurrence on or after the reference calendar date;
// "next Friday" on a Friday means seven days later.
func ResolveRelativeDate(phrase string, reference time.Time) (string, bool) {
	normalized := normalizeText(phrase)
	date := time.Date(reference.Year(), reference.Month(), reference.Day(), 0, 0, 0, 0, reference.Location())
	tomorrow := containsNormalized(normalized, "tomorrow")
	fortnight := containsNormalized(normalized, "within two weeks") || containsNormalized(normalized, "within 2 weeks") || containsNormalized(normalized, "within fourteen days") || containsNormalized(normalized, "within 14 days")
	var weekdayName string
	var weekdayDay time.Weekday
	weekdays := []struct {
		name string
		day  time.Weekday
	}{
		{"sunday", time.Sunday}, {"monday", time.Monday}, {"tuesday", time.Tuesday},
		{"wednesday", time.Wednesday}, {"thursday", time.Thursday},
		{"friday", time.Friday}, {"saturday", time.Saturday},
	}
	for _, weekday := range weekdays {
		if !containsNormalized(normalized, weekday.name) {
			continue
		}
		if weekdayName != "" {
			return "", false
		}
		weekdayName, weekdayDay = weekday.name, weekday.day
	}
	cueCount := 0
	if tomorrow {
		cueCount++
	}
	if fortnight {
		cueCount++
	}
	if weekdayName != "" {
		cueCount++
	}
	if cueCount > 1 {
		return "", false
	}
	switch {
	case tomorrow:
		return date.AddDate(0, 0, 1).Format("2006-01-02"), true
	case fortnight:
		return date.AddDate(0, 0, 14).Format("2006-01-02"), true
	case weekdayName != "":
		delta := (int(weekdayDay) - int(date.Weekday()) + 7) % 7
		if delta == 0 && containsNormalized(normalized, "next "+weekdayName) {
			delta = 7
		}
		return date.AddDate(0, 0, delta).Format("2006-01-02"), true
	}
	return "", false
}

func hasRelativeDateCue(text string) bool {
	normalized := normalizeText(text)
	if unsupportedRelativeDatePattern.MatchString(normalized) || strings.Contains(normalized, "end of the week") || strings.Contains(normalized, "end of week") {
		return true
	}
	if containsNormalized(normalized, "tomorrow") || containsNormalized(normalized, "within two weeks") || containsNormalized(normalized, "within 2 weeks") || containsNormalized(normalized, "within fourteen days") || containsNormalized(normalized, "within 14 days") {
		return true
	}
	for _, weekday := range []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"} {
		if containsNormalized(normalized, weekday) {
			return true
		}
	}
	return false
}

func requestHasRelativeDate(text string) bool {
	return hasRelativeDateCue(text)
}
