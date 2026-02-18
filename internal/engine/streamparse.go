package engine

import (
	"encoding/json"
	"strings"
)

// UsageStats holds extracted token usage from stream-json content.
type UsageStats struct {
	InputTokens  int
	OutputTokens int
	Model        string
}

// streamEvent is used for initial type dispatch.
type streamEvent struct {
	Type string `json:"type"`
}

// resultEvent extracts usage from result events.
type resultEvent struct {
	Usage struct {
		InputTokens  int `json:"input_tokens"`
		OutputTokens int `json:"output_tokens"`
	} `json:"usage"`
}

// assistantEvent extracts the model from assistant events.
type assistantEvent struct {
	Message struct {
		Model string `json:"model"`
	} `json:"message"`
}

// ParseUsageFromContent scans stream-json lines and extracts token usage
// and model information. It sums across multiple result events.
func ParseUsageFromContent(content string) UsageStats {
	var stats UsageStats

	for _, line := range strings.Split(content, "\n") {
		line = strings.TrimSpace(line)
		if len(line) == 0 || line[0] != '{' {
			continue
		}

		var evt streamEvent
		if err := json.Unmarshal([]byte(line), &evt); err != nil {
			continue
		}

		switch evt.Type {
		case "result":
			var r resultEvent
			if err := json.Unmarshal([]byte(line), &r); err == nil {
				stats.InputTokens += r.Usage.InputTokens
				stats.OutputTokens += r.Usage.OutputTokens
			}
		case "assistant":
			var a assistantEvent
			if err := json.Unmarshal([]byte(line), &a); err == nil && a.Message.Model != "" {
				stats.Model = a.Message.Model
			}
		}
	}

	return stats
}
