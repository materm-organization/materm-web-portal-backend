package apperror

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/materm-organization/materm-web-portal-backend/pkg/logs"
	"github.com/materm-organization/materm-web-portal-backend/pkg/requestid"
	"github.com/materm-organization/materm-web-portal-backend/pkg/utilities/validator"
)

// SQLSTATE codes the handler understands.
const (
	sqlUniqueViolation     = "23505"
	sqlForeignKeyViolation = "23503"
	sqlCheckViolation      = "23514"
	sqlSerializationFail   = "40001"
	sqlDeadlock            = "40P01"
	sqlQueryCanceled       = "57014"
)

// ConstraintMap tells the handler which field a database constraint belongs to, so that a duplicate or a
// CHECK violation can be reported against the field the user typed.
type ConstraintMap struct {
	In   string // default "body"
	Path string // JSON Pointer, e.g. "/email"
	Code string // default "duplicate" for unique, "check_violation" for check
}

var (
	constraintsMu sync.RWMutex
	constraints   = map[string]ConstraintMap{}
)

// RegisterConstraint maps a constraint / unique index name (as in migrations/post) to a request field.
// Modules call it from their init or constructor; it is safe for concurrent use.
func RegisterConstraint(name string, m ConstraintMap) {
	constraintsMu.Lock()
	defer constraintsMu.Unlock()
	constraints[name] = m
}

func lookupConstraint(name string) ConstraintMap {
	constraintsMu.RLock()
	defer constraintsMu.RUnlock()
	return constraints[name]
}

// Handler is the Fiber ErrorHandler: it classifies any error a handler returned (or a panic converted by
// the recover middleware) into an *Error, logs server-side failures, and writes the error envelope.
func Handler(c *fiber.Ctx, err error) error {
	ae := Classify(err)
	logFailure(c, ae)
	return Write(c, ae)
}

// Write writes ae as the response (status, Retry-After, Content-Language, body).
func Write(c *fiber.Ctx, ae *Error) error {
	lang := NegotiateLang(c.Get(fiber.HeaderAcceptLanguage))
	rid := requestid.FromFiber(c)
	if rid == "" { // errors raised before the middleware ran (e.g. body too large)
		rid = c.Get(requestid.Header)
		if rid == "" {
			rid = c.GetRespHeader(requestid.Header)
		}
	}
	c.Set(fiber.HeaderContentLanguage, lang)
	if rid != "" {
		c.Set(requestid.Header, rid)
	}
	if ra := retryAfterSeconds(ae.RetryAfter); ra != "" {
		c.Set(fiber.HeaderRetryAfter, ra)
	}
	msg := ae.Message
	if msg == "" {
		msg = Message(ae.Code, lang)
	}
	return c.Status(ae.Status).JSON(Body{
		Success: false, Message: msg, Code: ae.Code,
		Errors: ae.Errors, Details: ae.Details, RequestID: rid,
	})
}

// Classify maps an arbitrary error to an *Error (it never returns nil). Unknown errors become
// INTERNAL_ERROR with the original as Cause so the log has it and the client does not.
func Classify(err error) *Error {
	if err == nil {
		return Internal(errors.New("nil error"))
	}
	if ae, ok := AsError(err); ok {
		return ae
	}
	var fe *fiber.Error
	if errors.As(err, &fe) {
		return fromFiber(fe)
	}
	if issues, ok := validator.Issues(err); ok {
		fields := make([]FieldError, len(issues))
		for i, is := range issues {
			fields[i] = FieldError{In: InBody, Path: is.Path, Code: is.Code, Params: is.Params}
		}
		return Validation(fields...)
	}
	var pe *pgconn.PgError
	if errors.As(err, &pe) {
		return fromPG(pe, err)
	}
	switch {
	case errors.Is(err, gorm.ErrRecordNotFound):
		return NotFound()
	case errors.Is(err, context.DeadlineExceeded):
		return ServiceUnavailable(err, time.Second)
	case errors.Is(err, context.Canceled):
		return ServiceUnavailable(err, 0)
	}
	return Internal(err)
}

func fromFiber(fe *fiber.Error) *Error {
	switch fe.Code {
	case http.StatusBadRequest:
		return BadRequest()
	case http.StatusUnauthorized:
		return Unauthenticated()
	case http.StatusForbidden:
		return Forbidden()
	case http.StatusNotFound:
		return NotFound()
	case http.StatusMethodNotAllowed:
		return newErr(http.StatusMethodNotAllowed, CodeMethodNotAllowed)
	case http.StatusRequestEntityTooLarge:
		return PayloadTooLarge()
	case http.StatusUnsupportedMediaType:
		return UnsupportedMediaType()
	case http.StatusTooManyRequests:
		return RateLimited(0)
	case http.StatusServiceUnavailable, http.StatusRequestTimeout, http.StatusGatewayTimeout:
		return ServiceUnavailable(fe, 0)
	}
	if fe.Code >= 500 {
		return Internal(fe)
	}
	e := BadRequest()
	e.Status = fe.Code
	return e
}

func fromPG(pe *pgconn.PgError, err error) *Error {
	switch pe.Code {
	case sqlUniqueViolation:
		cm := lookupConstraint(pe.ConstraintName)
		code := cm.Code
		if code == "" {
			code = "duplicate"
		}
		in := cm.In
		if in == "" {
			in = InBody
		}
		return ConflictDuplicate(FieldError{In: in, Path: cm.Path, Code: code}).WithCause(err)
	case sqlForeignKeyViolation:
		return EntityReferenced().WithCause(err)
	case sqlCheckViolation:
		// A CHECK constraint carries its name; a trigger's `raise exception ... check_violation` does not.
		// Triggers (last super admin, BR-22/23) are the last line of defence behind the service's own
		// pre-validation, so reaching one is a bug: answer 500 and log (doc/05 section 1.5).
		if pe.ConstraintName == "" {
			return Internal(err)
		}
		cm := lookupConstraint(pe.ConstraintName)
		code := cm.Code
		if code == "" {
			code = "check_violation"
		}
		in := cm.In
		if in == "" {
			in = InBody
		}
		return Validation(FieldError{In: in, Path: cm.Path, Code: code}).WithCause(err)
	case sqlSerializationFail, sqlDeadlock:
		return ServiceUnavailable(err, time.Second)
	case sqlQueryCanceled:
		return ServiceUnavailable(err, 0)
	}
	return Internal(err)
}

// logFailure logs what a client cannot see: the cause of 5xx answers, with the request id and the route
// template. Query strings, bodies and headers are never logged (doc/06 section 8.1).
func logFailure(c *fiber.Ctx, ae *Error) {
	if ae.Status < 500 {
		return
	}
	route := c.Path()
	if r := c.Route(); r != nil && r.Path != "" {
		route = r.Path
	}
	cause := ""
	if ae.Cause != nil {
		cause = strings.TrimSpace(ae.Cause.Error())
	}
	logs.Error("request failed",
		logs.String("code", ae.Code), logs.Int("status", ae.Status),
		logs.String("method", c.Method()), logs.String("route", route),
		logs.String("request_id", requestid.FromFiber(c)), logs.String("cause", cause))
}
