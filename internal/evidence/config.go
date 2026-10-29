package evidence

import (
	"errors"
	"os"
	"strings"
)

const (
	ProviderIDEnv  = "BUYRIGHTAGENTS_MOCK_EVIDENCE_PROVIDER_ID"
	ProviderKeyEnv = "BUYRIGHTAGENTS_MOCK_EVIDENCE_HMAC_KEY"
)

// MockProviderFromEnv loads the provider identity and key held by the mock
// provider process. The key is never returned in command output or logs.
func MockProviderFromEnv() (*MockProvider, error) {
	id := strings.TrimSpace(os.Getenv(ProviderIDEnv))
	key := os.Getenv(ProviderKeyEnv)
	if id == "" || key == "" {
		return nil, errors.New(ProviderIDEnv + " and " + ProviderKeyEnv + " are required for mock checkout evidence verification")
	}
	return NewMockProvider(id, []byte(key))
}
