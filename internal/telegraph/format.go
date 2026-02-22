package telegraph

import (
	"fmt"
	"strings"

	"github.com/zulandar/railyard/internal/orchestration"
)

// Color constants for event severity.
const (
	ColorSuccess = "#36a64f"
	ColorInfo    = "#2196f3"
	ColorWarning = "#ff9800"
	ColorError   = "#e53935"
)

// severityColor maps a severity string to a sidebar color.
func severityColor(severity string) string {
	switch severity {
	case "success":
		return ColorSuccess
	case "info":
		return ColorInfo
	case "warning":
		return ColorWarning
	case "error":
		return ColorError
	default:
		return ColorInfo
	}
}

// carStatusVerb returns a human-friendly verb for a car status transition.
func carStatusVerb(newStatus string) string {
	switch newStatus {
	case "open":
		return "opened"
	case "in_progress":
		return "claimed"
	case "done":
		return "completed"
	case "merged":
		return "merged"
	case "blocked":
		return "blocked"
	case "merge-failed":
		return "merge failed"
	case "cancelled":
		return "cancelled"
	case "draft":
		return "created"
	default:
		return newStatus
	}
}

// carStatusSeverity returns the appropriate severity for a car status.
func carStatusSeverity(newStatus string) string {
	switch newStatus {
	case "done", "merged":
		return "success"
	case "blocked", "merge-failed":
		return "warning"
	case "cancelled":
		return "info"
	default:
		return "info"
	}
}

// FormatCarEvent formats a car status change event.
func FormatCarEvent(event DetectedEvent) FormattedEvent {
	verb := carStatusVerb(event.NewStatus)
	severity := carStatusSeverity(event.NewStatus)

	title := fmt.Sprintf("Car %s %s", event.CarID, verb)

	var bodyParts []string
	if event.Title != "" {
		bodyParts = append(bodyParts, event.Title)
	}
	if event.OldStatus != "" {
		bodyParts = append(bodyParts, fmt.Sprintf("%s → %s", event.OldStatus, event.NewStatus))
	}
	body := strings.Join(bodyParts, "\n")

	fields := []Field{
		{Name: "Car", Value: event.CarID, Short: true},
		{Name: "Status", Value: event.NewStatus, Short: true},
	}
	if event.Track != "" {
		fields = append(fields, Field{Name: "Track", Value: event.Track, Short: true})
	}

	return FormattedEvent{
		Title:    title,
		Body:     body,
		Severity: severity,
		Color:    severityColor(severity),
		Fields:   fields,
	}
}

// FormatStallEvent formats a stalled engine event.
func FormatStallEvent(event DetectedEvent) FormattedEvent {
	title := fmt.Sprintf("Engine %s stalled", event.EngineID)

	var bodyParts []string
	if event.CurrentCar != "" {
		bodyParts = append(bodyParts, fmt.Sprintf("Working on car %s", event.CurrentCar))
	}
	if event.Track != "" {
		bodyParts = append(bodyParts, fmt.Sprintf("Track: %s", event.Track))
	}
	body := strings.Join(bodyParts, "\n")

	fields := []Field{
		{Name: "Engine", Value: event.EngineID, Short: true},
	}
	if event.CurrentCar != "" {
		fields = append(fields, Field{Name: "Car", Value: event.CurrentCar, Short: true})
	}
	if event.Track != "" {
		fields = append(fields, Field{Name: "Track", Value: event.Track, Short: true})
	}

	return FormattedEvent{
		Title:    title,
		Body:     body,
		Severity: "warning",
		Color:    ColorWarning,
		Fields:   fields,
	}
}

// FormatEscalation formats an escalation message event.
func FormatEscalation(event DetectedEvent) FormattedEvent {
	severity := "warning"
	if event.Priority == "high" || event.Priority == "urgent" {
		severity = "error"
	}

	title := event.Subject
	if title == "" {
		title = fmt.Sprintf("Escalation from %s", event.FromAgent)
	}

	fields := []Field{
		{Name: "From", Value: event.FromAgent, Short: true},
	}
	if event.CarID != "" {
		fields = append(fields, Field{Name: "Car", Value: event.CarID, Short: true})
	}
	if event.Priority != "" {
		fields = append(fields, Field{Name: "Priority", Value: event.Priority, Short: true})
	}

	return FormattedEvent{
		Title:    title,
		Body:     event.Body,
		Severity: severity,
		Color:    severityColor(severity),
		Fields:   fields,
	}
}

// FormatPulse formats a status pulse digest from orchestration status info.
func FormatPulse(info *orchestration.StatusInfo) FormattedEvent {
	var totalActive, totalReady, totalDone, totalBlocked int64
	for _, ts := range info.TrackSummary {
		totalActive += ts.InProgress
		totalReady += ts.Ready
		totalDone += ts.Done
		totalBlocked += ts.Blocked
	}

	engineCount := len(info.Engines)
	var workingEngines int
	for _, e := range info.Engines {
		if e.Status == "working" {
			workingEngines++
		}
	}

	var bodyLines []string
	bodyLines = append(bodyLines, fmt.Sprintf("**Engines**: %d total, %d working", engineCount, workingEngines))
	bodyLines = append(bodyLines, fmt.Sprintf("**Cars**: %d active, %d ready, %d done, %d blocked",
		totalActive, totalReady, totalDone, totalBlocked))
	if info.TotalTokens > 0 {
		bodyLines = append(bodyLines, fmt.Sprintf("**Tokens**: %d total", info.TotalTokens))
	}
	if info.MessageDepth > 0 {
		bodyLines = append(bodyLines, fmt.Sprintf("**Messages**: %d pending", info.MessageDepth))
	}

	body := strings.Join(bodyLines, "\n")

	fields := []Field{
		{Name: "Engines", Value: fmt.Sprintf("%d/%d working", workingEngines, engineCount), Short: true},
		{Name: "Active Cars", Value: fmt.Sprintf("%d", totalActive), Short: true},
		{Name: "Ready", Value: fmt.Sprintf("%d", totalReady), Short: true},
		{Name: "Done", Value: fmt.Sprintf("%d", totalDone), Short: true},
	}
	if totalBlocked > 0 {
		fields = append(fields, Field{Name: "Blocked", Value: fmt.Sprintf("%d", totalBlocked), Short: true})
	}

	return FormattedEvent{
		Title:    "Railyard Pulse",
		Body:     body,
		Severity: "info",
		Color:    ColorInfo,
		Fields:   fields,
	}
}
