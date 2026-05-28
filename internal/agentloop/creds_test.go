package agentloop

import (
	"strings"
	"testing"
)

func TestResolveCredentials_OpenRouterDefaults(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	t.Setenv("OPENROUTER_BASE_URL", "")

	creds, err := ResolveCredentials("openrouter")
	if err != nil {
		t.Fatalf("ResolveCredentials: %v", err)
	}
	if creds.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("BaseURL = %q, want https://openrouter.ai/api/v1", creds.BaseURL)
	}
	if creds.APIKey != "sk-or-test" {
		t.Errorf("APIKey = %q, want sk-or-test", creds.APIKey)
	}
	// OpenRouter ranking headers must be present.
	if creds.Headers["HTTP-Referer"] == "" {
		t.Errorf("Headers missing HTTP-Referer: %v", creds.Headers)
	}
	if creds.Headers["X-Title"] == "" {
		t.Errorf("Headers missing X-Title: %v", creds.Headers)
	}
}

func TestResolveCredentials_OpenRouterBaseURLOverride(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	t.Setenv("OPENROUTER_BASE_URL", "https://proxy.internal/api/v1/")

	creds, err := ResolveCredentials("openrouter")
	if err != nil {
		t.Fatalf("ResolveCredentials: %v", err)
	}
	// Trailing slash must be trimmed so baseURL+"/chat/completions" is clean.
	if creds.BaseURL != "https://proxy.internal/api/v1" {
		t.Errorf("BaseURL = %q, want https://proxy.internal/api/v1", creds.BaseURL)
	}
}

func TestResolveCredentials_OpenRouterMissingKey(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "")

	_, err := ResolveCredentials("openrouter")
	if err == nil {
		t.Fatal("expected error for missing OPENROUTER_API_KEY, got nil")
	}
	if !strings.Contains(err.Error(), "OPENROUTER_API_KEY") {
		t.Errorf("error = %q, want to name OPENROUTER_API_KEY", err.Error())
	}
}

func TestResolveCredentials_OpenAICompat(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://api.example.com/v1")
	t.Setenv("OPENAI_API_BASE", "")
	t.Setenv("OPENAI_API_KEY", "sk-oai")

	creds, err := ResolveCredentials("openai_compat")
	if err != nil {
		t.Fatalf("ResolveCredentials: %v", err)
	}
	if creds.BaseURL != "https://api.example.com/v1" {
		t.Errorf("BaseURL = %q, want https://api.example.com/v1", creds.BaseURL)
	}
	if creds.APIKey != "sk-oai" {
		t.Errorf("APIKey = %q, want sk-oai", creds.APIKey)
	}
}

func TestResolveCredentials_OpenAICompat_APIBaseFallback(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_BASE", "https://alt.example.com/v1")
	t.Setenv("OPENAI_API_KEY", "sk-oai")

	creds, err := ResolveCredentials("openai_compat")
	if err != nil {
		t.Fatalf("ResolveCredentials: %v", err)
	}
	if creds.BaseURL != "https://alt.example.com/v1" {
		t.Errorf("BaseURL = %q, want https://alt.example.com/v1 (OPENAI_API_BASE fallback)", creds.BaseURL)
	}
}

func TestResolveCredentials_OpenAICompat_MissingBaseURL(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "")
	t.Setenv("OPENAI_API_BASE", "")
	t.Setenv("OPENAI_API_KEY", "sk-oai")

	_, err := ResolveCredentials("openai_compat")
	if err == nil {
		t.Fatal("expected error for missing base URL, got nil")
	}
	if !strings.Contains(err.Error(), "OPENAI_BASE_URL") {
		t.Errorf("error = %q, want to name OPENAI_BASE_URL", err.Error())
	}
}

func TestResolveCredentials_OpenAICompat_MissingKey(t *testing.T) {
	t.Setenv("OPENAI_BASE_URL", "https://api.example.com/v1")
	t.Setenv("OPENAI_API_KEY", "")

	_, err := ResolveCredentials("openai_compat")
	if err == nil {
		t.Fatal("expected error for missing OPENAI_API_KEY, got nil")
	}
	if !strings.Contains(err.Error(), "OPENAI_API_KEY") {
		t.Errorf("error = %q, want to name OPENAI_API_KEY", err.Error())
	}
}

func TestResolveCredentials_NonNativeMethod(t *testing.T) {
	_, err := ResolveCredentials("api_key")
	if err == nil {
		t.Fatal("expected error for non-native auth method, got nil")
	}
}

func TestNewClientFromEnv(t *testing.T) {
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	t.Setenv("OPENROUTER_BASE_URL", "")

	c, err := NewClientFromEnv("openrouter")
	if err != nil {
		t.Fatalf("NewClientFromEnv: %v", err)
	}
	if c == nil {
		t.Fatal("NewClientFromEnv returned nil client")
	}
	if c.creds.BaseURL != "https://openrouter.ai/api/v1" {
		t.Errorf("client BaseURL = %q, want default", c.creds.BaseURL)
	}
}
