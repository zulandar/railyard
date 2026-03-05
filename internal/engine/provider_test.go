package engine

import (
	"context"
	"os/exec"
	"sort"
	"testing"
)

// mockProvider implements AgentProvider for testing.
type mockProvider struct {
	name string
}

func (m *mockProvider) Name() string { return m.name }
func (m *mockProvider) BuildCommand(ctx context.Context, opts SpawnOpts) (*exec.Cmd, context.CancelFunc) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, "echo", "mock")
	return cmd, cancel
}
func (m *mockProvider) ParseOutput(content string) UsageStats {
	return UsageStats{}
}
func (m *mockProvider) ValidateBinary() error { return nil }

func TestRegisterAndGetProvider(t *testing.T) {
	ResetRegistry()
	p := &mockProvider{name: "test-provider"}
	RegisterProvider(p)

	got, err := GetProvider("test-provider")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	if got.Name() != "test-provider" {
		t.Errorf("Name() = %q, want %q", got.Name(), "test-provider")
	}
}

func TestGetProvider_NotFound(t *testing.T) {
	ResetRegistry()
	_, err := GetProvider("nonexistent")
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestRegisterProvider_Overwrites(t *testing.T) {
	ResetRegistry()
	p1 := &mockProvider{name: "dup"}
	p2 := &mockProvider{name: "dup"}
	RegisterProvider(p1)
	RegisterProvider(p2)

	got, err := GetProvider("dup")
	if err != nil {
		t.Fatalf("GetProvider: %v", err)
	}
	// Should get the second one (overwrite)
	if got != p2 {
		t.Error("expected second registered provider to overwrite first")
	}
}

func TestListProviders(t *testing.T) {
	ResetRegistry()
	RegisterProvider(&mockProvider{name: "alpha"})
	RegisterProvider(&mockProvider{name: "beta"})

	names := ListProviders()
	sort.Strings(names)
	if len(names) != 2 {
		t.Fatalf("len = %d, want 2", len(names))
	}
	if names[0] != "alpha" || names[1] != "beta" {
		t.Errorf("names = %v, want [alpha beta]", names)
	}
}

func TestListProviders_Empty(t *testing.T) {
	ResetRegistry()
	names := ListProviders()
	if len(names) != 0 {
		t.Errorf("len = %d, want 0", len(names))
	}
}

func TestResetRegistry(t *testing.T) {
	ResetRegistry()
	RegisterProvider(&mockProvider{name: "to-be-cleared"})
	ResetRegistry()
	_, err := GetProvider("to-be-cleared")
	if err == nil {
		t.Fatal("expected error after ResetRegistry")
	}
}
