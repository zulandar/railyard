package db

import (
	"fmt"

	"gorm.io/driver/mysql"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// DSN builds a MySQL-compatible DSN for connecting to a Dolt database.
func DSN(host string, port int, database string) string {
	return fmt.Sprintf("root@tcp(%s:%d)/%s?parseTime=true", host, port, database)
}

// Connect opens a GORM connection to a Dolt database.
func Connect(host string, port int, database string) (*gorm.DB, error) {
	dsn := DSN(host, port, database)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("db: connect to %s:%d/%s: %w", host, port, database, err)
	}
	return db, nil
}

// ConnectAdmin opens a GORM connection to the Dolt server without selecting
// a specific database, used for CREATE DATABASE operations.
func ConnectAdmin(host string, port int) (*gorm.DB, error) {
	dsn := fmt.Sprintf("root@tcp(%s:%d)/?parseTime=true", host, port)
	db, err := gorm.Open(mysql.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Silent),
	})
	if err != nil {
		return nil, fmt.Errorf("db: admin connect to %s:%d: %w", host, port, err)
	}
	return db, nil
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
