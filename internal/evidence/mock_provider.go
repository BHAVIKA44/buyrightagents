// Package evidence contains the trusted mock-provider boundary used by the
// prototype. It does not contact a merchant or establish production identity.
package evidence

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/bhavika44/buyrightagents/internal/verification"
)

// MockProvider owns in-process HMAC material for one synthetic evidence
// source. Its key never appears in an attestation or verification report.
type MockProvider struct {
	id  string
	key []byte
}

func NewMockProvider(id string, key []byte) (*MockProvider, error) {
	if strings.TrimSpace(id) == "" {
		return nil, errors.New("mock evidence provider id is required")
	}
	if len(key) < 16 {
		return nil, errors.New("mock evidence provider key must contain at least 16 bytes")
	}
	return &MockProvider{id: id, key: append([]byte(nil), key...)}, nil
}

func (provider *MockProvider) ID() string { return provider.id }

// Attest returns a copy of evidence bearing an HMAC attestation bound to the
// supplied canonical snapshot. The verifier remains responsible for checking
// structural evidence-to-item binding and freshness.
func (provider *MockProvider) Attest(snapshot verification.CartSnapshot, evidence verification.Evidence) (verification.Evidence, error) {
	if provider == nil {
		return verification.Evidence{}, errors.New("mock evidence provider is required")
	}
	hash, err := verification.SnapshotSHA256(snapshot)
	if err != nil {
		return verification.Evidence{}, fmt.Errorf("hash evidence cart snapshot: %w", err)
	}
	attestation := verification.EvidenceAttestation{
		Version: verification.MockEvidenceAttestationV1, ProviderID: provider.id, CartSnapshotSHA256: hash,
	}
	payload, err := verification.EvidenceAttestationPayload(attestation, evidence)
	if err != nil {
		return verification.Evidence{}, fmt.Errorf("canonicalize evidence attestation: %w", err)
	}
	attestation.Signature = provider.sign(payload)
	evidence.Attestation = &attestation
	return evidence, nil
}

// VerifyMockEvidenceAttestation implements verification.EvidenceAttestationVerifier.
func (provider *MockProvider) VerifyMockEvidenceAttestation(payload []byte, signature string) bool {
	if provider == nil {
		return false
	}
	want, err := hex.DecodeString(signature)
	if err != nil {
		return false
	}
	return hmac.Equal(want, provider.mac(payload))
}

func (provider *MockProvider) sign(payload []byte) string {
	return hex.EncodeToString(provider.mac(payload))
}

func (provider *MockProvider) mac(payload []byte) []byte {
	mac := hmac.New(sha256.New, provider.key)
	_, _ = mac.Write(payload)
	return mac.Sum(nil)
}

// TrustPolicy creates a strict policy for this provider. ReferenceTime is set
// by the verifier or checkout gate at evaluation time so tests need no sleeps.
func (provider *MockProvider) TrustPolicy(maxAge, futureTolerance time.Duration) verification.EvidenceTrustPolicy {
	return verification.EvidenceTrustPolicy{
		RequireAttestation: true,
		Providers:          map[string]verification.EvidenceAttestationVerifier{provider.id: provider},
		MaxAge:             maxAge,
		FutureTolerance:    futureTolerance,
	}
}
