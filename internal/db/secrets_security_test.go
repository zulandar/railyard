package db

import (
	"strings"
	"testing"
)

// Secrets security tests verify that credentials are never leaked through
// error messages, DSN strings, or driver output. These complement the
// auth_security_test.go suite with deeper coverage of edge cases.

// ---------------------------------------------------------------------------
// 1. DSN credential sanitization — regex strips user:pass@host even without
//    an explicit password argument.
// ---------------------------------------------------------------------------

func TestSecrets_SanitizeDBError_RegexStripsWithoutPassword(t *testing.T) {
	tests := []struct {
		name   string
		errMsg string
		leak   string // substring that must NOT appear after sanitization
	}{
		{
			name:   "user:pass with no explicit password arg",
			errMsg: "dial tcp operator:hunter2@tcp(10.0.0.1:3306): i/o timeout",
			leak:   "hunter2",
		},
		{
			name:   "user only no colon is left alone",
			errMsg: "dial tcp root@tcp(10.0.0.1:3306): connection refused",
			leak:   "", // nothing secret to leak
		},
		{
			name:   "long password stripped by regex alone",
			errMsg: "admin:ThisIsAVeryLongPasswordThatShouldBeRemoved@tcp(db:3306): timeout",
			leak:   "ThisIsAVeryLongPasswordThatShouldBeRemoved",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeDBError(tc.errMsg, "") // empty explicit password
			if tc.leak != "" && strings.Contains(got, tc.leak) {
				t.Errorf("sanitizeDBError leaked %q in: %s", tc.leak, got)
			}
			if tc.leak != "" && !strings.Contains(got, "***@") {
				t.Errorf("expected ***@ replacement, got: %s", got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 2. URL-encoded passwords (special chars like %40 %23 etc.)
// ---------------------------------------------------------------------------

func TestSecrets_SanitizeDBError_URLEncodedPasswords(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		password string
		leak     string
	}{
		{
			name:     "password with encoded @",
			errMsg:   "admin:p%40ssword@tcp(host:3306): access denied",
			password: "p%40ssword",
			leak:     "p%40ssword",
		},
		{
			name:     "password with encoded #",
			errMsg:   "admin:s3cr%23t@tcp(host:3306): access denied",
			password: "s3cr%23t",
			leak:     "s3cr%23t",
		},
		{
			name:     "password with encoded colon",
			errMsg:   "user:pass%3Aword@tcp(host:3306): denied",
			password: "pass%3Aword",
			leak:     "pass%3Aword",
		},
		{
			name:     "password with multiple encoded chars",
			errMsg:   "deploy:p%40ss%23w0rd%21@tcp(db:3306): timeout",
			password: "p%40ss%23w0rd%21",
			leak:     "p%40ss%23w0rd%21",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeDBError(tc.errMsg, tc.password)
			if strings.Contains(got, tc.leak) {
				t.Errorf("sanitizeDBError leaked URL-encoded password %q in: %s", tc.leak, got)
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 3. Multiple DSN formats — MySQL tcp(host:port) and PostgreSQL user:pass@host
// ---------------------------------------------------------------------------

func TestSecrets_SanitizeDBError_MultipleDSNFormats(t *testing.T) {
	tests := []struct {
		name     string
		errMsg   string
		password string
		leaks    []string
	}{
		{
			name:     "MySQL tcp format",
			errMsg:   "root:mysql_secret@tcp(127.0.0.1:3306)/mydb: connection refused",
			password: "mysql_secret",
			leaks:    []string{"mysql_secret"},
		},
		{
			name:     "PostgreSQL host format",
			errMsg:   "failed to connect to user:pg_secret@pghost:5432/db: timeout",
			password: "pg_secret",
			leaks:    []string{"pg_secret"},
		},
		{
			name:     "DSN with @ in password, explicit password provided",
			errMsg:   `admin:p@ss@tcp(host:3306): denied for admin:p@ss`,
			password: "p@ss",
			leaks:    []string{"p@ss"},
		},
		{
			name:     "Unix socket format",
			errMsg:   "deploy:socketpass@unix(/var/run/mysql.sock)/db: error",
			password: "socketpass",
			leaks:    []string{"socketpass"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := sanitizeDBError(tc.errMsg, tc.password)
			for _, leak := range tc.leaks {
				if strings.Contains(got, leak) {
					t.Errorf("sanitizeDBError leaked %q in: %s", leak, got)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// 4. Connect error messages don't contain credentials
// ---------------------------------------------------------------------------

func TestSecrets_Connect_ErrorOmitsPassword(t *testing.T) {
	password := "ConnectS3cretP@ss!"
	// Use localhost with a port that should be refused immediately (not a
	// blackhole IP like 192.0.2.1 which would cause a TCP timeout).
	_, err := Connect("127.0.0.1", 1, "testdb", "admin", password)
	if err == nil {
		t.Fatal("expected Connect to fail with unreachable host")
	}
	errStr := err.Error()
	if strings.Contains(errStr, password) {
		t.Errorf("Connect error contains password: %s", errStr)
	}
	// Also verify the user:pass@ DSN pattern was scrubbed.
	if strings.Contains(errStr, "admin:"+password) {
		t.Errorf("Connect error contains user:password DSN fragment: %s", errStr)
	}
}

// ---------------------------------------------------------------------------
// 5. ConnectAdmin error messages don't contain credentials
// ---------------------------------------------------------------------------

func TestSecrets_ConnectAdmin_ErrorOmitsPassword(t *testing.T) {
	password := "AdminS3cretP@ss!"
	_, err := ConnectAdmin("127.0.0.1", 1, "admin", password)
	if err == nil {
		t.Fatal("expected ConnectAdmin to fail with unreachable host")
	}
	errStr := err.Error()
	if strings.Contains(errStr, password) {
		t.Errorf("ConnectAdmin error contains password: %s", errStr)
	}
	if strings.Contains(errStr, "admin:"+password) {
		t.Errorf("ConnectAdmin error contains user:password DSN fragment: %s", errStr)
	}
}
