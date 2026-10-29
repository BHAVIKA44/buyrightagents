package intent

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bhavika44/buyrightagents/internal/verification"
)

type sequenceClient struct {
	responses []string
	errors    []error
	calls     int
}

func (client *sequenceClient) Complete(context.Context, string, string) (string, error) {
	index := client.calls
	client.calls++
	if index < len(client.errors) && client.errors[index] != nil {
		return "", client.errors[index]
	}
	if index < len(client.responses) {
		return client.responses[index], nil
	}
	if len(client.responses) > 0 {
		return client.responses[len(client.responses)-1], nil
	}
	return `{"requirements":[],"clarification_questions":[],"unsupported_constraints":[]}`, nil
}

func checkpointTestCase(id, request string, expected PurchaseIntent) IntentCase {
	return IntentCase{ID: id, Request: request, ReferenceTime: "2026-10-28T18:00:00+05:30", ExpectedIntent: expected}
}

func checkpointTestOptions(dataset []byte, model, endpoint, path string, resume bool, progress io.Writer) IntentCheckpointOptions {
	return IntentCheckpointOptions{Dataset: dataset, Model: model, Endpoint: endpoint, Path: path, Resume: resume, Progress: progress, Wait: func(context.Context, time.Duration) error { return nil }}
}

func emptyIntentJSON() string {
	return `{"requirements":[],"clarification_questions":[],"unsupported_constraints":[]}`
}

func colorIntentJSON() string {
	return `{"requirements":[{"attribute":"color","operator":"equals","expected":{"type":"string","value":"red"},"severity":"preference","source_text":"red"}],"clarification_questions":[],"unsupported_constraints":[]}`
}

func colorExpected() PurchaseIntent {
	return PurchaseIntent{
		Requirements:           []Requirement{{ID: "expected-color", Attribute: "color", Operator: verification.Equals, Expected: verification.StringValue("red"), Severity: verification.Preference, SourceText: "red"}},
		ClarificationQuestions: []ClarificationQuestion{},
		UnsupportedConstraints: []UnsupportedConstraint{},
	}
}

func TestCheckpointCreationResumeSkipsSavedCasesAndUsesAllResultsForMetrics(t *testing.T) {
	cases := []IntentCase{
		checkpointTestCase("case-1", "red", PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}}),
		checkpointTestCase("case-2", "red", colorExpected()),
	}
	dataset := []byte("authored dataset identity")
	path := filepath.Join(t.TempDir(), "intent-checkpoint.jsonl")
	client := &sequenceClient{responses: []string{colorIntentJSON()}, errors: []error{nil, errors.New("simulated interruption")}}
	var progress strings.Builder
	summary, err := RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: client}, cases, checkpointTestOptions(dataset, "test-model", "https://mock.example/v1", path, false, &progress))
	var failure *CaseEvaluationFailure
	if !errors.As(err, &failure) || failure.CaseID != "case-2" || summary.CasesRun != 1 {
		t.Fatalf("first run summary=%#v error=%v", summary, err)
	}
	if client.calls != 2 {
		t.Fatalf("initial calls=%d, want 2", client.calls)
	}
	if !strings.Contains(progress.String(), "Completed case case-1 (1/2 outcomes recorded)") {
		t.Fatalf("progress=%q", progress.String())
	}

	resumeClient := &sequenceClient{responses: []string{colorIntentJSON()}}
	summary, err = RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: resumeClient}, cases, checkpointTestOptions(dataset, "test-model", "https://mock.example/v1", path, true, io.Discard))
	if err != nil {
		t.Fatal(err)
	}
	if resumeClient.calls != 1 {
		t.Fatalf("resume calls=%d, want 1 call for only the missing case", resumeClient.calls)
	}
	if summary.CasesRun != 2 || summary.ExactRequests != 1 || summary.Constraints.TruePositive != 1 || summary.Constraints.FalsePositive != 1 {
		t.Fatalf("resumed metrics did not include checkpointed and new cases: %#v", summary)
	}
	if summary.Results[0].Case.ID != "case-1" || summary.Results[1].Case.ID != "case-2" {
		t.Fatalf("result order=%q, %q", summary.Results[0].Case.ID, summary.Results[1].Case.ID)
	}
	if _, err := RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: resumeClient}, cases, checkpointTestOptions(dataset, "test-model", "https://mock.example/v1", path, false, io.Discard)); err == nil {
		t.Fatal("creating over an existing checkpoint succeeded")
	}
}

func TestCheckpointRecordsInvalidResponsesContinuesAndResumeSkipsThem(t *testing.T) {
	dateExpected := PurchaseIntent{
		Requirements:           []Requirement{{Attribute: "delivery_date", Operator: verification.Before, Expected: verification.DateValue("2026-10-30"), Severity: verification.Hard, SourceText: "before Friday"}},
		ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{},
	}
	emptyExpected := PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}}
	cases := []IntentCase{
		checkpointTestCase("case-malformed", "plain bag", emptyExpected),
		checkpointTestCase("case-unsupported", "must arrive before Friday", dateExpected),
		checkpointTestCase("case-success", "plain bag", emptyExpected),
		checkpointTestCase("case-interrupted", "plain bag", emptyExpected),
	}
	dataset := []byte("invalid response recovery dataset")
	path := filepath.Join(t.TempDir(), "checkpoint.jsonl")
	client := &sequenceClient{
		responses: []string{"{", `{"requirements":[{"attribute":"delivery_date","operator":"less_than","expected":{"type":"date","value":"2026-10-30"},"severity":"hard","source_text":"before Friday"}],"clarification_questions":[],"unsupported_constraints":[]}`, emptyIntentJSON()},
		errors:    []error{nil, nil, nil, &HTTPStatusError{StatusCode: http.StatusInternalServerError}},
	}
	var progress strings.Builder
	summary, err := RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: client}, cases, checkpointTestOptions(dataset, "test-model", "https://mock.example/v1", path, false, &progress))
	var failure *CaseEvaluationFailure
	if !errors.As(err, &failure) || failure.CaseID != "case-interrupted" || failure.Category != "server_error" {
		t.Fatalf("failure=%#v error=%v", failure, err)
	}
	if summary.CasesRun != 3 || summary.ValidResponses != 1 || summary.InvalidResponses != 2 || summary.Constraints.FalseNegative != 1 || client.calls != 4 {
		t.Fatalf("partial summary=%#v calls=%d", summary, client.calls)
	}
	if !strings.Contains(progress.String(), "Recorded invalid response for case case-malformed") || !strings.Contains(progress.String(), "Recorded invalid response for case case-unsupported") {
		t.Fatalf("invalid response progress missing: %q", progress.String())
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(data)), "\n")
	if len(lines) != 4 { // Header, two terminal failures, one valid result.
		t.Fatalf("checkpoint has %d records, want 4", len(lines))
	}
	for _, line := range lines[1:3] {
		var record map[string]any
		if err := json.Unmarshal([]byte(line), &record); err != nil {
			t.Fatal(err)
		}
		if record["status"] != "invalid_response" || record["error_category"] != "invalid_response" || record["raw_response_preserved"] != false || record["actual"] != nil || strings.TrimSpace(record["diagnostic"].(string)) == "" {
			t.Fatalf("invalid response checkpoint record has unsafe or missing fields: %v", record)
		}
	}

	resumeClient := &sequenceClient{responses: []string{emptyIntentJSON()}}
	summary, err = RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: resumeClient}, cases, checkpointTestOptions(dataset, "test-model", "https://mock.example/v1", path, true, io.Discard))
	if err != nil || resumeClient.calls != 1 || summary.CasesRun != 4 || summary.ValidResponses != 2 || summary.InvalidResponses != 2 {
		t.Fatalf("resume summary=%#v calls=%d error=%v", summary, resumeClient.calls, err)
	}
	if summary.Results[0].Case.ID != "case-malformed" || summary.Results[0].Valid || summary.Results[1].Case.ID != "case-unsupported" || summary.Results[1].Valid {
		t.Fatalf("recorded failure outcomes not retained in order: %#v", summary.Results)
	}
}

func TestCheckpointReaderAcceptsLegacySuccessfulResultWithoutStatus(t *testing.T) {
	cases := []IntentCase{
		checkpointTestCase("legacy", "plain bag", PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}}),
		checkpointTestCase("new", "plain bag", PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}}),
	}
	dataset := []byte("legacy checkpoint dataset")
	header := checkpointHeader{Type: "header", SchemaVersion: intentCheckpointSchema, Model: "test-model", Endpoint: "https://mock.example/v1", DatasetSHA256: datasetIdentity(dataset), CaseIDs: intentCaseIDs(cases)}
	headerJSON, err := json.Marshal(header)
	if err != nil {
		t.Fatal(err)
	}
	legacy := struct {
		Type   string         `json:"type"`
		CaseID string         `json:"case_id"`
		Actual PurchaseIntent `json:"actual"`
	}{Type: "result", CaseID: "legacy", Actual: cases[0].ExpectedIntent}
	legacyJSON, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "legacy.jsonl")
	data := append(append(headerJSON, '\n'), legacyJSON...)
	data = append(data, '\n')
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	client := &sequenceClient{responses: []string{emptyIntentJSON()}}
	summary, err := RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: client}, cases, checkpointTestOptions(dataset, "test-model", "https://mock.example/v1", path, true, io.Discard))
	if err != nil || client.calls != 1 || summary.CasesRun != 2 || summary.ValidResponses != 2 {
		t.Fatalf("legacy checkpoint resume summary=%#v calls=%d error=%v", summary, client.calls, err)
	}
}

func TestCheckpointResumeRejectsModelDatasetAndMalformedData(t *testing.T) {
	cases := []IntentCase{checkpointTestCase("case-1", "plain bag", PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}})}
	dataset := []byte("dataset-one")
	path := filepath.Join(t.TempDir(), "checkpoint.jsonl")
	if _, err := RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: &sequenceClient{}}, cases, checkpointTestOptions(dataset, "model-one", "https://mock.example/v1", path, false, io.Discard)); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, model, endpoint string
		data                  []byte
		want                  string
	}{
		{name: "model", model: "model-two", endpoint: "https://mock.example/v1", data: dataset, want: "model mismatch"},
		{name: "dataset", model: "model-one", endpoint: "https://mock.example/v1", data: []byte("dataset-two"), want: "dataset identity mismatch"},
		{name: "endpoint", model: "model-one", endpoint: "https://other.example/v1", data: dataset, want: "endpoint mismatch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			client := &sequenceClient{}
			_, err := RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: client}, cases, checkpointTestOptions(test.data, test.model, test.endpoint, path, true, io.Discard))
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error=%v, want %q", err, test.want)
			}
			if client.calls != 0 {
				t.Fatalf("incompatible checkpoint caused %d model calls", client.calls)
			}
		})
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, append(data, []byte("{broken\n")...), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: &sequenceClient{}}, cases, checkpointTestOptions(dataset, "model-one", "https://mock.example/v1", path, true, io.Discard)); err == nil || !strings.Contains(err.Error(), "checkpoint line") {
		t.Fatalf("malformed checkpoint error=%v", err)
	}
}

func TestCheckpointFailureRecoveryRetainsCompletedPrefix(t *testing.T) {
	cases := []IntentCase{
		checkpointTestCase("case-1", "plain bag", PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}}),
		checkpointTestCase("case-2", "plain bag", PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}}),
		checkpointTestCase("case-3", "plain bag", PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}}),
	}
	dataset := []byte("failure recovery dataset")
	path := filepath.Join(t.TempDir(), "checkpoint.jsonl")
	failedClient := &sequenceClient{responses: []string{emptyIntentJSON()}, errors: []error{nil, &HTTPStatusError{StatusCode: 500}}}
	summary, err := RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: failedClient}, cases, checkpointTestOptions(dataset, "model", "https://mock.example/v1", path, false, io.Discard))
	var failure *CaseEvaluationFailure
	if !errors.As(err, &failure) || failure.CaseID != "case-2" || failure.Category != "server_error" {
		t.Fatalf("failure=%#v error=%v", failure, err)
	}
	if summary.CasesRun != 1 {
		t.Fatalf("completed=%d, want 1", summary.CasesRun)
	}

	resumeClient := &sequenceClient{responses: []string{emptyIntentJSON()}}
	summary, err = RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: resumeClient}, cases, checkpointTestOptions(dataset, "model", "https://mock.example/v1", path, true, io.Discard))
	if err != nil || summary.CasesRun != 3 || resumeClient.calls != 2 {
		t.Fatalf("recovery summary=%#v calls=%d error=%v", summary, resumeClient.calls, err)
	}
}

func TestRateLimitRetriesOnceOnlyWhenResetIsReliableAndShort(t *testing.T) {
	cases := []IntentCase{checkpointTestCase("case-1", "plain bag", PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}})}
	dataset := []byte("rate limited dataset")
	path := filepath.Join(t.TempDir(), "retry.jsonl")
	client := &sequenceClient{responses: []string{emptyIntentJSON()}, errors: []error{&HTTPStatusError{StatusCode: 429, RetryAfter: "2", ResetRequests: "3s", ResetTokens: "4s"}}}
	var waits []time.Duration
	options := checkpointTestOptions(dataset, "model", "endpoint", path, false, io.Discard)
	options.Wait = func(_ context.Context, delay time.Duration) error { waits = append(waits, delay); return nil }
	summary, err := RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: client}, cases, options)
	if err != nil || summary.CasesRun != 1 || client.calls != 2 {
		t.Fatalf("summary=%#v calls=%d error=%v", summary, client.calls, err)
	}
	if len(waits) != 1 || waits[0] != 4*time.Second {
		t.Fatalf("waits=%v, want one 4s wait", waits)
	}

	path = filepath.Join(t.TempDir(), "no-retry.jsonl")
	client = &sequenceClient{errors: []error{&HTTPStatusError{StatusCode: 429}}}
	options = checkpointTestOptions(dataset, "model", "endpoint", path, false, io.Discard)
	summary, err = RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: client}, cases, options)
	var failure *CaseEvaluationFailure
	if !errors.As(err, &failure) || failure.CaseID != "case-1" || failure.Category != "rate_limit" || summary.CasesRun != 0 || client.calls != 1 {
		t.Fatalf("missing-reset behavior summary=%#v calls=%d error=%v", summary, client.calls, err)
	}
	if !strings.Contains(err.Error(), "no reliable Retry-After") {
		t.Fatalf("missing-reset explanation=%v", err)
	}

	path = filepath.Join(t.TempDir(), "bounded-retry.jsonl")
	client = &sequenceClient{errors: []error{&HTTPStatusError{StatusCode: 429, RetryAfter: "1"}, &HTTPStatusError{StatusCode: 429, RetryAfter: "1"}}}
	options = checkpointTestOptions(dataset, "model", "endpoint", path, false, io.Discard)
	options.Wait = func(context.Context, time.Duration) error { return nil }
	_, err = RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: client}, cases, options)
	if !errors.As(err, &failure) || client.calls != 2 || failure.Category != "rate_limit" {
		t.Fatalf("retry bound calls=%d failure=%#v error=%v", client.calls, failure, err)
	}

	if _, retryable, reason := rateLimitRetryDelay(&HTTPStatusError{StatusCode: 429, ResetRequests: "24h"}, time.Now()); retryable || !strings.Contains(reason, "beyond") {
		t.Fatalf("daily quota decision retryable=%t reason=%q", retryable, reason)
	}
}

func TestRequestIntervalWaitsBetweenUncompletedCases(t *testing.T) {
	cases := []IntentCase{
		checkpointTestCase("case-1", "plain bag", PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}}),
		checkpointTestCase("case-2", "plain bag", PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}}),
	}
	options := checkpointTestOptions([]byte("paced"), "model", "endpoint", filepath.Join(t.TempDir(), "paced.jsonl"), false, io.Discard)
	options.RequestInterval = 3 * time.Second
	var waits []time.Duration
	options.Wait = func(_ context.Context, duration time.Duration) error { waits = append(waits, duration); return nil }
	client := &sequenceClient{}
	summary, err := RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: client}, cases, options)
	if err != nil || summary.CasesRun != 2 || client.calls != 2 {
		t.Fatalf("summary=%#v calls=%d error=%v", summary, client.calls, err)
	}
	if len(waits) != 1 || waits[0] <= 0 || waits[0] > 3*time.Second {
		t.Fatalf("pacing waits=%v", waits)
	}
}

func TestHTTP429IncludesSafeRateLimitMetadataAndNeverLeaksSecrets(t *testing.T) {
	const secret = "never-log-this-api-key"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+secret {
			t.Errorf("authorization header missing")
		}
		writer.Header().Set("Retry-After", "17")
		writer.Header().Set("x-ratelimit-remaining-requests", "0")
		writer.Header().Set("x-ratelimit-reset-requests", "2s")
		writer.Header().Set("x-ratelimit-remaining-tokens", "9")
		writer.Header().Set("x-ratelimit-reset-tokens", "1s-"+secret)
		writer.WriteHeader(http.StatusTooManyRequests)
		_, _ = io.WriteString(writer, `{"error":"`+secret+`"}`)
	}))
	defer server.Close()
	client, err := NewHTTPClient(Config{APIKey: secret, Model: "fixture", BaseURL: server.URL})
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.Complete(context.Background(), "system", "user")
	if err == nil {
		t.Fatal("expected HTTP 429")
	}
	message := err.Error()
	for _, want := range []string{"HTTP 429 Too Many Requests", "Retry-After=17", "remaining-requests=0", "reset-requests=2s", "remaining-tokens=9", "reset-tokens=1s-[REDACTED]"} {
		if !strings.Contains(message, want) {
			t.Errorf("error %q missing %q", message, want)
		}
	}
	if strings.Contains(message, secret) || strings.Contains(message, `"error"`) || IntentErrorCategory(err) != "rate_limit" {
		t.Fatalf("unsafe error or wrong category: %q", message)
	}
}

func TestCheckpointAndProgressNeverContainCredentials(t *testing.T) {
	const secret = "local-secret-for-test"
	cases := []IntentCase{checkpointTestCase("case-1", "plain bag", PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}})}
	path := filepath.Join(t.TempDir(), "checkpoint.jsonl")
	var progress strings.Builder
	_, err := RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: &sequenceClient{}}, cases, checkpointTestOptions([]byte("dataset"), "model", "https://mock.example/v1", path, false, &progress))
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), secret) || strings.Contains(progress.String(), secret) {
		t.Fatal("checkpoint or progress leaked credentials")
	}
}

func TestCheckpointHeaderRejectsChangedCaseIDs(t *testing.T) {
	cases := []IntentCase{checkpointTestCase("case-1", "plain bag", PurchaseIntent{Requirements: []Requirement{}, ClarificationQuestions: []ClarificationQuestion{}, UnsupportedConstraints: []UnsupportedConstraint{}})}
	path := filepath.Join(t.TempDir(), "checkpoint.jsonl")
	if _, err := RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: &sequenceClient{}}, cases, checkpointTestOptions([]byte("dataset"), "model", "endpoint", path, false, io.Discard)); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	data = []byte(strings.Replace(string(data), `"case-1"`, `"case-2"`, 1))
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = RunIntentEvaluationCheckpointed(context.Background(), Interpreter{Client: &sequenceClient{}}, cases, checkpointTestOptions([]byte("dataset"), "model", "endpoint", path, true, io.Discard))
	if err == nil || !strings.Contains(err.Error(), "case IDs") {
		t.Fatalf("case ID change error=%v", err)
	}
}
