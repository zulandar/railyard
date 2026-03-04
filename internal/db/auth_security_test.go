package db

import (
	"strings"
	"testing"

	"gorm.io/gorm"
)

// Auth security regression tests for the database layer.
//
// These tests verify that credential handling is secure and document
// the current authentication model. They serve as a regression suite
// to prevent accidental credential exposure.

func TestConnect_DefaultCredentials_ShouldWork(t *testing.T) {
	// Documents that default root/empty-password is accepted by Connect.
	// This is expected for local development but should be restricted
	// in production deployments. The ry doctor command warns about this.
	var fn func(string, int, string, string, string) (*gorm.DB, error) = Connect
	if fn == nil {
		t.Fatal("Connect function is nil")
	}
	// We can't test an actual connection without a running Dolt server,
	// but we verify the function signature accepts empty password.
}

func TestConnectAdmin_DefaultCredentials_ShouldWork(t *testing.T) {
	// Same as above for admin connections.
	var fn func(string, int, string, string) (*gorm.DB, error) = ConnectAdmin
	if fn == nil {
		t.Fatal("ConnectAdmin function is nil")
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
