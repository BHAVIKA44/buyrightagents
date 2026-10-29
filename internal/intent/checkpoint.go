package intent

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
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const intentCheckpointSchema = 1
const maxAutomaticRateLimitWait = 30 * time.Second

type IntentCheckpointOptions struct {
	Dataset         []byte
	Model           string
	Endpoint        string
	Path            string
	Resume          bool
	RequestInterval time.Duration
	Progress        io.Writer
	Wait            func(context.Context, time.Duration) error
}

type checkpointHeader struct {
	Type          string   `json:"type"`
	SchemaVersion int      `json:"schema_version"`
	Model         string   `json:"model"`
	Endpoint      string   `json:"endpoint"`
	DatasetSHA256 string   `json:"dataset_sha256"`
	CaseIDs       []string `json:"case_ids"`
}

type checkpointResult struct {
	Type                 string          `json:"type"`
	CaseID               string          `json:"case_id"`
	Status               string          `json:"status,omitempty"`
	Actual               *PurchaseIntent `json:"actual,omitempty"`
	ErrorCategory        string          `json:"error_category,omitempty"`
	Diagnostic           string          `json:"diagnostic,omitempty"`
	RawResponsePreserved *bool           `json:"raw_response_preserved,omitempty"`
}

type checkpointState struct {
	Results  map[string]PurchaseIntent
	Failures map[string]checkpointResult
}

type CaseEvaluationFailure struct {
	CaseID   string
	Category string
	Cause    error
}

func (failure *CaseEvaluationFailure) Error() string {
	return fmt.Sprintf("case %q failed (%s): %v", failure.CaseID, failure.Category, failure.Cause)
}

func (failure *CaseEvaluationFailure) Unwrap() error { return failure.Cause }

// RunIntentEvaluationCheckpointed records each successful interpretation as a
// separate synced JSONL record. Existing checkpoint files are never replaced.
func RunIntentEvaluationCheckpointed(ctx context.Context, interpreter Interpreter, cases []IntentCase, options IntentCheckpointOptions) (IntentEvalSummary, error) {
	if len(cases) == 0 {
		return IntentEvalSummary{}, errors.New("intent case corpus is empty")
	}
	if strings.TrimSpace(options.Path) == "" {
		return IntentEvalSummary{}, errors.New("checkpoint path is required")
	}
	if options.RequestInterval < 0 {
		return IntentEvalSummary{}, errors.New("request interval cannot be negative")
	}
	if options.Wait == nil {
		options.Wait = waitForIntentDuration
	}
	header := checkpointHeader{Type: "header", SchemaVersion: intentCheckpointSchema, Model: options.Model, Endpoint: options.Endpoint, DatasetSHA256: datasetIdentity(options.Dataset), CaseIDs: intentCaseIDs(cases)}
	saved := checkpointState{Results: make(map[string]PurchaseIntent), Failures: make(map[string]checkpointResult)}
	var file *os.File
	var err error
	if options.Resume {
		saved, err = readIntentCheckpoint(options.Path, header, cases)
		if err != nil {
			return IntentEvalSummary{}, err
		}
		file, err = os.OpenFile(options.Path, os.O_WRONLY|os.O_APPEND, 0)
		if err != nil {
			return IntentEvalSummary{}, fmt.Errorf("open checkpoint for append: %w", err)
		}
	} else {
		file, err = os.OpenFile(options.Path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
		if err != nil {
			return IntentEvalSummary{}, fmt.Errorf("create checkpoint (use --resume to continue an existing file): %w", err)
		}
		if err := writeCheckpointRecord(file, header); err != nil {
			_ = file.Close()
			return IntentEvalSummary{}, fmt.Errorf("write checkpoint header: %w", err)
		}
	}
	defer file.Close()

	var lastRequest time.Time
	if options.Resume && checkpointOutcomeCount(saved) > 0 && checkpointOutcomeCount(saved) < len(cases) {
		lastRequest = time.Now()
	}
	for _, item := range cases {
		if _, ok := saved.Results[item.ID]; ok {
			if options.Progress != nil {
				fmt.Fprintf(options.Progress, "Skipped checkpointed case %s (%d/%d outcomes recorded)\n", item.ID, checkpointOutcomeCount(saved), len(cases))
			}
			continue
		}
		if _, ok := saved.Failures[item.ID]; ok {
			if options.Progress != nil {
				fmt.Fprintf(options.Progress, "Skipped terminal invalid response %s (%d/%d outcomes recorded)\n", item.ID, checkpointOutcomeCount(saved), len(cases))
			}
			continue
		}
		if !lastRequest.IsZero() {
			if err := waitAfter(lastRequest, options.RequestInterval, options.Wait, ctx); err != nil {
				return summarizeCheckpointResults(cases, saved), fmt.Errorf("wait before case %q: %w", item.ID, err)
			}
		}
		actual, callErr := interpreter.Interpret(ctx, item.Request, item.ReferenceTime)
		lastRequest = time.Now()
		if callErr != nil {
			var statusFailure *HTTPStatusError
			if errors.As(callErr, &statusFailure) && statusFailure.StatusCode == 429 {
				delay, retryable, reason := rateLimitRetryDelay(statusFailure, time.Now())
				if retryable {
					if intervalWait := remainingInterval(lastRequest, options.RequestInterval); intervalWait > delay {
						delay = intervalWait
					}
					if options.Progress != nil {
						fmt.Fprintf(options.Progress, "Rate limit on case %s; waiting %s before one retry.\n", item.ID, delay)
					}
					if waitErr := options.Wait(ctx, delay); waitErr != nil {
						callErr = fmt.Errorf("%w; rate-limit wait interrupted (retry not attempted): %v", callErr, waitErr)
					} else {
						actual, callErr = interpreter.Interpret(ctx, item.Request, item.ReferenceTime)
						lastRequest = time.Now()
					}
				} else {
					callErr = fmt.Errorf("%w; automatic retry skipped: %s", callErr, reason)
				}
			}
		}
		if callErr != nil {
			if IntentErrorCategory(callErr) == "invalid_response" {
				if err := recordInvalidCheckpointOutcome(file, saved, item.ID, callErr); err != nil {
					return summarizeCheckpointResults(cases, saved), fmt.Errorf("persist invalid response for case %q: %w", item.ID, err)
				}
				if options.Progress != nil {
					fmt.Fprintf(options.Progress, "Recorded invalid response for case %s (%d/%d outcomes recorded)\n", item.ID, checkpointOutcomeCount(saved), len(cases))
				}
				continue
			}
			summary := summarizeCheckpointResults(cases, saved)
			return summary, &CaseEvaluationFailure{CaseID: item.ID, Category: IntentErrorCategory(callErr), Cause: callErr}
		}
		if validationErr := validateCheckpointIntent(actual, item); validationErr != nil {
			if err := recordInvalidCheckpointOutcome(file, saved, item.ID, validationErr); err != nil {
				return summarizeCheckpointResults(cases, saved), fmt.Errorf("persist invalid response for case %q: %w", item.ID, err)
			}
			if options.Progress != nil {
				fmt.Fprintf(options.Progress, "Recorded invalid response for case %s (%d/%d outcomes recorded)\n", item.ID, checkpointOutcomeCount(saved), len(cases))
			}
			continue
		}
		if err := writeCheckpointRecord(file, checkpointResult{Type: "result", CaseID: item.ID, Status: "success", Actual: &actual}); err != nil {
			summary := summarizeCheckpointResults(cases, saved)
			return summary, fmt.Errorf("persist case %q result: %w", item.ID, err)
		}
		saved.Results[item.ID] = actual
		if options.Progress != nil {
			fmt.Fprintf(options.Progress, "Completed case %s (%d/%d outcomes recorded)\n", item.ID, checkpointOutcomeCount(saved), len(cases))
		}
	}
	return summarizeCheckpointResults(cases, saved), nil
}

func waitForIntentDuration(ctx context.Context, duration time.Duration) error {
	if duration <= 0 {
		return nil
	}
	timer := time.NewTimer(duration)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func waitAfter(last time.Time, interval time.Duration, wait func(context.Context, time.Duration) error, ctx context.Context) error {
	delay := remainingInterval(last, interval)
	if delay <= 0 {
		return nil
	}
	return wait(ctx, delay)
}

func remainingInterval(last time.Time, interval time.Duration) time.Duration {
	if interval <= 0 {
		return 0
	}
	remaining := interval - time.Since(last)
	if remaining < 0 {
		return 0
	}
	return remaining
}

func rateLimitRetryDelay(failure *HTTPStatusError, now time.Time) (time.Duration, bool, string) {
	var delays []time.Duration
	for _, header := range []struct {
		name, value string
		allowDate   bool
	}{
		{"Retry-After", failure.RetryAfter, true},
		{"request reset", failure.ResetRequests, false},
		{"token reset", failure.ResetTokens, false},
	} {
		if strings.TrimSpace(header.value) == "" {
			continue
		}
		delay, ok := parseRateLimitDelay(header.value, now, header.allowDate)
		if !ok {
			return 0, false, "rate-limit reset metadata is not a reliable short duration"
		}
		delays = append(delays, delay)
	}
	if len(delays) == 0 {
		return 0, false, "no reliable Retry-After or reset duration was provided"
	}
	var wait time.Duration
	for _, delay := range delays {
		if delay > wait {
			wait = delay
		}
	}
	if wait <= 0 {
		return 0, false, "rate-limit reset duration is not in the future"
	}
	if wait > maxAutomaticRateLimitWait {
		return wait, false, fmt.Sprintf("rate-limit reset is %s, beyond the %s automatic wait limit", wait, maxAutomaticRateLimitWait)
	}
	return wait, true, "short reset duration available"
}

func parseRateLimitDelay(value string, now time.Time, allowDate bool) (time.Duration, bool) {
	value = strings.TrimSpace(value)
	if allowDate {
		if seconds, err := strconv.ParseFloat(value, 64); err == nil {
			if seconds < 0 {
				return 0, false
			}
			return time.Duration(seconds * float64(time.Second)), true
		}
		if date, err := http.ParseTime(value); err == nil {
			return date.Sub(now), true
		}
	}
	if duration, err := time.ParseDuration(value); err == nil {
		return duration, duration >= 0
	}
	if seconds, err := strconv.ParseFloat(value, 64); err == nil && seconds >= 0 {
		return time.Duration(seconds * float64(time.Second)), true
	}
	return 0, false
}

func IntentErrorCategory(err error) string {
	var invalidOutput *invalidModelOutputError
	if errors.As(err, &invalidOutput) {
		return "invalid_response"
	}
	var statusErr *HTTPStatusError
	if errors.As(err, &statusErr) {
		switch {
		case statusErr.StatusCode == 429:
			return "rate_limit"
		case statusErr.StatusCode == 401 || statusErr.StatusCode == 403:
			return "authentication"
		case statusErr.StatusCode >= 500:
			return "server_error"
		default:
			return "http_error"
		}
	}
	if strings.Contains(err.Error(), "LLM request failed") {
		return "transport_error"
	}
	return "interpretation_error"
}

func validateCheckpointIntent(actual PurchaseIntent, item IntentCase) error {
	if actual.Requirements == nil || actual.ClarificationQuestions == nil || actual.UnsupportedConstraints == nil {
		return errors.New("checkpoint result must include all three intent arrays")
	}
	reference, err := time.Parse(time.RFC3339Nano, item.ReferenceTime)
	if err != nil {
		return fmt.Errorf("invalid reference time for case %q: %w", item.ID, err)
	}
	if _, err := validateOutput(toModelOutput(actual), item.Request, reference); err != nil {
		return fmt.Errorf("invalid structured result: %w", err)
	}
	return nil
}

func summarizeCheckpointResults(cases []IntentCase, saved checkpointState) IntentEvalSummary {
	summary := IntentEvalSummary{Results: make([]IntentCaseResult, 0, checkpointOutcomeCount(saved))}
	for _, item := range cases {
		if actual, ok := saved.Results[item.ID]; ok {
			addIntentCaseResult(&summary, item, actual)
		} else if failure, ok := saved.Failures[item.ID]; ok {
			addInvalidIntentCaseResult(&summary, item, failure.ErrorCategory, failure.Diagnostic)
		}
	}
	return summary
}

func checkpointOutcomeCount(saved checkpointState) int {
	return len(saved.Results) + len(saved.Failures)
}

func recordInvalidCheckpointOutcome(file *os.File, saved checkpointState, caseID string, cause error) error {
	rawResponsePreserved := false
	diagnostic := strings.TrimSpace(cause.Error())
	if len(diagnostic) > 500 {
		diagnostic = diagnostic[:500]
	}
	result := checkpointResult{
		Type: "result", CaseID: caseID, Status: "invalid_response",
		ErrorCategory: "invalid_response", Diagnostic: diagnostic,
		RawResponsePreserved: &rawResponsePreserved,
	}
	if err := writeCheckpointRecord(file, result); err != nil {
		return err
	}
	saved.Failures[caseID] = result
	return nil
}

func datasetIdentity(dataset []byte) string {
	digest := sha256.Sum256(dataset)
	return hex.EncodeToString(digest[:])
}

func intentCaseIDs(cases []IntentCase) []string {
	ids := make([]string, len(cases))
	for index, item := range cases {
		ids[index] = item.ID
	}
	return ids
}

func writeCheckpointRecord(writer *os.File, record any) error {
	encoder := json.NewEncoder(writer)
	if err := encoder.Encode(record); err != nil {
		return err
	}
	if err := writer.Sync(); err != nil {
		return err
	}
	return nil
}

func readIntentCheckpoint(path string, expected checkpointHeader, cases []IntentCase) (checkpointState, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return checkpointState{}, fmt.Errorf("open checkpoint: %w", err)
	}
	if len(data) == 0 {
		return checkpointState{}, errors.New("checkpoint is empty")
	}
	if !bytes.HasSuffix(data, []byte("\n")) {
		return checkpointState{}, errors.New("checkpoint ends with an incomplete record")
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)
	line := 0
	var header checkpointHeader
	saved := checkpointState{Results: make(map[string]PurchaseIntent), Failures: make(map[string]checkpointResult)}
	caseByID := make(map[string]IntentCase, len(cases))
	for _, item := range cases {
		caseByID[item.ID] = item
	}
	for scanner.Scan() {
		line++
		data := bytes.TrimSpace(scanner.Bytes())
		if len(data) == 0 {
			return checkpointState{}, fmt.Errorf("checkpoint line %d is blank", line)
		}
		if err := rejectDuplicateJSONKeys(data); err != nil {
			return checkpointState{}, fmt.Errorf("checkpoint line %d: %w", line, err)
		}
		var kind struct {
			Type string `json:"type"`
		}
		if err := json.Unmarshal(data, &kind); err != nil {
			return checkpointState{}, fmt.Errorf("checkpoint line %d: %w", line, err)
		}
		if line == 1 {
			if kind.Type != "header" {
				return checkpointState{}, errors.New("checkpoint must begin with a header record")
			}
			if err := decodeCheckpointJSON(data, &header); err != nil {
				return checkpointState{}, fmt.Errorf("checkpoint header: %w", err)
			}
			if err := validateCheckpointHeader(header, expected); err != nil {
				return checkpointState{}, err
			}
			continue
		}
		if kind.Type != "result" {
			return checkpointState{}, fmt.Errorf("checkpoint line %d has unsupported record type %q", line, kind.Type)
		}
		var result checkpointResult
		if err := decodeCheckpointJSON(data, &result); err != nil {
			return checkpointState{}, fmt.Errorf("checkpoint line %d: %w", line, err)
		}
		item, exists := caseByID[result.CaseID]
		if !exists {
			return checkpointState{}, fmt.Errorf("checkpoint contains unknown case id %q", result.CaseID)
		}
		if _, exists := saved.Results[result.CaseID]; exists {
			return checkpointState{}, fmt.Errorf("checkpoint contains duplicate result for case %q", result.CaseID)
		}
		if _, exists := saved.Failures[result.CaseID]; exists {
			return checkpointState{}, fmt.Errorf("checkpoint contains duplicate result for case %q", result.CaseID)
		}
		if result.Status == "" && result.Actual != nil {
			result.Status = "success" // Compatibility with schema-1 checkpoints written before statuses were recorded.
		}
		switch result.Status {
		case "success":
			if result.Actual == nil || result.ErrorCategory != "" || result.Diagnostic != "" || result.RawResponsePreserved != nil {
				return checkpointState{}, fmt.Errorf("checkpoint success for case %q has invalid fields", result.CaseID)
			}
			if err := validateCheckpointIntent(*result.Actual, item); err != nil {
				return checkpointState{}, fmt.Errorf("checkpoint result for case %q is invalid: %w", result.CaseID, err)
			}
			saved.Results[result.CaseID] = *result.Actual
		case "invalid_response":
			if result.Actual != nil || result.ErrorCategory != "invalid_response" || strings.TrimSpace(result.Diagnostic) == "" || result.RawResponsePreserved == nil || *result.RawResponsePreserved {
				return checkpointState{}, fmt.Errorf("checkpoint invalid response for case %q has invalid fields", result.CaseID)
			}
			saved.Failures[result.CaseID] = result
		default:
			return checkpointState{}, fmt.Errorf("checkpoint result for case %q has unsupported status %q", result.CaseID, result.Status)
		}
	}
	if err := scanner.Err(); err != nil {
		return checkpointState{}, fmt.Errorf("read checkpoint: %w", err)
	}
	if line == 0 {
		return checkpointState{}, errors.New("checkpoint is empty")
	}
	return saved, nil
}

func validateCheckpointHeader(actual, expected checkpointHeader) error {
	if actual.Type != "header" || actual.SchemaVersion != intentCheckpointSchema {
		return fmt.Errorf("incompatible checkpoint schema: got version %d, want %d", actual.SchemaVersion, intentCheckpointSchema)
	}
	if actual.Model != expected.Model {
		return fmt.Errorf("checkpoint model mismatch: got %q, want %q", actual.Model, expected.Model)
	}
	if actual.Endpoint != expected.Endpoint {
		return fmt.Errorf("checkpoint endpoint mismatch: got %q, want %q", actual.Endpoint, expected.Endpoint)
	}
	if actual.DatasetSHA256 != expected.DatasetSHA256 {
		return errors.New("checkpoint dataset identity mismatch")
	}
	if !sameStrings(actual.CaseIDs, expected.CaseIDs) {
		return errors.New("checkpoint case IDs do not match the current dataset")
	}
	return nil
}

func decodeCheckpointJSON(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	if err := requireJSONEOF(decoder); err != nil {
		return err
	}
	return nil
}
