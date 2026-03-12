package db

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"os"
	"regexp"
	"strings"

	gomysql "github.com/go-sql-driver/mysql"
	"github.com/zulandar/railyard/internal/config"
	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// openDB opens a GORM connection for the given DSN. It is a package-level
// variable so tests can substitute a stub without requiring a live database.
var openDB = func(dsn string) (*gorm.DB, error) {
	return gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
}

// DSN builds a MySQL-compatible DSN for connecting to the database.
func DSN(host string, port int, database, username, password string) string {
	creds := username
	if password != "" {
		creds = username + ":" + password
	}
	return fmt.Sprintf("%s@tcp(%s:%d)/%s?parseTime=true", creds, host, port, database)
}

// Connect opens a GORM connection to the database.
func Connect(host string, port int, database, username, password string) (*gorm.DB, error) {
	dsn := DSN(host, port, database, username, password)
	db, err := openDB(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: connect to %s:%d/%s: %s", host, port, database, sanitizeDBError(err.Error(), password))
	}
	return db, nil
}

// ConnectAdmin opens a GORM connection to the database server without selecting
// a specific database, used for CREATE DATABASE operations.
func ConnectAdmin(host string, port int, username, password string) (*gorm.DB, error) {
	creds := username
	if password != "" {
		creds = username + ":" + password
	}
	dsn := fmt.Sprintf("%s@tcp(%s:%d)/?parseTime=true", creds, host, port)
	db, err := openDB(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: admin connect to %s:%d: %s", host, port, sanitizeDBError(err.Error(), password))
	}
	return db, nil
}

// sanitizeDBError removes credentials from database error messages to prevent
// password leakage in logs and CLI output.
func sanitizeDBError(errMsg, password string) string {
	if password != "" {
		errMsg = strings.ReplaceAll(errMsg, password, "***")
	}
	// Strip user:pass@host patterns that drivers may include in DSN fragments.
	errMsg = dsnCredentialPattern.ReplaceAllString(errMsg, "$1***@")
	return errMsg
}

// dsnCredentialPattern matches user:password@ in DSN-style strings.
var dsnCredentialPattern = regexp.MustCompile(`(\w+):([^@]+)@`)

// RegisterTLS registers a custom TLS configuration with the MySQL driver.
// If cfg.Enabled is false, this is a no-op.
func RegisterTLS(cfg config.TLSConfig) error {
	if !cfg.Enabled {
		return nil
	}

	tlsCfg := &tls.Config{
		InsecureSkipVerify: cfg.SkipVerify,
	}

	if cfg.CACert != "" {
		caPEM, err := os.ReadFile(cfg.CACert)
		if err != nil {
			return fmt.Errorf("db: read ca_cert %s: %w", cfg.CACert, err)
		}
		caPool := x509.NewCertPool()
		if !caPool.AppendCertsFromPEM(caPEM) {
			return fmt.Errorf("db: ca_cert %s: no valid certificates found", cfg.CACert)
		}
		tlsCfg.RootCAs = caPool
	}

	if cfg.ClientCert != "" && cfg.ClientKey != "" {
		cert, err := tls.LoadX509KeyPair(cfg.ClientCert, cfg.ClientKey)
		if err != nil {
			return fmt.Errorf("db: load client cert/key: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}

	return gomysql.RegisterTLSConfig("custom", tlsCfg)
}

// DSNFromConfig builds a MySQL-compatible DSN from a DatabaseConfig.
// When TLS is enabled, it appends tls=custom.
func DSNFromConfig(cfg config.DatabaseConfig) string {
	dsn := DSN(cfg.Host, cfg.Port, cfg.Database, cfg.Username, cfg.Password)
	if cfg.TLS.Enabled {
		dsn += "&tls=custom"
	}
	return dsn
}

// ConnectWithConfig opens a GORM connection using a DatabaseConfig.
// When TLS is enabled, RegisterTLS is called to register the "custom" TLS
// profile with the MySQL driver before opening the connection.
func ConnectWithConfig(cfg config.DatabaseConfig) (*gorm.DB, error) {
	if err := RegisterTLS(cfg.TLS); err != nil {
		return nil, fmt.Errorf("db: register TLS config: %w", err)
	}
	dsn := DSNFromConfig(cfg)
	db, err := openDB(dsn)
	if err != nil {
		return nil, fmt.Errorf("db: connect to %s:%d/%s: %s", cfg.Host, cfg.Port, cfg.Database, sanitizeDBError(err.Error(), cfg.Password))
	}
	return db, nil
}

// validDBName matches safe database identifier names (alphanumeric, underscore, hyphen).
var validDBName = regexp.MustCompile(`^[a-zA-Z0-9_\-]+$`)

// DropDatabase drops the named database if it exists.
func DropDatabase(adminDB *gorm.DB, name string) error {
	if !validDBName.MatchString(name) {
		return fmt.Errorf("db: invalid database name: %q", name)
	}
	sql := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name)
	if err := adminDB.Exec(sql).Error; err != nil {
		return fmt.Errorf("db: drop database %s: %w", name, err)
	}
	return nil
}

// CreateDatabase creates the named database if it doesn't already exist.
func CreateDatabase(adminDB *gorm.DB, name string) error {
	if !validDBName.MatchString(name) {
		return fmt.Errorf("db: invalid database name: %q", name)
	}
	sql := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`", name)
	if err := adminDB.Exec(sql).Error; err != nil {
		return fmt.Errorf("db: create database %s: %w", name, err)
	}
	return nil
}
