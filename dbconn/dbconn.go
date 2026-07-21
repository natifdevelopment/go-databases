package dbconn

import (
	"fmt"
	"log"
	"os"
	"time"

	"gorm.io/gorm/logger"
)

// Config holds the PostgreSQL connection parameters shared across services.
// It is intentionally dependency-free (no service-specific configs package)
// so any service can reuse it without pulling in heavy model packages.
type Config struct {
	Host     string
	Port     int
	User     string
	Password string
	DbName   string
}

// BuildDSN builds a standard PostgreSQL DSN string (sslmode=disable) from a
// Config. This is the single source of truth for the DSN format used by all
// BBO services.
func BuildDSN(c Config) string {
	return fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=disable",
		c.Host, c.Port, c.User, c.Password, c.DbName,
	)
}

// NewLogger returns the standard gorm logger configuration used across all
// BBO services (slow threshold 1200ms, Error level, ignore record-not-found,
// colorful output to stdout).
func NewLogger() logger.Interface {
	return logger.New(
		log.New(os.Stdout, "\r\n", log.LstdFlags),
		logger.Config{
			SlowThreshold:             1200 * time.Millisecond,
			LogLevel:                  logger.Error,
			IgnoreRecordNotFoundError: true,
			Colorful:                  true,
		},
	)
}
