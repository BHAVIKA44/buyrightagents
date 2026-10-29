package evidence

import "testing"

func TestMockProviderFromEnvAndValidation(t *testing.T) {
	t.Run("configured", func(t *testing.T) {
		t.Setenv(ProviderIDEnv, "configured-catalogue")
		t.Setenv(ProviderKeyEnv, "configured-provider-key-0123456789")
		provider, err := MockProviderFromEnv()
		if err != nil || provider.ID() != "configured-catalogue" {
			t.Fatalf("provider=%#v err=%v", provider, err)
		}
	})
	t.Run("missing configuration", func(t *testing.T) {
		t.Setenv(ProviderIDEnv, "")
		t.Setenv(ProviderKeyEnv, "")
		if _, err := MockProviderFromEnv(); err == nil {
			t.Fatal("missing provider configuration was accepted")
		}
	})
	t.Run("invalid provider", func(t *testing.T) {
		if _, err := NewMockProvider("", []byte("provider-key-0123456789")); err == nil {
			t.Fatal("empty provider id was accepted")
		}
		if _, err := NewMockProvider("catalogue", []byte("short")); err == nil {
			t.Fatal("short provider key was accepted")
		}
	})
}
