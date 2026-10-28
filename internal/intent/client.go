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
	"strings"
	"time"
)

const (
	DefaultBaseURL   = "https://api.openai.com/v1"
	RequestTimeout   = 30 * time.Second
	MaxResponseBytes = 1 << 20
)

type Config struct {
	APIKey  string
	Model   string
	BaseURL string
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
		return "", fmt.Errorf("LLM endpoint returned HTTP %d %s", response.StatusCode, http.StatusText(response.StatusCode))
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
