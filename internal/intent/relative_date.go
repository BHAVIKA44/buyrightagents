package intent

import (
	"strings"
	"time"
)

// ResolveRelativeDate resolves the supported, unambiguous relative phrases
// against the supplied reference time in that timestamp's local offset. A
// weekday means its next occurrence on or after the reference calendar date;
// "next Friday" on a Friday means seven days later.
func ResolveRelativeDate(phrase string, reference time.Time) (string, bool) {
	normalized := normalizeText(phrase)
	date := time.Date(reference.Year(), reference.Month(), reference.Day(), 0, 0, 0, 0, reference.Location())
	switch {
	case containsNormalized(normalized, "tomorrow"):
		return date.AddDate(0, 0, 1).Format("2006-01-02"), true
	case containsNormalized(normalized, "within two weeks"), containsNormalized(normalized, "within 2 weeks"), containsNormalized(normalized, "within fourteen days"), containsNormalized(normalized, "within 14 days"):
		return date.AddDate(0, 0, 14).Format("2006-01-02"), true
	}
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
		delta := (int(weekday.day) - int(date.Weekday()) + 7) % 7
		if delta == 0 && containsNormalized(normalized, "next "+weekday.name) {
			delta = 7
		}
		return date.AddDate(0, 0, delta).Format("2006-01-02"), true
	}
	return "", false
}

func requestHasRelativeDate(text string) bool {
	normalized := normalizeText(text)
	if strings.Contains(normalized, "tomorrow") || strings.Contains(normalized, "within two weeks") || strings.Contains(normalized, "within 2 weeks") || strings.Contains(normalized, "within fourteen days") || strings.Contains(normalized, "within 14 days") {
		return true
	}
	for _, weekday := range []string{"sunday", "monday", "tuesday", "wednesday", "thursday", "friday", "saturday"} {
		if containsNormalized(normalized, weekday) {
			return true
		}
	}
	return false
}
