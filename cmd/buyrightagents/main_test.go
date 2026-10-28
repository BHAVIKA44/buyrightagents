package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunHelpAndVersion(t *testing.T) {
	for _, tt := range []struct {
		args []string
		want string
	}{
		{[]string{"help"}, "Usage: buyrightagents"},
		{[]string{"version"}, "BuyRightAgents 0.1.0"},
	} {
		var stdout, stderr bytes.Buffer
		if code := run(tt.args, &stdout, &stderr); code != 0 {
			t.Fatalf("run(%v) code = %d, stderr %q", tt.args, code, stderr.String())
		}
		if !strings.Contains(stdout.String(), tt.want) || stderr.Len() != 0 {
			t.Fatalf("run(%v) stdout %q, stderr %q", tt.args, stdout.String(), stderr.String())
		}
	}
}

func TestRunVerify(t *testing.T) {
	inputPath := filepath.Join("..", "..", "examples", "vegan-backpack.json")
	var stdout, stderr bytes.Buffer
	if code := run([]string{"verify", inputPath}, &stdout, &stderr); code != 0 {
		t.Fatalf("run verify code = %d, stderr %q", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), `"decision": "ALLOW"`) || !strings.Contains(stdout.String(), `"cart_snapshot_sha256":`) || !strings.Contains(stdout.String(), `"source_id": "fictional-catalogue:backpack-001"`) || !strings.Contains(stdout.String(), `"source_id": "local-cart-snapshot"`) {
		t.Fatalf("verify output missing decision or evidence provenance: %s", stdout.String())
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
}

func TestRunVerifyErrors(t *testing.T) {
	malformedPath := filepath.Join(t.TempDir(), "malformed.json")
	if err := os.WriteFile(malformedPath, []byte(`{"requirements": [`), 0o600); err != nil {
		t.Fatal(err)
	}
	validButInvalidPath := filepath.Join(t.TempDir(), "invalid.json")
	invalidInput := `{"cart_snapshot":{"cart_id":"cart-1","currency":"INR","captured_at":"2026-10-28T10:00:00Z","items":[],"shipping_paise":0,"discount_paise":0},"requirements":[{"id":"","attribute":"vegan","scope":"cart","subject_id":"cart-1","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard"}],"evidence":[]}`
	if err := os.WriteFile(validButInvalidPath, []byte(invalidInput), 0o600); err != nil {
		t.Fatal(err)
	}
	unknownFieldPath := filepath.Join(t.TempDir(), "unknown-field.json")
	if err := os.WriteFile(unknownFieldPath, []byte(`{"unexpected":true}`), 0o600); err != nil {
		t.Fatal(err)
	}
	duplicateTopLevelPath := filepath.Join(t.TempDir(), "duplicate-top-level.json")
	if err := os.WriteFile(duplicateTopLevelPath, []byte(`{"cart_snapshot":{},"cart_snapshot":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	duplicateNestedPath := filepath.Join(t.TempDir(), "duplicate-nested.json")
	if err := os.WriteFile(duplicateNestedPath, []byte(`{"cart_snapshot":{"cart_id":"cart-1","cart_id":"cart-2"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	duplicateDeepPath := filepath.Join(t.TempDir(), "duplicate-deep.json")
	if err := os.WriteFile(duplicateDeepPath, []byte(`{"evidence":[{"value":{"type":"number","type":"boolean"}}]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	missingShippingPath := filepath.Join(t.TempDir(), "missing-shipping.json")
	if err := os.WriteFile(missingShippingPath, []byte(`{"cart_snapshot":{"cart_id":"cart-1","currency":"INR","captured_at":"2026-10-28T10:00:00Z","items":[],"discount_paise":0},"requirements":[],"evidence":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}
	missingItemPricePath := filepath.Join(t.TempDir(), "missing-item-price.json")
	if err := os.WriteFile(missingItemPricePath, []byte(`{"cart_snapshot":{"cart_id":"cart-1","currency":"INR","captured_at":"2026-10-28T10:00:00Z","items":[{"item_id":"item-1","product_id":"product-1","variant_id":"variant-1","quantity":1}],"shipping_paise":0,"discount_paise":0},"requirements":[],"evidence":[]}`), 0o600); err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name string
		args []string
		want string
	}{
		{"malformed JSON", []string{"verify", malformedPath}, "read input"},
		{"nonexistent file", []string{"verify", filepath.Join(t.TempDir(), "missing.json")}, "open input"},
		{"invalid evaluation input", []string{"verify", validButInvalidPath}, "evaluate input"},
		{"unknown JSON field", []string{"verify", unknownFieldPath}, "unknown field"},
		{"duplicate top-level JSON key", []string{"verify", duplicateTopLevelPath}, "duplicate JSON object key"},
		{"duplicate nested JSON key", []string{"verify", duplicateNestedPath}, "duplicate JSON object key"},
		{"duplicate deeply nested JSON key", []string{"verify", duplicateDeepPath}, "duplicate JSON object key"},
		{"missing required shipping field", []string{"verify", missingShippingPath}, "shipping_paise is required"},
		{"missing required item price", []string{"verify", missingItemPricePath}, "unit_price_paise is required"},
		{"missing file argument", []string{"verify"}, "usage: buyrightagents verify"},
		{"extra file argument", []string{"verify", malformedPath, "extra"}, "usage: buyrightagents verify"},
		{"unknown command", []string{"purchase"}, "unknown command"},
		{"extra help argument", []string{"help", "extra"}, "help does not accept arguments"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := run(tt.args, &stdout, &stderr); code == 0 {
				t.Fatalf("run(%v) succeeded; stderr %q", tt.args, stderr.String())
			}
			if !strings.Contains(stderr.String(), tt.want) {
				t.Fatalf("stderr %q does not contain %q", stderr.String(), tt.want)
			}
			if stdout.Len() != 0 {
				t.Fatalf("error wrote successful output to stdout: %q", stdout.String())
			}
		})
	}
}
