package audit

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"gorm.io/gorm"
)

// Log records an audit event to the database (if db is non-nil) and writes
// structured JSON to w (if w is non-nil). detail is marshalled as the "detail"
// field in both the DB row and the JSON output.
func Log(db *gorm.DB, w io.Writer, eventType, actor, resource string, detail interface{}) error {
	now := time.Now().UTC()

	detailJSON, err := marshalDetail(detail)
	if err != nil {
		return fmt.Errorf("audit: marshal detail: %w", err)
	}

	if db != nil {
		evt := AuditEvent{
			EventType: eventType,
			Actor:     actor,
			Resource:  resource,
			Detail:    detailJSON,
			CreatedAt: now,
		}
		if err := db.Create(&evt).Error; err != nil {
			return fmt.Errorf("audit: db insert: %w", err)
		}
	}

	if w != nil {
		entry := map[string]interface{}{
			"audit":      true,
			"level":      "info",
			"event_type": eventType,
			"actor":      actor,
			"resource":   resource,
			"timestamp":  now.Format(time.RFC3339Nano),
		}
		if detail != nil {
			entry["detail"] = detail
		}
		data, err := json.Marshal(entry)
		if err != nil {
			return fmt.Errorf("audit: marshal json: %w", err)
		}
		data = append(data, '\n')
		if _, err := w.Write(data); err != nil {
			return fmt.Errorf("audit: write json: %w", err)
		}
	}

	return nil
}

func marshalDetail(v interface{}) (string, error) {
	if v == nil {
		return "", nil
	}
	data, err := json.Marshal(v)
	if err != nil {
		return "", err
	}
	return string(data), nil
}
