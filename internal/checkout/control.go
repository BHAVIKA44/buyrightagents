// Package checkout implements a deliberately in-memory mock checkout gate.
// It is not a merchant, payment, or persistent authorization service.
package checkout

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/bhavika44/buyrightagents/internal/verification"
)

const policyVersion = "mock-checkout-v1"

const (
	DefaultEvidenceMaxAge          = 24 * time.Hour
	DefaultEvidenceFutureTolerance = 5 * time.Minute
)

// PurchaseContext is the complete purchase context which a mock approval
// binds. It is intentionally separate from an LLM response: requirements and
// the decision are produced by the deterministic purchase-check path.
type PurchaseContext struct {
	Request       string                     `json:"request"`
	Requirements  []verification.Requirement `json:"requirements"`
	CartSnapshot  verification.CartSnapshot  `json:"cart_snapshot"`
	Evidence      []verification.Evidence    `json:"evidence"`
	ReferenceTime string                     `json:"reference_time"`
	Decision      verification.Decision      `json:"decision"`
}

// Approval is an opaque mock authorization. Credential is intentionally
// excluded from JSON output so command results never print a bearer secret.
type Approval struct {
	Reference     string `json:"reference"`
	BindingSHA256 string `json:"binding_sha256"`
	PolicyVersion string `json:"policy_version"`
	Credential    string `json:"-"`
}

type AuditOutcome string

const (
	ApprovalIssued    AuditOutcome = "APPROVAL_ISSUED"
	ApprovalRejected  AuditOutcome = "APPROVAL_REJECTED"
	CheckoutSucceeded AuditOutcome = "CHECKOUT_SUCCEEDED"
	CheckoutRejected  AuditOutcome = "CHECKOUT_REJECTED"
)

// AuditRecord is an in-memory diagnostic record. It is not a tamper-proof or
// persistent ledger.
type AuditRecord struct {
	Timestamp              string `json:"timestamp"`
	ApprovalReference      string `json:"approval_reference,omitempty"`
	PurchaseSnapshotSHA256 string `json:"purchase_snapshot_sha256,omitempty"`
	// Decision is the composed decision supplied by the trusted purchase-check
	// path. VerifierDecision is recorded separately so an audit of a rejected,
	// forged context cannot falsely suggest that the verifier agreed with it.
	Decision               verification.Decision `json:"decision"`
	VerifierDecision       verification.Decision `json:"verifier_decision"`
	RelevantRequirementIDs []string              `json:"relevant_requirement_ids"`
	Outcome                AuditOutcome          `json:"outcome"`
	RejectionReason        string                `json:"rejection_reason,omitempty"`
}

type CheckoutResult struct {
	Authorized bool        `json:"authorized"`
	Reason     string      `json:"reason,omitempty"`
	Audit      AuditRecord `json:"audit"`
}

type approvalState struct {
	approval Approval
	consumed bool
}

// Gate is trusted in-process state for the mock checkout boundary. Its key and
// consumed-approval state never leave the process.
type Gate struct {
	mu             sync.Mutex
	key            []byte
	now            func() time.Time
	evidencePolicy verification.EvidenceTrustPolicy
	approvals      map[string]approvalState
	audit          []AuditRecord
}

// NewGate creates a mock gate. Tests may supply a stable key and clock. A nil
// key receives cryptographically random in-process key material.
func NewGate(key []byte, now func() time.Time) (*Gate, error) {
	return NewGateWithEvidencePolicy(key, now, verification.EvidenceTrustPolicy{
		RequireAttestation: true,
		MaxAge:             DefaultEvidenceMaxAge,
		FutureTolerance:    DefaultEvidenceFutureTolerance,
	})
}

// NewGateWithEvidencePolicy creates a gate with an explicit trusted evidence
// policy. The gate injects its own clock at each issue and checkout attempt;
// callers cannot choose a stale reference time to bypass freshness checks.
func NewGateWithEvidencePolicy(key []byte, now func() time.Time, evidencePolicy verification.EvidenceTrustPolicy) (*Gate, error) {
	if len(key) == 0 {
		key = make([]byte, 32)
		if _, err := rand.Read(key); err != nil {
			return nil, fmt.Errorf("generate mock checkout key: %w", err)
		}
	}
	if len(key) < 16 {
		return nil, errors.New("mock checkout key must contain at least 16 bytes")
	}
	if now == nil {
		now = time.Now
	}
	if evidencePolicy.MaxAge < 0 {
		return nil, errors.New("mock checkout evidence max age must not be negative")
	}
	if evidencePolicy.FutureTolerance < 0 {
		return nil, errors.New("mock checkout evidence future tolerance must not be negative")
	}
	if evidencePolicy.RequireAttestation && evidencePolicy.MaxAge == 0 {
		evidencePolicy.MaxAge = DefaultEvidenceMaxAge
	}
	if evidencePolicy.Providers == nil {
		evidencePolicy.Providers = make(map[string]verification.EvidenceAttestationVerifier)
	}
	return &Gate{key: append([]byte(nil), key...), now: now, evidencePolicy: cloneEvidencePolicy(evidencePolicy), approvals: make(map[string]approvalState), audit: make([]AuditRecord, 0)}, nil
}

// Issue creates a one-time approval only for a context whose declared and
// independently re-evaluated decision are both ALLOW.
func (gate *Gate) Issue(context PurchaseContext) (Approval, error) {
	binding, report, err := bindContext(context, gate.policyAt(gate.now()))
	if err != nil {
		gate.record(gate.auditFrom(report, context.Decision, ApprovalRejected, "invalid approval context: "+err.Error(), ""))
		return Approval{}, err
	}
	if context.Decision != verification.Allow || report.Decision != verification.Allow {
		err := fmt.Errorf("decision %q is not eligible for mock checkout approval", context.Decision)
		gate.record(gate.auditFrom(report, context.Decision, ApprovalRejected, err.Error(), ""))
		return Approval{}, err
	}
	reference, err := randomReference()
	if err != nil {
		return Approval{}, err
	}
	approval := Approval{Reference: reference, BindingSHA256: binding, PolicyVersion: policyVersion}
	approval.Credential = gate.credential(approval.Reference, approval.BindingSHA256)

	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.approvals[approval.Reference] = approvalState{approval: approval}
	gate.audit = append(gate.audit, gate.auditFrom(report, context.Decision, ApprovalIssued, "", approval.Reference))
	return approval, nil
}

// Checkout verifies the presented artifact and context, then atomically
// consumes a matching approval. Rejections leave an unconsumed approval usable
// for a later matching checkout attempt.
func (gate *Gate) Checkout(approval Approval, context PurchaseContext) CheckoutResult {
	binding, report, err := bindContext(context, gate.policyAt(gate.now()))
	if err != nil {
		return gate.reject(report, context.Decision, approval.Reference, "invalid checkout context: "+err.Error())
	}
	if context.Decision != verification.Allow || report.Decision != verification.Allow {
		return gate.reject(report, context.Decision, approval.Reference, "checkout context is not an ALLOW decision")
	}

	gate.mu.Lock()
	defer gate.mu.Unlock()
	state, ok := gate.approvals[approval.Reference]
	if !ok {
		return gate.rejectLocked(report, context.Decision, approval.Reference, "unknown approval")
	}
	if approval.PolicyVersion != policyVersion || approval.BindingSHA256 != state.approval.BindingSHA256 || !hmac.Equal([]byte(approval.Credential), []byte(gate.credential(approval.Reference, approval.BindingSHA256))) {
		return gate.rejectLocked(report, context.Decision, approval.Reference, "approval authentication failed")
	}
	if state.consumed {
		return gate.rejectLocked(report, context.Decision, approval.Reference, "approval has already been consumed")
	}
	if binding != state.approval.BindingSHA256 {
		return gate.rejectLocked(report, context.Decision, approval.Reference, "checkout context does not match the approved purchase")
	}
	state.consumed = true
	gate.approvals[approval.Reference] = state
	record := gate.auditFrom(report, context.Decision, CheckoutSucceeded, "", approval.Reference)
	gate.audit = append(gate.audit, record)
	return CheckoutResult{Authorized: true, Audit: record}
}

// AuditTrail returns a snapshot of the in-memory records.
func (gate *Gate) AuditTrail() []AuditRecord {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return append([]AuditRecord(nil), gate.audit...)
}

// EvidencePolicy returns the gate policy with a fresh, gate-owned reference
// time. It exposes provider verifiers, never their key material, so composed
// command paths can show the same REVIEW result the gate will enforce.
func (gate *Gate) EvidencePolicy() verification.EvidenceTrustPolicy {
	return gate.policyAt(gate.now())
}

func (gate *Gate) reject(report verification.Report, decision verification.Decision, reference, reason string) CheckoutResult {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	return gate.rejectLocked(report, decision, reference, reason)
}

func (gate *Gate) rejectLocked(report verification.Report, decision verification.Decision, reference, reason string) CheckoutResult {
	record := gate.auditFrom(report, decision, CheckoutRejected, reason, reference)
	gate.audit = append(gate.audit, record)
	return CheckoutResult{Reason: reason, Audit: record}
}

func (gate *Gate) record(record AuditRecord) {
	gate.mu.Lock()
	defer gate.mu.Unlock()
	gate.audit = append(gate.audit, record)
}

func (gate *Gate) credential(reference, binding string) string {
	mac := hmac.New(sha256.New, gate.key)
	_, _ = mac.Write([]byte(policyVersion))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(reference))
	_, _ = mac.Write([]byte{0})
	_, _ = mac.Write([]byte(binding))
	return hex.EncodeToString(mac.Sum(nil))
}

func randomReference() (string, error) {
	bytes := make([]byte, 16)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate mock approval reference: %w", err)
	}
	return "mock-approval-" + hex.EncodeToString(bytes), nil
}

func bindContext(context PurchaseContext, evidencePolicy verification.EvidenceTrustPolicy) (string, verification.Report, error) {
	if strings.TrimSpace(context.Request) == "" {
		return "", verification.Report{}, errors.New("original request is required")
	}
	if _, err := time.Parse(time.RFC3339Nano, context.ReferenceTime); err != nil {
		return "", verification.Report{}, errors.New("reference time must be RFC3339")
	}
	report, err := verification.Evaluate(verification.Input{CartSnapshot: context.CartSnapshot, Requirements: context.Requirements, Evidence: context.Evidence, EvidencePolicy: &evidencePolicy})
	if err != nil {
		return "", verification.Report{}, err
	}
	canonical := canonicalPurchaseContext{PolicyVersion: policyVersion, Request: context.Request, Requirements: sortedRequirements(context.Requirements), CartSnapshot: sortedCart(context.CartSnapshot), Evidence: sortedEvidence(context.Evidence), ReferenceTime: context.ReferenceTime, Decision: context.Decision}
	data, err := json.Marshal(canonical)
	if err != nil {
		return "", verification.Report{}, fmt.Errorf("canonicalize purchase context: %w", err)
	}
	digest := sha256.Sum256(data)
	return hex.EncodeToString(digest[:]), report, nil
}

func (gate *Gate) policyAt(reference time.Time) verification.EvidenceTrustPolicy {
	policy := cloneEvidencePolicy(gate.evidencePolicy)
	policy.ReferenceTime = reference.UTC()
	return policy
}

func cloneEvidencePolicy(policy verification.EvidenceTrustPolicy) verification.EvidenceTrustPolicy {
	copy := policy
	copy.Providers = make(map[string]verification.EvidenceAttestationVerifier, len(policy.Providers))
	for id, provider := range policy.Providers {
		copy.Providers[id] = provider
	}
	return copy
}

type canonicalPurchaseContext struct {
	PolicyVersion string                     `json:"policy_version"`
	Request       string                     `json:"request"`
	Requirements  []verification.Requirement `json:"requirements"`
	CartSnapshot  verification.CartSnapshot  `json:"cart_snapshot"`
	Evidence      []verification.Evidence    `json:"evidence"`
	ReferenceTime string                     `json:"reference_time"`
	Decision      verification.Decision      `json:"decision"`
}

func sortedCart(snapshot verification.CartSnapshot) verification.CartSnapshot {
	copy := snapshot
	copy.Items = append([]verification.CartItem(nil), snapshot.Items...)
	sort.Slice(copy.Items, func(left, right int) bool { return copy.Items[left].ItemID < copy.Items[right].ItemID })
	return copy
}

func sortedRequirements(requirements []verification.Requirement) []verification.Requirement {
	copy := append([]verification.Requirement(nil), requirements...)
	sort.Slice(copy, func(left, right int) bool { return canonicalKey(copy[left]) < canonicalKey(copy[right]) })
	return copy
}

func sortedEvidence(evidence []verification.Evidence) []verification.Evidence {
	copy := append([]verification.Evidence(nil), evidence...)
	sort.Slice(copy, func(left, right int) bool { return canonicalKey(copy[left]) < canonicalKey(copy[right]) })
	return copy
}

func canonicalKey(value any) string {
	data, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("%#v", value)
	}
	return string(data)
}

func (gate *Gate) auditFrom(report verification.Report, declared verification.Decision, outcome AuditOutcome, reason, reference string) AuditRecord {
	relevant := make([]string, 0)
	for _, result := range report.Results {
		if result.Outcome == verification.Fail || result.Outcome == verification.Unknown {
			relevant = append(relevant, result.RequirementID)
		}
	}
	sort.Strings(relevant)
	return AuditRecord{Timestamp: gate.now().UTC().Format(time.RFC3339Nano), ApprovalReference: reference, PurchaseSnapshotSHA256: report.CartSnapshotSHA256, Decision: declared, VerifierDecision: report.Decision, RelevantRequirementIDs: relevant, Outcome: outcome, RejectionReason: reason}
}
