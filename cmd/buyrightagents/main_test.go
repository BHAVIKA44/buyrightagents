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
	if !strings.Contains(stdout.String(), `"decision": "ALLOW"`) || !strings.Contains(stdout.String(), `"source_id": "fictional-catalogue:backpack-001"`) {
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
	if err := os.WriteFile(validButInvalidPath, []byte(`{"requirements": [{"id":"", "attribute":"vegan", "operator":"equals", "expected":{"type":"boolean","value":true}, "severity":"hard"}], "evidence": []}`), 0o600); err != nil {
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
