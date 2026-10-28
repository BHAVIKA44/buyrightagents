package verification

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestStringAndBooleanRequirements(t *testing.T) {
	tests := []struct {
		name     string
		required Value
		observed Value
		operator Operator
		want     Outcome
	}{
		{"string equality passes", StringValue("vegan"), StringValue("vegan"), Equals, Pass},
		{"string equality fails", StringValue("vegan"), StringValue("leather"), Equals, Fail},
		{"string inequality passes", StringValue("red"), StringValue("blue"), NotEquals, Pass},
		{"boolean true passes", BooleanValue(true), BooleanValue(true), Equals, Pass},
		{"boolean false fails", BooleanValue(true), BooleanValue(false), Equals, Fail},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report := evaluateOne(t, tt.required, tt.observed, tt.operator)
			if got := report.Results[0].Outcome; got != tt.want {
				t.Fatalf("outcome = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNumericBoundaries(t *testing.T) {
	for _, tt := range []struct {
		name  string
		price float64
		want  Outcome
	}{
		{"exactly at limit", 500000, Pass},
		{"below limit", 499999, Pass},
		{"over limit", 500001, Fail},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report := evaluateOne(t, NumberValue(500000), NumberValue(tt.price), LessThanOrEqual)
			if got := report.Results[0].Outcome; got != tt.want {
				t.Fatalf("outcome = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestNumberComparisonPreservesJSONPrecision(t *testing.T) {
	report := evaluateOne(t,
		Value{Type: NumberType, Value: json.Number("9007199254740992")},
		Value{Type: NumberType, Value: json.Number("9007199254740993")},
		Equals,
	)
	if got := report.Results[0].Outcome; got != Fail {
		t.Fatalf("outcome = %s, want %s", got, Fail)
	}
	if got := compareNumbers(json.Number("1e3"), json.Number("1000.0")); got != 0 {
		t.Fatalf("equivalent exponent numbers compare as %d, want 0", got)
	}
}

func TestDateComparisons(t *testing.T) {
	for _, tt := range []struct {
		name     string
		operator Operator
		actual   string
		want     Outcome
	}{
		{"due date is inclusive", BeforeOrEqual, "2026-10-30", Pass},
		{"earlier date passes", BeforeOrEqual, "2026-10-29", Pass},
		{"later date fails", BeforeOrEqual, "2026-10-31", Fail},
		{"after or equal passes", AfterOrEqual, "2026-10-31", Pass},
	} {
		t.Run(tt.name, func(t *testing.T) {
			report := evaluateOne(t, DateValue("2026-10-30"), DateValue(tt.actual), tt.operator)
			if got := report.Results[0].Outcome; got != tt.want {
				t.Fatalf("outcome = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestMissingAndContradictoryEvidence(t *testing.T) {
	requirement := req("r1", Hard, "vegan", Equals, BooleanValue(true))
	report, err := Evaluate(Input{Requirements: []Requirement{requirement}})
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Outcome != Unknown || report.Decision != Review {
		t.Fatalf("missing evidence report = %#v", report)
	}

	input := Input{
		Requirements: []Requirement{requirement},
		Evidence: []Evidence{
			evidence("e1", "vegan", BooleanValue(true)),
			evidence("e2", "vegan", BooleanValue(false)),
		},
	}
	report, err = Evaluate(input)
	if err != nil {
		t.Fatal(err)
	}
	if report.Results[0].Outcome != Unknown || report.Decision != Review {
		t.Fatalf("contradictory evidence report = %#v", report)
	}
	if got := len(report.Results[0].Assessments); got != 2 {
		t.Fatalf("assessment count = %d, want 2", got)
	}
}

func TestOverallDecisionPolicy(t *testing.T) {
	tests := []struct {
		name         string
		requirements []Requirement
		evidence     []Evidence
		want         Decision
	}{
		{
			name:         "hard failure blocks",
			requirements: []Requirement{req("hard", Hard, "vegan", Equals, BooleanValue(true))},
			evidence:     []Evidence{evidence("e1", "vegan", BooleanValue(false))},
			want:         Block,
		},
		{
			name:         "unknown hard requirement requires review",
			requirements: []Requirement{req("hard", Hard, "vegan", Equals, BooleanValue(true))},
			want:         Review,
		},
		{
			name: "failed preference does not block",
			requirements: []Requirement{
				req("hard", Hard, "vegan", Equals, BooleanValue(true)),
				req("soft", Preference, "color", Equals, StringValue("red")),
			},
			evidence: []Evidence{
				evidence("e1", "vegan", BooleanValue(true)),
				evidence("e2", "color", StringValue("blue")),
			},
			want: Allow,
		},
		{name: "empty requirements require review", want: Review},
		{
			name:         "preferences without hard requirements cannot allow",
			requirements: []Requirement{req("soft", Preference, "color", Equals, StringValue("red"))},
			evidence:     []Evidence{evidence("e1", "color", StringValue("red"))},
			want:         Review,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			report, err := Evaluate(Input{Requirements: tt.requirements, Evidence: tt.evidence})
			if err != nil {
				t.Fatal(err)
			}
			if report.Decision != tt.want {
				t.Fatalf("decision = %s, want %s", report.Decision, tt.want)
			}
		})
	}
}

func TestAssessmentPreservesEvidenceProvenance(t *testing.T) {
	item := evidence("evidence-1", "vegan", BooleanValue(true))
	report, err := Evaluate(Input{
		Requirements: []Requirement{req("r1", Hard, "vegan", Equals, BooleanValue(true))},
		Evidence:     []Evidence{item},
	})
	if err != nil {
		t.Fatal(err)
	}
	assessment := report.Results[0].Assessments[0]
	result := report.Results[0]
	if result.Operator != Equals || result.Expected.Value != true {
		t.Fatalf("requirement comparator missing from report: %#v", result)
	}
	if assessment.EvidenceID != item.ID || assessment.Value.Value != true || assessment.SourceID != item.SourceID || assessment.ObservedAt != item.ObservedAt || assessment.Outcome != Pass {
		t.Fatalf("assessment lost provenance: %#v", assessment)
	}
}

func TestEvaluateRejectsInvalidInput(t *testing.T) {
	valid := Input{
		Requirements: []Requirement{req("r1", Hard, "price", LessThanOrEqual, NumberValue(500000))},
		Evidence:     []Evidence{evidence("e1", "price", NumberValue(500000))},
	}
	tests := []struct {
		name string
		edit func(*Input)
		want string
	}{
		{"duplicate requirement ids", func(in *Input) { in.Requirements = append(in.Requirements, in.Requirements[0]) }, "duplicate requirement id"},
		{"duplicate evidence ids", func(in *Input) { in.Evidence = append(in.Evidence, in.Evidence[0]) }, "duplicate evidence id"},
		{"missing requirement id", func(in *Input) { in.Requirements[0].ID = " " }, "id is required"},
		{"missing evidence id", func(in *Input) { in.Evidence[0].ID = "" }, "id is required"},
		{"invalid severity", func(in *Input) { in.Requirements[0].Severity = "optional" }, "invalid severity"},
		{"unsupported operator", func(in *Input) { in.Requirements[0].Operator = "contains" }, "unsupported operator"},
		{"operator with incompatible type", func(in *Input) { in.Requirements[0].Expected = StringValue("500000") }, "requires a number"},
		{"bad numeric Go value", func(in *Input) { in.Requirements[0].Expected = Value{Type: NumberType, Value: "500000"} }, "finite number"},
		{"mismatched evidence type", func(in *Input) { in.Evidence[0].Value = StringValue("500000") }, "expected \"number\""},
		{"malformed date", func(in *Input) {
			in.Requirements[0].Expected = DateValue("2026-02-30")
			in.Requirements[0].Operator = BeforeOrEqual
			in.Evidence[0].Value = DateValue("2026-02-28")
		}, "valid ISO calendar date"},
		{"missing provenance", func(in *Input) { in.Evidence[0].SourceID = "" }, "source_id is required"},
		{"invalid timestamp", func(in *Input) { in.Evidence[0].ObservedAt = "yesterday" }, "expected RFC3339 timestamp"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			input := cloneInput(valid)
			tt.edit(&input)
			_, err := Evaluate(input)
			if err == nil || !strings.Contains(err.Error(), tt.want) {
				t.Fatalf("error = %v, want substring %q", err, tt.want)
			}
		})
	}
}

func evaluateOne(t *testing.T, expected, actual Value, operator Operator) Report {
	t.Helper()
	attribute := "value"
	report, err := Evaluate(Input{
		Requirements: []Requirement{req("r1", Hard, attribute, operator, expected)},
		Evidence:     []Evidence{evidence("e1", attribute, actual)},
	})
	if err != nil {
		t.Fatal(err)
	}
	return report
}

func req(id string, severity Severity, attribute string, operator Operator, expected Value) Requirement {
	return Requirement{ID: id, Severity: severity, Attribute: attribute, Operator: operator, Expected: expected}
}

func evidence(id, attribute string, value Value) Evidence {
	return Evidence{
		ID: id, Attribute: attribute, Value: value,
		SourceID: "catalogue:item-123", ObservedAt: "2026-10-28T10:00:00Z",
	}
}

func cloneInput(input Input) Input {
	return Input{
		Requirements: append([]Requirement(nil), input.Requirements...),
		Evidence:     append([]Evidence(nil), input.Evidence...),
	}
}
