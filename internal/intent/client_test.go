package intent

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func testHTTPClient(t *testing.T, serverURL string) *HTTPClient {
	t.Helper()
	client, err := NewHTTPClient(Config{APIKey: "unit-test-secret", Model: "fixture-model", BaseURL: serverURL})
	if err != nil {
		t.Fatal(err)
	}
	return client
}

func TestHTTPClientConstructsChatRequestAndExtractsContent(t *testing.T) {
	content := `{"requirements":[],"clarification_questions":[],"unsupported_constraints":[]}`
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/v1/chat/completions" || request.Method != http.MethodPost {
			t.Errorf("unexpected request %s %s", request.Method, request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer unit-test-secret" {
			t.Errorf("authorization header missing")
		}
		var body struct {
			Model          string  `json:"model"`
			Temperature    float64 `json:"temperature"`
			ResponseFormat struct {
				Type string `json:"type"`
			} `json:"response_format"`
			Messages []struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Errorf("decode request: %v", err)
		}
		if body.Model != "fixture-model" || body.Temperature != 0 || body.ResponseFormat.Type != "json_object" || len(body.Messages) != 2 {
			t.Errorf("request body missing model contract: %#v", body)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(writer, `{"choices":[{"message":{"content":`+mustJSONString(content)+`}}]}`)
	}))
	defer server.Close()

	client := testHTTPClient(t, server.URL+"/v1")
	got, err := client.Complete(context.Background(), "system", "user")
	if err != nil {
		t.Fatal(err)
	}
	if got != content || client.Host() == "" {
		t.Fatalf("content=%q host=%q", got, client.Host())
	}
}

func TestHTTPClientReportsHTTPMalformedAndMissingContentErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{"http failure does not echo response secrets", http.StatusUnauthorized, `{"error":"unit-test-secret"}`, "HTTP 401"},
		{"malformed body", http.StatusOK, `{`, "decode LLM response"},
		{"missing content", http.StatusOK, `{"choices":[{"message":{"content":" "}}]}`, "missing message content"},
		{"empty choices", http.StatusOK, `{"choices":[]}`, "missing message content"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
				writer.WriteHeader(test.status)
				_, _ = io.WriteString(writer, test.body)
			}))
			defer server.Close()
			_, err := testHTTPClient(t, server.URL).Complete(context.Background(), "system", "user")
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("error = %v, want substring %q", err, test.want)
			}
			if strings.Contains(err.Error(), "unit-test-secret") {
				t.Fatalf("error leaked API key: %v", err)
			}
		})
	}
}

func TestHTTPClientRejectsOversizedResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(writer, strings.Repeat("x", MaxResponseBytes+1))
	}))
	defer server.Close()
	_, err := testHTTPClient(t, server.URL).Complete(context.Background(), "system", "user")
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("oversized response error = %v", err)
	}
}

func TestHTTPClientHonorsTimeoutAndContextCancellation(t *testing.T) {
	client := testHTTPClient(t, "http://127.0.0.1")
	client.client = &http.Client{Timeout: 20 * time.Millisecond, Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		<-request.Context().Done()
		return nil, request.Context().Err()
	})}
	if _, err := client.Complete(context.Background(), "system", "user"); err == nil {
		t.Fatal("HTTP timeout was ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := client.Complete(ctx, "system", "user"); err == nil {
		t.Fatal("context cancellation was ignored")
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (roundTrip roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return roundTrip(request)
}

func TestConfigFromEnvValidationAndDefault(t *testing.T) {
	t.Setenv("BUYRIGHTAGENTS_LLM_API_KEY", "")
	t.Setenv("BUYRIGHTAGENTS_LLM_MODEL", "")
	t.Setenv("BUYRIGHTAGENTS_LLM_BASE_URL", "")
	if _, err := ConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "BUYRIGHTAGENTS_LLM_API_KEY") {
		t.Fatalf("missing API key error = %v", err)
	}
	t.Setenv("BUYRIGHTAGENTS_LLM_API_KEY", "fake-key")
	if _, err := ConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "BUYRIGHTAGENTS_LLM_MODEL") {
		t.Fatalf("missing model error = %v", err)
	}
	t.Setenv("BUYRIGHTAGENTS_LLM_MODEL", "fake-model")
	config, err := ConfigFromEnv()
	if err != nil || config.BaseURL != DefaultBaseURL {
		t.Fatalf("default config = %#v, error %v", config, err)
	}
	t.Setenv("BUYRIGHTAGENTS_LLM_BASE_URL", "file:///tmp/invalid")
	if _, err := ConfigFromEnv(); err == nil || !strings.Contains(err.Error(), "absolute HTTP or HTTPS") {
		t.Fatalf("invalid base URL error = %v", err)
	}
	t.Setenv("BUYRIGHTAGENTS_LLM_BASE_URL", "https://user:embedded-password@example.invalid/v1")
	if _, err := ConfigFromEnv(); err == nil || strings.Contains(err.Error(), "embedded-password") {
		t.Fatalf("embedded URL credentials were accepted or exposed: %v", err)
	}
}

func mustJSONString(value string) string {
	encoded, _ := json.Marshal(value)
	return string(encoded)
}
