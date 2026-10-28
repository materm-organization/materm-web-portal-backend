// Package repositories is the portal's only access to preview tokens: the SECURITY DEFINER function
// cms_consume_preview_token (EXECUTE granted to cms_portal_ro). The call goes through db.Raw like
// forms_submit_form; the portal role has no privilege on cms_preview_tokens or cms_content_revisions.
package repositories

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// ErrInvalid: the token is unknown, expired, revoked or used up (the function returns no row; the cases are
// deliberately indistinguishable).
var ErrInvalid = errors.New("preview: token is not usable")

// Consumed is what the function returns for a usable token.
type Consumed struct {
	EntityType string
	EntityID   int64
	// LocaleCode is the locale the link was issued for (nil when the issuer set none).
	LocaleCode *string
	Snapshot   []byte
}

// TokenRepository calls cms_consume_preview_token.
type TokenRepository interface {
	// Consume counts one use of the token whose sha256 is tokenHash and returns its frozen revision.
	// ErrInvalid when no row comes back.
	Consume(ctx context.Context, tokenHash []byte) (*Consumed, error)
}

// New builds the repository over the read-only pool.
func New(db *gorm.DB) TokenRepository { return &repo{db} }

type repo struct{ db *gorm.DB }

// hashArg makes GORM bind the hash as ONE bytea parameter. A plain []byte right after "(" is taken for the list of
// an IN (?) clause and expanded into 32 parameters (SQLSTATE 42883); a driver.Valuer is never expanded.
type hashArg []byte

func (h hashArg) Value() (driver.Value, error) { return []byte(h), nil }

const callSQL = `SELECT entity_type, entity_id, locale_code, snapshot FROM cms_consume_preview_token(?::bytea)`

func (r *repo) Consume(ctx context.Context, tokenHash []byte) (*Consumed, error) {
	rows, err := r.db.WithContext(ctx).Raw(callSQL, hashArg(tokenHash)).Rows()
	if err != nil {
		return nil, classify(err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return nil, classify(err)
		}
		return nil, ErrInvalid
	}
	var c Consumed
	var loc sql.NullString
	if err := rows.Scan(&c.EntityType, &c.EntityID, &loc, &c.Snapshot); err != nil {
		return nil, errors.New("preview: cms_consume_preview_token returned an unreadable row")
	}
	if loc.Valid {
		c.LocaleCode = &loc.String
	}
	return &c, rows.Err()
}

// classify reduces a database error to its SQLSTATE: the hash is a parameter and pgx errors may quote values.
func classify(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return fmt.Errorf("cms_consume_preview_token failed (sqlstate %s)", pe.Code)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	return errors.New("cms_consume_preview_token failed")
}
