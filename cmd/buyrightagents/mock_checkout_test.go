package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/bhavika44/buyrightagents/internal/checkout"
	"github.com/bhavika44/buyrightagents/internal/evidence"
	"github.com/bhavika44/buyrightagents/internal/intent"
	"github.com/bhavika44/buyrightagents/internal/verification"
)

type sequenceIntentClient struct {
	responses []string
	calls     int
}

func (client *sequenceIntentClient) Complete(_ context.Context, _, _ string) (string, error) {
	if client.calls >= len(client.responses) {
		return "", errors.New("unexpected extra model call")
	}
	response := client.responses[client.calls]
	client.calls++
	return response, nil
}

func testMockEvidenceProvider(t *testing.T) *evidence.MockProvider {
	t.Helper()
	provider, err := evidence.NewMockProvider("test-catalogue", []byte("mock-evidence-test-key-0123456789"))
	if err != nil {
		t.Fatal(err)
	}
	return provider
}

func testMockCheckoutGate(t *testing.T) *checkout.Gate {
	t.Helper()
	provider := testMockEvidenceProvider(t)
	gate, err := checkout.NewGateWithEvidencePolicy([]byte("mock-cli-checkout-test-key-0123456789"), func() time.Time {
		return time.Date(2026, 10, 28, 10, 0, 0, 0, time.UTC)
	}, provider.TrustPolicy(24*time.Hour, 5*time.Minute))
	if err != nil {
		t.Fatal(err)
	}
	return gate
}

func writeCheckoutCart(t *testing.T, file cartEvidenceFile) string {
	t.Helper()
	provider := testMockEvidenceProvider(t)
	if file.Evidence != nil {
		attested := make([]verification.Evidence, 0, len(*file.Evidence))
		for _, item := range *file.Evidence {
			trusted, err := provider.Attest(file.CartSnapshot, item)
			if err != nil {
				t.Fatal(err)
			}
			attested = append(attested, trusted)
		}
		file.Evidence = &attested
	}
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cart.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func writeRawCheckoutCart(t *testing.T, file cartEvidenceFile) string {
	t.Helper()
	data, err := json.Marshal(file)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "cart.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestMockCheckoutCommandAuthorizesAndRejectsTampering(t *testing.T) {
	approvedPath := writeCheckoutCart(t, testCart(true, true, 450000, ""))
	tamperedPath := writeCheckoutCart(t, testCart(true, true, 455000, ""))
	args := []string{"--reference-time", demoReferenceTime, "--cart", approvedPath, "Find a vegan bag under ₹5,000"}

	for _, test := range []struct {
		name string
		args []string
		want bool
	}{
		{"authorized", args, true},
		{"changed cart", []string{"--reference-time", demoReferenceTime, "--cart", approvedPath, "--checkout-cart", tamperedPath, "Find a vegan bag under ₹5,000"}, false},
		{"changed request", []string{"--reference-time", demoReferenceTime, "--cart", approvedPath, "--checkout-request", "Find a vegan bag under ₹4,000", "Find a vegan bag under ₹5,000"}, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			if code := mockCheckoutCommand(test.args, &stdout, &stderr, &fixedIntentClient{response: veganAndPriceResponse()}, testMockCheckoutGate(t)); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr.String())
			}
			var output mockCheckoutOutput
			if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
				t.Fatalf("decode output: %v\n%s", err, stdout.String())
			}
			if output.Checkout.Authorized != test.want || output.PurchaseCheck.Decision != verification.Allow || strings.Contains(stdout.String(), "credential") {
				t.Fatalf("unexpected checkout output %#v", output)
			}
			if test.want && (output.Approval == nil || output.Checkout.Audit.Outcome != checkout.CheckoutSucceeded) {
				t.Fatalf("approved checkout lacks approval/audit: %#v", output)
			}
			if !test.want && output.Checkout.Audit.Outcome != checkout.CheckoutRejected {
				t.Fatalf("tampering was not audited as rejection: %#v", output)
			}
		})
	}
}

func TestTargetedRepairControlsFinalMockCheckoutAuthorization(t *testing.T) {
	partial := `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	ungrounded := `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"bag"},"severity":"hard","source_text":"leather suitcase"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	for _, test := range []struct {
		name           string
		responses      []string
		wantAuthorized bool
		wantRepairOK   bool
	}{
		{"successful repair", []string{partial, veganAndPriceResponse()}, true, true},
		{"unresolved repair", []string{partial, partial}, false, false},
		{"ungrounded repair", []string{partial, ungrounded}, false, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			cart, err := readCartEvidenceFile(writeCheckoutCart(t, testCart(true, true, 450000, "")))
			if err != nil {
				t.Fatal(err)
			}
			gate := testMockCheckoutGate(t)
			policy := gate.EvidencePolicy()
			model := &sequenceIntentClient{responses: test.responses}
			check, err := evaluatePurchaseWithEvidencePolicy(context.Background(), intent.Interpreter{
				Client: model, Repair: intent.RepairPolicy{MaxAttempts: 1},
			}, "Find a vegan bag under ₹5,000", demoReferenceTime, cart, &policy)
			if err != nil {
				t.Fatal(err)
			}
			if model.calls != 2 || check.Interpretation == nil || check.Interpretation.RepairCalls != 1 || check.Interpretation.RepairSucceeded != test.wantRepairOK {
				t.Fatalf("repair telemetry=%#v calls=%d", check.Interpretation, model.calls)
			}
			approval, issueErr := gate.Issue(checkoutContext(check, cart))
			if test.wantAuthorized {
				if check.Decision != verification.Allow || issueErr != nil {
					t.Fatalf("successful repair did not produce eligible ALLOW: check=%#v err=%v", check, issueErr)
				}
				if checkout := gate.Checkout(approval, checkoutContext(check, cart)); !checkout.Authorized {
					t.Fatalf("successful repair did not authorize a verified mock checkout: %#v", checkout)
				}
				return
			}
			if check.Decision != verification.Review || issueErr == nil || approval.Reference != "" {
				t.Fatalf("unresolved repair authorized checkout: check=%#v approval=%#v err=%v", check, approval, issueErr)
			}
		})
	}
}

func TestMockCheckoutCommandDeniesNonAllowAndModelFailure(t *testing.T) {
	tests := []struct {
		name, request, response string
		cart                    cartEvidenceFile
	}{
		{"hard budget failure", "Find a vegan bag under ₹5,000", veganAndPriceResponse(), testCart(true, true, 510000, "")},
		{"missing hard evidence", "Find a vegan bag under ₹5,000", veganAndPriceResponse(), testCart(false, true, 450000, "")},
		{"conflicting intent", "vegan and not vegan", `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"},{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":false},"severity":"hard","source_text":"not vegan"}],"clarification_questions":[],"unsupported_constraints":[]}`, testCart(true, true, 450000, "")},
		{"unsupported intent", "Find a vegan bag with a unicorn horn", `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"}],"clarification_questions":[],"unsupported_constraints":[{"source_text":"unicorn horn","reason":"no supported attribute"}]}`, testCart(true, true, 450000, "")},
		{"model failure", "Find a vegan bag under ₹5,000", `{`, testCart(true, true, 450000, "")},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			args := []string{"--reference-time", demoReferenceTime, "--cart", writeCheckoutCart(t, test.cart), test.request}
			if code := mockCheckoutCommand(args, &stdout, &stderr, &fixedIntentClient{response: test.response}, testMockCheckoutGate(t)); code != 0 {
				t.Fatalf("code=%d stderr=%s", code, stderr.String())
			}
			var output mockCheckoutOutput
			if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
				t.Fatal(err)
			}
			if output.Checkout.Authorized || output.Approval != nil || output.Checkout.Audit.Outcome != checkout.ApprovalRejected {
				t.Fatalf("non-allow decision received checkout approval: %#v", output)
			}
		})
	}
}

func TestMockCheckoutCommandDeniesInterpreterError(t *testing.T) {
	var stdout, stderr bytes.Buffer
	args := []string{"--reference-time", demoReferenceTime, "--cart", writeCheckoutCart(t, testCart(true, true, 450000, "")), "Find a vegan bag under ₹5,000"}
	if code := mockCheckoutCommand(args, &stdout, &stderr, &fixedIntentClient{err: errors.New("synthetic model unavailable")}, testMockCheckoutGate(t)); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var output mockCheckoutOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.Checkout.Authorized || output.Approval != nil || output.PurchaseCheck.Decision != verification.Review || output.Checkout.Audit.Outcome != checkout.ApprovalRejected {
		t.Fatalf("interpreter error received checkout approval: %#v", output)
	}
}

func TestMockCheckoutCommandUsesOpenAICompatibleMock(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost || request.URL.Path != "/v1/chat/completions" || request.Header.Get("Authorization") != "Bearer local-test-key" {
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		_ = json.NewEncoder(writer).Encode(map[string]any{
			"choices": []any{map[string]any{"message": map[string]string{"content": veganAndPriceResponse()}}},
		})
	}))
	defer server.Close()
	t.Setenv("BUYRIGHTAGENTS_LLM_API_KEY", "local-test-key")
	t.Setenv("BUYRIGHTAGENTS_LLM_MODEL", "local-test-model")
	t.Setenv("BUYRIGHTAGENTS_LLM_BASE_URL", server.URL+"/v1")
	t.Setenv(evidence.ProviderIDEnv, "test-catalogue")
	t.Setenv(evidence.ProviderKeyEnv, "mock-evidence-test-key-0123456789")

	cart := testCart(true, true, 450000, "")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	cart.CartSnapshot.CapturedAt = now
	for index := range *cart.Evidence {
		(*cart.Evidence)[index].ObservedAt = now
	}
	var stdout, stderr bytes.Buffer
	args := []string{"--reference-time", demoReferenceTime, "--cart", writeCheckoutCart(t, cart), "Find a vegan bag under ₹5,000"}
	if code := mockCheckoutCommand(args, &stdout, &stderr, nil, nil); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var output mockCheckoutOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.PurchaseCheck.Decision != verification.Allow || !output.Checkout.Authorized || output.Checkout.Audit.Outcome != checkout.CheckoutSucceeded {
		t.Fatalf("OpenAI-compatible mock did not reach checkout enforcement: %#v", output)
	}
}

func TestMockCheckoutCommandConfiguresStrictEvidenceGate(t *testing.T) {
	t.Setenv(evidence.ProviderIDEnv, "test-catalogue")
	t.Setenv(evidence.ProviderKeyEnv, "mock-evidence-test-key-0123456789")
	// A nil gate owns its clock, so this fixture must be fresh relative to the
	// actual invocation time rather than the deliberately old intent reference.
	cart := testCart(true, true, 450000, "")
	now := time.Now().UTC().Format(time.RFC3339Nano)
	cart.CartSnapshot.CapturedAt = now
	for index := range *cart.Evidence {
		(*cart.Evidence)[index].ObservedAt = now
	}
	args := []string{"--reference-time", demoReferenceTime, "--cart", writeCheckoutCart(t, cart), "Find a vegan bag under ₹5,000"}
	var stdout, stderr bytes.Buffer
	if code := mockCheckoutCommand(args, &stdout, &stderr, &fixedIntentClient{response: veganAndPriceResponse()}, nil); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var output mockCheckoutOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.PurchaseCheck.Decision != verification.Allow || output.Approval == nil || !output.Checkout.Authorized {
		t.Fatalf("configured trusted evidence did not permit checkout: %#v", output)
	}
}

func TestMockCheckoutCommandRejectsUnattestedEvidence(t *testing.T) {
	t.Setenv(evidence.ProviderIDEnv, "test-catalogue")
	t.Setenv(evidence.ProviderKeyEnv, "mock-evidence-test-key-0123456789")
	args := []string{"--reference-time", demoReferenceTime, "--cart", writeRawCheckoutCart(t, testCart(true, true, 450000, "")), "Find a vegan bag under ₹5,000"}
	var stdout, stderr bytes.Buffer
	if code := mockCheckoutCommand(args, &stdout, &stderr, &fixedIntentClient{response: veganAndPriceResponse()}, nil); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var output mockCheckoutOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.PurchaseCheck.Decision != verification.Review || output.Approval != nil || output.Checkout.Authorized || output.Checkout.Audit.Outcome != checkout.ApprovalRejected || output.Checkout.Audit.VerifierDecision != verification.Review {
		t.Fatalf("unattested evidence issued mock checkout approval: %#v", output)
	}
}

func TestMockCheckoutCommandAllowsPreferenceFailureWhenHardRequirementsPass(t *testing.T) {
	response := `{"requirements":[{"attribute":"color","operator":"equals","expected":{"type":"string","value":"red"},"severity":"preference","source_text":"Prefer red"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"total must be under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	var stdout, stderr bytes.Buffer
	args := []string{"--reference-time", demoReferenceTime, "--cart", writeCheckoutCart(t, testCart(true, true, 450000, "blue")), "Prefer red, but total must be under ₹5,000"}
	if code := mockCheckoutCommand(args, &stdout, &stderr, &fixedIntentClient{response: response}, testMockCheckoutGate(t)); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var output mockCheckoutOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.PurchaseCheck.Decision != verification.Allow || !output.Checkout.Authorized || output.Approval == nil {
		t.Fatalf("preference failure incorrectly prevented checkout: %#v", output)
	}
}

func TestMockCheckoutCommandAuditsMalformedCheckoutCartWithoutConsumingApproval(t *testing.T) {
	approvedPath := writeCheckoutCart(t, testCart(true, true, 450000, ""))
	brokenPath := filepath.Join(t.TempDir(), "broken-cart.json")
	if err := os.WriteFile(brokenPath, []byte(`{"cart_snapshot":{}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	args := []string{"--reference-time", demoReferenceTime, "--cart", approvedPath, "--checkout-cart", brokenPath, "Find a vegan bag under ₹5,000"}
	if code := mockCheckoutCommand(args, &stdout, &stderr, &fixedIntentClient{response: veganAndPriceResponse()}, testMockCheckoutGate(t)); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var output mockCheckoutOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.Approval == nil || output.Checkout.Authorized || output.Checkout.Audit.Outcome != checkout.CheckoutRejected || !strings.Contains(output.Checkout.Reason, "invalid checkout context") {
		t.Fatalf("malformed checkout cart was not safely audited: %#v", output)
	}
}

func TestMockCheckoutCommandRejectsChangedMultiItemBinding(t *testing.T) {
	evidence := []verification.Evidence{
		itemEvidence("vegan-a", "vegan", "item-a", "product-bag", "variant-red", verification.BooleanValue(true)),
		itemEvidence("vegan-b", "vegan", "item-b", "product-bag", "variant-black", verification.BooleanValue(true)),
	}
	approvedCart := multiItemCart(evidence, []itemRequirementBinding{{Attribute: "vegan", ItemID: "item-a"}})
	attemptCart := multiItemCart(evidence, []itemRequirementBinding{{Attribute: "vegan", ItemID: "item-b"}})
	response := `{"requirements":[{"attribute":"vegan","operator":"equals","expected":{"type":"boolean","value":true},"severity":"hard","source_text":"vegan"}],"clarification_questions":[],"unsupported_constraints":[]}`
	var stdout, stderr bytes.Buffer
	args := []string{"--reference-time", demoReferenceTime, "--cart", writeCheckoutCart(t, approvedCart), "--checkout-cart", writeCheckoutCart(t, attemptCart), "Find a vegan item"}
	if code := mockCheckoutCommand(args, &stdout, &stderr, &fixedIntentClient{response: response}, testMockCheckoutGate(t)); code != 0 {
		t.Fatalf("code=%d stderr=%s", code, stderr.String())
	}
	var output mockCheckoutOutput
	if err := json.Unmarshal(stdout.Bytes(), &output); err != nil {
		t.Fatal(err)
	}
	if output.PurchaseCheck.Decision != verification.Allow || output.Checkout.Authorized || output.Checkout.Audit.Outcome != checkout.CheckoutRejected || !strings.Contains(output.Checkout.Reason, "does not match") {
		t.Fatalf("changed item binding was accepted: %#v", output)
	}
}

func TestMockCheckoutCommandRejectsInvalidArguments(t *testing.T) {
	var stdout, stderr bytes.Buffer
	if code := mockCheckoutCommand([]string{"--cart", "missing.json", "vegan"}, &stdout, &stderr, &fixedIntentClient{}, testMockCheckoutGate(t)); code == 0 || !strings.Contains(stderr.String(), "usage: buyrightagents mock-checkout") {
		t.Fatalf("invalid arguments were accepted: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}

func TestNegativeCategoryMockCheckout(t *testing.T) {
	request := "I need a backpack, not a tote, under ₹5,000."
	response := `{"requirements":[{"attribute":"product_category","operator":"equals","expected":{"type":"string","value":"backpack"},"severity":"hard","source_text":"backpack"},{"attribute":"product_category","operator":"not_equals","expected":{"type":"string","value":"tote"},"severity":"hard","source_text":"not a tote"},{"attribute":"total_price_paise","operator":"less_than","expected":{"type":"number","value":500000},"severity":"hard","source_text":"under ₹5,000"}],"clarification_questions":[],"unsupported_constraints":[]}`
	for _, tt := range []struct {
		name, category string
		want           verification.Decision
	}{
		{name: "backpack", category: "backpack", want: verification.Allow},
		{name: "tote", category: "tote", want: verification.Block},
		{name: "missing category", want: verification.Review},
	} {
		t.Run(tt.name, func(t *testing.T) {
			cart := testCart(true, true, 199000, "")
			records := make([]verification.Evidence, 0, len(*cart.Evidence))
			for _, e := range *cart.Evidence {
				if e.Attribute == "product_category" {
					if tt.category == "" {
						continue
					}
					e.Value = verification.StringValue(tt.category)
				}
				records = append(records, e)
			}
			cart.Evidence = &records
			trusted, err := readCartEvidenceFile(writeCheckoutCart(t, cart))
			if err != nil {
				t.Fatal(err)
			}
			gate := testMockCheckoutGate(t)
			policy := gate.EvidencePolicy()
			check, err := evaluatePurchaseWithEvidencePolicy(context.Background(), testInterpreter(response), request, demoReferenceTime, trusted, &policy)
			if err != nil {
				t.Fatal(err)
			}
			if check.Decision != tt.want {
				t.Fatalf("check=%+v", check)
			}
			approval, issueErr := gate.Issue(checkoutContext(check, trusted))
			if tt.want == verification.Allow {
				if issueErr != nil {
					t.Fatal(issueErr)
				}
				if result := gate.Checkout(approval, checkoutContext(check, trusted)); !result.Authorized {
					t.Fatalf("checkout=%+v", result)
				}
			} else if issueErr == nil {
				t.Fatal("non-ALLOW issued approval")
			}
		})
	}
}
