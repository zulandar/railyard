package engine

import (
	"fmt"
	"strings"
	"testing"
)

func TestClaimCar_EmptyEngineID(t *testing.T) {
	_, err := ClaimCar(nil, "", "backend")
	if err == nil {
		t.Fatal("expected error for empty engineID")
	}
	if !strings.Contains(err.Error(), "engineID is required") {
		t.Errorf("error = %q", err)
	}
}

func TestClaimCar_EmptyTrack(t *testing.T) {
	_, err := ClaimCar(nil, "eng-001", "")
	if err == nil {
		t.Fatal("expected error for empty track")
	}
	if !strings.Contains(err.Error(), "track is required") {
		t.Errorf("error = %q", err)
	}
}

func TestIsSerializationError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil error", nil, false},
		{"generic error", fmt.Errorf("something went wrong"), false},
		{"record not found", fmt.Errorf("record not found"), false},
		{"error 1213 code", fmt.Errorf("Error 1213 (40001): serialization failure"), true},
		{"serialization failure text", fmt.Errorf("serialization failure: this transaction conflicts"), true},
		{"deadlock", fmt.Errorf("Deadlock found when trying to get lock"), true},
		{"wrapped 1213", fmt.Errorf("engine: claim car car-abc12: %w", fmt.Errorf("Error 1213 (40001): serialization failure")), true},
		{"wrapped serialization", fmt.Errorf("engine: find ready car: %w", fmt.Errorf("serialization failure: conflict")), true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isSerializationError(tt.err)
			if got != tt.want {
				t.Errorf("isSerializationError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestClaimMaxRetries(t *testing.T) {
	if claimMaxRetries < 2 {
		t.Errorf("claimMaxRetries = %d, want at least 2", claimMaxRetries)
	}
	if claimMaxRetries > 5 {
		t.Errorf("claimMaxRetries = %d, want at most 5 to avoid excessive delays", claimMaxRetries)
	}
}
