package controller

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/modules/system/models"
	"github.com/materm-organization/materm-web-portal-backend/modules/system/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/requestid"
)

func app(db, redis func(context.Context) error, strict bool) *fiber.App {
	a := fiber.New(fiber.Config{ErrorHandler: apperror.Handler})
	a.Use(requestid.New())
	c := NewSystemCtrl(services.NewSystemService(services.Deps{PingDB: db, PingRedis: redis, Strict: strict}))
	a.Get("/health", c.Health)
	a.Get("/ready", c.Ready)
	return a
}

func get(t *testing.T, a *fiber.App, path string) (int, map[string]any, string, string) {
	t.Helper()
	resp, err := a.Test(httptest.NewRequest("GET", path, nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	var body map[string]any
	_ = json.Unmarshal(b, &body)
	return resp.StatusCode, body, string(b), resp.Header.Get("Cache-Control")
}

func up(context.Context) error { return nil }
func down(context.Context) error {
	return errors.New("dial tcp 10.1.2.3:5432: connection refused (user portal_ro)")
}

func TestHealth(t *testing.T) {
	status, body, raw, cc := get(t, app(down, down, true), "/health") // liveness ignores dependencies
	data, _ := body["data"].(map[string]any)
	if status != 200 || body["success"] != true || data["status"] != "ok" || cc != "no-store" {
		t.Fatalf("health: %d %s cache=%q", status, raw, cc)
	}
	if len(data) != 1 {
		t.Errorf("health must carry nothing but status: %v", data)
	}
}

func TestReadyOKAndDegraded(t *testing.T) {
	status, body, _, cc := get(t, app(up, up, true), "/ready")
	if data, _ := body["data"].(map[string]any); status != 200 || data["status"] != "ok" || cc != "no-store" {
		t.Errorf("ok: %d %v", status, body)
	}
	// Redis down alone is degraded (200), never 503: in dev and in staging/prod (doc/08 section 2.7 item 5)
	for name, a := range map[string]*fiber.App{
		"dev, Redis not connected":  app(up, nil, false),
		"dev, Redis down":           app(up, down, false),
		"prod, Redis down":          app(up, down, true),
		"prod, Redis not connected": app(up, nil, true),
	} {
		status, body, raw, _ := get(t, a, "/ready")
		data, _ := body["data"].(map[string]any)
		if status != 200 || body["success"] != true || data["status"] != "degraded" {
			t.Errorf("%s: %d %v", name, status, body)
		}
		for _, leak := range []string{"10.1.2.3", "5432", "portal_ro", "refused", "redis", "postgres", "database"} {
			if strings.Contains(strings.ToLower(raw), leak) {
				t.Errorf("%s: the degraded body mentions %q: %s", name, leak, raw)
			}
		}
	}
}

// 503 carries the standard error shape plus data.status=down and nothing about the stack.
func TestReadyDownRevealsNothing(t *testing.T) {
	for name, a := range map[string]*fiber.App{
		"db down, prod":     app(down, up, true),
		"db down, dev":      app(down, up, false),
		"db and redis down": app(down, down, true),
		"db down, no redis": app(down, nil, false),
	} {
		status, body, raw, _ := get(t, a, "/ready")
		data, _ := body["data"].(map[string]any)
		if status != 503 || body["success"] != false || body["code"] != "SERVICE_UNAVAILABLE" || data["status"] != models.StatusDown || body["request_id"] == "" || body["message"] == "" {
			t.Errorf("%s: %d %s", name, status, raw)
		}
		for _, leak := range []string{"10.1.2.3", "5432", "portal_ro", "refused", "redis", "postgres", "database"} {
			if strings.Contains(strings.ToLower(raw), leak) {
				t.Errorf("%s: the probe body mentions %q: %s", name, leak, raw)
			}
		}
	}
}
