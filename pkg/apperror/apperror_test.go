package apperror

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/go-playground/validator/v10"
	"github.com/gofiber/fiber/v2"
	"github.com/jackc/pgx/v5/pgconn"
	"gorm.io/gorm"

	"github.com/materm-organization/materm-web-portal-backend/pkg/requestid"
)

func app(h fiber.Handler) *fiber.App {
	a := fiber.New(fiber.Config{ErrorHandler: Handler})
	a.Use(requestid.New())
	a.Get("/x", h)
	return a
}

func call(t *testing.T, a *fiber.App, hdr map[string]string) (*http.Response, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/x", nil)
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := a.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp, string(b)
}

func TestGoldenErrorEnvelope(t *testing.T) {
	a := app(func(*fiber.Ctx) error {
		return Validation(
			FieldError{In: InBody, Path: "/new_password", Code: "password_policy.min_length", Params: map[string]any{"min": 12}},
			Field("/email", "invalid_format"))
	})
	resp, body := call(t, a, map[string]string{"X-Request-Id": "req-12345678"})
	want := `{"success":false,"message":"The submitted data is not valid.","code":"VALIDATION_FAILED",` +
		`"errors":[{"in":"body","path":"/new_password","code":"password_policy.min_length","params":{"min":12}},` +
		`{"in":"body","path":"/email","code":"invalid_format"}],"request_id":"req-12345678"}`
	if body != want {
		t.Errorf("body:\n got %s\nwant %s", body, want)
	}
	if resp.StatusCode != 422 || resp.Header.Get("Content-Language") != "en" || resp.Header.Get("X-Request-Id") != "req-12345678" {
		t.Errorf("status/headers: %d %v", resp.StatusCode, resp.Header)
	}
}

func TestDetailsAndRetryAfter(t *testing.T) {
	_, body := call(t, app(func(*fiber.Ctx) error { return RoleInUse(3) }), nil)
	if !strings.Contains(body, `"code":"ROLE_IN_USE"`) || !strings.Contains(body, `"details":{"user_count":3}`) {
		t.Errorf("ROLE_IN_USE: %s", body)
	}
	resp, _ := call(t, app(func(*fiber.Ctx) error { return RateLimited(1500 * time.Millisecond) }), nil)
	if resp.StatusCode != 429 || resp.Header.Get("Retry-After") != "2" {
		t.Errorf("429: %d Retry-After=%q (want rounded up to 2)", resp.StatusCode, resp.Header.Get("Retry-After"))
	}
	resp, _ = call(t, app(func(*fiber.Ctx) error { return Forbidden() }), nil)
	if resp.Header.Get("Retry-After") != "" {
		t.Error("no Retry-After without a retry hint")
	}
}

func TestLanguageNegotiation(t *testing.T) {
	cases := map[string]string{"": "en", "en-US,en;q=0.9": "en", "la": "la", "lo": "la", "lo-LA, en;q=0.8": "la", "fr, en": "en", "fr": "en", "garbage;;": "en"}
	for in, want := range cases {
		if got := NegotiateLang(in); got != want {
			t.Errorf("NegotiateLang(%q) = %q, want %q", in, got, want)
		}
	}
	resp, body := call(t, app(func(*fiber.Ctx) error { return Forbidden() }), map[string]string{"Accept-Language": "lo"})
	if resp.Header.Get("Content-Language") != "la" || !strings.Contains(body, Message(CodeForbidden, LangEN)) {
		t.Errorf("la falls back to English text until translated: %s", body)
	}
}

func TestEveryCodeHasAMessage(t *testing.T) {
	codes := []string{CodeBadRequest, CodeInvalidOldPassword, CodeInvalidToken, CodeUnauthenticated, CodeInvalidCredentials, CodeTokenExpired,
		CodeSessionRevoked, CodeRefreshReused, CodeAuthzChanged, CodeRefreshReplayGone, CodeDefaultLocaleProtect, CodeForbidden, CodePasswordChangeNeeded, CodeSuperAdminProtected, CodeNotFound,
		CodeMethodNotAllowed, CodeConflictDuplicate, CodeConflictLockVersion, CodeLastSuperAdmin, CodeRoleInUse, CodeSystemRoleProtected,
		CodeSelfActionNotAllowed, CodeInvalidState, CodeEntityReferenced, CodeIdempotencyInProgress, CodePayloadTooLarge, CodeUnsupportedMediaType,
		CodeValidationFailed, CodeIdempotencyKeyReused, CodeExportTooLarge, CodePreconditionRequired, CodeRateLimited, CodeInternal, CodeServiceUnavailable}
	for _, c := range codes {
		if m := Message(c, LangEN); m == c || m == "" {
			t.Errorf("code %s has no English message", c)
		}
	}
	if len(knownCodes()) != len(codes) {
		t.Errorf("catalog has %d entries, constants list has %d: keep them aligned", len(knownCodes()), len(codes))
	}
	if Message("SOMETHING_NEW", LangEN) != "SOMETHING_NEW" {
		t.Error("unknown codes fall back to the code itself")
	}
}

func TestConstructorsStatusAndCode(t *testing.T) {
	cases := []struct {
		e      *Error
		status int
		code   string
	}{
		{BadRequest(), 400, "BAD_REQUEST"}, {InvalidOldPassword(), 400, "INVALID_OLD_PASSWORD"}, {InvalidToken(), 400, "INVALID_TOKEN"},
		{Unauthenticated(), 401, "UNAUTHENTICATED"}, {InvalidCredentials(), 401, "INVALID_CREDENTIALS"}, {TokenExpired(), 401, "TOKEN_EXPIRED"},
		{SessionRevoked(), 401, "SESSION_REVOKED"}, {RefreshReused(), 401, "REFRESH_REUSED"}, {AuthzChanged(), 401, "AUTHZ_CHANGED"}, {RefreshReplayUnavailable(), 401, "REFRESH_REPLAY_UNAVAILABLE"}, {DefaultLocaleProtected(), 409, "DEFAULT_LOCALE_PROTECTED"},
		{Forbidden(), 403, "FORBIDDEN"}, {PasswordChangeRequired(), 403, "PASSWORD_CHANGE_REQUIRED"}, {SuperAdminProtected(), 403, "SUPER_ADMIN_PROTECTED"},
		{NotFound(), 404, "NOT_FOUND"}, {ConflictDuplicate(), 409, "CONFLICT_DUPLICATE"}, {ConflictLockVersion(), 409, "CONFLICT_LOCK_VERSION"},
		{LastSuperAdmin(), 409, "LAST_SUPER_ADMIN"}, {RoleInUse(1), 409, "ROLE_IN_USE"}, {SystemRoleProtected(), 409, "SYSTEM_ROLE_PROTECTED"},
		{SelfActionNotAllowed(), 409, "SELF_ACTION_NOT_ALLOWED"}, {InvalidState(), 409, "INVALID_STATE"}, {EntityReferenced(), 409, "ENTITY_REFERENCED"},
		{IdempotencyInProgress(), 409, "IDEMPOTENCY_IN_PROGRESS"}, {PayloadTooLarge(), 413, "PAYLOAD_TOO_LARGE"}, {UnsupportedMediaType(), 415, "UNSUPPORTED_MEDIA_TYPE"},
		{Validation(), 422, "VALIDATION_FAILED"}, {IdempotencyKeyReused(), 422, "IDEMPOTENCY_KEY_REUSED"}, {ExportTooLarge(), 422, "EXPORT_TOO_LARGE"},
		{PreconditionRequired(), 428, "PRECONDITION_REQUIRED"}, {RateLimited(0), 429, "RATE_LIMITED"}, {Internal(nil), 500, "INTERNAL_ERROR"},
		{ServiceUnavailable(nil, 0), 503, "SERVICE_UNAVAILABLE"},
	}
	for _, c := range cases {
		if c.e.Status != c.status || c.e.Code != c.code {
			t.Errorf("%s: status %d code %s", c.code, c.e.Status, c.e.Code)
		}
	}
	// the frontend turns every 401 into a refresh: only token problems may use it
	for _, c := range cases {
		if c.status == 401 {
			switch c.code {
			case "UNAUTHENTICATED", "INVALID_CREDENTIALS", "TOKEN_EXPIRED", "SESSION_REVOKED", "REFRESH_REUSED", "AUTHZ_CHANGED", "REFRESH_REPLAY_UNAVAILABLE":
			default:
				t.Errorf("unexpected 401 code %s", c.code)
			}
		}
	}
	if InvalidOldPassword().Status == 401 {
		t.Error("a wrong old password must never be 401")
	}
}

func TestErrorsIsAndWrap(t *testing.T) {
	err := fmt.Errorf("service: %w", Forbidden())
	if !errors.Is(err, Forbidden()) || errors.Is(err, NotFound()) {
		t.Error("errors.Is must match on code")
	}
	if ae, ok := AsError(err); !ok || ae.Code != CodeForbidden {
		t.Error("AsError through a wrap")
	}
	cause := errors.New("db down")
	e := Internal(cause)
	if !errors.Is(e, cause) {
		t.Error("Unwrap must reach the cause")
	}
	d := Forbidden().WithDetails(map[string]any{"a": 1})
	if Forbidden().Details != nil || d.Details["a"] != 1 {
		t.Error("With* must copy, not mutate shared values")
	}
}

type reqBody struct {
	Name  string   `json:"name" validate:"required,min=3"`
	Items []string `json:"items" validate:"dive,required"`
	Role  string   `json:"role" validate:"oneof=a b"`
	Age   int      `json:"age" validate:"max=10"`
}

func TestClassify(t *testing.T) {
	v := validator.New()
	v.RegisterTagNameFunc(func(f reflect.StructField) string { return strings.SplitN(f.Tag.Get("json"), ",", 2)[0] })
	verr := v.Struct(reqBody{Name: "x", Items: []string{"ok", ""}, Role: "z", Age: 11})
	type tc struct {
		name   string
		err    error
		status int
		code   string
	}
	cases := []tc{
		{"app error", Forbidden(), 403, "FORBIDDEN"},
		{"wrapped app error", fmt.Errorf("x: %w", NotFound()), 404, "NOT_FOUND"},
		{"fiber 404", fiber.ErrNotFound, 404, "NOT_FOUND"},
		{"fiber 400", fiber.ErrBadRequest, 400, "BAD_REQUEST"},
		{"fiber 405", fiber.ErrMethodNotAllowed, 405, "METHOD_NOT_ALLOWED"},
		{"fiber 413", fiber.ErrRequestEntityTooLarge, 413, "PAYLOAD_TOO_LARGE"},
		{"fiber 415", fiber.ErrUnsupportedMediaType, 415, "UNSUPPORTED_MEDIA_TYPE"},
		{"fiber 429", fiber.ErrTooManyRequests, 429, "RATE_LIMITED"},
		{"fiber 503", fiber.ErrServiceUnavailable, 503, "SERVICE_UNAVAILABLE"},
		{"fiber 500", fiber.ErrInternalServerError, 500, "INTERNAL_ERROR"},
		{"validator", verr, 422, "VALIDATION_FAILED"},
		{"record not found", gorm.ErrRecordNotFound, 404, "NOT_FOUND"},
		{"deadline", fmt.Errorf("q: %w", context.DeadlineExceeded), 503, "SERVICE_UNAVAILABLE"},
		{"unique", &pgconn.PgError{Code: "23505", ConstraintName: "users_email_uq"}, 409, "CONFLICT_DUPLICATE"},
		{"foreign key", &pgconn.PgError{Code: "23503"}, 409, "ENTITY_REFERENCED"},
		{"check constraint", &pgconn.PgError{Code: "23514", ConstraintName: "users_username_check"}, 422, "VALIDATION_FAILED"},
		{"check by trigger is a bug", &pgconn.PgError{Code: "23514"}, 500, "INTERNAL_ERROR"},
		{"deadlock", &pgconn.PgError{Code: "40P01"}, 503, "SERVICE_UNAVAILABLE"},
		{"serialization", &pgconn.PgError{Code: "40001"}, 503, "SERVICE_UNAVAILABLE"},
		{"statement timeout", &pgconn.PgError{Code: "57014"}, 503, "SERVICE_UNAVAILABLE"},
		{"not null", &pgconn.PgError{Code: "23502"}, 500, "INTERNAL_ERROR"},
		{"unknown", errors.New("boom"), 500, "INTERNAL_ERROR"},
		{"nil", nil, 500, "INTERNAL_ERROR"},
	}
	RegisterConstraint("users_email_uq", ConstraintMap{Path: "/email"})
	RegisterConstraint("users_username_check", ConstraintMap{Path: "/username", Code: "pattern_mismatch"})
	for _, c := range cases {
		got := Classify(c.err)
		if got.Status != c.status || got.Code != c.code {
			t.Errorf("%s: %d %s, want %d %s", c.name, got.Status, got.Code, c.status, c.code)
		}
	}

	dup := Classify(&pgconn.PgError{Code: "23505", ConstraintName: "users_email_uq"})
	if len(dup.Errors) != 1 || dup.Errors[0].Path != "/email" || dup.Errors[0].Code != "duplicate" || dup.Errors[0].In != "body" {
		t.Errorf("duplicate field error: %+v", dup.Errors)
	}
	chk := Classify(&pgconn.PgError{Code: "23514", ConstraintName: "users_username_check"})
	if chk.Errors[0].Path != "/username" || chk.Errors[0].Code != "pattern_mismatch" {
		t.Errorf("check field error: %+v", chk.Errors)
	}
	val := Classify(verr)
	got := map[string]string{}
	for _, f := range val.Errors {
		got[f.Path] = f.Code
	}
	want := map[string]string{"/name": "too_short", "/items/1": "required", "/role": "not_in_enum", "/age": "out_of_range"}
	for p, c := range want {
		if got[p] != c {
			t.Errorf("validator path %s: code %q, want %q (all: %v)", p, got[p], c, got)
		}
	}
}

func TestInternalCauseIsNeverSent(t *testing.T) {
	a := app(func(*fiber.Ctx) error {
		return &pgconn.PgError{Code: "XX000", Message: "relation \"secret_table\" does not exist", Detail: "Key (password_hash)=(abc)"}
	})
	resp, body := call(t, a, nil)
	if resp.StatusCode != 500 || strings.Contains(body, "secret_table") || strings.Contains(body, "abc") {
		t.Errorf("internal details leaked: %d %s", resp.StatusCode, body)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(body), &m); err != nil || m["code"] != "INTERNAL_ERROR" || m["success"] != false {
		t.Errorf("body: %s", body)
	}
}
