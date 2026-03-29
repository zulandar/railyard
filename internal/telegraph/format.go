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

// statusEmoji returns a unicode emoji for a car status.
func statusEmoji(status string) string {
	switch status {
	case "draft":
		return "📝"
	case "open":
		return "📋"
	case "in_progress":
		return "🔧"
	case "done":
		return "✅"
	case "merged":
		return "🚀"
	case "blocked":
		return "⚠️"
	case "merge-failed":
		return "❌"
	case "cancelled":
		return "🚫"
	default:
		return ""
	}
}

// stallEmoji returns the emoji for stall events.
func stallEmoji() string { return "🛑" }

// carLink returns a markdown link to a car if dashboardURL is set, otherwise plain text.
func carLink(carID, dashboardURL string) string {
	if dashboardURL == "" {
		return carID
	}
	return fmt.Sprintf("[%s](%s/cars/%s)", carID, strings.TrimRight(dashboardURL, "/"), carID)
}

// engineLink returns a markdown link to an engine if dashboardURL is set, otherwise plain text.
func engineLink(engineID, dashboardURL string) string {
	if dashboardURL == "" {
		return engineID
	}
	return fmt.Sprintf("[%s](%s/engines/%s)", engineID, strings.TrimRight(dashboardURL, "/"), engineID)
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
func FormatCarEvent(event DetectedEvent, dashboardURL string) FormattedEvent {
	verb := carStatusVerb(event.NewStatus)
	severity := carStatusSeverity(event.NewStatus)

	emoji := statusEmoji(event.NewStatus)
	title := fmt.Sprintf("Car %s %s", event.CarID, verb)
	if emoji != "" {
		title = fmt.Sprintf("%s Car %s %s", emoji, event.CarID, verb)
	}

	carRef := carLink(event.CarID, dashboardURL)

	var bodyParts []string
	if event.Title != "" {
		bodyParts = append(bodyParts, event.Title)
	}
	if event.OldStatus != "" {
		bodyParts = append(bodyParts, fmt.Sprintf("%s → %s", event.OldStatus, event.NewStatus))
	}
	body := strings.Join(bodyParts, "\n")

	fields := []Field{
		{Name: "Car", Value: carRef, Short: true},
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
func FormatStallEvent(event DetectedEvent, dashboardURL string) FormattedEvent {
	title := fmt.Sprintf("%s Engine %s stalled", stallEmoji(), event.EngineID)

	engRef := engineLink(event.EngineID, dashboardURL)
	carRef := carLink(event.CurrentCar, dashboardURL)

	var bodyParts []string
	bodyParts = append(bodyParts, fmt.Sprintf("Engine %s stalled", engRef))
	if event.CurrentCar != "" {
		bodyParts = append(bodyParts, fmt.Sprintf("Working on car %s", carRef))
	}
	if event.Track != "" {
		bodyParts = append(bodyParts, fmt.Sprintf("Track: %s", event.Track))
	}
	body := strings.Join(bodyParts, "\n")

	fields := []Field{
		{Name: "Engine", Value: engRef, Short: true},
	}
	if event.CurrentCar != "" {
		fields = append(fields, Field{Name: "Car", Value: carRef, Short: true})
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
func FormatEscalation(event DetectedEvent, dashboardURL string) FormattedEvent {
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
		fields = append(fields, Field{Name: "Car", Value: carLink(event.CarID, dashboardURL), Short: true})
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
func FormatPulse(info *orchestration.StatusInfo, dashboardURL string) FormattedEvent {
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
		Title:    "💓 Railyard Pulse",
		Body:     body,
		Severity: "info",
		Color:    ColorInfo,
		Fields:   fields,
	}
}
