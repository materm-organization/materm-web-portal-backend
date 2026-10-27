// Package repositories reads the public views of the CMS database (doc/08 section 2.5): one small repository
// per area, each returning entities of modules/content/entities and nothing else. The portal role can only
// SELECT these views, so every method is a read. All methods take a locale that the caller has already
// normalised (pkg/localize): the views only hold ACTIVE locales, so an inactive code would give zero rows and
// look like a 404.
package repositories

import (
	"errors"
	"fmt"
	"strings"
	"sync"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"
)

// ErrNotFound is returned by the single-row finders when no row matches.
var ErrNotFound = errors.New("content: not found")

// maxIDs bounds an IN list (a page has far fewer references).
const maxIDs = 1000

var schemaCache sync.Map

// Columns returns the database column names of an entity, in field order. It is how the repositories build
// an explicit SELECT list (no SELECT *: the post body must not travel in a list), and what the drift test
// compares with information_schema.
func Columns(entity any) ([]string, error) {
	s, err := schema.Parse(entity, &schemaCache, schema.NamingStrategy{})
	if err != nil {
		return nil, fmt.Errorf("content: parse %T: %w", entity, err)
	}
	cols := make([]string, 0, len(s.DBNames))
	cols = append(cols, s.DBNames...)
	return cols, nil
}

// TableOf returns the view an entity reads.
func TableOf(entity any) (string, error) {
	s, err := schema.Parse(entity, &schemaCache, schema.NamingStrategy{})
	if err != nil {
		return "", err
	}
	return s.Table, nil
}

// sel starts a query on the view of T with an explicit column list.
func sel[T any](db *gorm.DB) *gorm.DB {
	var zero T
	cols, err := Columns(&zero)
	if err != nil {
		// a programming error in an entity tag; surface it as a failed query rather than a panic
		_ = db.AddError(err)
		return db
	}
	quoted := make([]string, len(cols))
	for i, c := range cols {
		quoted[i] = `"` + c + `"`
	}
	return db.Model(&zero).Select(strings.Join(quoted, ", "))
}

// first runs q with LIMIT 1 and returns the row or ErrNotFound. (First() would add ORDER BY on a view
// without a primary key.)
func first[T any](q *gorm.DB) (*T, error) {
	var rows []T
	if err := q.Limit(1).Find(&rows).Error; err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, ErrNotFound
	}
	return &rows[0], nil
}

func list[T any](q *gorm.DB) ([]T, error) {
	rows := []T{}
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return rows, nil
}

func checkIDs(ids []int64) error {
	if len(ids) > maxIDs {
		return fmt.Errorf("content: %d ids in one query (max %d)", len(ids), maxIDs)
	}
	return nil
}
