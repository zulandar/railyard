package main

import (
	"fmt"
	"strings"
)

// formatTokenCount formats an integer with comma separators (e.g. 45230 -> "45,230").
func formatTokenCount(n int64) string {
	if n < 0 {
		return "-" + formatTokenCount(-n)
	}
	s := fmt.Sprintf("%d", n)
	if len(s) <= 3 {
		return s
	}

	var b strings.Builder
	remainder := len(s) % 3
	if remainder > 0 {
		b.WriteString(s[:remainder])
	}
	for i := remainder; i < len(s); i += 3 {
		if b.Len() > 0 {
			b.WriteByte(',')
		}
		b.WriteString(s[i : i+3])
	}
	return b.String()
}

// estimateCost estimates the USD cost for the given model and token counts.
func estimateCost(model string, inputTokens, outputTokens int64) float64 {
	var inputRate, outputRate float64 // per million tokens

	switch {
	case strings.HasPrefix(model, "claude-opus"):
		inputRate = 15.0
		outputRate = 75.0
	case strings.HasPrefix(model, "claude-sonnet"):
		inputRate = 3.0
		outputRate = 15.0
	case strings.HasPrefix(model, "claude-haiku"):
		inputRate = 0.80
		outputRate = 4.0
	default:
		// Unknown model: use sonnet pricing as a reasonable default.
		inputRate = 3.0
		outputRate = 15.0
	}

	return float64(inputTokens)/1_000_000*inputRate + float64(outputTokens)/1_000_000*outputRate
}
