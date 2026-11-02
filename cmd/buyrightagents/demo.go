package main

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/bhavika44/buyrightagents/internal/verification"
)

// demoScenario deliberately points to the same public fixtures supported by
// `verify`. Labels are presentation only; decision, reason, and totals come
// from the evaluated verification.Report.
type demoScenario struct {
	name     string
	fixture  string
	expected verification.Decision
}

func demoCommand(stdout, stderr io.Writer) int {
	fixturesDir := demoFixturesDir()
	scenarios := []demoScenario{
		{name: "ALLOW", fixture: filepath.Join(fixturesDir, "vegan-backpack.json"), expected: verification.Allow},
		{name: "BLOCK", fixture: filepath.Join(fixturesDir, "non-vegan-backpack.json"), expected: verification.Block},
		{name: "REVIEW", fixture: filepath.Join(fixturesDir, "missing-vegan-evidence.json"), expected: verification.Review},
	}

	fmt.Fprintln(stdout, "BuyRightAgents — Purchase Verification Demo")
	fmt.Fprintln(stdout, "Synthetic fixtures; no network, LLM, merchant, or payment call.")
	fmt.Fprintln(stdout, strings.Repeat("-", 64))

	counts := map[verification.Decision]int{}
	for index, scenario := range scenarios {
		input, err := readDemoInput(scenario.fixture)
		if err != nil {
			return reportError(stderr, fmt.Sprintf("demo scenario %s: %v", scenario.name, err))
		}
		report, err := verification.Evaluate(input)
		if err != nil {
			return reportError(stderr, fmt.Sprintf("demo scenario %s: evaluate input: %v", scenario.name, err))
		}
		if report.Decision != scenario.expected {
			return reportError(stderr, fmt.Sprintf("demo scenario %s: decision = %s, want %s", scenario.name, report.Decision, scenario.expected))
		}

		fmt.Fprintf(stdout, "SCENARIO %d: %s\n", index+1, scenario.name)
		fmt.Fprintf(stdout, "Hard requirements: %s\n", describeHardRequirements(report.Results))
		fmt.Fprintf(stdout, "Cart total: %s\n", formatPaise(cartTotalPaise(report)))
		fmt.Fprintf(stdout, "Evidence: %s\n", demoEvidenceSummary(report))
		fmt.Fprintf(stdout, "Decision: %s\n", report.Decision)
		fmt.Fprintf(stdout, "Reason: %s\n\n", demoReason(report))
		counts[report.Decision]++
	}

	fmt.Fprintln(stdout, strings.Repeat("-", 64))
	fmt.Fprintf(stdout, "%d scenarios executed\n", len(scenarios))
	fmt.Fprintf(stdout, "%d ALLOW | %d BLOCK | %d REVIEW\n", counts[verification.Allow], counts[verification.Block], counts[verification.Review])
	fmt.Fprintln(stdout, "Full JSON reports: go run ./cmd/buyrightagents verify <fixture.json>")
	return 0
}

func demoFixturesDir() string {
	if _, err := os.Stat("examples"); err == nil {
		return "examples"
	}
	return filepath.Join("..", "..", "examples")
}

func readDemoInput(path string) (verification.Input, error) {
	file, err := os.Open(path)
	if err != nil {
		return verification.Input{}, fmt.Errorf("open fixture %q: %w", path, err)
	}
	defer file.Close()
	input, err := decodeInput(file)
	if err != nil {
		return verification.Input{}, fmt.Errorf("read fixture %q: %w", path, err)
	}
	return input, nil
}

func describeHardRequirements(results []verification.RequirementResult) string {
	attributes := make([]string, 0, len(results))
	for _, result := range results {
		if result.Severity == verification.Hard {
			attributes = append(attributes, describeRequirement(result))
		}
	}
	return strings.Join(attributes, "; ")
}

func describeRequirement(result verification.RequirementResult) string {
	switch result.Attribute {
	case "vegan":
		return "vegan"
	case "laptop_fit_inches":
		return fmt.Sprintf("laptop fit ≥ %v inches", result.Expected.Value)
	case "total_price_paise":
		value, ok := result.Expected.Value.(interface{ Int64() (int64, error) })
		if !ok {
			return result.Attribute
		}
		paise, err := value.Int64()
		if err != nil {
			return result.Attribute
		}
		if result.Operator == verification.LessThan {
			return "total price < " + formatPaise(paise)
		}
		return "total price ≤ " + formatPaise(paise)
	case "delivery_date":
		if result.Operator == verification.BeforeOrEqual {
			return fmt.Sprintf("delivery by %v", result.Expected.Value)
		}
		return fmt.Sprintf("delivery %s %v", result.Operator, result.Expected.Value)
	default:
		return result.Attribute
	}
}

func cartTotalPaise(report verification.Report) int64 {
	for _, evidence := range report.Evidence {
		if evidence.ID != "cart-snapshot:total-price-paise" {
			continue
		}
		if value, ok := evidence.Value.Value.(interface{ Int64() (int64, error) }); ok {
			total, err := value.Int64()
			if err == nil {
				return total
			}
		}
	}
	return 0
}

func formatPaise(paise int64) string {
	rupees := fmt.Sprintf("%d", paise/100)
	for offset := len(rupees) - 3; offset > 0; offset -= 3 {
		rupees = rupees[:offset] + "," + rupees[offset:]
	}
	if paise%100 == 0 {
		return "₹" + rupees
	}
	return fmt.Sprintf("₹%s.%02d", rupees, paise%100)
}

func demoEvidenceSummary(report verification.Report) string {
	for _, result := range report.Results {
		switch result.Outcome {
		case verification.Fail:
			return fmt.Sprintf("accepted %s evidence establishes a violation", result.Attribute)
		case verification.Unknown:
			return fmt.Sprintf("no accepted evidence establishes %s", result.Attribute)
		}
	}
	return "accepted evidence supports every hard requirement"
}

func demoReason(report verification.Report) string {
	for _, result := range report.Results {
		if result.Severity != verification.Hard {
			continue
		}
		switch result.Outcome {
		case verification.Fail:
			return fmt.Sprintf("confirmed hard-constraint violation: %s", result.Attribute)
		case verification.Unknown:
			return fmt.Sprintf("missing evidence for hard requirement: %s", result.Attribute)
		}
	}
	return "all hard requirements passed"
}
