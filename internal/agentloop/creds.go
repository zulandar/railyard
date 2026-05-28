package agentloop

import (
	"fmt"
	"os"
	"strings"
)

// Auth methods that route agent roles through the native loop instead of a CLI
// provider. These mirror Config.AuthMethod values (yaml: auth_method).
const (
	AuthOpenRouter   = "openrouter"
	AuthOpenAICompat = "openai_compat"
)

// defaultOpenRouterBaseURL is OpenRouter's OpenAI-compatible API root. It
// already includes /v1; do NOT append another path segment.
const defaultOpenRouterBaseURL = "https://openrouter.ai/api/v1"

// IsNativeLoopMethod reports whether authMethod selects the Railyard-owned
// agent loop (rather than a CLI provider like claude/codex/gemini/copilot).
func IsNativeLoopMethod(authMethod string) bool {
	switch authMethod {
	case AuthOpenRouter, AuthOpenAICompat:
		return true
	default:
		return false
	}
}

// ResolveCredentials reads endpoint + key for a native-loop auth method from
// the environment (the chart injects these; they are never read from
// railyard.yaml). It returns an actionable error naming the missing variable.
func ResolveCredentials(authMethod string) (Credentials, error) {
	switch authMethod {
	case AuthOpenRouter:
		key := os.Getenv("OPENROUTER_API_KEY")
		if key == "" {
			return Credentials{}, fmt.Errorf(
				"auth_method=openrouter requires OPENROUTER_API_KEY to be set in the environment")
		}
		base := os.Getenv("OPENROUTER_BASE_URL")
		if base == "" {
			base = defaultOpenRouterBaseURL
		}
		return Credentials{
			BaseURL: strings.TrimRight(base, "/"),
			APIKey:  key,
			Headers: map[string]string{
				"HTTP-Referer": "https://github.com/zulandar/railyard",
				"X-Title":      "Railyard",
			},
		}, nil

	case AuthOpenAICompat:
		base := os.Getenv("OPENAI_BASE_URL")
		if base == "" {
			base = os.Getenv("OPENAI_API_BASE")
		}
		if base == "" {
			return Credentials{}, fmt.Errorf(
				"auth_method=openai_compat requires OPENAI_BASE_URL (or OPENAI_API_BASE) to be set in the environment")
		}
		key := os.Getenv("OPENAI_API_KEY")
		if key == "" {
			return Credentials{}, fmt.Errorf(
				"auth_method=openai_compat requires OPENAI_API_KEY to be set in the environment")
		}
		return Credentials{
			BaseURL: strings.TrimRight(base, "/"),
			APIKey:  key,
		}, nil

	default:
		return Credentials{}, fmt.Errorf(
			"auth_method=%q does not use the native agent loop (expected openrouter or openai_compat)", authMethod)
	}
}

// ValidateEnv checks that the environment is sufficient to construct a native
// loop client for authMethod, returning the same actionable error as
// ResolveCredentials. Used by config validation at startup.
func ValidateEnv(authMethod string) error {
	_, err := ResolveCredentials(authMethod)
	return err
}

// NewClientFromEnv resolves credentials from the environment and builds a Client.
func NewClientFromEnv(authMethod string, opts ...Option) (*Client, error) {
	creds, err := ResolveCredentials(authMethod)
	if err != nil {
		return nil, err
	}
	return NewClient(creds, opts...), nil
}
