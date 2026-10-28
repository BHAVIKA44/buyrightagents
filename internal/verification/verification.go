// Package verification evaluates structured purchase requirements against
// explicitly typed evidence and a validated local cart snapshot.
package verification

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/big"
	"sort"
	"strconv"
	"strings"
	"time"
)

type ValueType string

const (
	StringType  ValueType = "string"
	NumberType  ValueType = "number"
	BooleanType ValueType = "boolean"
	DateType    ValueType = "date"
)

// Value uses an explicit {"type": ..., "value": ...} JSON representation.
// Its value must have the corresponding Go type when constructed directly.
type Value struct {
	Type  ValueType `json:"type"`
	Value any       `json:"value"`
}

func StringValue(value string) Value { return Value{Type: StringType, Value: value} }
func NumberValue(value float64) Value {
	return Value{Type: NumberType, Value: json.Number(strconv.FormatFloat(value, 'g', -1, 64))}
}
func NumberInt64Value(value int64) Value {
	return Value{Type: NumberType, Value: json.Number(strconv.FormatInt(value, 10))}
}
func BooleanValue(value bool) Value { return Value{Type: BooleanType, Value: value} }
func DateValue(value string) Value  { return Value{Type: DateType, Value: value} }

type Severity string

const (
	Hard       Severity = "hard"
	Preference Severity = "preference"
)

type Scope string

const (
	CartScope Scope = "cart"
	ItemScope Scope = "item"
)

type Operator string

const (
	Equals             Operator = "equals"
	NotEquals          Operator = "not_equals"
	LessThanOrEqual    Operator = "less_than_or_equal"
	GreaterThanOrEqual Operator = "greater_than_or_equal"
	BeforeOrEqual      Operator = "before_or_equal"
	AfterOrEqual       Operator = "after_or_equal"
)

type Outcome string

const (
	Pass    Outcome = "PASS"
	Fail    Outcome = "FAIL"
	Unknown Outcome = "UNKNOWN"
)

type Decision string

const (
	Allow  Decision = "ALLOW"
	Block  Decision = "BLOCK"
	Review Decision = "REVIEW"
)

type CartItem struct {
	ItemID         string `json:"item_id"`
	ProductID      string `json:"product_id"`
	VariantID      string `json:"variant_id"`
	Quantity       int64  `json:"quantity"`
	UnitPricePaise int64  `json:"unit_price_paise"`
}

// UnmarshalJSON distinguishes omitted integer fields from legitimate zeroes.
func (item *CartItem) UnmarshalJSON(data []byte) error {
	var wire struct {
		ItemID         string `json:"item_id"`
		ProductID      string `json:"product_id"`
		VariantID      string `json:"variant_id"`
		Quantity       *int64 `json:"quantity"`
		UnitPricePaise *int64 `json:"unit_price_paise"`
	}
	if err := decodeStrict(data, &wire); err != nil {
		return err
	}
	if wire.Quantity == nil {
		return fmt.Errorf("quantity is required")
	}
	if wire.UnitPricePaise == nil {
		return fmt.Errorf("unit_price_paise is required")
	}
	*item = CartItem{
		ItemID: wire.ItemID, ProductID: wire.ProductID, VariantID: wire.VariantID,
		Quantity: *wire.Quantity, UnitPricePaise: *wire.UnitPricePaise,
	}
	return nil
}

type CartSnapshot struct {
	CartID        string     `json:"cart_id"`
	Currency      string     `json:"currency"`
	CapturedAt    string     `json:"captured_at"`
	Items         []CartItem `json:"items"`
	ShippingPaise int64      `json:"shipping_paise"`
	DiscountPaise int64      `json:"discount_paise"`
	DeliveryDate  *string    `json:"delivery_date,omitempty"`
}

// UnmarshalJSON ensures required zero-valued money fields and the items list
// are present instead of silently defaulting omitted fields.
func (snapshot *CartSnapshot) UnmarshalJSON(data []byte) error {
	var wire struct {
		CartID        string      `json:"cart_id"`
		Currency      string      `json:"currency"`
		CapturedAt    string      `json:"captured_at"`
		Items         *[]CartItem `json:"items"`
		ShippingPaise *int64      `json:"shipping_paise"`
		DiscountPaise *int64      `json:"discount_paise"`
		DeliveryDate  *string     `json:"delivery_date"`
	}
	if err := decodeStrict(data, &wire); err != nil {
		return err
	}
	if wire.Items == nil {
		return fmt.Errorf("items list is required")
	}
	if wire.ShippingPaise == nil {
		return fmt.Errorf("shipping_paise is required")
	}
	if wire.DiscountPaise == nil {
		return fmt.Errorf("discount_paise is required")
	}
	*snapshot = CartSnapshot{
		CartID: wire.CartID, Currency: wire.Currency, CapturedAt: wire.CapturedAt,
		Items: *wire.Items, ShippingPaise: *wire.ShippingPaise,
		DiscountPaise: *wire.DiscountPaise, DeliveryDate: wire.DeliveryDate,
	}
	return nil
}

func decodeStrict(data []byte, target any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	var trailing any
	err := decoder.Decode(&trailing)
	if err == nil {
		return fmt.Errorf("unexpected trailing JSON value")
	}
	if !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

type Requirement struct {
	ID        string   `json:"id"`
	Attribute string   `json:"attribute"`
	Scope     Scope    `json:"scope"`
	SubjectID string   `json:"subject_id"`
	Operator  Operator `json:"operator"`
	Expected  Value    `json:"expected"`
	Severity  Severity `json:"severity"`
}

type Evidence struct {
	ID         string `json:"id"`
	Attribute  string `json:"attribute"`
	Scope      Scope  `json:"scope"`
	SubjectID  string `json:"subject_id"`
	ProductID  string `json:"product_id,omitempty"`
	VariantID  string `json:"variant_id,omitempty"`
	Value      Value  `json:"value"`
	SourceID   string `json:"source_id"`
	ObservedAt string `json:"observed_at"`
}

type Input struct {
	CartSnapshot CartSnapshot  `json:"cart_snapshot"`
	Requirements []Requirement `json:"requirements"`
	Evidence     []Evidence    `json:"evidence"`
}

type EvidenceAssessment struct {
	EvidenceID string  `json:"evidence_id"`
	Value      Value   `json:"value"`
	Scope      Scope   `json:"scope"`
	SubjectID  string  `json:"subject_id"`
	ProductID  string  `json:"product_id,omitempty"`
	VariantID  string  `json:"variant_id,omitempty"`
	SourceID   string  `json:"source_id"`
	ObservedAt string  `json:"observed_at"`
	Outcome    Outcome `json:"outcome"`
}

type RequirementResult struct {
	RequirementID string               `json:"requirement_id"`
	Attribute     string               `json:"attribute"`
	Scope         Scope                `json:"scope"`
	SubjectID     string               `json:"subject_id"`
	Operator      Operator             `json:"operator"`
	Expected      Value                `json:"expected"`
	Severity      Severity             `json:"severity"`
	Outcome       Outcome              `json:"outcome"`
	Assessments   []EvidenceAssessment `json:"assessments"`
}

type Report struct {
	Decision           Decision            `json:"decision"`
	CartSnapshotSHA256 string              `json:"cart_snapshot_sha256"`
	Evidence           []Evidence          `json:"evidence"`
	Results            []RequirementResult `json:"results"`
}

// Evaluate validates the cart and all evidence, derives cart-level facts, and
// evaluates each requirement only against evidence with the same attribute,
// scope, and subject ID.
func Evaluate(input Input) (Report, error) {
	total, err := validateCartSnapshot(input.CartSnapshot)
	if err != nil {
		return Report{}, err
	}
	if err := validateInput(input); err != nil {
		return Report{}, err
	}
	hash, err := hashSnapshot(input.CartSnapshot)
	if err != nil {
		return Report{}, err
	}

	evidence := append([]Evidence(nil), input.Evidence...)
	evidence = append(evidence, Evidence{
		ID: "cart-snapshot:total-price-paise", Attribute: "total_price_paise",
		Scope: CartScope, SubjectID: input.CartSnapshot.CartID,
		Value: NumberInt64Value(total), SourceID: "local-cart-snapshot",
		ObservedAt: input.CartSnapshot.CapturedAt,
	})
	if input.CartSnapshot.DeliveryDate != nil {
		evidence = append(evidence, Evidence{
			ID: "cart-snapshot:delivery-date", Attribute: "delivery_date",
			Scope: CartScope, SubjectID: input.CartSnapshot.CartID,
			Value: DateValue(*input.CartSnapshot.DeliveryDate), SourceID: "local-cart-snapshot",
			ObservedAt: input.CartSnapshot.CapturedAt,
		})
	}

	report := Report{
		Results:            make([]RequirementResult, 0, len(input.Requirements)),
		CartSnapshotSHA256: hash,
		Evidence:           evidence,
	}
	hardCount := 0
	hardFailed := false
	hardUnknown := false

	for _, requirement := range input.Requirements {
		result := RequirementResult{
			RequirementID: requirement.ID,
			Attribute:     requirement.Attribute,
			Scope:         requirement.Scope,
			SubjectID:     requirement.SubjectID,
			Operator:      requirement.Operator,
			Expected:      requirement.Expected,
			Severity:      requirement.Severity,
			Outcome:       Unknown,
			Assessments:   make([]EvidenceAssessment, 0),
		}
		allPass, allFail := true, true
		matched := false
		for _, item := range evidence {
			if item.Attribute != requirement.Attribute || item.Scope != requirement.Scope || item.SubjectID != requirement.SubjectID {
				continue
			}
			matched = true
			assessmentOutcome := compare(requirement.Operator, requirement.Expected, item.Value)
			result.Assessments = append(result.Assessments, EvidenceAssessment{
				EvidenceID: item.ID, Value: item.Value, Scope: item.Scope, SubjectID: item.SubjectID,
				ProductID: item.ProductID, VariantID: item.VariantID,
				SourceID: item.SourceID, ObservedAt: item.ObservedAt, Outcome: assessmentOutcome,
			})
			if assessmentOutcome != Pass {
				allPass = false
			}
			if assessmentOutcome != Fail {
				allFail = false
			}
		}
		if matched {
			switch {
			case allPass:
				result.Outcome = Pass
			case allFail:
				result.Outcome = Fail
			}
		}
		report.Results = append(report.Results, result)

		if requirement.Severity == Hard {
			hardCount++
			switch result.Outcome {
			case Fail:
				hardFailed = true
			case Unknown:
				hardUnknown = true
			}
		}
	}

	switch {
	case hardFailed:
		report.Decision = Block
	case hardUnknown || hardCount == 0:
		report.Decision = Review
	default:
		report.Decision = Allow
	}
	return report, nil
}

func validateCartSnapshot(snapshot CartSnapshot) (int64, error) {
	if err := requireText("cart snapshot", snapshot.CartID, "cart_id"); err != nil {
		return 0, err
	}
	if snapshot.Currency != "INR" {
		return 0, fmt.Errorf("cart snapshot currency %q is unsupported; only INR is supported", snapshot.Currency)
	}
	if _, err := time.Parse(time.RFC3339Nano, snapshot.CapturedAt); err != nil {
		return 0, fmt.Errorf("cart snapshot has invalid captured_at %q: expected RFC3339 timestamp", snapshot.CapturedAt)
	}
	if snapshot.Items == nil {
		return 0, fmt.Errorf("cart snapshot items list is required")
	}
	if len(snapshot.Items) == 0 {
		return 0, fmt.Errorf("cart snapshot must contain at least one item")
	}
	if snapshot.ShippingPaise < 0 || snapshot.DiscountPaise < 0 {
		return 0, fmt.Errorf("cart shipping and discount amounts must be non-negative")
	}
	if snapshot.DeliveryDate != nil && !isISODate(*snapshot.DeliveryDate) {
		return 0, fmt.Errorf("cart snapshot delivery_date must be a valid ISO calendar date (YYYY-MM-DD)")
	}

	itemIDs := make(map[string]struct{}, len(snapshot.Items))
	var subtotal int64
	for _, item := range snapshot.Items {
		if err := requireText("cart item", item.ItemID, "item_id"); err != nil {
			return 0, err
		}
		if _, exists := itemIDs[item.ItemID]; exists {
			return 0, fmt.Errorf("duplicate cart item id %q", item.ItemID)
		}
		itemIDs[item.ItemID] = struct{}{}
		if err := requireText(fmt.Sprintf("cart item %q", item.ItemID), item.ProductID, "product_id"); err != nil {
			return 0, err
		}
		if err := requireText(fmt.Sprintf("cart item %q", item.ItemID), item.VariantID, "variant_id"); err != nil {
			return 0, err
		}
		if item.Quantity <= 0 {
			return 0, fmt.Errorf("cart item %q quantity must be positive", item.ItemID)
		}
		if item.UnitPricePaise < 0 {
			return 0, fmt.Errorf("cart item %q unit_price_paise must be non-negative", item.ItemID)
		}
		lineTotal, ok := safeMultiply(item.Quantity, item.UnitPricePaise)
		if !ok {
			return 0, fmt.Errorf("cart item %q line total overflows int64", item.ItemID)
		}
		subtotal, ok = safeAdd(subtotal, lineTotal)
		if !ok {
			return 0, fmt.Errorf("cart item subtotal overflows int64")
		}
	}
	gross, ok := safeAdd(subtotal, snapshot.ShippingPaise)
	if !ok {
		return 0, fmt.Errorf("cart total before discount overflows int64")
	}
	if snapshot.DiscountPaise > gross {
		return 0, fmt.Errorf("cart discount exceeds subtotal plus shipping")
	}
	return gross - snapshot.DiscountPaise, nil
}

func validateInput(input Input) error {
	items := make(map[string]CartItem, len(input.CartSnapshot.Items))
	for _, item := range input.CartSnapshot.Items {
		items[item.ItemID] = item
	}

	requirementIDs := make(map[string]struct{}, len(input.Requirements))
	for _, requirement := range input.Requirements {
		if err := requireText("requirement", requirement.ID, "id"); err != nil {
			return err
		}
		if _, exists := requirementIDs[requirement.ID]; exists {
			return fmt.Errorf("duplicate requirement id %q", requirement.ID)
		}
		requirementIDs[requirement.ID] = struct{}{}
		if err := requireText("requirement", requirement.Attribute, "attribute"); err != nil {
			return err
		}
		if err := validateBuiltInRequirement(requirement); err != nil {
			return err
		}
		if requirement.Severity != Hard && requirement.Severity != Preference {
			return fmt.Errorf("requirement %q has invalid severity %q", requirement.ID, requirement.Severity)
		}
		if err := validateScope(requirement.Scope, requirement.SubjectID, input.CartSnapshot, items); err != nil {
			return fmt.Errorf("requirement %q: %w", requirement.ID, err)
		}
		if err := validateValue(fmt.Sprintf("requirement %q expected value", requirement.ID), requirement.Expected); err != nil {
			return err
		}
		if err := validateOperator(requirement.ID, requirement.Operator, requirement.Expected.Type); err != nil {
			return err
		}
	}

	evidenceIDs := make(map[string]struct{}, len(input.Evidence))
	reservedEvidenceIDs := map[string]struct{}{
		"cart-snapshot:total-price-paise": {},
		"cart-snapshot:delivery-date":     {},
	}
	for _, item := range input.Evidence {
		if err := requireText("evidence", item.ID, "id"); err != nil {
			return err
		}
		if _, exists := evidenceIDs[item.ID]; exists {
			return fmt.Errorf("duplicate evidence id %q", item.ID)
		}
		if _, reserved := reservedEvidenceIDs[item.ID]; reserved {
			return fmt.Errorf("evidence id %q is reserved for derived cart evidence", item.ID)
		}
		evidenceIDs[item.ID] = struct{}{}
		if err := requireText("evidence", item.Attribute, "attribute"); err != nil {
			return err
		}
		if err := validateScope(item.Scope, item.SubjectID, input.CartSnapshot, items); err != nil {
			return fmt.Errorf("evidence %q: %w", item.ID, err)
		}
		if item.Scope == ItemScope {
			cartItem := items[item.SubjectID]
			if item.ProductID != cartItem.ProductID || item.VariantID != cartItem.VariantID {
				return fmt.Errorf("evidence %q product_id or variant_id does not match cart item %q", item.ID, item.SubjectID)
			}
		} else if item.ProductID != "" || item.VariantID != "" {
			return fmt.Errorf("cart-scoped evidence %q must not specify product_id or variant_id", item.ID)
		}
		if item.Attribute == "total_price_paise" || item.Attribute == "delivery_date" {
			return fmt.Errorf("evidence %q cannot override cart snapshot attribute %q", item.ID, item.Attribute)
		}
		if err := validateValue(fmt.Sprintf("evidence %q value", item.ID), item.Value); err != nil {
			return err
		}
		if err := requireText(fmt.Sprintf("evidence %q", item.ID), item.SourceID, "source_id"); err != nil {
			return err
		}
		if _, err := time.Parse(time.RFC3339Nano, item.ObservedAt); err != nil {
			return fmt.Errorf("evidence %q has invalid observed_at %q: expected RFC3339 timestamp", item.ID, item.ObservedAt)
		}
	}

	for _, requirement := range input.Requirements {
		for _, item := range input.Evidence {
			if item.Attribute == requirement.Attribute && item.Scope == requirement.Scope && item.SubjectID == requirement.SubjectID && item.Value.Type != requirement.Expected.Type {
				return typeMismatchError(requirement, item)
			}
		}
	}
	return nil
}

// validateBuiltInRequirement defines the type and operator contracts for
// attributes derived directly from the cart snapshot. It runs before generic
// scope/operator validation so malformed requirements fail before comparison.
func validateBuiltInRequirement(requirement Requirement) error {
	var wantType ValueType
	var allowedOperators map[Operator]bool
	switch requirement.Attribute {
	case "total_price_paise":
		wantType = NumberType
		allowedOperators = map[Operator]bool{
			Equals: true, NotEquals: true, LessThanOrEqual: true, GreaterThanOrEqual: true,
		}
	case "delivery_date":
		wantType = DateType
		allowedOperators = map[Operator]bool{
			Equals: true, NotEquals: true, BeforeOrEqual: true, AfterOrEqual: true,
		}
	default:
		return nil
	}
	if requirement.Scope != CartScope {
		return fmt.Errorf("requirement %q: built-in cart attribute %q must use cart scope", requirement.ID, requirement.Attribute)
	}
	if requirement.Expected.Type != wantType {
		return fmt.Errorf("requirement %q: built-in cart attribute %q requires expected type %q, got %q", requirement.ID, requirement.Attribute, wantType, requirement.Expected.Type)
	}
	if !allowedOperators[requirement.Operator] {
		return fmt.Errorf("requirement %q: operator %q is unsupported for built-in cart attribute %q", requirement.ID, requirement.Operator, requirement.Attribute)
	}
	return nil
}

func validateScope(scope Scope, subjectID string, snapshot CartSnapshot, items map[string]CartItem) error {
	if err := requireText("scope", subjectID, "subject_id"); err != nil {
		return err
	}
	switch scope {
	case CartScope:
		if subjectID != snapshot.CartID {
			return fmt.Errorf("cart scope subject_id %q does not match cart_id %q", subjectID, snapshot.CartID)
		}
	case ItemScope:
		if _, ok := items[subjectID]; !ok {
			return fmt.Errorf("item scope subject_id %q does not reference a cart item", subjectID)
		}
	default:
		return fmt.Errorf("unsupported scope %q", scope)
	}
	return nil
}

func typeMismatchError(requirement Requirement, item Evidence) error {
	return fmt.Errorf("evidence %q for requirement %q has type %q; expected %q", item.ID, requirement.ID, item.Value.Type, requirement.Expected.Type)
}

func requireText(kind, value, field string) error {
	if strings.TrimSpace(value) == "" {
		return fmt.Errorf("%s %s is required", kind, field)
	}
	return nil
}

func validateValue(label string, value Value) error {
	switch value.Type {
	case StringType:
		if _, ok := value.Value.(string); !ok {
			return fmt.Errorf("%s has type %q but value must be a string", label, value.Type)
		}
	case NumberType:
		if _, ok := parseNumber(value.Value); !ok {
			return fmt.Errorf("%s has type %q but value must be a finite number", label, value.Type)
		}
	case BooleanType:
		if _, ok := value.Value.(bool); !ok {
			return fmt.Errorf("%s has type %q but value must be a boolean", label, value.Type)
		}
	case DateType:
		date, ok := value.Value.(string)
		if !ok || !isISODate(date) {
			return fmt.Errorf("%s must be a valid ISO calendar date (YYYY-MM-DD)", label)
		}
	default:
		return fmt.Errorf("%s has unsupported value type %q", label, value.Type)
	}
	return nil
}

func isISODate(value string) bool {
	if len(value) != len("2006-01-02") {
		return false
	}
	parsed, err := time.Parse("2006-01-02", value)
	return err == nil && parsed.Format("2006-01-02") == value
}

func validateOperator(requirementID string, operator Operator, valueType ValueType) error {
	switch operator {
	case Equals, NotEquals:
		return nil
	case LessThanOrEqual, GreaterThanOrEqual:
		if valueType != NumberType {
			return fmt.Errorf("requirement %q operator %q requires a number value", requirementID, operator)
		}
	case BeforeOrEqual, AfterOrEqual:
		if valueType != DateType {
			return fmt.Errorf("requirement %q operator %q requires a date value", requirementID, operator)
		}
	default:
		return fmt.Errorf("requirement %q has unsupported operator %q", requirementID, operator)
	}
	return nil
}

func compare(operator Operator, expected, actual Value) Outcome {
	var satisfied bool
	switch operator {
	case Equals:
		satisfied = equalValues(expected, actual)
	case NotEquals:
		satisfied = !equalValues(expected, actual)
	case LessThanOrEqual:
		satisfied = compareNumbers(actual.Value, expected.Value) <= 0
	case GreaterThanOrEqual:
		satisfied = compareNumbers(actual.Value, expected.Value) >= 0
	case BeforeOrEqual:
		satisfied = actual.Value.(string) <= expected.Value.(string)
	case AfterOrEqual:
		satisfied = actual.Value.(string) >= expected.Value.(string)
	}
	if satisfied {
		return Pass
	}
	return Fail
}

func equalValues(left, right Value) bool {
	switch left.Type {
	case StringType, DateType:
		return left.Value.(string) == right.Value.(string)
	case NumberType:
		return compareNumbers(left.Value, right.Value) == 0
	case BooleanType:
		return left.Value.(bool) == right.Value.(bool)
	default:
		return false
	}
}

func compareNumbers(left, right any) int {
	leftNumber, _ := parseNumber(left)
	rightNumber, _ := parseNumber(right)
	return leftNumber.Cmp(rightNumber)
}

func parseNumber(value any) (*big.Rat, bool) {
	var text string
	switch number := value.(type) {
	case json.Number:
		text = number.String()
	case float64:
		text = strconv.FormatFloat(number, 'g', -1, 64)
	default:
		return nil, false
	}
	if _, err := strconv.ParseFloat(text, 64); err != nil {
		return nil, false
	}
	result, ok := new(big.Rat).SetString(text)
	return result, ok
}

type canonicalCartItem struct {
	ItemID         string `json:"item_id"`
	ProductID      string `json:"product_id"`
	VariantID      string `json:"variant_id"`
	Quantity       int64  `json:"quantity"`
	UnitPricePaise int64  `json:"unit_price_paise"`
}

type canonicalCartSnapshot struct {
	CartID        string              `json:"cart_id"`
	Currency      string              `json:"currency"`
	CapturedAt    string              `json:"captured_at"`
	Items         []canonicalCartItem `json:"items"`
	ShippingPaise int64               `json:"shipping_paise"`
	DiscountPaise int64               `json:"discount_paise"`
	DeliveryDate  *string             `json:"delivery_date"`
}

func hashSnapshot(snapshot CartSnapshot) (string, error) {
	items := make([]canonicalCartItem, len(snapshot.Items))
	for i, item := range snapshot.Items {
		items[i] = canonicalCartItem{
			ItemID: item.ItemID, ProductID: item.ProductID, VariantID: item.VariantID,
			Quantity: item.Quantity, UnitPricePaise: item.UnitPricePaise,
		}
	}
	sort.Slice(items, func(i, j int) bool { return items[i].ItemID < items[j].ItemID })
	canonical := canonicalCartSnapshot{
		CartID: snapshot.CartID, Currency: snapshot.Currency, CapturedAt: snapshot.CapturedAt,
		Items: items, ShippingPaise: snapshot.ShippingPaise,
		DiscountPaise: snapshot.DiscountPaise, DeliveryDate: snapshot.DeliveryDate,
	}
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", fmt.Errorf("encode canonical cart snapshot: %w", err)
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func safeAdd(left, right int64) (int64, bool) {
	if right > math.MaxInt64-left {
		return 0, false
	}
	return left + right, true
}

func safeMultiply(left, right int64) (int64, bool) {
	if left != 0 && right > math.MaxInt64/left {
		return 0, false
	}
	return left * right, true
}
