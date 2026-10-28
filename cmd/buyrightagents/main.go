package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

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
	default:
		return reportError(stderr, fmt.Sprintf("unknown command %q; run 'buyrightagents help' for usage", args[0]))
	}
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
	fmt.Fprintln(writer, "Usage: buyrightagents [help|version|verify <input.json>]")
}

func reportError(writer io.Writer, message string) int {
	fmt.Fprintf(writer, "error: %s\n", message)
	return 2
}
