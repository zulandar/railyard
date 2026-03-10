package audit

import (
	"bytes"
	"encoding/json"
	"testing"
	"time"
)

func TestAuditEvent_HasRequiredFields(t *testing.T) {
	now := time.Now()
	e := AuditEvent{
		EventType: "config.loaded",
		Actor:     "system",
		Resource:  "config.yaml",
		Detail:    `{"path":"/etc/railyard/config.yaml"}`,
		CreatedAt: now,
	}
	if e.EventType != "config.loaded" {
		t.Errorf("EventType = %q", e.EventType)
	}
	if e.Actor != "system" {
		t.Errorf("Actor = %q", e.Actor)
	}
	if e.Resource != "config.yaml" {
		t.Errorf("Resource = %q", e.Resource)
	}
	if e.Detail != `{"path":"/etc/railyard/config.yaml"}` {
		t.Errorf("Detail = %q", e.Detail)
	}
	if e.CreatedAt != now {
		t.Errorf("CreatedAt = %v", e.CreatedAt)
	}
}

func TestLog_NilDB_WritesJSON(t *testing.T) {
	var buf bytes.Buffer
	err := Log(nil, &buf, "config.loaded", "system", "config.yaml", map[string]string{"path": "/tmp/config.yaml"})
	if err != nil {
		t.Fatalf("Log() error: %v", err)
	}

	var got map[string]interface{}
	if err := json.Unmarshal(buf.Bytes(), &got); err != nil {
		t.Fatalf("invalid JSON: %v\nbody: %s", err, buf.String())
	}
	if got["event_type"] != "config.loaded" {
		t.Errorf("event_type = %v", got["event_type"])
	}
	if got["actor"] != "system" {
		t.Errorf("actor = %v", got["actor"])
	}
	if got["resource"] != "config.yaml" {
		t.Errorf("resource = %v", got["resource"])
	}
	if got["audit"] != true {
		t.Errorf("audit marker missing")
	}
	if _, ok := got["timestamp"]; !ok {
		t.Error("timestamp missing")
	}
	detail, ok := got["detail"].(map[string]interface{})
	if !ok {
		t.Fatalf("detail not a map: %T", got["detail"])
	}
	if detail["path"] != "/tmp/config.yaml" {
		t.Errorf("detail.path = %v", detail["path"])
	}
}

func TestLog_NilDB_NilWriter_NoError(t *testing.T) {
	err := Log(nil, nil, "config.loaded", "system", "config.yaml", nil)
	if err != nil {
		t.Fatalf("Log() error: %v", err)
	}
}

func TestLog_MultipleEvents_AllWritten(t *testing.T) {
	var buf bytes.Buffer
	for _, et := range []string{"config.seed_tracks", "config.seed_config"} {
		if err := Log(nil, &buf, et, "system", "railyard", nil); err != nil {
			t.Fatalf("Log(%s) error: %v", et, err)
		}
	}
	lines := bytes.Split(bytes.TrimSpace(buf.Bytes()), []byte("\n"))
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSON lines, got %d", len(lines))
	}
}
