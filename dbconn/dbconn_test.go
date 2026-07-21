package dbconn

import (
	"testing"

	gormlogger "gorm.io/gorm/logger"

	"github.com/stretchr/testify/assert"
)

func TestBuildDSN(t *testing.T) {
	c := Config{Host: "localhost", Port: 5432, User: "postgres", Password: "secret", DbName: "bbov2"}
	got := BuildDSN(c)
	want := "host=localhost port=5432 user=postgres password=secret dbname=bbov2 sslmode=disable"
	assert.Equal(t, want, got)
}

func TestNewLogger(t *testing.T) {
	l := NewLogger()
	assert.NotNil(t, l)
}

// Ensure the returned logger satisfies gorm's logger.Interface at compile time.
var _ gormlogger.Interface = NewLogger()
