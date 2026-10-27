package portalkey

import (
	"encoding/json"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
)

const (
	keyA = "portal-test-key-A-0123456789abcdef0123"
	keyB = "portal-test-key-B-0123456789abcdef0123"
)

func TestMatch(t *testing.T) {
	v := New([]string{keyA, keyB})
	for _, k := range []string{keyA, keyB} {
		if !v.Match(k) {
			t.Errorf("configured key refused")
		}
	}
	for name, k := range map[string]string{
		"empty": "", "wrong": "nope", "prefix": keyA[:len(keyA)-1], "extended": keyA + "x",
		"case": strings.ToUpper(keyA), "space": " " + keyA, "both": keyA + keyB,
	} {
		if v.Match(k) {
			t.Errorf("%s: wrong key accepted", name)
		}
	}
}

// Rotation: while two keys are configured both work; after the old one is removed only the new one does.
func TestRotation(t *testing.T) {
	both, onlyNew := New([]string{keyA, keyB}), New([]string{keyB})
	if !both.Match(keyA) || !both.Match(keyB) {
		t.Error("both keys must work during the rotation")
	}
	if onlyNew.Match(keyA) || !onlyNew.Match(keyB) {
		t.Error("after the rotation only the new key may work")
	}
}

// Fail closed: with no key configured nothing is accepted, not even the empty string.
func TestNoKeysRejectEverything(t *testing.T) {
	for _, v := range []*Verifier{New(nil), New([]string{}), New([]string{"", ""})} {
		if v.Configured() {
			t.Error("a Verifier without keys must report not configured")
		}
		if v.Match("") || v.Match("anything") {
			t.Error("a Verifier without keys must reject everything")
		}
	}
}

func app(v *Verifier) *fiber.App {
	a := fiber.New(fiber.Config{ErrorHandler: apperror.Handler})
	a.Use(v.Middleware())
	a.Get("/x", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"internal": Internal(c)}) })
	return a
}

func call(t *testing.T, a *fiber.App, header map[string]string) (int, map[string]any, string, string) {
	t.Helper()
	req := httptest.NewRequest("GET", "/x", nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	resp, err := a.Test(req, -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var body map[string]any
	_ = json.Unmarshal(b, &body)
	return resp.StatusCode, body, string(b), resp.Header.Get("Cache-Control")
}

func TestMiddleware(t *testing.T) {
	a := app(New([]string{keyA, keyB}))

	for name, h := range map[string]map[string]string{
		"missing": nil,
		"empty":   {Header: ""},
		"wrong":   {Header: "wrong-key"},
		"bearer":  {"Authorization": "Bearer " + keyA}, // the key is only read from X-Portal-Key
	} {
		status, body, raw, cc := call(t, a, h)
		if status != 401 || body["code"] != "UNAUTHENTICATED" || body["success"] != false {
			t.Errorf("%s: %d %v", name, status, body)
		}
		if cc != "no-store" {
			t.Errorf("%s: a refusal must be no-store, got %q", name, cc)
		}
		if strings.Contains(raw, keyA) || strings.Contains(raw, "wrong-key") {
			t.Errorf("%s: a key leaked into the body: %s", name, raw)
		}
	}

	for _, k := range []string{keyA, keyB} {
		status, body, _, _ := call(t, a, map[string]string{Header: k})
		if status != 200 || body["internal"] != true {
			t.Errorf("valid key refused: %d %v", status, body)
		}
	}
}

// Missing and wrong keys must be indistinguishable (same status, code and message).
func TestMissingAndWrongAreTheSameAnswer(t *testing.T) {
	a := app(New([]string{keyA}))
	_, b1, _, _ := call(t, a, nil)
	_, b2, _, _ := call(t, a, map[string]string{Header: "wrong"})
	if b1["code"] != b2["code"] || b1["message"] != b2["message"] {
		t.Errorf("distinguishable answers: %v vs %v", b1, b2)
	}
}

func TestInternalFalseWithoutMiddleware(t *testing.T) {
	a := fiber.New()
	a.Get("/x", func(c *fiber.Ctx) error { return c.JSON(fiber.Map{"internal": Internal(c)}) })
	_, body, _, _ := call(t, a, map[string]string{Header: keyA})
	if body["internal"] != false {
		t.Error("Internal must be false when the middleware did not run")
	}
}

// The comparison hashes both sides first, so a key of a different length takes the same path; this guards the
// helper from being replaced by a direct length-leaking comparison.
func TestMatchDoesNotShortCircuitOnLength(t *testing.T) {
	v := New([]string{keyA})
	if v.Match("a") || v.Match(strings.Repeat("a", 10000)) {
		t.Error("keys of other lengths must be refused")
	}
}

func BenchmarkMatch(b *testing.B) {
	v := New([]string{keyA, keyB})
	for i := 0; i < b.N; i++ {
		v.Match(keyB)
	}
}
