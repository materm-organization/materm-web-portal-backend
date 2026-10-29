// Package apperror is the error vocabulary of the API (doc/05 sections 1.3-1.5, doc/08 section 2.1). It is a
// copy of the admin backend's package, kept identical on purpose (doc/06 section 1.3): both services answer
// errors with the same envelope, so the catalog below also holds codes only the admin backend raises. Change
// it in both repositories or in neither.
//
// A handler, service or repository returns an *Error (or wraps a cause with Internal); the Fiber
// ErrorHandler (Handler) turns it into
//
//	{ "success": false, "message": "<human text in the requested language>", "code": "FORBIDDEN",
//	  "errors": [ { "in": "body", "path": "/new_password", "code": "password_policy.min_length", "params": {...} } ],
//	  "details": {...}, "request_id": "..." }
//
// Clients decide on `code` (never on `message`). The set of codes is the table of doc/05 section 1.4; the
// HTTP status is part of the code, so use the constructors below instead of building an Error by hand.
// Note: 401 is reserved for problems with the credential itself (for the portal: a missing or wrong
// X-Portal-Key); the admin frontend answers every 401 with a silent refresh and, if that fails, a redirect to
// sign-in.
package apperror

import (
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"
)

// Codes (doc/05 section 1.4). The comment gives the HTTP status each is used with.
const (
	CodeBadRequest            = "BAD_REQUEST"                // 400
	CodeInvalidOldPassword    = "INVALID_OLD_PASSWORD"       // 400 (never 401: the axios interceptor would sign the user out)
	CodeInvalidToken          = "INVALID_TOKEN"              // 400 link token (reset/invite)
	CodeUnauthenticated       = "UNAUTHENTICATED"            // 401
	CodeInvalidCredentials    = "INVALID_CREDENTIALS"        // 401
	CodeTokenExpired          = "TOKEN_EXPIRED"              // 401
	CodeSessionRevoked        = "SESSION_REVOKED"            // 401
	CodeRefreshReused         = "REFRESH_REUSED"             // 401
	CodeAuthzChanged          = "AUTHZ_CHANGED"              // 401 (so that the silent refresh fetches the new permissions)
	CodeRefreshReplayGone     = "REFRESH_REPLAY_UNAVAILABLE" // 401 (rotated token re-sent inside the replay window but the cached pair is gone; the family is NOT revoked)
	CodeForbidden             = "FORBIDDEN"                  // 403
	CodePasswordChangeNeeded  = "PASSWORD_CHANGE_REQUIRED"   // 403
	CodeSuperAdminProtected   = "SUPER_ADMIN_PROTECTED"      // 403
	CodeNotFound              = "NOT_FOUND"                  // 404
	CodeMethodNotAllowed      = "METHOD_NOT_ALLOWED"         // 405 (additive: unknown method on a known route)
	CodeConflictDuplicate     = "CONFLICT_DUPLICATE"         // 409
	CodeConflictLockVersion   = "CONFLICT_LOCK_VERSION"      // 409
	CodeLastSuperAdmin        = "LAST_SUPER_ADMIN"           // 409
	CodeRoleInUse             = "ROLE_IN_USE"                // 409
	CodeSystemRoleProtected   = "SYSTEM_ROLE_PROTECTED"      // 409
	CodeSelfActionNotAllowed  = "SELF_ACTION_NOT_ALLOWED"    // 409
	CodeInvalidState          = "INVALID_STATE"              // 409
	CodeDefaultLocaleProtect  = "DEFAULT_LOCALE_PROTECTED"   // 409 (additive: BR-05, the default locale cannot be deactivated)
	CodeEntityReferenced      = "ENTITY_REFERENCED"          // 409 (foreign key: still referenced / unknown reference)
	CodeIdempotencyInProgress = "IDEMPOTENCY_IN_PROGRESS"    // 409
	CodePayloadTooLarge       = "PAYLOAD_TOO_LARGE"          // 413
	CodeUnsupportedMediaType  = "UNSUPPORTED_MEDIA_TYPE"     // 415
	CodeValidationFailed      = "VALIDATION_FAILED"          // 422
	CodeIdempotencyKeyReused  = "IDEMPOTENCY_KEY_REUSED"     // 422
	CodeExportTooLarge        = "EXPORT_TOO_LARGE"           // 422
	CodePublishBlocked        = "PUBLISH_BLOCKED"            // 422 (a publish the rules refuse: errors[] lists the blockers, details the counts)
	CodePreconditionRequired  = "PRECONDITION_REQUIRED"      // 428
	CodeRateLimited           = "RATE_LIMITED"               // 429
	CodeInternal              = "INTERNAL_ERROR"             // 500
	CodeServiceUnavailable    = "SERVICE_UNAVAILABLE"        // 503
)

// Locations of a field error (FieldError.In).
const (
	InBody   = "body"
	InQuery  = "query"
	InPath   = "path"
	InHeader = "header"
)

// FieldError is one field-level problem (doc/05 section 1.5). Path is a JSON Pointer (RFC 6901) inside In:
// "/email", "/grants/3/actions/1", "" for the whole object. Code is a field code ("required", "too_short",
// "password_policy.min_length", ...); Params carries values the UI needs (min, max, available, ...).
type FieldError struct {
	In      string         `json:"in"`
	Path    string         `json:"path"`
	Code    string         `json:"code"`
	Message string         `json:"message,omitempty"`
	Params  map[string]any `json:"params,omitempty"`
}

// Body is the JSON body of an error response.
type Body struct {
	Success   bool           `json:"success"`
	Message   string         `json:"message"`
	Code      string         `json:"code"`
	Errors    []FieldError   `json:"errors,omitempty"`
	Details   map[string]any `json:"details,omitempty"`
	RequestID string         `json:"request_id,omitempty"`
}

// Error is an API error: an HTTP status, a machine code and optional field errors / details. Message, when
// set, overrides the catalog text (use it for text that is not in the catalog; it is not translated).
type Error struct {
	Status  int
	Code    string
	Message string
	Errors  []FieldError
	Details map[string]any
	// RetryAfter, when positive, is sent as the Retry-After header (429 / 503).
	RetryAfter time.Duration
	// Cause is the underlying error. It is logged but never sent to the client.
	Cause error
}

// Error implements error. It never includes Cause text that could carry values; use errors.Unwrap to
// reach the cause when logging.
func (e *Error) Error() string {
	if e.Cause != nil {
		return fmt.Sprintf("%s (%d): %v", e.Code, e.Status, e.Cause)
	}
	return fmt.Sprintf("%s (%d)", e.Code, e.Status)
}

// Unwrap returns Cause.
func (e *Error) Unwrap() error { return e.Cause }

// Is matches on Code so that errors.Is(err, apperror.Forbidden()) works across instances.
func (e *Error) Is(target error) bool {
	t, ok := target.(*Error)
	return ok && t.Code == e.Code && t.Status == e.Status
}

// WithDetails returns a copy of e with Details set (e.g. {"user_count": 3} for ROLE_IN_USE).
func (e *Error) WithDetails(d map[string]any) *Error {
	c := *e
	c.Details = d
	return &c
}

// WithMessage returns a copy of e with a message that replaces the catalog text (not translated).
func (e *Error) WithMessage(m string) *Error {
	c := *e
	c.Message = m
	return &c
}

// WithCause returns a copy of e carrying cause for the log.
func (e *Error) WithCause(cause error) *Error {
	c := *e
	c.Cause = cause
	return &c
}

// WithErrors returns a copy of e with field errors set.
func (e *Error) WithErrors(f ...FieldError) *Error {
	c := *e
	c.Errors = f
	return &c
}

// AsError returns the *Error inside err (via errors.As), if any.
func AsError(err error) (*Error, bool) {
	var ae *Error
	if errors.As(err, &ae) {
		return ae, true
	}
	return nil, false
}

func newErr(status int, code string) *Error { return &Error{Status: status, Code: code} }

// --- 400 -------------------------------------------------------------------------------------------

// BadRequest is a malformed request (JSON that cannot be parsed, a query of the wrong type).
func BadRequest() *Error { return newErr(http.StatusBadRequest, CodeBadRequest) }

// InvalidOldPassword: the current password sent to change-password is wrong. 400, never 401.
func InvalidOldPassword() *Error { return newErr(http.StatusBadRequest, CodeInvalidOldPassword) }

// InvalidToken: a link token (reset / invitation) is unknown, used or expired (one message for all cases).
func InvalidToken() *Error { return newErr(http.StatusBadRequest, CodeInvalidToken) }

// --- 401: only for problems with the token itself ---------------------------------------------------

// Unauthenticated: no token, malformed, bad signature, wrong token type.
func Unauthenticated() *Error { return newErr(http.StatusUnauthorized, CodeUnauthenticated) }

// InvalidCredentials: a failed login, the same answer for every reason.
func InvalidCredentials() *Error { return newErr(http.StatusUnauthorized, CodeInvalidCredentials) }

// TokenExpired: the access token expired (refresh) or the refresh token expired / hit the session cap (sign in).
func TokenExpired() *Error { return newErr(http.StatusUnauthorized, CodeTokenExpired) }

// SessionRevoked: the session was ended by logout, an admin or a password change.
func SessionRevoked() *Error { return newErr(http.StatusUnauthorized, CodeSessionRevoked) }

// RefreshReused: a rotated refresh token was presented again outside the grace window; the family is revoked.
func RefreshReused() *Error { return newErr(http.StatusUnauthorized, CodeRefreshReused) }

// AuthzChanged: the authz_version in the token no longer matches (role, grants or status changed).
func AuthzChanged() *Error { return newErr(http.StatusUnauthorized, CodeAuthzChanged) }

// RefreshReplayUnavailable: a refresh token that was rotated a moment ago is presented again inside the
// replay window, but the sealed answer is no longer in Redis (Redis down or expired). The client must sign
// in again; the family is NOT revoked (this is not a theft signal).
func RefreshReplayUnavailable() *Error { return newErr(http.StatusUnauthorized, CodeRefreshReplayGone) }

// --- 403 -------------------------------------------------------------------------------------------

// Forbidden: the caller lacks permission_key:action (record the denial in the audit log).
func Forbidden() *Error { return newErr(http.StatusForbidden, CodeForbidden) }

// PasswordChangeRequired: must_change_password is set and the route is not in the allow-list.
func PasswordChangeRequired() *Error { return newErr(http.StatusForbidden, CodePasswordChangeNeeded) }

// SuperAdminProtected: a non-super user tried to change a super account or grant a super role (BR-21).
func SuperAdminProtected() *Error { return newErr(http.StatusForbidden, CodeSuperAdminProtected) }

// --- 404 / 409 -------------------------------------------------------------------------------------

// NotFound: the resource does not exist (or is deleted).
func NotFound() *Error { return newErr(http.StatusNotFound, CodeNotFound) }

// ConflictDuplicate: a unique value is already taken; point at the field(s) with errors.
func ConflictDuplicate(fields ...FieldError) *Error {
	e := newErr(http.StatusConflict, CodeConflictDuplicate)
	e.Errors = fields
	return e
}

// ConflictLockVersion: If-Match / version does not match (somebody else edited first).
func ConflictLockVersion() *Error { return newErr(http.StatusConflict, CodeConflictLockVersion) }

// LastSuperAdmin: the change would remove the last active super admin (BR-20).
func LastSuperAdmin() *Error { return newErr(http.StatusConflict, CodeLastSuperAdmin) }

// RoleInUse: the role still has users (details.user_count).
func RoleInUse(userCount int64) *Error {
	e := newErr(http.StatusConflict, CodeRoleInUse)
	e.Details = map[string]any{"user_count": userCount}
	return e
}

// SystemRoleProtected: deleting a system role or editing the grants of a super role.
func SystemRoleProtected() *Error { return newErr(http.StatusConflict, CodeSystemRoleProtected) }

// SelfActionNotAllowed: changing your own role / status, deleting or admin-resetting yourself (BR-21).
func SelfActionNotAllowed() *Error { return newErr(http.StatusConflict, CodeSelfActionNotAllowed) }

// InvalidState: the request contradicts the current state of the resource.
func InvalidState() *Error { return newErr(http.StatusConflict, CodeInvalidState) }

// DefaultLocaleProtected: the default locale cannot be deactivated (BR-05).
func DefaultLocaleProtected() *Error { return newErr(http.StatusConflict, CodeDefaultLocaleProtect) }

// EntityReferenced: a foreign key blocks the change (still referenced, or the reference does not exist).
func EntityReferenced() *Error { return newErr(http.StatusConflict, CodeEntityReferenced) }

// IdempotencyInProgress: the first request with this Idempotency-Key has not finished.
func IdempotencyInProgress() *Error { return newErr(http.StatusConflict, CodeIdempotencyInProgress) }

// --- 413 / 415 / 422 / 428 -------------------------------------------------------------------------

// PayloadTooLarge: the body is over the limit.
func PayloadTooLarge() *Error { return newErr(http.StatusRequestEntityTooLarge, CodePayloadTooLarge) }

// UnsupportedMediaType: the body is not JSON.
func UnsupportedMediaType() *Error {
	return newErr(http.StatusUnsupportedMediaType, CodeUnsupportedMediaType)
}

// Validation is a 422 VALIDATION_FAILED with field errors.
func Validation(fields ...FieldError) *Error {
	e := newErr(http.StatusUnprocessableEntity, CodeValidationFailed)
	e.Errors = fields
	return e
}

// IdempotencyKeyReused: same Idempotency-Key, different body.
func IdempotencyKeyReused() *Error {
	return newErr(http.StatusUnprocessableEntity, CodeIdempotencyKeyReused)
}

// ExportTooLarge: more rows than an export may carry.
func ExportTooLarge() *Error { return newErr(http.StatusUnprocessableEntity, CodeExportTooLarge) }

// PublishBlocked: a publish that the publish rules refuse. The caller adds errors[] (the blockers) and details; the
// message may be replaced with WithMessage (e.g. to count the blockers).
func PublishBlocked() *Error { return newErr(http.StatusUnprocessableEntity, CodePublishBlocked) }

// PreconditionRequired: the endpoint requires If-Match.
func PreconditionRequired() *Error {
	return newErr(http.StatusPreconditionRequired, CodePreconditionRequired)
}

// --- 429 / 5xx -------------------------------------------------------------------------------------

// RateLimited is a 429 with Retry-After (rounded up to whole seconds). It never says whether the cause is
// the account or the IP.
func RateLimited(retryAfter time.Duration) *Error {
	e := newErr(http.StatusTooManyRequests, CodeRateLimited)
	e.RetryAfter = retryAfter
	return e
}

// Internal is a 500; cause is logged, never sent.
func Internal(cause error) *Error {
	e := newErr(http.StatusInternalServerError, CodeInternal)
	e.Cause = cause
	return e
}

// ServiceUnavailable is a 503 (a dependency failed, or the server is overloaded); retryAfter may be zero.
func ServiceUnavailable(cause error, retryAfter time.Duration) *Error {
	e := newErr(http.StatusServiceUnavailable, CodeServiceUnavailable)
	e.Cause, e.RetryAfter = cause, retryAfter
	return e
}

// Field is a shorthand for a FieldError in the request body.
func Field(path, code string) FieldError { return FieldError{In: InBody, Path: path, Code: code} }

// retryAfterSeconds rounds d up to whole seconds (minimum 1 when d > 0).
func retryAfterSeconds(d time.Duration) string {
	if d <= 0 {
		return ""
	}
	s := int64((d + time.Second - 1) / time.Second)
	return strconv.FormatInt(s, 10)
}
