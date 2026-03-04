package db

import (
	"fmt"
	"strings"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DSN builds a MySQL-compatible DSN for connecting to a Dolt database.
func DSN(host string, port int, database, username, password string) string {
	creds := username
	if password != "" {
		creds = username + ":" + password
	}
	return fmt.Sprintf("%s@tcp(%s:%d)/%s?parseTime=true", creds, host, port, database)
}

// Connect opens a GORM connection to a Dolt database.
func Connect(host string, port int, database, username, password string) (*gorm.DB, error) {
	dsn := DSN(host, port, database, username, password)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("db: connect to %s:%d/%s: %s", host, port, database, sanitizeDBError(err.Error(), password))
	}
	return db, nil
}

// ConnectAdmin opens a GORM connection to the Dolt server without selecting
// a specific database, used for CREATE DATABASE operations.
func ConnectAdmin(host string, port int, username, password string) (*gorm.DB, error) {
	creds := username
	if password != "" {
		creds = username + ":" + password
	}
	dsn := fmt.Sprintf("%s@tcp(%s:%d)/?parseTime=true", creds, host, port)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
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
	return errMsg
}

// DropDatabase drops the named database if it exists.
func DropDatabase(adminDB *gorm.DB, name string) error {
	sql := fmt.Sprintf("DROP DATABASE IF EXISTS `%s`", name)
	if err := adminDB.Exec(sql).Error; err != nil {
		return fmt.Errorf("db: drop database %s: %w", name, err)
	}
	return nil
}

// CreateDatabase creates the named database if it doesn't already exist.
func CreateDatabase(adminDB *gorm.DB, name string) error {
	sql := fmt.Sprintf("CREATE DATABASE IF NOT EXISTS `%s`", name)
	if err := adminDB.Exec(sql).Error; err != nil {
		return fmt.Errorf("db: create database %s: %w", name, err)
	}
	return nil
}
