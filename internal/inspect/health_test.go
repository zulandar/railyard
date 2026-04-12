package inspect

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestHealthServer_Healthz(t *testing.T) {
	hs := NewHealthServer(30 * time.Second)
	mux := http.NewServeMux()
	registerHealthHandlers(mux, hs)

	req := httptest.NewRequest(http.MethodGet, "/healthz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestHealthServer_ReadyzFresh(t *testing.T) {
	hs := NewHealthServer(30 * time.Second)
	mux := http.NewServeMux()
	registerHealthHandlers(mux, hs)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Errorf("status = %d, want 200", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Errorf("body = %q, want %q", body, "ok")
	}
}

func TestHealthServer_ReadyzStale(t *testing.T) {
	hs := NewHealthServer(1 * time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	mux := http.NewServeMux()
	registerHealthHandlers(mux, hs)

	req := httptest.NewRequest(http.MethodGet, "/readyz", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)
	if !strings.Contains(string(body), "not ready") {
		t.Errorf("body = %q, want contains 'not ready'", body)
	}
}

func TestHealthServer_RecordPoll(t *testing.T) {
	hs := NewHealthServer(1 * time.Millisecond)
	time.Sleep(5 * time.Millisecond)

	if hs.IsReady() {
		t.Error("expected not ready before RecordPoll")
	}

	hs.RecordPoll()

	if !hs.IsReady() {
		t.Error("expected ready after RecordPoll")
	}
}
