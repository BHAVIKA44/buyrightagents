package intent

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	DefaultBaseURL   = "https://api.openai.com/v1"
	RequestTimeout   = 30 * time.Second
	MaxResponseBytes = 1 << 20
)

type Config struct {
	APIKey         string
	Model          string
	BaseURL        string
	RepairAttempts int
}

func ConfigFromEnv() (Config, error) {
	config := Config{
		APIKey:  strings.TrimSpace(os.Getenv("BUYRIGHTAGENTS_LLM_API_KEY")),
		Model:   strings.TrimSpace(os.Getenv("BUYRIGHTAGENTS_LLM_MODEL")),
		BaseURL: strings.TrimRight(strings.TrimSpace(os.Getenv("BUYRIGHTAGENTS_LLM_BASE_URL")), "/"),
	}
	if config.APIKey == "" {
		return Config{}, errors.New("BUYRIGHTAGENTS_LLM_API_KEY is required")
	}
	if config.Model == "" {
		return Config{}, errors.New("BUYRIGHTAGENTS_LLM_MODEL is required")
	}
	if config.BaseURL == "" {
		config.BaseURL = DefaultBaseURL
	}
	if configured := strings.TrimSpace(os.Getenv("BUYRIGHTAGENTS_LLM_REPAIR_ATTEMPTS")); configured != "" {
		attempts, parseErr := strconv.Atoi(configured)
		if parseErr != nil || attempts < 0 || attempts > 1 {
			return Config{}, errors.New("BUYRIGHTAGENTS_LLM_REPAIR_ATTEMPTS must be 0 or 1")
		}
		config.RepairAttempts = attempts
	}
	if err := config.validate(); err != nil {
		return Config{}, err
	}
	return config, nil
}

func (config Config) validate() error {
	if strings.TrimSpace(config.APIKey) == "" {
		return errors.New("LLM API key is required")
	}
	if strings.TrimSpace(config.Model) == "" {
		return errors.New("LLM model identifier is required")
	}
	if config.RepairAttempts < 0 || config.RepairAttempts > 1 {
		return errors.New("LLM repair attempts must be 0 or 1")
	}
	parsed, err := url.Parse(config.BaseURL)
	if err != nil || parsed.Host == "" || parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" || (parsed.Scheme != "https" && parsed.Scheme != "http") {
		return errors.New("LLM base URL must be an absolute HTTP or HTTPS URL without embedded credentials, query, or fragment")
	}
	return nil
}

type HTTPClient struct {
	config Config
	client *http.Client
}

func NewHTTPClient(config Config) (*HTTPClient, error) {
	if config.BaseURL == "" {
		config.BaseURL = DefaultBaseURL
	}
	if err := config.validate(); err != nil {
		return nil, err
	}
	return &HTTPClient{
		config: config,
		client: &http.Client{Timeout: RequestTimeout},
	}, nil
}

func (client *HTTPClient) Host() string {
	parsed, _ := url.Parse(client.config.BaseURL)
	if parsed == nil {
		return ""
	}
	return parsed.Host
}

type chatRequest struct {
	Model          string         `json:"model"`
	Messages       []chatMessage  `json:"messages"`
	Temperature    float64        `json:"temperature"`
	ResponseFormat responseFormat `json:"response_format"`
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type responseFormat struct {
	Type string `json:"type"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
}

// HTTPStatusError contains only safe response metadata. Response bodies and
// request credentials are deliberately excluded.
type HTTPStatusError struct {
	StatusCode        int
	RetryAfter        string
	RemainingRequests string
	ResetRequests     string
	RemainingTokens   string
	ResetTokens       string
}

func (failure *HTTPStatusError) Error() string {
	message := fmt.Sprintf("LLM endpoint returned HTTP %d %s", failure.StatusCode, http.StatusText(failure.StatusCode))
	metadata := make([]string, 0, 5)
	if failure.RetryAfter != "" {
		metadata = append(metadata, "Retry-After="+failure.RetryAfter)
	}
	if failure.RemainingRequests != "" {
		metadata = append(metadata, "remaining-requests="+failure.RemainingRequests)
	}
	if failure.ResetRequests != "" {
		metadata = append(metadata, "reset-requests="+failure.ResetRequests)
	}
	if failure.RemainingTokens != "" {
		metadata = append(metadata, "remaining-tokens="+failure.RemainingTokens)
	}
	if failure.ResetTokens != "" {
		metadata = append(metadata, "reset-tokens="+failure.ResetTokens)
	}
	if len(metadata) > 0 {
		message += " (" + strings.Join(metadata, ", ") + ")"
	}
	return message
}

func (client *HTTPClient) Complete(ctx context.Context, system, user string) (string, error) {
	requestBody := chatRequest{
		Model:          client.config.Model,
		Messages:       []chatMessage{{Role: "system", Content: system}, {Role: "user", Content: user}},
		Temperature:    0,
		ResponseFormat: responseFormat{Type: "json_object"},
	}
	encoded, err := json.Marshal(requestBody)
	if err != nil {
		return "", fmt.Errorf("encode LLM request: %w", err)
	}
	endpoint := strings.TrimRight(client.config.BaseURL, "/") + "/chat/completions"
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(encoded))
	if err != nil {
		return "", fmt.Errorf("create LLM request: %w", err)
	}
	request.Header.Set("Authorization", "Bearer "+client.config.APIKey)
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Accept", "application/json")
	response, err := client.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("LLM request failed: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return "", &HTTPStatusError{
			StatusCode:        response.StatusCode,
			RetryAfter:        safeHeaderValue(response.Header.Get("Retry-After"), client.config.APIKey),
			RemainingRequests: safeHeaderValue(response.Header.Get("x-ratelimit-remaining-requests"), client.config.APIKey),
			ResetRequests:     safeHeaderValue(response.Header.Get("x-ratelimit-reset-requests"), client.config.APIKey),
			RemainingTokens:   safeHeaderValue(response.Header.Get("x-ratelimit-remaining-tokens"), client.config.APIKey),
			ResetTokens:       safeHeaderValue(response.Header.Get("x-ratelimit-reset-tokens"), client.config.APIKey),
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, MaxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("read LLM response: %w", err)
	}
	if len(body) > MaxResponseBytes {
		return "", fmt.Errorf("LLM response exceeds %d bytes", MaxResponseBytes)
	}
	var parsed chatResponse
	if err := json.Unmarshal(body, &parsed); err != nil {
		return "", fmt.Errorf("decode LLM response body: %w", err)
	}
	if len(parsed.Choices) == 0 || strings.TrimSpace(parsed.Choices[0].Message.Content) == "" {
		return "", errors.New("LLM response is missing message content")
	}
	return parsed.Choices[0].Message.Content, nil
}

func safeHeaderValue(value, secret string) string {
	value = strings.TrimSpace(value)
	if secret != "" {
		value = strings.ReplaceAll(value, secret, "[REDACTED]")
	}
	if len(value) > 128 {
		value = value[:128]
	}
	for _, r := range value {
		if r < 0x20 || r == 0x7f {
			return ""
		}
	}
	return value
}
