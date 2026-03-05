package engine

import (
	"context"
	"fmt"
	"os/exec"
	"sync"
)

// AgentProvider defines the interface for AI CLI tool integrations.
type AgentProvider interface {
	// Name returns the provider identifier (e.g., "claude", "opencode").
	Name() string
	// BuildCommand constructs the exec.Cmd for the provider's CLI tool.
	BuildCommand(ctx context.Context, opts SpawnOpts) (*exec.Cmd, context.CancelFunc)
	// ParseOutput extracts token usage statistics from the provider's output.
	ParseOutput(content string) UsageStats
	// ValidateBinary checks that the provider's CLI binary is available.
	ValidateBinary() error
}

// registry holds registered providers.
var (
	registryMu sync.RWMutex
	registry   = make(map[string]AgentProvider)
)

// RegisterProvider adds a provider to the global registry.
func RegisterProvider(p AgentProvider) {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry[p.Name()] = p
}

// GetProvider retrieves a provider by name from the registry.
func GetProvider(name string) (AgentProvider, error) {
	registryMu.RLock()
	defer registryMu.RUnlock()
	p, ok := registry[name]
	if !ok {
		return nil, fmt.Errorf("engine: unknown provider %q", name)
	}
	return p, nil
}

// ListProviders returns the names of all registered providers.
func ListProviders() []string {
	registryMu.RLock()
	defer registryMu.RUnlock()
	names := make([]string, 0, len(registry))
	for name := range registry {
		names = append(names, name)
	}
	return names
}

// ResetRegistry clears all registered providers (for testing).
func ResetRegistry() {
	registryMu.Lock()
	defer registryMu.Unlock()
	registry = make(map[string]AgentProvider)
}
