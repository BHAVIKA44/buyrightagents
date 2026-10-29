package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bhavika44/buyrightagents/internal/evaluation"
	"github.com/bhavika44/buyrightagents/internal/intent"
	"github.com/bhavika44/buyrightagents/internal/verification"
)

const version = "0.1.0"

func main() {
	os.Exit(run(os.Args[1:], os.Stdout, os.Stderr))
}

func run(args []string, stdout, stderr io.Writer) int {
	if len(args) == 0 {
		printHelp(stdout)
		return 0
	}

	switch args[0] {
	case "help", "--help", "-h":
		if len(args) != 1 {
			return reportError(stderr, "help does not accept arguments")
		}
		printHelp(stdout)
		return 0
	case "version", "--version", "-v":
		if len(args) != 1 {
			return reportError(stderr, "version does not accept arguments")
		}
		fmt.Fprintf(stdout, "BuyRightAgents %s\n", version)
		return 0
	case "verify":
		if len(args) != 2 {
			return reportError(stderr, "usage: buyrightagents verify <input.json>")
		}
		if args[1] == "" {
			return reportError(stderr, "input file path is required")
		}
		return verifyFile(args[1], stdout, stderr)
	case "check":
		return checkCommand(args[1:], stdout, stderr, nil)
	case "mock-checkout":
		return mockCheckoutCommand(args[1:], stdout, stderr, nil, nil)
	case "eval":
		if len(args) != 2 {
			return reportError(stderr, "usage: buyrightagents eval <cases.jsonl>")
		}
		if args[1] == "" {
			return reportError(stderr, "evaluation dataset path is required")
		}
		return evaluateFile(args[1], stdout, stderr)
	case "interpret":
		return interpretCommand(args[1:], stdout, stderr)
	case "eval-intent":
		return evaluateIntentCommand(args[1:], stdout, stderr)
	case "adversarial-eval":
		return adversarialEvalCommand(args[1:], stdout, stderr)
	case "saved-groq-checkout-eval":
		return savedGroqCheckoutEvalCommand(args[1:], stdout, stderr)
	default:
		return reportError(stderr, fmt.Sprintf("unknown command %q; run 'buyrightagents help' for usage", args[0]))
	}
}

func interpretCommand(args []string, stdout, stderr io.Writer) int {
	if len(args) < 3 || args[0] != "--reference-time" {
		return reportError(stderr, "usage: buyrightagents interpret --reference-time <RFC3339> <request text>")
	}
	referenceTime := args[1]
	if _, err := time.Parse(time.RFC3339Nano, referenceTime); err != nil {
		return reportError(stderr, fmt.Sprintf("reference time %q must be RFC3339 with an offset", referenceTime))
	}
	requestText := strings.TrimSpace(strings.Join(args[2:], " "))
	if requestText == "" {
		return reportError(stderr, "request text is required")
	}
	config, err := intent.ConfigFromEnv()
	if err != nil {
		return reportError(stderr, err.Error())
	}
	client, err := intent.NewHTTPClient(config)
	if err != nil {
		return reportError(stderr, err.Error())
	}
	interpreted, _, err := (intent.Interpreter{Client: client, Repair: intent.RepairPolicy{MaxAttempts: config.RepairAttempts}}).InterpretWithMetadata(context.Background(), requestText, referenceTime)
	if err != nil {
		return reportError(stderr, err.Error())
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(interpreted); err != nil {
		return reportError(stderr, fmt.Sprintf("write intent: %v", err))
	}
	return 0
}

func evaluateIntentCommand(args []string, stdout, stderr io.Writer) int {
	checkpointPath := ""
	datasetPath := "evals/intent-cases.jsonl"
	resume := false
	requestInterval := 3 * time.Second
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--checkpoint":
			if checkpointPath != "" || index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return reportError(stderr, "usage: buyrightagents eval-intent --checkpoint <path> [--resume] [--request-interval <duration>] [--dataset <path>]")
			}
			checkpointPath = args[index+1]
			index++
		case "--resume":
			if resume {
				return reportError(stderr, "--resume may only be specified once")
			}
			resume = true
		case "--request-interval":
			if index+1 >= len(args) {
				return reportError(stderr, "usage: buyrightagents eval-intent --checkpoint <path> [--resume] [--request-interval <duration>] [--dataset <path>]")
			}
			parsed, err := time.ParseDuration(args[index+1])
			if err != nil || parsed < 0 {
				return reportError(stderr, "request interval must be a non-negative duration such as 3s or 0s")
			}
			requestInterval = parsed
			index++
		case "--dataset":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return reportError(stderr, "usage: buyrightagents eval-intent --checkpoint <path> [--resume] [--request-interval <duration>] [--dataset <path>]")
			}
			datasetPath = args[index+1]
			index++
		default:
			return reportError(stderr, "usage: buyrightagents eval-intent --checkpoint <path> [--resume] [--request-interval <duration>] [--dataset <path>]")
		}
	}
	if checkpointPath == "" {
		return reportError(stderr, "usage: buyrightagents eval-intent --checkpoint <path> [--resume] [--request-interval <duration>] [--dataset <path>]")
	}
	config, err := intent.ConfigFromEnv()
	if err != nil {
		return reportError(stderr, err.Error())
	}
	client, err := intent.NewHTTPClient(config)
	if err != nil {
		return reportError(stderr, err.Error())
	}
	dataset, err := os.ReadFile(datasetPath)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("read intent evaluation corpus %q: %v", datasetPath, err))
	}
	cases, err := intent.LoadIntentCases(bytes.NewReader(dataset))
	if err != nil {
		return reportError(stderr, fmt.Sprintf("load intent evaluation corpus: %v", err))
	}
	fmt.Fprintf(stdout, "Intent evaluation configuration: model=%s endpoint=%s request-interval=%s dataset=%s checkpoint=%s\n", config.Model, config.BaseURL, requestInterval, datasetPath, checkpointPath)
	summary, err := intent.RunIntentEvaluationCheckpointed(context.Background(), intent.Interpreter{Client: client}, cases, intent.IntentCheckpointOptions{
		Dataset: dataset, Model: config.Model, Endpoint: config.BaseURL, Path: checkpointPath,
		Resume: resume, RequestInterval: requestInterval, Progress: stdout,
	})
	if err != nil {
		var caseFailure *intent.CaseEvaluationFailure
		if errors.As(err, &caseFailure) {
			fmt.Fprintf(stdout, "\nIntent evaluation INCOMPLETE: %d/%d cases have recorded outcomes; stopped at case %s; error category: %s.\n", summary.CasesRun, len(cases), caseFailure.CaseID, caseFailure.Category)
			fmt.Fprintln(stdout, "Partial metrics only (not final corpus metrics):")
			intent.WriteIntentEvalSummary(stdout, summary, config.Model, client.Host())
			fmt.Fprintf(stdout, "Resume command: buyrightagents eval-intent --checkpoint %s --resume --request-interval %s --dataset %s\n", shellQuote(checkpointPath), requestInterval, shellQuote(datasetPath))
			return reportError(stderr, err.Error())
		}
		return reportError(stderr, fmt.Sprintf("run intent evaluation: %v", err))
	}
	fmt.Fprintf(stdout, "\nIntent evaluation COMPLETE: %d/%d cases have recorded outcomes (%d valid, %d invalid).\n", summary.CasesRun, len(cases), summary.ValidResponses, summary.InvalidResponses)
	intent.WriteIntentEvalSummary(stdout, summary, config.Model, client.Host())
	if !summary.AllExact() {
		return 1
	}
	return 0
}

func shellQuote(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "'\"'\"'") + "'"
}

func evaluateFile(path string, stdout, stderr io.Writer) int {
	file, err := os.Open(path)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("open evaluation dataset %q: %v", path, err))
	}
	defer file.Close()

	cases, err := evaluation.LoadCases(file)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("load evaluation dataset %q: %v", path, err))
	}
	summary, err := evaluation.Run(cases)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("run evaluation dataset %q: %v", path, err))
	}
	evaluation.WriteSummary(stdout, summary)
	for _, result := range summary.Cases {
		if !result.DecisionMatches || len(result.MismatchedOutcomes) > 0 {
			return 1
		}
	}
	return 0
}

func verifyFile(path string, stdout, stderr io.Writer) int {
	file, err := os.Open(path)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("open input %q: %v", path, err))
	}
	defer file.Close()

	input, err := decodeInput(file)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("read input %q: %v", path, err))
	}

	report, err := verification.Evaluate(input)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("evaluate input %q: %v", path, err))
	}

	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(report); err != nil {
		return reportError(stderr, fmt.Sprintf("write report: %v", err))
	}
	return 0
}

func decodeInput(reader io.Reader) (verification.Input, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return verification.Input{}, err
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return verification.Input{}, err
	}
	var file struct {
		CartSnapshot verification.CartSnapshot   `json:"cart_snapshot"`
		Requirements *[]verification.Requirement `json:"requirements"`
		Evidence     *[]verification.Evidence    `json:"evidence"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&file); err != nil {
		return verification.Input{}, err
	}
	if file.Requirements == nil {
		return verification.Input{}, errors.New("requirements array is required")
	}
	if file.Evidence == nil {
		return verification.Input{}, errors.New("evidence array is required")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return verification.Input{}, errors.New("input must contain exactly one JSON value")
	} else if !errors.Is(err, io.EOF) {
		return verification.Input{}, err
	}
	return verification.Input{
		CartSnapshot: file.CartSnapshot,
		Requirements: *file.Requirements,
		Evidence:     *file.Evidence,
	}, nil
}

func rejectDuplicateJSONKeys(data []byte) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.UseNumber()
	if err := scanJSONValue(decoder); err != nil {
		return err
	}
	if _, err := decoder.Token(); err == nil {
		return fmt.Errorf("input must contain exactly one JSON value")
	} else if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

func scanJSONValue(decoder *json.Decoder) error {
	token, err := decoder.Token()
	if err != nil {
		return err
	}
	delimiter, isDelimiter := token.(json.Delim)
	if !isDelimiter {
		return nil
	}
	switch delimiter {
	case '{':
		keys := make(map[string]struct{})
		for decoder.More() {
			keyToken, err := decoder.Token()
			if err != nil {
				return err
			}
			key, ok := keyToken.(string)
			if !ok {
				return fmt.Errorf("JSON object key is not a string")
			}
			if _, exists := keys[key]; exists {
				return fmt.Errorf("duplicate JSON object key %q", key)
			}
			keys[key] = struct{}{}
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim('}') {
			return fmt.Errorf("malformed JSON object")
		}
	case '[':
		for decoder.More() {
			if err := scanJSONValue(decoder); err != nil {
				return err
			}
		}
		closing, err := decoder.Token()
		if err != nil {
			return err
		}
		if closing != json.Delim(']') {
			return fmt.Errorf("malformed JSON array")
		}
	default:
		return fmt.Errorf("unexpected JSON delimiter %q", delimiter)
	}
	return nil
}

func printHelp(writer io.Writer) {
	fmt.Fprintln(writer, "BuyRightAgents: purchase requirement verification prototype")
	fmt.Fprintln(writer, "Usage: buyrightagents [help|version|verify <input.json>|check --reference-time <RFC3339> --cart <cart.json> <request text>|mock-checkout --reference-time <RFC3339> --cart <cart.json> [--checkout-cart <cart.json>] [--checkout-request <request>] <request text>|eval <cases.jsonl>|adversarial-eval --dataset <cases.jsonl> --json <results.json> --report <report.md>|saved-groq-checkout-eval --dataset <cases.jsonl> --checkpoint <saved.jsonl> --manifest <manifest.json> --json <results.json> --report <report.md> [--freeze-only]|interpret --reference-time <RFC3339> <request text>|eval-intent --checkpoint <path> [--resume] [--request-interval <duration>] [--dataset <path>]]")
	fmt.Fprintln(writer, "Note: adversarial-eval is offline. Check, interpret, mock-checkout, and eval-intent contact the configured remote LLM; mock-checkout also requires configured mock-provider evidence attestations; eval-intent may incur API charges.")
}

func reportError(writer io.Writer, message string) int {
	fmt.Fprintf(writer, "error: %s\n", message)
	return 2
}
