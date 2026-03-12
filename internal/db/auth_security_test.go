package db

import (
	"fmt"
	"strings"
	"testing"

	"gorm.io/gorm"
)

// Auth security regression tests for the database layer.
//
// These tests verify that credential handling is secure and document
// the current authentication model. They serve as a regression suite
// to prevent accidental credential exposure.

func TestConnect_UsesExpectedDSN(t *testing.T) {
	var captured string
	orig := openDB
	openDB = func(dsn string) (*gorm.DB, error) {
		captured = dsn
		return nil, fmt.Errorf("stub: no real db")
	}
	defer func() { openDB = orig }()

	tests := []struct {
		name     string
		password string
		wantDSN  string
	}{
		{"empty password", "", "root@tcp(127.0.0.1:3306)/testdb?parseTime=true"},
		{"with password", "secret", "root:secret@tcp(127.0.0.1:3306)/testdb?parseTime=true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captured = ""
			_, err := Connect("127.0.0.1", 3306, "testdb", "root", tt.password)
			if err == nil {
				t.Fatal("expected stub error")
			}
			if captured != tt.wantDSN {
				t.Errorf("Connect DSN = %q, want %q", captured, tt.wantDSN)
			}
		})
	}
}

func TestConnectAdmin_UsesExpectedDSN(t *testing.T) {
	var captured string
	orig := openDB
	openDB = func(dsn string) (*gorm.DB, error) {
		captured = dsn
		return nil, fmt.Errorf("stub: no real db")
	}
	defer func() { openDB = orig }()

	tests := []struct {
		name     string
		password string
		wantDSN  string
	}{
		{"empty password", "", "root@tcp(127.0.0.1:3306)/?parseTime=true"},
		{"with password", "secret", "root:secret@tcp(127.0.0.1:3306)/?parseTime=true"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			captured = ""
			_, err := ConnectAdmin("127.0.0.1", 3306, "root", tt.password)
			if err == nil {
				t.Fatal("expected stub error")
			}
			if captured != tt.wantDSN {
				t.Errorf("ConnectAdmin DSN = %q, want %q", captured, tt.wantDSN)
			}
		})
	}
}

func TestDSN_PasswordNotInErrorContext(t *testing.T) {
	// Verify that DSN construction with passwords works but that
	// the password would be caught by sanitizeDBError if leaked.
	password := "super-secret-p@ss!"
	dsn := DSN("127.0.0.1", 3306, "testdb", "admin", password)

	// DSN contains password (necessary for connection)
	if !strings.Contains(dsn, password) {
		t.Error("DSN should contain the password for connection purposes")
	}

	// But sanitizeDBError strips it
	sanitized := sanitizeDBError(dsn, password)
	if strings.Contains(sanitized, password) {
		t.Errorf("sanitizeDBError should strip password from DSN: %s", sanitized)
	}
	if !strings.Contains(sanitized, "***") {
		t.Error("sanitizeDBError should replace password with ***")
	}
}

func TestConnect_RejectsEmptyHost(t *testing.T) {
	// Empty host should produce an error, not silently connect.
	_, err := Connect("", 0, "testdb", "root", "")
	if err == nil {
		t.Error("Connect with empty host should fail")
	}
}

func TestConnectAdmin_RejectsEmptyHost(t *testing.T) {
	_, err := ConnectAdmin("", 0, "root", "")
	if err == nil {
		t.Error("ConnectAdmin with empty host should fail")
	}
}

func TestSanitizeDBError_MultipleOccurrences(t *testing.T) {
	// Password might appear multiple times in error message
	password := "secret123"
	errMsg := "connect secret123@tcp(host:3306): Access denied for secret123"
	got := sanitizeDBError(errMsg, password)

	if strings.Contains(got, password) {
		t.Errorf("sanitizeDBError should replace ALL occurrences: %s", got)
	}
	if strings.Count(got, "***") != 2 {
		t.Errorf("expected 2 replacements, got %d in: %s", strings.Count(got, "***"), got)
	}
}

func TestSanitizeDBError_EmptyPassword(t *testing.T) {
	// Empty password should not cause issues
	errMsg := "connection refused"
	got := sanitizeDBError(errMsg, "")
	if got != errMsg {
		t.Errorf("sanitizeDBError with empty password should not modify message: %s", got)
	}
}

func TestSanitizeDBError_DSNPattern(t *testing.T) {
	// Regex should catch user:pass@host patterns even without explicit password
	errMsg := "dial tcp admin:s3cret@tcp(127.0.0.1:3306): connection refused"
	got := sanitizeDBError(errMsg, "")
	if strings.Contains(got, "s3cret") {
		t.Errorf("sanitizeDBError should strip DSN credentials: %s", got)
	}
	if !strings.Contains(got, "***@") {
		t.Errorf("sanitizeDBError should replace DSN creds with ***@: %s", got)
	}
}

func TestSanitizeDBError_PostgresDSNPattern(t *testing.T) {
	errMsg := "failed to connect: user:password@pghost:5432/db"
	got := sanitizeDBError(errMsg, "")
	if strings.Contains(got, "password") {
		t.Errorf("sanitizeDBError should strip postgres DSN creds: %s", got)
	}
}
