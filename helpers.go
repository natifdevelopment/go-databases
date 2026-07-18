package databases

import (
	"strings"

	"gorm.io/gorm"
)

// GenPreload generates a preloaded GORM query for the given associations.
func GenPreload(query *gorm.DB, preloads []string) *gorm.DB {
	for _, p := range preloads {
		query = query.Preload(p)
	}
	return query
}

// GenPreloadWithUserId generates a preloaded GORM query scoped to a user ID.
func GenPreloadWithUserId(query *gorm.DB, preloads []string, userId string) *gorm.DB {
	for _, p := range preloads {
		if strings.Contains(p, ".") {
			query = query.Preload(p, "user_id = ?", userId)
		} else {
			query = query.Preload(p)
		}
	}
	return query
}
