package databases

import (
	"fmt"
	"time"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
	"gorm.io/plugin/dbresolver"
)

// PostgreConfig holds the configuration for a PostgreSQL connection.
type PostgreConfig struct {
	Host     string
	Port     int
	User     string
	Password string
	DbName   string
	SSLMode  string
	Slave    *PostgreConfig
}

// PostgreConn is the global GORM DB connection. Services should call
// SetupDatabase() during initialization to populate this.
var PostgreConn *gorm.DB

// MigrationModels is the list of models to auto-migrate.
// Services should populate this before calling SetupDatabase().
var MigrationModels []interface{}

// SetupDatabase connects to PostgreSQL and runs auto-migration.
func SetupDatabase(config PostgreConfig) error {
	conn, err := PostgreConnection(config)
	if err != nil {
		return err
	}
	PostgreConn = conn

	if len(MigrationModels) > 0 {
		if err := runMigrations(conn); err != nil {
			return fmt.Errorf("databases: migration failed: %w", err)
		}
	}

	return nil
}

// PostgreConnection creates and returns a GORM DB connection.
func PostgreConnection(config PostgreConfig) (*gorm.DB, error) {
	dsn := fmt.Sprintf(
		"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
		config.Host, config.Port, config.User, config.Password, config.DbName, config.SSLMode,
	)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{
		Logger: logger.Default.LogMode(logger.Warn),
	})
	if err != nil {
		return nil, fmt.Errorf("databases: failed to connect to PostgreSQL: %w", err)
	}

	sqlDB, err := db.DB()
	if err != nil {
		return nil, fmt.Errorf("databases: failed to get underlying sql.DB: %w", err)
	}

	sqlDB.SetMaxIdleConns(10)
	sqlDB.SetMaxOpenConns(100)
	sqlDB.SetConnMaxLifetime(time.Hour)

	if config.Slave != nil && config.Slave.Host != "" {
		slaveDSN := fmt.Sprintf(
			"host=%s port=%d user=%s password=%s dbname=%s sslmode=%s",
			config.Slave.Host, config.Slave.Port, config.Slave.User, config.Slave.Password, config.Slave.DbName, config.Slave.SSLMode,
		)
		if err := db.Use(dbresolver.Register(dbresolver.Config{
			Replicas: []gorm.Dialector{postgres.Open(slaveDSN)},
			policy:   dbresolver.RandomPolicy{},
		}).SetMaxOpenConns(100).SetMaxIdleConns(10).SetConnMaxLifetime(time.Hour)); err != nil {
			return nil, fmt.Errorf("databases: failed to register slave replica: %w", err)
		}
	}

	return db, nil
}

func runMigrations(db *gorm.DB) error {
	if err := db.AutoMigrate(MigrationModels...); err != nil {
		return fmt.Errorf("databases: auto-migrate failed: %w", err)
	}
	return nil
}
