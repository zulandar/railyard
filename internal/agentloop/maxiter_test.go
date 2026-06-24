package agentloop

import (
	"errors"
	"fmt"
	"testing"
)

func TestIsMaxIterationsError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil is not a cap hit", err: nil, want: false},
		{
			name: "sentinel wrapped with %w is detected",
			err:  fmt.Errorf("inspect: native run prompt: %w (after 30 iterations)", ErrMaxIterations),
			want: true,
		},
		{
			name: "deeply wrapped sentinel is detected",
			err:  fmt.Errorf("outer: %w", fmt.Errorf("inner: %w", ErrMaxIterations)),
			want: true,
		},
		{
			name: "legacy substring (no sentinel) is still detected",
			err:  fmt.Errorf("inspect: native run prompt: agent did not finish within 16 iterations"),
			want: true,
		},
		{
			name: "unrelated error is not a cap hit",
			err:  errors.New("network timeout"),
			want: false,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsMaxIterationsError(tt.err); got != tt.want {
				t.Errorf("IsMaxIterationsError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
