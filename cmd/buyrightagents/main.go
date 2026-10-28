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
		if len(args) != 1 {
			return reportError(stderr, "usage: buyrightagents eval-intent")
		}
		return evaluateIntentCommand(stdout, stderr)
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
	interpreted, err := (intent.Interpreter{Client: client}).Interpret(context.Background(), requestText, referenceTime)
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

func evaluateIntentCommand(stdout, stderr io.Writer) int {
	config, err := intent.ConfigFromEnv()
	if err != nil {
		return reportError(stderr, err.Error())
	}
	client, err := intent.NewHTTPClient(config)
	if err != nil {
		return reportError(stderr, err.Error())
	}
	file, err := os.Open("evals/intent-cases.jsonl")
	if err != nil {
		return reportError(stderr, fmt.Sprintf("open intent evaluation corpus: %v", err))
	}
	defer file.Close()
	cases, err := intent.LoadIntentCases(file)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("load intent evaluation corpus: %v", err))
	}
	summary, err := intent.EvaluateIntentCases(context.Background(), intent.Interpreter{Client: client}, cases)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("run intent evaluation: %v", err))
	}
	intent.WriteIntentEvalSummary(stdout, summary, config.Model, client.Host())
	if !summary.AllExact() {
		return 1
	}
	return 0
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
	fmt.Fprintln(writer, "Usage: buyrightagents [help|version|verify <input.json>|eval <cases.jsonl>|interpret --reference-time <RFC3339> <request text>|eval-intent]")
	fmt.Fprintln(writer, "Note: interpret and eval-intent contact the configured remote LLM; eval-intent may incur API charges.")
}

func reportError(writer io.Writer, message string) int {
	fmt.Fprintf(writer, "error: %s\n", message)
	return 2
}
