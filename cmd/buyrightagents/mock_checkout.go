package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/bhavika44/buyrightagents/internal/checkout"
	"github.com/bhavika44/buyrightagents/internal/evidence"
	"github.com/bhavika44/buyrightagents/internal/intent"
	"github.com/bhavika44/buyrightagents/internal/verification"
)

type mockCheckoutOutput struct {
	PurchaseCheck PurchaseCheckResult     `json:"purchase_check"`
	Approval      *checkout.Approval      `json:"approval,omitempty"`
	Checkout      checkout.CheckoutResult `json:"checkout"`
}

// mockCheckoutCommand is a single-process demonstration of the enforcement
// boundary. A new in-memory gate is created for every command invocation;
// approvals deliberately do not survive process restarts in this prototype.
func mockCheckoutCommand(args []string, stdout, stderr io.Writer, model intent.ModelClient, gate *checkout.Gate) int {
	referenceTime, cartPath, checkoutCartPath, checkoutRequest, request, err := parseMockCheckoutArgs(args)
	repair := intent.RepairPolicy{}
	if err != nil {
		return reportError(stderr, err.Error())
	}
	approvedCart, err := readCartEvidenceFile(cartPath)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("read approval cart %q: %v", cartPath, err))
	}
	if model == nil {
		config, err := intent.ConfigFromEnv()
		if err != nil {
			return reportError(stderr, err.Error())
		}
		client, err := intent.NewHTTPClient(config)
		if err != nil {
			return reportError(stderr, err.Error())
		}
		model = client
		repair = intent.RepairPolicy{MaxAttempts: config.RepairAttempts}
	}
	if gate == nil {
		provider, providerErr := evidence.MockProviderFromEnv()
		if providerErr != nil {
			return reportError(stderr, providerErr.Error())
		}
		gate, err = checkout.NewGateWithEvidencePolicy(nil, nil, provider.TrustPolicy(checkout.DefaultEvidenceMaxAge, checkout.DefaultEvidenceFutureTolerance))
		if err != nil {
			return reportError(stderr, fmt.Sprintf("create mock checkout gate: %v", err))
		}
	}
	policy := gate.EvidencePolicy()
	check, err := evaluatePurchaseWithEvidencePolicy(context.Background(), intent.Interpreter{Client: model, Repair: repair}, request, referenceTime, approvedCart, &policy)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("evaluate supplied cart: %v", err))
	}
	approvedContext := checkoutContext(check, approvedCart)
	approval, err := gate.Issue(approvedContext)
	output := mockCheckoutOutput{PurchaseCheck: check}
	if err != nil {
		output.Checkout = deniedCheckoutResult(gate, err.Error())
		return writeMockCheckoutOutput(stdout, stderr, output)
	}
	output.Approval = &approval

	attemptCart := approvedCart
	if checkoutCartPath != "" {
		attemptCart, err = readCartEvidenceFile(checkoutCartPath)
		if err != nil {
			// This is a checkout attempt with malformed input, not an approval
			// failure. Route it through the gate with an empty snapshot so it is
			// denied and audited without consuming the valid approval.
			output.Checkout = gate.Checkout(approval, checkout.PurchaseContext{
				Request: check.Request, ReferenceTime: check.ReferenceTime, Decision: check.Decision,
			})
			return writeMockCheckoutOutput(stdout, stderr, output)
		}
	}
	attemptContext := checkoutContext(check, attemptCart)
	if checkoutRequest != "" {
		attemptContext.Request = checkoutRequest
	}
	output.Checkout = gate.Checkout(approval, attemptContext)
	return writeMockCheckoutOutput(stdout, stderr, output)
}

func parseMockCheckoutArgs(args []string) (referenceTime, cartPath, checkoutCartPath, checkoutRequest, request string, err error) {
	var requestParts []string
	for index := 0; index < len(args); index++ {
		flag := args[index]
		switch flag {
		case "--reference-time", "--cart", "--checkout-cart", "--checkout-request":
			if index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return "", "", "", "", "", mockCheckoutUsageError()
			}
			value := args[index+1]
			index++
			switch flag {
			case "--reference-time":
				if referenceTime != "" {
					return "", "", "", "", "", mockCheckoutUsageError()
				}
				referenceTime = value
			case "--cart":
				if cartPath != "" {
					return "", "", "", "", "", mockCheckoutUsageError()
				}
				cartPath = value
			case "--checkout-cart":
				if checkoutCartPath != "" {
					return "", "", "", "", "", mockCheckoutUsageError()
				}
				checkoutCartPath = value
			case "--checkout-request":
				if checkoutRequest != "" {
					return "", "", "", "", "", mockCheckoutUsageError()
				}
				checkoutRequest = value
			}
		default:
			requestParts = append(requestParts, flag)
		}
	}
	if _, parseErr := time.Parse(time.RFC3339Nano, referenceTime); parseErr != nil || cartPath == "" {
		return "", "", "", "", "", mockCheckoutUsageError()
	}
	request = strings.TrimSpace(strings.Join(requestParts, " "))
	if request == "" {
		return "", "", "", "", "", mockCheckoutUsageError()
	}
	return referenceTime, cartPath, checkoutCartPath, checkoutRequest, request, nil
}

func mockCheckoutUsageError() error {
	return fmt.Errorf("usage: buyrightagents mock-checkout --reference-time <RFC3339> --cart <cart.json> [--checkout-cart <cart.json>] [--checkout-request <request>] <request text>")
}

func readCartEvidenceFile(path string) (cartEvidenceFile, error) {
	file, err := os.Open(path)
	if err != nil {
		return cartEvidenceFile{}, err
	}
	defer file.Close()
	return decodeCartEvidence(file)
}

func checkoutContext(check PurchaseCheckResult, cart cartEvidenceFile) checkout.PurchaseContext {
	var requirements []verification.Requirement
	if check.Intent != nil {
		mapped, err := mapIntentRequirements(*check.Intent, cart.CartSnapshot, cart.ItemBindings)
		if err == nil {
			requirements = mapped
		}
	}
	evidence := []verification.Evidence(nil)
	if cart.Evidence != nil {
		evidence = append(evidence, (*cart.Evidence)...)
	}
	return checkout.PurchaseContext{Request: check.Request, Requirements: requirements, CartSnapshot: cart.CartSnapshot, Evidence: evidence, ReferenceTime: check.ReferenceTime, Decision: check.Decision}
}

func deniedCheckoutResult(gate *checkout.Gate, reason string) checkout.CheckoutResult {
	audit := gate.AuditTrail()
	if len(audit) == 0 {
		return checkout.CheckoutResult{Reason: reason}
	}
	return checkout.CheckoutResult{Reason: reason, Audit: audit[len(audit)-1]}
}

func writeMockCheckoutOutput(stdout, stderr io.Writer, output mockCheckoutOutput) int {
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(output); err != nil {
		return reportError(stderr, fmt.Sprintf("write mock checkout result: %v", err))
	}
	return 0
}
