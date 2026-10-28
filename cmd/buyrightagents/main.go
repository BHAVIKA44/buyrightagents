package main

import (
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
	var file struct {
		Requirements *[]verification.Requirement `json:"requirements"`
		Evidence     *[]verification.Evidence    `json:"evidence"`
	}
	decoder := json.NewDecoder(reader)
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
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return verification.Input{}, errors.New("input must contain exactly one JSON value")
		}
		return verification.Input{}, err
	}
	return verification.Input{Requirements: *file.Requirements, Evidence: *file.Evidence}, nil
}

func printHelp(writer io.Writer) {
	fmt.Fprintln(writer, "BuyRightAgents: purchase requirement verification prototype")
	fmt.Fprintln(writer, "Usage: buyrightagents [help|version|verify <input.json>]")
}

func reportError(writer io.Writer, message string) int {
	fmt.Fprintf(writer, "error: %s\n", message)
	return 2
}
