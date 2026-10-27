// Package repositories is the one WRITE path of the portal (doc/08 section 2.10): the call of the SECURITY
// DEFINER function forms_submit_form. The portal role has no INSERT privilege on any table; EXECUTE on this
// function is all it gets. The call goes through db.Raw, never through a GORM model.
package repositories

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"
)

// SQLSTATEs the function raises (doc/03, 009_views_functions.sql).
const (
	sqlStateFormUnavailable = "P0002" // the form is not published (or does not exist)
	sqlStateBadPayload      = "22023" // payload is not an object, or is over max_payload_bytes
)

// Errors of Submit. They carry no data of the submission.
var (
	// ErrFormUnavailable: the form is not published (P0002).
	ErrFormUnavailable = errors.New("forms: form is not available")
	// ErrInvalidPayload: the function refused the payload (22023).
	ErrInvalidPayload = errors.New("forms: payload refused")
)

// Submission is the argument list of forms_submit_form.
type Submission struct {
	FormSlug       string
	Locale         string
	DataJSON       []byte // a JSON object
	IPHash         []byte // HMAC-SHA256(ip, salt of the day); never the address
	UserAgent      string // "" = NULL
	SpamScore      int16  // >= 80 is stored with status 'spam'
	SourcePath     string // "" = NULL
	ConsentAt      *time.Time
	ConsentVersion string // "" = NULL
}

// SubmissionRepository calls forms_submit_form.
type SubmissionRepository interface {
	// Submit stores the submission and returns its public_id.
	Submit(ctx context.Context, s Submission) (string, error)
}

// New builds the repository over the read-only pool (EXECUTE on the function is granted to cms_portal_ro).
func New(db *gorm.DB) SubmissionRepository { return &submitRepo{db} }

type submitRepo struct{ db *gorm.DB }

const callSQL = `SELECT forms_submit_form(?::text, ?::text, ?::jsonb, ?::bytea, ?::text, ?::smallint, ?::text, ?::timestamptz, ?::text)`

func (r *submitRepo) Submit(ctx context.Context, s Submission) (string, error) {
	var id sql.NullString
	err := r.db.WithContext(ctx).Raw(callSQL,
		s.FormSlug, nullIfEmpty(s.Locale), string(s.DataJSON), s.IPHash, nullIfEmpty(s.UserAgent), s.SpamScore,
		nullIfEmpty(s.SourcePath), s.ConsentAt, nullIfEmpty(s.ConsentVersion),
	).Row().Scan(&id)
	if err != nil {
		return "", classify(err)
	}
	if !id.Valid {
		return "", errors.New("forms_submit_form returned no id")
	}
	return id.String, nil
}

func nullIfEmpty(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// classify maps a SQLSTATE to a sentinel; any other error is reduced to its SQLSTATE so that nothing of the
// submission (which pgx errors may quote) reaches a log.
func classify(err error) error {
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		switch pe.Code {
		case sqlStateFormUnavailable:
			return ErrFormUnavailable
		case sqlStateBadPayload:
			return ErrInvalidPayload
		}
		return fmt.Errorf("forms_submit_form failed (sqlstate %s)", pe.Code)
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		return err
	}
	return errors.New("forms_submit_form failed")
}
