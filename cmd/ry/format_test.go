package main

import (
	"math"
	"testing"
)

func TestFormatTokenCount(t *testing.T) {
	tests := []struct {
		input int64
		want  string
	}{
		{0, "0"},
		{1, "1"},
		{100, "100"},
		{1000, "1,000"},
		{12345, "12,345"},
		{1234567, "1,234,567"},
		{45230, "45,230"},
		{999, "999"},
		{1000000, "1,000,000"},
	}

	for _, tt := range tests {
		got := formatTokenCount(tt.input)
		if got != tt.want {
			t.Errorf("formatTokenCount(%d) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

func TestEstimateCost(t *testing.T) {
	tests := []struct {
		model  string
		input  int64
		output int64
		want   float64
	}{
		// Opus: $15/MTok in, $75/MTok out
		{"claude-opus-4-6", 1_000_000, 100_000, 22.5},
		// Sonnet: $3/MTok in, $15/MTok out
		{"claude-sonnet-4-5-20250514", 1_000_000, 100_000, 4.5},
		// Haiku: $0.80/MTok in, $4/MTok out
		{"claude-haiku-4-5-20251001", 1_000_000, 100_000, 1.2},
		// Unknown model: sonnet pricing
		{"unknown-model", 1_000_000, 100_000, 4.5},
		// Zero tokens
		{"claude-sonnet-4-5-20250514", 0, 0, 0},
		// Realistic usage
		{"claude-sonnet-4-5-20250514", 45230, 12450, 0.32259},
	}

	for _, tt := range tests {
		got := estimateCost(tt.model, tt.input, tt.output)
		if math.Abs(got-tt.want) > 0.001 {
			t.Errorf("estimateCost(%q, %d, %d) = %.5f, want %.5f", tt.model, tt.input, tt.output, got, tt.want)
		}
	}
}
