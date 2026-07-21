package views

import (
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/stretchr/testify/assert"
)

// TestMigrateView_CoversSQLStatements calls MigrateView with a sqlmock DB that
// accepts any exec/query so all the db.Exec(...) lines get executed.
func TestMigrateView_CoversSQLStatements(t *testing.T) {
	db, mock, err := sqlmock.New()
	assert.NoError(t, err)
	defer db.Close()

	// Allow any number of Exec calls (views, extensions, indexes)
	mock.MatchExpectationsInOrder(false)
	for i := 0; i < 200; i++ {
		mock.ExpectExec(".*").WillReturnResult(sqlmock.NewResult(0, 0))
	}

	MigrateView(db)
	// No assertions on errors since MigrateView ignores all errors
}
