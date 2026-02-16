package engine

import (
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
