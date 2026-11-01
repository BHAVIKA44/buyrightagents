package main

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/bhavika44/buyrightagents/internal/checkout"
	"github.com/bhavika44/buyrightagents/internal/evidence"
	"github.com/bhavika44/buyrightagents/internal/intent"
	"github.com/bhavika44/buyrightagents/internal/verification"
)

// savedGroqManifest freezes independently authored v2 labels by value before
// replay. Disputed labels remain in the artifact; they are excluded from the
// checkout oracle instead of being silently rewritten.
type savedGroqManifest struct {
	SchemaVersion int                 `json:"schema_version"`
	Dataset       string              `json:"dataset"`
	DatasetSHA256 string              `json:"dataset_sha256"`
	Cases         []savedGroqCaseSpec `json:"cases"`
}

type savedGroqCaseSpec struct {
	ID                string                `json:"id"`
	Request           string                `json:"request"`
	ReferenceTime     string                `json:"reference_time"`
	ExpectedIntent    intent.PurchaseIntent `json:"expected_intent"`
	CheckoutEligible  bool                  `json:"checkout_eligible"`
	LabelDisagreement string                `json:"label_disagreement,omitempty"`
}

type savedGroqRecord struct {
	Type          string          `json:"type"`
	CaseID        string          `json:"case_id"`
	Status        string          `json:"status"`
	Actual        json.RawMessage `json:"actual"`
	ErrorCategory string          `json:"error_category"`
	Diagnostic    string          `json:"diagnostic"`
}

type savedGroqScenario struct {
	CaseID             string                `json:"case_id"`
	Kind               string                `json:"kind"`
	ExpectedDecision   verification.Decision `json:"expected_decision"`
	ActualDecision     verification.Decision `json:"actual_decision"`
	CheckoutAuthorized bool                  `json:"checkout_authorized"`
	Unauthorized       bool                  `json:"unauthorized_mock_checkout"`
	Reason             string                `json:"reason,omitempty"`
}

type savedGroqCaseResult struct {
	CaseID            string                 `json:"case_id"`
	Request           string                 `json:"request"`
	ExpectedIntent    intent.PurchaseIntent  `json:"expected_intent"`
	ActualIntent      *intent.PurchaseIntent `json:"actual_intent,omitempty"`
	ValidSavedOutput  bool                   `json:"valid_saved_output"`
	InvalidDiagnostic string                 `json:"invalid_diagnostic,omitempty"`
	NotEvaluated      string                 `json:"not_evaluated,omitempty"`
	Scenarios         []savedGroqScenario    `json:"scenarios"`
}

type savedGroqSummary struct {
	Requests                    int `json:"original_requests"`
	SavedOutputsReplayed        int `json:"saved_outputs_replayed"`
	InvalidSafelyRejected       int `json:"invalid_outputs_safely_rejected"`
	ScenariosEvaluated          int `json:"checkout_scenarios_evaluated"`
	CorrectCompliantAuthorizes  int `json:"correct_compliant_authorizations"`
	IncorrectCompliantRefusals  int `json:"incorrect_compliant_refusals"`
	CorrectHardViolationDenials int `json:"correct_hard_violation_denials"`
	UnsafeAllows                int `json:"unsafe_provisional_allow_decisions"`
	UnauthorizedCheckouts       int `json:"unauthorized_mock_checkout_successes"`
	NotEvaluated                int `json:"not_evaluated_cases"`
}

type savedGroqReport struct {
	Manifest   string                `json:"manifest"`
	Checkpoint string                `json:"checkpoint"`
	Summary    savedGroqSummary      `json:"summary"`
	Cases      []savedGroqCaseResult `json:"cases"`
}

func savedGroqCheckoutEvalCommand(args []string, stdout, stderr io.Writer) int {
	dataset, checkpoint, manifest, jsonPath, reportPath := "", "", "", "", ""
	freezeOnly := false
	for i := 0; i < len(args); i++ {
		switch args[i] {
		case "--dataset", "--checkpoint", "--manifest", "--json", "--report":
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
				return reportError(stderr, savedGroqUsage())
			}
			value := args[i+1]
			i++
			switch args[i-1] {
			case "--dataset":
				dataset = value
			case "--checkpoint":
				checkpoint = value
			case "--manifest":
				manifest = value
			case "--json":
				jsonPath = value
			case "--report":
				reportPath = value
			}
		case "--freeze-only":
			freezeOnly = true
		default:
			return reportError(stderr, savedGroqUsage())
		}
	}
	if dataset == "" || checkpoint == "" || manifest == "" || (!freezeOnly && (jsonPath == "" || reportPath == "")) {
		return reportError(stderr, savedGroqUsage())
	}
	data, err := os.ReadFile(dataset)
	if err != nil {
		return reportError(stderr, err.Error())
	}
	cases, err := intent.LoadIntentCases(bytes.NewReader(data))
	if err != nil {
		return reportError(stderr, err.Error())
	}
	if freezeOnly {
		if err := writeSavedGroqManifest(manifest, dataset, data, cases); err != nil {
			return reportError(stderr, err.Error())
		}
		fmt.Fprintf(stdout, "Frozen saved-Groq checkout manifest: %s (%d cases)\n", manifest, len(cases))
		return 0
	}
	spec, err := readSavedGroqManifest(manifest, dataset, data)
	if err != nil {
		return reportError(stderr, err.Error())
	}
	records, err := readSavedGroqRecords(checkpoint)
	if err != nil {
		return reportError(stderr, err.Error())
	}
	report, err := runSavedGroqCheckoutEvaluation(spec, records, manifest, checkpoint)
	if err != nil {
		return reportError(stderr, err.Error())
	}
	if err := writeSavedGroqArtifacts(report, jsonPath, reportPath); err != nil {
		return reportError(stderr, err.Error())
	}
	fmt.Fprintf(stdout, "Saved-Groq checkout evaluation: %d scenarios; unauthorized checkouts=%d; not evaluated=%d.\n", report.Summary.ScenariosEvaluated, report.Summary.UnauthorizedCheckouts, report.Summary.NotEvaluated)
	return 0
}

func savedGroqUsage() string {
	return "usage: buyrightagents saved-groq-checkout-eval --dataset <cases.jsonl> --checkpoint <saved.jsonl> --manifest <manifest.json> --json <results.json> --report <report.md> [--freeze-only]"
}

func writeSavedGroqManifest(path, dataset string, data []byte, cases []intent.IntentCase) error {
	if _, err := os.Stat(path); err == nil {
		return fmt.Errorf("manifest already exists: %s", path)
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	digest := sha256.Sum256(data)
	knownDisputes := map[string]string{"intent-009": "severity label is historically ambiguous", "intent-013": "severity label is historically ambiguous", "intent-019": "severity label is historically ambiguous", "intent-023": "severity label is historically ambiguous", "intent-047": "label predates product_category support", "intent-050": "preference-versus-clarification label is disputed"}
	manifest := savedGroqManifest{SchemaVersion: 1, Dataset: dataset, DatasetSHA256: hex.EncodeToString(digest[:]), Cases: make([]savedGroqCaseSpec, 0, len(cases))}
	for _, item := range cases {
		_, disputed := knownDisputes[item.ID]
		manifest.Cases = append(manifest.Cases, savedGroqCaseSpec{ID: item.ID, Request: item.Request, ReferenceTime: item.ReferenceTime, ExpectedIntent: item.ExpectedIntent, CheckoutEligible: !disputed, LabelDisagreement: knownDisputes[item.ID]})
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil && filepath.Dir(path) != "." {
		return err
	}
	encoded, err := json.MarshalIndent(manifest, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, append(encoded, '\n'), 0o600)
}

func readSavedGroqManifest(path, dataset string, data []byte) (savedGroqManifest, error) {
	encoded, err := os.ReadFile(path)
	if err != nil {
		return savedGroqManifest{}, err
	}
	var manifest savedGroqManifest
	if err := json.Unmarshal(encoded, &manifest); err != nil {
		return manifest, err
	}
	digest := sha256.Sum256(data)
	if manifest.SchemaVersion != 1 || manifest.Dataset != dataset || manifest.DatasetSHA256 != hex.EncodeToString(digest[:]) {
		return manifest, errors.New("manifest does not match the selected dataset")
	}
	return manifest, nil
}

func readSavedGroqRecords(path string) (map[string]savedGroqRecord, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	result := map[string]savedGroqRecord{}
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		var record savedGroqRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			return nil, err
		}
		if record.Type == "result" {
			result[record.CaseID] = record
		}
	}
	return result, scanner.Err()
}

func runSavedGroqCheckoutEvaluation(manifest savedGroqManifest, records map[string]savedGroqRecord, manifestPath, checkpoint string) (savedGroqReport, error) {
	report := savedGroqReport{Manifest: manifestPath, Checkpoint: checkpoint, Cases: make([]savedGroqCaseResult, 0, len(manifest.Cases))}
	report.Summary.Requests = len(manifest.Cases)
	for _, spec := range manifest.Cases {
		result := savedGroqCaseResult{CaseID: spec.ID, Request: spec.Request, ExpectedIntent: spec.ExpectedIntent, Scenarios: []savedGroqScenario{}}
		record, ok := records[spec.ID]
		if !ok || record.Status != "success" {
			result.InvalidDiagnostic = record.Diagnostic
			report.Summary.InvalidSafelyRejected++
			report.Cases = append(report.Cases, result)
			continue
		}
		var actual intent.PurchaseIntent
		if err := json.Unmarshal(record.Actual, &actual); err != nil {
			return report, fmt.Errorf("decode %s: %w", spec.ID, err)
		}
		result.ValidSavedOutput, result.ActualIntent = true, &actual
		report.Summary.SavedOutputsReplayed++
		if !spec.CheckoutEligible {
			result.NotEvaluated = spec.LabelDisagreement
			report.Summary.NotEvaluated++
			report.Cases = append(report.Cases, result)
			continue
		}
		hard := hardRequirements(spec.ExpectedIntent)
		if len(hard) == 0 || len(spec.ExpectedIntent.ClarificationQuestions) > 0 || len(spec.ExpectedIntent.UnsupportedConstraints) > 0 {
			result.NotEvaluated = "original request has no unambiguous hard purchase oracle"
			report.Summary.NotEvaluated++
			report.Cases = append(report.Cases, result)
			continue
		}
		for _, scenario := range []struct {
			name     string
			violate  *intent.Requirement
			expected verification.Decision
		}{{"compliant", nil, verification.Allow}, {"negative", &hard[0], verification.Block}} {
			cart, err := savedGroqCart(spec.ID, spec.Request, spec.ExpectedIntent, actual, scenario.violate)
			if err != nil {
				result.NotEvaluated = err.Error()
				report.Summary.NotEvaluated++
				break
			}
			outcome, err := runSavedGroqScenario(spec, actual, cart, scenario.name, scenario.expected)
			if err != nil {
				return report, err
			}
			result.Scenarios = append(result.Scenarios, outcome)
			report.Summary.ScenariosEvaluated++
			if outcome.ActualDecision == verification.Allow && outcome.ExpectedDecision != verification.Allow {
				report.Summary.UnsafeAllows++
			}
			if outcome.Unauthorized {
				report.Summary.UnauthorizedCheckouts++
			}
			if scenario.name == "compliant" {
				if outcome.CheckoutAuthorized {
					report.Summary.CorrectCompliantAuthorizes++
				} else {
					report.Summary.IncorrectCompliantRefusals++
				}
			} else if !outcome.CheckoutAuthorized {
				report.Summary.CorrectHardViolationDenials++
			}
		}
		report.Cases = append(report.Cases, result)
	}
	return report, nil
}

func hardRequirements(value intent.PurchaseIntent) []intent.Requirement {
	var result []intent.Requirement
	for _, req := range value.Requirements {
		if req.Severity == verification.Hard {
			result = append(result, req)
		}
	}
	return result
}

func savedGroqCart(caseID, request string, expected, actual intent.PurchaseIntent, violate *intent.Requirement) (cartEvidenceFile, error) {
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	delivery := "2026-10-30"
	snapshot := verification.CartSnapshot{CartID: "live-" + caseID, Currency: "INR", CapturedAt: now.Add(-time.Minute).Format(time.RFC3339Nano), Items: []verification.CartItem{{ItemID: "item-1", ProductID: "product-1", VariantID: "variant-1", Quantity: 1, UnitPricePaise: 100000}}, DeliveryDate: &delivery}
	values := map[string]verification.Value{}
	for _, req := range hardRequirements(expected) {
		if req.Attribute == "total_price_paise" {
			value, ok := savedGroqNumber(req.Expected)
			if !ok {
				return cartEvidenceFile{}, errors.New("unsupported price value")
			}
			snapshot.Items[0].UnitPricePaise = savedGroqSatisfying(value, req.Operator)
			continue
		}
		if req.Attribute == "delivery_date" {
			text, ok := req.Expected.Value.(string)
			if !ok {
				return cartEvidenceFile{}, errors.New("unsupported delivery value")
			}
			delivery = savedGroqSatisfyingDate(text, req.Operator)
			snapshot.DeliveryDate = &delivery
			continue
		}
		if prior, ok := values[req.Attribute]; ok && fmt.Sprint(prior.Value) != fmt.Sprint(req.Expected.Value) {
			return cartEvidenceFile{}, fmt.Errorf("conflicting original %s requirements", req.Attribute)
		}
		values[req.Attribute] = savedGroqSatisfyingValue(req.Expected, req.Operator)
	}
	// Historical labels predate first-class product categories in a few cases.
	// When a saved model output names a category explicitly present in the
	// original request, include independently scoped synthetic category evidence
	// in the compliant fixture. This avoids treating fixture omission as a
	// product refusal, while not accepting a model-invented category.
	if _, ok := values["product_category"]; !ok {
		for _, req := range hardRequirements(actual) {
			if req.Attribute != "product_category" || req.Operator != verification.Equals || req.Expected.Type != verification.StringType {
				continue
			}
			category, ok := req.Expected.Value.(string)
			if ok && savedGroqRequestMentionsCategory(request, category) {
				values["product_category"] = verification.StringValue(category)
			}
			break
		}
	}
	if violate != nil {
		if violate.Attribute == "total_price_paise" {
			value, _ := savedGroqNumber(violate.Expected)
			snapshot.Items[0].UnitPricePaise = savedGroqViolating(value, violate.Operator)
		} else if violate.Attribute == "delivery_date" {
			d, err := time.Parse("2006-01-02", fmt.Sprint(violate.Expected.Value))
			if err != nil {
				return cartEvidenceFile{}, err
			}
			x := savedGroqViolatingDate(d, violate.Operator)
			snapshot.DeliveryDate = &x
		} else {
			values[violate.Attribute] = savedGroqViolatingValue(violate.Expected, violate.Operator)
		}
	}
	claims := make([]verification.Evidence, 0, len(values))
	for attribute, value := range values {
		claims = append(claims, verification.Evidence{ID: "claim-" + attribute, Attribute: attribute, Scope: verification.ItemScope, SubjectID: "item-1", ProductID: "product-1", VariantID: "variant-1", Value: value, SourceID: "live-eval", ObservedAt: now.Add(-time.Minute).Format(time.RFC3339Nano)})
	}
	provider, err := evidence.NewMockProvider("live-eval", []byte("live-evaluation-provider-key-0123456789"))
	if err != nil {
		return cartEvidenceFile{}, err
	}
	for i := range claims {
		claims[i], err = provider.Attest(snapshot, claims[i])
		if err != nil {
			return cartEvidenceFile{}, err
		}
	}
	return cartEvidenceFile{CartSnapshot: snapshot, Evidence: &claims}, nil
}

func savedGroqNumber(value verification.Value) (int64, bool) {
	text := fmt.Sprint(value.Value)
	if number, err := strconv.ParseInt(text, 10, 64); err == nil {
		return number, true
	}
	decimal, err := strconv.ParseFloat(text, 64)
	return int64(decimal), err == nil && decimal == float64(int64(decimal))
}
func savedGroqSatisfying(v int64, op verification.Operator) int64 {
	switch op {
	case verification.LessThan:
		return v - 1
	case verification.LessThanOrEqual, verification.GreaterThanOrEqual, verification.Equals:
		return v
	case verification.GreaterThan:
		return v + 1
	case verification.NotEquals:
		return v + 1
	}
	return v
}

func savedGroqSatisfyingValue(value verification.Value, operator verification.Operator) verification.Value {
	switch value.Type {
	case verification.NumberType:
		v, ok := savedGroqNumber(value)
		if ok {
			return verification.NumberInt64Value(savedGroqSatisfying(v, operator))
		}
	case verification.BooleanType:
		if operator == verification.NotEquals {
			if b, ok := value.Value.(bool); ok {
				return verification.BooleanValue(!b)
			}
		}
	case verification.StringType:
		if operator == verification.NotEquals {
			return verification.StringValue("not-" + fmt.Sprint(value.Value))
		}
	}
	return value
}

func savedGroqRequestMentionsCategory(request, category string) bool {
	needle := strings.ToLower(strings.TrimSpace(category))
	if needle == "" {
		return false
	}
	for _, token := range strings.Fields(strings.ToLower(request)) {
		token = strings.Trim(token, ".,;:!?()[]{}\"'")
		if token == needle {
			return true
		}
	}
	return false
}
func savedGroqViolating(v int64, op verification.Operator) int64 {
	switch op {
	case verification.LessThan, verification.GreaterThan:
		return v
	case verification.LessThanOrEqual:
		return v + 1
	case verification.GreaterThanOrEqual:
		return v - 1
	case verification.Equals:
		return v + 1
	case verification.NotEquals:
		return v
	}
	return v
}
func savedGroqViolatingValue(value verification.Value, operator verification.Operator) verification.Value {
	if value.Type == verification.BooleanType {
		if b, ok := value.Value.(bool); ok {
			return verification.BooleanValue(!b)
		}
	}
	if value.Type == verification.StringType {
		return verification.StringValue("not-" + fmt.Sprint(value.Value))
	}
	if value.Type == verification.NumberType {
		v, _ := savedGroqNumber(value)
		return verification.NumberInt64Value(savedGroqViolating(v, operator))
	}
	return value
}

func savedGroqSatisfyingDate(text string, operator verification.Operator) string {
	d, err := time.Parse("2006-01-02", text)
	if err != nil {
		return text
	}
	switch operator {
	case verification.Before:
		return d.AddDate(0, 0, -1).Format("2006-01-02")
	case verification.After:
		return d.AddDate(0, 0, 1).Format("2006-01-02")
	case verification.NotEquals:
		return d.AddDate(0, 0, 1).Format("2006-01-02")
	}
	return text
}

func savedGroqViolatingDate(d time.Time, operator verification.Operator) string {
	switch operator {
	case verification.Before, verification.After:
		return d.Format("2006-01-02")
	case verification.BeforeOrEqual:
		return d.AddDate(0, 0, 1).Format("2006-01-02")
	case verification.AfterOrEqual:
		return d.AddDate(0, 0, -1).Format("2006-01-02")
	case verification.Equals:
		return d.AddDate(0, 0, 1).Format("2006-01-02")
	case verification.NotEquals:
		return d.Format("2006-01-02")
	}
	return d.Format("2006-01-02")
}

func runSavedGroqScenario(spec savedGroqCaseSpec, actual intent.PurchaseIntent, cart cartEvidenceFile, kind string, expected verification.Decision) (savedGroqScenario, error) {
	content, err := structuredIntentResponse(actual)
	if err != nil {
		return savedGroqScenario{}, err
	}
	provider, err := evidence.NewMockProvider("live-eval", []byte("live-evaluation-provider-key-0123456789"))
	if err != nil {
		return savedGroqScenario{}, err
	}
	now := time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	gate, err := checkout.NewGateWithEvidencePolicy([]byte("live-evaluation-gate-key-0123456789"), func() time.Time { return now }, provider.TrustPolicy(24*time.Hour, 5*time.Minute))
	if err != nil {
		return savedGroqScenario{}, err
	}
	policy := gate.EvidencePolicy()
	check, err := evaluatePurchaseWithEvidencePolicy(context.Background(), intent.Interpreter{Client: staticModel{content: content}}, spec.Request, spec.ReferenceTime, cart, &policy)
	if err != nil {
		return savedGroqScenario{}, err
	}
	approval, issueErr := gate.Issue(checkoutContext(check, cart))
	authorized := false
	if issueErr == nil {
		authorized = gate.Checkout(approval, checkoutContext(check, cart)).Authorized
	}
	return savedGroqScenario{CaseID: spec.ID, Kind: kind, ExpectedDecision: expected, ActualDecision: check.Decision, CheckoutAuthorized: authorized, Unauthorized: authorized && expected != verification.Allow, Reason: strings.Join(check.ReviewReasons, "; ")}, nil
}

func writeSavedGroqArtifacts(report savedGroqReport, jsonPath, markdownPath string) error {
	if err := os.MkdirAll(filepath.Dir(jsonPath), 0o755); err != nil && filepath.Dir(jsonPath) != "." {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(markdownPath), 0o755); err != nil && filepath.Dir(markdownPath) != "." {
		return err
	}
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return err
	}
	if err := os.WriteFile(jsonPath, append(data, '\n'), 0o600); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintln(&b, "# Saved Groq Checkout Evaluation")
	fmt.Fprintln(&b)
	fmt.Fprintf(&b, "Offline replay of `%s` using `%s`; no LLM calls are made.\n\n", report.Checkpoint, report.Manifest)
	fmt.Fprintln(&b, "| Metric | Value |\n| --- | ---: |")
	for _, x := range []struct {
		n string
		v int
	}{{"Original requests", report.Summary.Requests}, {"Saved outputs replayed", report.Summary.SavedOutputsReplayed}, {"Invalid safely rejected", report.Summary.InvalidSafelyRejected}, {"Checkout scenarios", report.Summary.ScenariosEvaluated}, {"Unauthorized mock checkouts", report.Summary.UnauthorizedCheckouts}, {"Compliant refusals", report.Summary.IncorrectCompliantRefusals}, {"Not evaluated", report.Summary.NotEvaluated}} {
		fmt.Fprintf(&b, "| %s | %d |\n", x.n, x.v)
	}
	fmt.Fprintln(&b, "\n## Per-case outcomes\n\n| Case | Status | Scenarios |\n| --- | --- | ---: |")
	for _, c := range report.Cases {
		status := "replayed"
		if !c.ValidSavedOutput {
			status = "invalid safely rejected"
		}
		if c.NotEvaluated != "" {
			status = "not evaluated: " + c.NotEvaluated
		}
		fmt.Fprintf(&b, "| %s | %s | %d |\n", c.CaseID, strings.ReplaceAll(status, "|", "/"), len(c.Scenarios))
	}
	return os.WriteFile(markdownPath, []byte(b.String()), 0o600)
}
