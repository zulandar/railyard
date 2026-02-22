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
			got := DSN(tt.host, tt.port, tt.database)
			if got != tt.want {
				t.Errorf("DSN() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDSN_ParseTimeFlag(t *testing.T) {
	dsn := DSN("localhost", 3306, "test")
	if !strings.Contains(dsn, "parseTime=true") {
		t.Errorf("DSN missing parseTime=true: %s", dsn)
	}
}

func TestDSN_Format(t *testing.T) {
	dsn := DSN("myhost", 9999, "mydb")
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
	var fn func(string, int, string) (*gorm.DB, error) = Connect
	if fn == nil {
		t.Fatal("Connect function is nil")
	}
}

func TestConnectAdmin_RequiresDolt(t *testing.T) {
	var fn func(string, int) (*gorm.DB, error) = ConnectAdmin
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
	if len(models) != 12 {
		t.Errorf("AllModels() returned %d models, want 12", len(models))
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
	_, err := Connect("127.0.0.1", 1, "nonexistent")
	if err == nil {
		t.Fatal("expected error connecting to invalid port")
	}
	if !strings.Contains(err.Error(), "db: connect to") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db: connect to")
	}
}

func TestConnectAdmin_Error(t *testing.T) {
	_, err := ConnectAdmin("127.0.0.1", 1)
	if err == nil {
		t.Fatal("expected error connecting to invalid port")
	}
	if !strings.Contains(err.Error(), "db: admin connect to") {
		t.Errorf("error = %q, want to contain %q", err.Error(), "db: admin connect to")
	}
}

func TestMarshalJSON_Error(t *testing.T) {
	// Channels cannot be marshaled to JSON.
	_, err := marshalJSON(make(chan int))
	if err == nil {
		t.Fatal("expected error marshaling channel")
	}
}
