package apperror

import "strings"

// Languages of `message` (doc/05 section 1.14): en and la; the alias lo maps to la; anything else is en.
const (
	LangEN = "en"
	LangLA = "la"
)

// NegotiateLang picks the message language from an Accept-Language header value: the first listed tag
// whose primary subtag is en, la or lo wins (q-values are ignored), otherwise en.
func NegotiateLang(header string) string {
	for _, part := range strings.Split(header, ",") {
		tag := strings.ToLower(strings.TrimSpace(strings.SplitN(part, ";", 2)[0]))
		if i := strings.IndexAny(tag, "-_"); i > 0 {
			tag = tag[:i]
		}
		switch tag {
		case "en":
			return LangEN
		case "la", "lo":
			return LangLA
		}
	}
	return LangEN
}

// messagesEN is the human text per code. Clients decide on `code`; `message` is what a toast shows.
// Texts are generic on purpose: they must never reveal whether an account exists, nor internals.
var messagesEN = map[string]string{
	CodeBadRequest:            "The request is malformed.",
	CodeInvalidOldPassword:    "The current password is incorrect.",
	CodeInvalidToken:          "This link is invalid or has expired.",
	CodeUnauthenticated:       "Authentication is required.",
	CodeInvalidCredentials:    "Username or password is incorrect.",
	CodeTokenExpired:          "The session has expired. Please sign in again.",
	CodeSessionRevoked:        "This session is no longer valid. Please sign in again.",
	CodeRefreshReused:         "This session is no longer valid. Please sign in again.",
	CodeAuthzChanged:          "Your permissions have changed.",
	CodeRefreshReplayGone:     "This session is no longer valid. Please sign in again.",
	CodeDefaultLocaleProtect:  "The default language cannot be deactivated.",
	CodeForbidden:             "You do not have permission to perform this action.",
	CodePasswordChangeNeeded:  "You must change your password before continuing.",
	CodeSuperAdminProtected:   "Super administrator accounts are protected.",
	CodeNotFound:              "The requested resource was not found.",
	CodeMethodNotAllowed:      "This method is not allowed for this resource.",
	CodeConflictDuplicate:     "A record with the same value already exists.",
	CodeConflictLockVersion:   "The record was changed by someone else. Reload and try again.",
	CodeLastSuperAdmin:        "The last active super administrator cannot be removed, demoted or deactivated.",
	CodeRoleInUse:             "The role is still assigned to users.",
	CodeSystemRoleProtected:   "System roles are protected.",
	CodeSelfActionNotAllowed:  "You cannot perform this action on your own account.",
	CodeInvalidState:          "The request conflicts with the current state of the resource.",
	CodeEntityReferenced:      "The record is referenced by other data, or refers to data that does not exist.",
	CodeIdempotencyInProgress: "A request with this idempotency key is still being processed.",
	CodePayloadTooLarge:       "The request body is too large.",
	CodeUnsupportedMediaType:  "The request body must be JSON.",
	CodeValidationFailed:      "The submitted data is not valid.",
	CodeIdempotencyKeyReused:  "This idempotency key was already used with different data.",
	CodeExportTooLarge:        "The export is too large. Narrow the filter.",
	CodePreconditionRequired:  "This request must include an If-Match header.",
	CodeRateLimited:           "Too many requests. Please try again later.",
	CodeInternal:              "An internal error occurred.",
	CodeServiceUnavailable:    "The service is temporarily unavailable.",
}

// messagesLA is the Lao text per code. It is intentionally empty until the copy is written and reviewed
// by a Lao speaker (same status as the portal site's `la` strings): Message falls back to English.
var messagesLA = map[string]string{}

// Message returns the text for code in lang (en or la), falling back to English, then to the code itself.
func Message(code, lang string) string {
	if lang == LangLA {
		if m, ok := messagesLA[code]; ok && m != "" {
			return m
		}
	}
	if m, ok := messagesEN[code]; ok {
		return m
	}
	return code
}

// knownCodes lists every code with a catalog entry (used by tests to keep catalog and constructors aligned).
func knownCodes() []string {
	out := make([]string, 0, len(messagesEN))
	for c := range messagesEN {
		out = append(out, c)
	}
	return out
}
