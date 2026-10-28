package bootstrap

import (
	"errors"
	"io"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gofiber/fiber/v2"
)

func TestFormatAccessLine(t *testing.T) {
	at := time.Date(2026, 10, 28, 9, 29, 40, 0, time.Local)
	got := formatAccessLine(at, 200, 1635130833*time.Nanosecond, "127.0.0.1", "GET", "/api/v1/settings/brand", "-", false)
	want := "09:29:40 | 200 |  1.635130833s | 127.0.0.1 | GET | /api/v1/settings/brand | -"
	if got != want {
		t.Fatalf("got  %q\nwant %q", got, want)
	}
	if strings.Contains(got, "\x1b") {
		t.Fatal("no color expected without a terminal")
	}
	colored := formatAccessLine(at, 500, time.Millisecond, "::1", "DELETE", "/x", "boom", true)
	if !strings.Contains(colored, "\x1b[31m500\x1b[0m") || !strings.Contains(colored, "\x1b[31mDELETE\x1b[0m") || !strings.HasSuffix(colored, "| boom") {
		t.Fatalf("unexpected colored line %q", colored)
	}
	if !strings.Contains(formatAccessLine(at, 204, time.Microsecond, "127.0.0.1", "OPTIONS", "/p", "", false), "| /p | -") {
		t.Fatal("an empty error column must print a dash")
	}
}

func TestPrettyAccessLogDefaultsToDevOnly(t *testing.T) {
	t.Setenv("LOG_CONSOLE", "")
	if !prettyAccessLog("dev") || !prettyAccessLog("DEV") {
		t.Fatal("dev should be pretty")
	}
	if prettyAccessLog("") || prettyAccessLog("prod") {
		t.Fatal("blank (= prod) and prod must stay JSON-only")
	}
	t.Setenv("LOG_CONSOLE", "off")
	if prettyAccessLog("dev") {
		t.Fatal("LOG_CONSOLE=off wins in dev")
	}
	t.Setenv("LOG_CONSOLE", "pretty")
	if !prettyAccessLog("prod") {
		t.Fatal("LOG_CONSOLE=pretty wins in prod")
	}
}

// The console line carries the concrete path but never the query string, and a failed request shows its error.
func TestAccessLogPrintsPathWithoutQuery(t *testing.T) {
	r, w, _ := os.Pipe()
	old := os.Stdout
	os.Stdout = w
	app := fiber.New()
	app.Use(newAccessLog(true))
	app.Get("/api/v1/things/:id", func(c *fiber.Ctx) error { return nil })
	app.Get("/api/v1/fail", func(c *fiber.Ctx) error { return errors.New("kaboom") })
	for _, target := range []string{"/api/v1/things/42?token=secret", "/api/v1/fail"} {
		if _, err := app.Test(httptest.NewRequest("GET", target, nil)); err != nil {
			t.Fatal(err)
		}
	}
	_ = w.Close()
	os.Stdout = old
	out, _ := io.ReadAll(r)
	text := string(out)
	if !strings.Contains(text, "| GET | /api/v1/things/42 | -") {
		t.Fatalf("missing the path line:\n%s", text)
	}
	if strings.Contains(text, "secret") || strings.Contains(text, "token") {
		t.Fatalf("the query string leaked into the console line:\n%s", text)
	}
	if !strings.Contains(text, "| 500 |") || !strings.Contains(text, "| /api/v1/fail | kaboom") {
		t.Fatalf("a failed request must show status 500 and its error:\n%s", text)
	}
}
