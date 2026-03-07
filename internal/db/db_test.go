package db

import (
	"strings"
	"testing"

	"github.com/zulandar/railyard/internal/config"
	"gorm.io/gorm"
)

func TestDSN(t *testing.T) {
	tests := []struct {
		name     string
		host     string
		port     int
		database string
		want     string
	}{
		{
			name:     "default local",
			host:     "127.0.0.1",
			port:     3306,
			database: "railyard_alice",
			want:     "root@tcp(127.0.0.1:3306)/railyard_alice?parseTime=true",
		},
		{
			name:     "custom host and port",
			host:     "10.0.0.5",
			port:     3307,
			database: "railyard_bob",
			want:     "root@tcp(10.0.0.5:3307)/railyard_bob?parseTime=true",
		},
		{
			name:     "production host",
			host:     "dolt-server.vpc.internal",
			port:     3306,
			database: "railyard_carol",
			want:     "root@tcp(dolt-server.vpc.internal:3306)/railyard_carol?parseTime=true",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := DSN(tt.host, tt.port, tt.database, "root", "")
			if got != tt.want {
				t.Errorf("DSN() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDSN_WithPassword(t *testing.T) {
	got := DSN("127.0.0.1", 3306, "mydb", "admin", "secret")
	want := "admin:secret@tcp(127.0.0.1:3306)/mydb?parseTime=true"
	if got != want {
		t.Errorf("DSN() = %q, want %q", got, want)
	}
}

func TestDSN_CustomUsername(t *testing.T) {
	got := DSN("127.0.0.1", 3306, "mydb", "deploy", "")
	want := "deploy@tcp(127.0.0.1:3306)/mydb?parseTime=true"
	if got != want {
		t.Errorf("DSN() = %q, want %q", got, want)
	}
}

func TestDSN_ParseTimeFlag(t *testing.T) {
	dsn := DSN("localhost", 3306, "test", "root", "")
	if !strings.Contains(dsn, "parseTime=true") {
		t.Errorf("DSN missing parseTime=true: %s", dsn)
	}
}

func TestDSN_Format(t *testing.T) {
	dsn := DSN("myhost", 9999, "mydb", "root", "")
	if !strings.HasPrefix(dsn, "root@tcp(") {
		t.Errorf("DSN should start with root@tcp(: %s", dsn)
	}
	if !strings.Contains(dsn, "myhost:9999") {
		t.Errorf("DSN should contain host:port: %s", dsn)
	}
	if !strings.Contains(dsn, "/mydb?") {
		t.Errorf("DSN should contain /database?: %s", dsn)
	}
}

func TestConnect_RequiresDolt(t *testing.T) {
	// Connect requires a running Dolt server; verify the function signature
	// compiles and returns (*gorm.DB, error). Full integration tests are in
	// the Foundation Test Suite (d88.5).
	var fn func(string, int, string, string, string) (*gorm.DB, error) = Connect
	if fn == nil {
		t.Fatal("Connect function is nil")
	}
}

func TestConnectAdmin_RequiresDolt(t *testing.T) {
	var fn func(string, int, string, string) (*gorm.DB, error) = ConnectAdmin
	if fn == nil {
		t.Fatal("ConnectAdmin function is nil")
	}
}

func TestCreateDatabase_RequiresDolt(t *testing.T) {
	var fn func(*gorm.DB, string) error = CreateDatabase
	if fn == nil {
		t.Fatal("CreateDatabase function is nil")
	}
}

func TestAllModels_Count(t *testing.T) {
	models := AllModels()
	if len(models) != 13 {
		t.Errorf("AllModels() returned %d models, want 13", len(models))
	}
}

func TestMarshalJSON(t *testing.T) {
	tests := []struct {
		name    string
		input   interface{}
		want    string
		wantErr bool
	}{
		{
			name:  "nil returns empty",
			input: nil,
			want:  "",
		},
		{
			name:  "string slice",
			input: []string{"cmd/**", "internal/**"},
			want:  `["cmd/**","internal/**"]`,
		},
		{
			name:  "map",
			input: map[string]interface{}{"go_version": "1.22"},
			want:  `{"go_version":"1.22"}`,
		},
		{
			name:  "empty slice",
			input: []string{},
			want:  `[]`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := marshalJSON(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("marshalJSON() error = %v, wantErr %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("marshalJSON() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestSeedTracks_EmptySlice(t *testing.T) {
	// SeedTracks with empty slice should be a no-op (no error, no DB call).
	// We can't fully test without a DB, but we verify the function handles
	// an empty input without panicking.
	err := SeedTracks(nil, []config.TrackConfig{})
	if err != nil {
		t.Errorf("SeedTracks(nil, []) = %v, want nil", err)
	}
}

func TestConnect_Error(t *testing.T) {
	// Port 1 is unlikely to have a MySQL server; expect connection error.
	_, err := Connect("127.0.0.1", 1, "nonexistent", "root", "")
	if err == nil {
		t.Fatal("expected error connecting to invalid port")
	}
	if !strings.Contains(err.Error(), "db: connect to") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db: connect to")
	}
}

func TestConnectAdmin_Error(t *testing.T) {
	_, err := ConnectAdmin("127.0.0.1", 1, "root", "")
	if err == nil {
		t.Fatal("expected error connecting to invalid port")
	}
	if !strings.Contains(err.Error(), "db: admin connect to") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db: admin connect to")
	}
}

func TestConnect_ErrorDoesNotLeakPassword(t *testing.T) {
	password := "s3cret-P@ssw0rd!"
	_, err := Connect("127.0.0.1", 1, "testdb", "admin", password)
	if err == nil {
		t.Fatal("expected error connecting to invalid port")
	}
	if strings.Contains(err.Error(), password) {
		t.Errorf("Connect error leaks password: %s", err.Error())
	}
}

func TestConnectAdmin_ErrorDoesNotLeakPassword(t *testing.T) {
	password := "s3cret-P@ssw0rd!"
	_, err := ConnectAdmin("127.0.0.1", 1, "admin", password)
	if err == nil {
		t.Fatal("expected error connecting to invalid port")
	}
	if strings.Contains(err.Error(), password) {
		t.Errorf("ConnectAdmin error leaks password: %s", err.Error())
	}
}

func TestSanitizeDBError(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		password string
		wantSafe bool // true if password should NOT appear in output
	}{
		{
			name:     "DSN in error",
			input:    "dial tcp: admin:s3cret@tcp(127.0.0.1:3306)/mydb connection refused",
			password: "s3cret",
			wantSafe: true,
		},
		{
			name:     "no password",
			input:    "dial tcp: connection refused",
			password: "",
			wantSafe: true,
		},
		{
			name:     "password in wrapped error",
			input:    "db: connect: Error 1045: Access denied for user 'admin'@'localhost' (using password: YES) admin:hunter2@tcp(host:3306)/db",
			password: "hunter2",
			wantSafe: true,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := sanitizeDBError(tt.input, tt.password)
			if tt.wantSafe && tt.password != "" && strings.Contains(got, tt.password) {
				t.Errorf("sanitizeDBError() still contains password %q: %s", tt.password, got)
			}
		})
	}
}

func TestDSNFromConfig_NoTLS(t *testing.T) {
	cfg := config.DoltConfig{
		Host:     "127.0.0.1",
		Port:     3306,
		Database: "railyard_alice",
		Username: "root",
	}
	got := DSNFromConfig(cfg)
	want := "root@tcp(127.0.0.1:3306)/railyard_alice?parseTime=true"
	if got != want {
		t.Errorf("DSNFromConfig() = %q, want %q", got, want)
	}
}

func TestDSNFromConfig_WithPassword(t *testing.T) {
	cfg := config.DoltConfig{
		Host:     "127.0.0.1",
		Port:     3306,
		Database: "mydb",
		Username: "admin",
		Password: "secret",
	}
	got := DSNFromConfig(cfg)
	want := "admin:secret@tcp(127.0.0.1:3306)/mydb?parseTime=true"
	if got != want {
		t.Errorf("DSNFromConfig() = %q, want %q", got, want)
	}
}

func TestDSNFromConfig_WithTLS(t *testing.T) {
	cfg := config.DoltConfig{
		Host:     "dolt.k8s.internal",
		Port:     3306,
		Database: "railyard_prod",
		Username: "root",
		TLS: config.TLSConfig{
			Enabled: true,
		},
	}
	got := DSNFromConfig(cfg)
	if !strings.Contains(got, "tls=custom") {
		t.Errorf("DSNFromConfig() = %q, want tls=custom parameter", got)
	}
	if !strings.Contains(got, "parseTime=true") {
		t.Errorf("DSNFromConfig() = %q, want parseTime=true", got)
	}
}

func TestDSNFromConfig_TLSDisabled_NoTLSParam(t *testing.T) {
	cfg := config.DoltConfig{
		Host:     "127.0.0.1",
		Port:     3306,
		Database: "mydb",
		Username: "root",
		TLS: config.TLSConfig{
			Enabled: false,
		},
	}
	got := DSNFromConfig(cfg)
	if strings.Contains(got, "tls=") {
		t.Errorf("DSNFromConfig() = %q, should not contain tls= when disabled", got)
	}
}

func TestRegisterTLS_SkipVerify(t *testing.T) {
	tlsCfg := config.TLSConfig{
		Enabled:    true,
		SkipVerify: true,
	}
	err := RegisterTLS(tlsCfg)
	if err != nil {
		t.Fatalf("RegisterTLS() error = %v", err)
	}
}

func TestRegisterTLS_NotEnabled(t *testing.T) {
	tlsCfg := config.TLSConfig{Enabled: false}
	err := RegisterTLS(tlsCfg)
	if err != nil {
		t.Fatalf("RegisterTLS() error = %v, want nil for disabled TLS", err)
	}
}

func TestRegisterTLS_BadCACert(t *testing.T) {
	tlsCfg := config.TLSConfig{
		Enabled: true,
		CACert:  "/nonexistent/ca.pem",
	}
	err := RegisterTLS(tlsCfg)
	if err == nil {
		t.Fatal("RegisterTLS() expected error for nonexistent CA cert")
	}
	if !strings.Contains(err.Error(), "ca_cert") {
		t.Errorf("error = %q, want to mention ca_cert", err.Error())
	}
}

func TestRegisterTLS_BadClientCert(t *testing.T) {
	tlsCfg := config.TLSConfig{
		Enabled:    true,
		SkipVerify: true,
		ClientCert: "/nonexistent/cert.pem",
		ClientKey:  "/nonexistent/key.pem",
	}
	err := RegisterTLS(tlsCfg)
	if err == nil {
		t.Fatal("RegisterTLS() expected error for nonexistent client cert")
	}
	if !strings.Contains(err.Error(), "client cert") {
		t.Errorf("error = %q, want to mention client cert", err.Error())
	}
}

func TestConnectWithConfig_RequiresDolt(t *testing.T) {
	var fn func(config.DoltConfig) (*gorm.DB, error) = ConnectWithConfig
	if fn == nil {
		t.Fatal("ConnectWithConfig function is nil")
	}
}

func TestConnectWithConfig_Error(t *testing.T) {
	cfg := config.DoltConfig{
		Host:     "127.0.0.1",
		Port:     1,
		Database: "nonexistent",
		Username: "root",
	}
	_, err := ConnectWithConfig(cfg)
	if err == nil {
		t.Fatal("expected error connecting to invalid port")
	}
	if !strings.Contains(err.Error(), "db: connect to") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db: connect to")
	}
}

func TestConnectWithConfig_TLSEnabled_RegistersBeforeOpen(t *testing.T) {
	// With TLS enabled and SkipVerify, RegisterTLS should succeed.
	// The connection will fail (no server) but the error should be a
	// connection error, NOT "invalid value / unknown config name: custom".
	cfg := config.DoltConfig{
		Host:     "127.0.0.1",
		Port:     1,
		Database: "testdb",
		Username: "root",
		TLS: config.TLSConfig{
			Enabled:    true,
			SkipVerify: true,
		},
	}
	_, err := ConnectWithConfig(cfg)
	if err == nil {
		t.Fatal("expected error connecting to invalid port")
	}
	// Should be a connection error, not a TLS registration error.
	if strings.Contains(err.Error(), "unknown") || strings.Contains(err.Error(), "invalid") {
		t.Errorf("TLS not registered before open: %s", err.Error())
	}
	if !strings.Contains(err.Error(), "db: connect to") {
		t.Errorf("error = %q, want connection error containing %q", err.Error(), "db: connect to")
	}
}

func TestConnectWithConfig_TLSBadCACert_ReturnsRegisterError(t *testing.T) {
	cfg := config.DoltConfig{
		Host:     "127.0.0.1",
		Port:     3306,
		Database: "testdb",
		Username: "root",
		TLS: config.TLSConfig{
			Enabled: true,
			CACert:  "/nonexistent/ca.pem",
		},
	}
	_, err := ConnectWithConfig(cfg)
	if err == nil {
		t.Fatal("expected TLS registration error")
	}
	if !strings.Contains(err.Error(), "register TLS") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "register TLS")
	}
}

func TestMarshalJSON_Error(t *testing.T) {
	// Channels cannot be marshaled to JSON.
	_, err := marshalJSON(make(chan int))
	if err == nil {
		t.Fatal("expected error marshaling channel")
	}
}

// TestDatabaseNameValidation verifies that DropDatabase and CreateDatabase
// reject names containing backticks or other injection characters.
func TestDatabaseNameValidation(t *testing.T) {
	// Only test invalid names (valid names need a real DB connection).
	invalidNames := []struct {
		name   string
		dbName string
	}{
		{"backtick injection", "test`; DROP TABLE cars; --"},
		{"semicolon", "test; DROP TABLE"},
		{"space", "test db"},
		{"empty", ""},
		{"dot", "test.db"},
		{"slash", "test/db"},
		{"quotes", `test"db`},
	}
	for _, tt := range invalidNames {
		t.Run("Drop_"+tt.name, func(t *testing.T) {
			err := DropDatabase(nil, tt.dbName)
			if err == nil || !strings.Contains(err.Error(), "invalid database name") {
				t.Errorf("DropDatabase(%q) should reject invalid name, got: %v", tt.dbName, err)
			}
		})
		t.Run("Create_"+tt.name, func(t *testing.T) {
			err := CreateDatabase(nil, tt.dbName)
			if err == nil || !strings.Contains(err.Error(), "invalid database name") {
				t.Errorf("CreateDatabase(%q) should reject invalid name, got: %v", tt.dbName, err)
			}
		})
	}

	// Verify valid patterns are accepted by the regex directly.
	validNames := []string{"railyard", "railyard_test", "railyard-test", "db123"}
	for _, name := range validNames {
		if !validDBName.MatchString(name) {
			t.Errorf("validDBName should accept %q", name)
		}
	}
}
