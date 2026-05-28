package agentloop

import "testing"

func TestIsNativeLoopMethod(t *testing.T) {
	cases := map[string]bool{
		"openrouter":      true,
		"openai_compat":   true,
		"api_key":         false,
		"oauth_token":     false,
		"bedrock":         false,
		"vertex":          false,
		"foundry":         false,
		"do_inference":    false, // routed through a CLI provider, not the native loop
		"openrouter_skin": false, // Approach B: claude CLI -> OpenRouter skin, not native
		"":                false,
	}
	for method, want := range cases {
		if got := IsNativeLoopMethod(method); got != want {
			t.Errorf("IsNativeLoopMethod(%q) = %v, want %v", method, got, want)
		}
	}
}

func TestValidateEnv_MatchesResolveCredentials(t *testing.T) {
	// Present key -> nil; missing key -> error (delegates to ResolveCredentials).
	t.Setenv("OPENROUTER_API_KEY", "sk-or-test")
	if err := ValidateEnv("openrouter"); err != nil {
		t.Errorf("ValidateEnv with key present: %v", err)
	}
	t.Setenv("OPENROUTER_API_KEY", "")
	if err := ValidateEnv("openrouter"); err == nil {
		t.Error("ValidateEnv with key absent: want error, got nil")
	}
}
