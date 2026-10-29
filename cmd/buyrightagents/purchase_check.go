package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"os"
	"strings"
	"time"

	"github.com/bhavika44/buyrightagents/internal/intent"
	"github.com/bhavika44/buyrightagents/internal/verification"
)

type cartEvidenceFile struct {
	CartSnapshot verification.CartSnapshot `json:"cart_snapshot"`
	Evidence     *[]verification.Evidence  `json:"evidence"`
	ItemBindings []itemRequirementBinding  `json:"item_bindings,omitempty"`
}

// itemRequirementBinding is an explicit operator-supplied target for a
// product-level requirement in a multi-item cart. Attribute-only binding is
// intentionally accepted only when the interpreted intent has exactly one
// requirement for that attribute; otherwise mapping is ambiguous and REVIEWs.
type itemRequirementBinding struct {
	Attribute string `json:"attribute"`
	ItemID    string `json:"item_id"`
}

type PurchaseCheckResult struct {
	Request            string                         `json:"request"`
	ReferenceTime      string                         `json:"reference_time"`
	Decision           verification.Decision          `json:"decision"`
	CartSnapshotSHA256 string                         `json:"cart_snapshot_sha256"`
	Intent             *intent.PurchaseIntent         `json:"intent,omitempty"`
	Interpretation     *intent.InterpretationMetadata `json:"interpretation,omitempty"`
	Requirements       []RequirementCheckResult       `json:"requirements"`
	ReviewReasons      []string                       `json:"review_reasons"`
}

type RequirementCheckResult struct {
	Result verification.RequirementResult `json:"result"`
	Reason string                         `json:"reason"`
}

func checkCommand(args []string, stdout, stderr io.Writer, model intent.ModelClient) int {
	referenceTime, cartPath := "", ""
	repair := intent.RepairPolicy{}
	var requestParts []string
	for index := 0; index < len(args); index++ {
		switch args[index] {
		case "--reference-time":
			if referenceTime != "" || index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return reportError(stderr, "usage: buyrightagents check --reference-time <RFC3339> --cart <cart.json> <request text>")
			}
			referenceTime = args[index+1]
			index++
		case "--cart":
			if cartPath != "" || index+1 >= len(args) || strings.HasPrefix(args[index+1], "--") {
				return reportError(stderr, "usage: buyrightagents check --reference-time <RFC3339> --cart <cart.json> <request text>")
			}
			cartPath = args[index+1]
			index++
		default:
			requestParts = append(requestParts, args[index])
		}
	}
	request := strings.TrimSpace(strings.Join(requestParts, " "))
	if _, err := time.Parse(time.RFC3339Nano, referenceTime); err != nil {
		return reportError(stderr, "--reference-time must be an RFC3339 timestamp with an offset")
	}
	if cartPath == "" || request == "" {
		return reportError(stderr, "usage: buyrightagents check --reference-time <RFC3339> --cart <cart.json> <request text>")
	}
	file, err := os.Open(cartPath)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("open cart input %q: %v", cartPath, err))
	}
	defer file.Close()
	cart, err := decodeCartEvidence(file)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("read cart input %q: %v", cartPath, err))
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
	result, err := evaluatePurchase(context.Background(), intent.Interpreter{Client: model, Repair: repair}, request, referenceTime, cart)
	if err != nil {
		return reportError(stderr, fmt.Sprintf("evaluate supplied cart: %v", err))
	}
	encoder := json.NewEncoder(stdout)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(result); err != nil {
		return reportError(stderr, fmt.Sprintf("write purchase check result: %v", err))
	}
	return 0
}

func decodeCartEvidence(reader io.Reader) (cartEvidenceFile, error) {
	data, err := io.ReadAll(reader)
	if err != nil {
		return cartEvidenceFile{}, err
	}
	if err := rejectDuplicateJSONKeys(data); err != nil {
		return cartEvidenceFile{}, err
	}
	var file struct {
		CartSnapshot *verification.CartSnapshot `json:"cart_snapshot"`
		Evidence     *[]verification.Evidence   `json:"evidence"`
		ItemBindings []itemRequirementBinding   `json:"item_bindings"`
	}
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(&file); err != nil {
		return cartEvidenceFile{}, err
	}
	var trailing any
	if err := decoder.Decode(&trailing); err == nil {
		return cartEvidenceFile{}, errors.New("cart input must contain exactly one JSON value")
	} else if !errors.Is(err, io.EOF) {
		return cartEvidenceFile{}, err
	}
	if file.CartSnapshot == nil || file.Evidence == nil {
		return cartEvidenceFile{}, errors.New("cart_snapshot and evidence are required")
	}
	return cartEvidenceFile{CartSnapshot: *file.CartSnapshot, Evidence: file.Evidence, ItemBindings: file.ItemBindings}, nil
}

func evaluatePurchase(ctx context.Context, interpreter intent.Interpreter, request, referenceTime string, cart cartEvidenceFile) (PurchaseCheckResult, error) {
	return evaluatePurchaseWithEvidencePolicy(ctx, interpreter, request, referenceTime, cart, nil)
}

func evaluatePurchaseWithEvidencePolicy(ctx context.Context, interpreter intent.Interpreter, request, referenceTime string, cart cartEvidenceFile, evidencePolicy *verification.EvidenceTrustPolicy) (PurchaseCheckResult, error) {
	if cart.Evidence == nil {
		return PurchaseCheckResult{}, errors.New("cart evidence array is required")
	}
	base, err := verification.Evaluate(verification.Input{CartSnapshot: cart.CartSnapshot, Evidence: *cart.Evidence})
	if err != nil {
		return PurchaseCheckResult{}, fmt.Errorf("validate cart snapshot and evidence: %w", err)
	}
	result := PurchaseCheckResult{
		Request: request, ReferenceTime: referenceTime, Decision: verification.Review,
		CartSnapshotSHA256: base.CartSnapshotSHA256,
		Requirements:       make([]RequirementCheckResult, 0), ReviewReasons: make([]string, 0),
	}
	interpreted, metadata, err := interpreter.InterpretWithMetadata(ctx, request, referenceTime)
	if len(metadata.CoverageDiagnostics) > 0 || metadata.RepairCalls > 0 {
		result.Interpretation = &metadata
	}
	if err != nil {
		result.ReviewReasons = append(result.ReviewReasons, "intent interpretation failed: "+err.Error())
		return result, nil
	}
	result.Intent = &interpreted
	requirements, err := mapIntentRequirements(interpreted, cart.CartSnapshot, cart.ItemBindings)
	if err != nil {
		result.ReviewReasons = append(result.ReviewReasons, "requirement mapping failed: "+err.Error())
		return result, nil
	}
	verified, err := verification.Evaluate(verification.Input{
		CartSnapshot: cart.CartSnapshot, Requirements: requirements, Evidence: *cart.Evidence, EvidencePolicy: evidencePolicy,
	})
	if err != nil {
		result.ReviewReasons = append(result.ReviewReasons, "verification could not safely evaluate interpreted requirements: "+err.Error())
		return result, nil
	}
	result.CartSnapshotSHA256 = verified.CartSnapshotSHA256
	for _, item := range verified.Results {
		result.Requirements = append(result.Requirements, RequirementCheckResult{Result: item, Reason: outcomeReason(item)})
		if item.Outcome == verification.Unknown && item.Severity == verification.Hard {
			result.ReviewReasons = append(result.ReviewReasons, fmt.Sprintf("hard requirement %q has UNKNOWN evidence", item.RequirementID))
		}
	}
	for _, trust := range verified.EvidenceTrust {
		if trust.Outcome != verification.EvidenceTrusted {
			result.ReviewReasons = append(result.ReviewReasons, fmt.Sprintf("evidence %q is unusable: %s", trust.EvidenceID, trust.Reason))
		}
	}
	conflictingRequirementIDs := conflictingHardRequirementIDs(requirements)
	conflict := len(conflictingRequirementIDs) > 0
	independentHardFailure := hasIndependentHardFailure(verified.Results, conflictingRequirementIDs)
	unresolvedIntent := len(interpreted.ClarificationQuestions) > 0 || len(interpreted.UnsupportedConstraints) > 0
	for _, question := range interpreted.ClarificationQuestions {
		result.ReviewReasons = append(result.ReviewReasons, "clarification required: "+question.Question)
	}
	for _, unsupported := range interpreted.UnsupportedConstraints {
		result.ReviewReasons = append(result.ReviewReasons, fmt.Sprintf("unsupported requirement %q: %s", unsupported.SourceText, unsupported.Reason))
	}
	if independentHardFailure {
		result.Decision = verification.Block
	} else if conflict {
		result.Decision = verification.Review
		result.ReviewReasons = append(result.ReviewReasons, "interpreted hard requirements conflict")
	} else if verified.Decision == verification.Block {
		result.Decision = verification.Block
	} else if unresolvedIntent {
		result.Decision = verification.Review
	} else {
		result.Decision = verified.Decision
	}
	return result, nil
}

func mapIntentRequirements(parsed intent.PurchaseIntent, snapshot verification.CartSnapshot, bindings []itemRequirementBinding) ([]verification.Requirement, error) {
	targets, err := resolveItemBindings(parsed, snapshot, bindings)
	if err != nil {
		return nil, err
	}
	result := make([]verification.Requirement, 0, len(parsed.Requirements))
	for _, item := range parsed.Requirements {
		requirement := verification.Requirement{
			ID: item.ID, Attribute: item.Attribute, Operator: item.Operator,
			Expected: item.Expected, Severity: item.Severity,
		}
		switch item.Attribute {
		case "total_price_paise", "delivery_date":
			requirement.Scope = verification.CartScope
			requirement.SubjectID = snapshot.CartID
		default:
			itemID := targets[item.Attribute]
			if itemID == "" {
				if len(snapshot.Items) != 1 {
					return nil, fmt.Errorf("requirement %q for product attribute %q has no unambiguous item target in a cart with %d items; supply an item_bindings entry", item.ID, item.Attribute, len(snapshot.Items))
				}
				itemID = snapshot.Items[0].ItemID
			}
			requirement.Scope = verification.ItemScope
			requirement.SubjectID = itemID
		}
		result = append(result, requirement)
	}
	return result, nil
}

func resolveItemBindings(parsed intent.PurchaseIntent, snapshot verification.CartSnapshot, bindings []itemRequirementBinding) (map[string]string, error) {
	items := make(map[string]struct{}, len(snapshot.Items))
	for _, item := range snapshot.Items {
		items[item.ItemID] = struct{}{}
	}
	requirementCount := make(map[string]int)
	for _, requirement := range parsed.Requirements {
		if requirement.Attribute != "total_price_paise" && requirement.Attribute != "delivery_date" {
			requirementCount[requirement.Attribute]++
		}
	}
	targets := make(map[string]string, len(bindings))
	for _, binding := range bindings {
		if strings.TrimSpace(binding.Attribute) == "" || strings.TrimSpace(binding.ItemID) == "" {
			return nil, errors.New("each item_bindings entry requires attribute and item_id")
		}
		if binding.Attribute == "total_price_paise" || binding.Attribute == "delivery_date" {
			return nil, fmt.Errorf("cart-wide attribute %q must not have an item binding", binding.Attribute)
		}
		if requirementCount[binding.Attribute] == 0 {
			return nil, fmt.Errorf("item binding for attribute %q has no matching interpreted product requirement", binding.Attribute)
		}
		if requirementCount[binding.Attribute] != 1 {
			return nil, fmt.Errorf("item binding for attribute %q is ambiguous because the intent has %d matching requirements", binding.Attribute, requirementCount[binding.Attribute])
		}
		if _, exists := targets[binding.Attribute]; exists {
			return nil, fmt.Errorf("duplicate item binding for attribute %q", binding.Attribute)
		}
		if _, exists := items[binding.ItemID]; !exists {
			return nil, fmt.Errorf("item binding for attribute %q references unknown cart item %q", binding.Attribute, binding.ItemID)
		}
		targets[binding.Attribute] = binding.ItemID
	}
	return targets, nil
}

func outcomeReason(result verification.RequirementResult) string {
	switch result.Outcome {
	case verification.Pass:
		return "supplied evidence satisfies this requirement"
	case verification.Fail:
		if result.Severity == verification.Preference {
			return "supplied evidence does not satisfy this preference; it is not a hard violation"
		}
		return "supplied evidence demonstrates that this hard requirement is violated"
	default:
		return "matching evidence is missing or inconclusive"
	}
}

func conflictingHardRequirementIDs(requirements []verification.Requirement) map[string]bool {
	conflicts := make(map[string]bool)
	for leftIndex, left := range requirements {
		if left.Severity != verification.Hard {
			continue
		}
		for _, right := range requirements[leftIndex+1:] {
			if right.Severity != verification.Hard || left.Attribute != right.Attribute || left.Scope != right.Scope || left.SubjectID != right.SubjectID {
				continue
			}
			if conflictingRequirementPair(left, right) {
				conflicts[left.ID], conflicts[right.ID] = true, true
			}
		}
	}
	return conflicts
}

// hasIndependentHardFailure excludes requirements that are themselves part of
// a contradictory pair. A FAIL of such a disputed interpretation alone is not
// enough to block. A separate, applicable hard FAIL remains a deterministic
// reason to BLOCK even if other hard requirements conflict or are unresolved.
func hasIndependentHardFailure(results []verification.RequirementResult, conflictingRequirementIDs map[string]bool) bool {
	for _, result := range results {
		if result.Severity == verification.Hard && result.Outcome == verification.Fail && !conflictingRequirementIDs[result.RequirementID] {
			return true
		}
	}
	return false
}

func conflictingRequirementPair(left, right verification.Requirement) bool {
	comparison, comparable := compareExpected(left.Expected, right.Expected)
	if !comparable {
		return false
	}
	leftEqual := left.Operator == verification.Equals
	rightEqual := right.Operator == verification.Equals
	leftNotEqual := left.Operator == verification.NotEquals
	rightNotEqual := right.Operator == verification.NotEquals
	if leftEqual && rightEqual && comparison != 0 {
		return true
	}
	if leftEqual && rightNotEqual || rightEqual && leftNotEqual {
		return comparison == 0
	}
	if leftEqual && violatesBound(right, comparison) || rightEqual && violatesBound(left, -comparison) {
		return true
	}
	leftLower, leftInclusive := requirementLowerBound(left)
	rightUpper, rightInclusive := requirementUpperBound(right)
	if leftLower && rightUpper && (comparison > 0 || comparison == 0 && !(leftInclusive && rightInclusive)) {
		return true
	}
	rightLower, rightLowerInclusive := requirementLowerBound(right)
	leftUpper, leftUpperInclusive := requirementUpperBound(left)
	return rightLower && leftUpper && (-comparison > 0 || comparison == 0 && !(rightLowerInclusive && leftUpperInclusive))
}

func violatesBound(bound verification.Requirement, equalityToBound int) bool {
	switch bound.Operator {
	case verification.GreaterThan, verification.GreaterThanOrEqual, verification.After, verification.AfterOrEqual:
		return equalityToBound < 0 || equalityToBound == 0 && !boundInclusive(bound.Operator)
	case verification.LessThan, verification.LessThanOrEqual, verification.Before, verification.BeforeOrEqual:
		return equalityToBound > 0 || equalityToBound == 0 && !boundInclusive(bound.Operator)
	default:
		return false
	}
}

func requirementLowerBound(requirement verification.Requirement) (bool, bool) {
	switch requirement.Operator {
	case verification.GreaterThan, verification.After:
		return true, false
	case verification.GreaterThanOrEqual, verification.AfterOrEqual:
		return true, true
	default:
		return false, false
	}
}

func requirementUpperBound(requirement verification.Requirement) (bool, bool) {
	switch requirement.Operator {
	case verification.LessThan, verification.Before:
		return true, false
	case verification.LessThanOrEqual, verification.BeforeOrEqual:
		return true, true
	default:
		return false, false
	}
}

func boundInclusive(operator verification.Operator) bool {
	return operator == verification.GreaterThanOrEqual || operator == verification.LessThanOrEqual || operator == verification.BeforeOrEqual || operator == verification.AfterOrEqual
}

func compareExpected(left, right verification.Value) (int, bool) {
	if left.Type != right.Type {
		return 0, false
	}
	switch left.Type {
	case verification.StringType, verification.DateType:
		leftValue, leftOK := left.Value.(string)
		rightValue, rightOK := right.Value.(string)
		if !leftOK || !rightOK {
			return 0, false
		}
		return strings.Compare(leftValue, rightValue), true
	case verification.BooleanType:
		leftValue, leftOK := left.Value.(bool)
		rightValue, rightOK := right.Value.(bool)
		if !leftOK || !rightOK {
			return 0, false
		}
		if leftValue == rightValue {
			return 0, true
		}
		if !leftValue {
			return -1, true
		}
		return 1, true
	case verification.NumberType:
		leftRat, leftOK := new(big.Rat).SetString(fmt.Sprint(left.Value))
		rightRat, rightOK := new(big.Rat).SetString(fmt.Sprint(right.Value))
		if !leftOK || !rightOK {
			return 0, false
		}
		return leftRat.Cmp(rightRat), true
	default:
		return 0, false
	}
}
