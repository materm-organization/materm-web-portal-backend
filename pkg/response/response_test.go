package response

import (
	"io"
	"net/http/httptest"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/pagination"
)

type item struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

func get(t *testing.T, h fiber.Handler) (int, string, map[string][]string) {
	t.Helper()
	app := fiber.New()
	app.Get("/x", h)
	resp, err := app.Test(httptest.NewRequest("GET", "/x", nil), -1)
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	_ = resp.Body.Close()
	return resp.StatusCode, string(b), resp.Header
}

func TestGoldenEnvelopes(t *testing.T) {
	cases := []struct {
		name   string
		h      fiber.Handler
		status int
		body   string
	}{
		{"ok", func(c *fiber.Ctx) error { return OK(c, item{7, "a"}) }, 200, `{"success":true,"data":{"id":7,"name":"a"}}`},
		{"ok nil is data null (no 204)", func(c *fiber.Ctx) error { return OK(c, nil) }, 200, `{"success":true,"data":null}`},
		{"message", func(c *fiber.Ctx) error { return OKMessage(c, "saved", "OK") }, 200, `{"success":true,"message":"saved","data":"OK"}`},
		{"created", func(c *fiber.Ctx) error { return Created(c, "/api/v1/x/7", item{7, "a"}) }, 201, `{"success":true,"data":{"id":7,"name":"a"}}`},
		{"list", func(c *fiber.Ctx) error {
			return List(c, []item{{1, "a"}, {2, "b"}}, pagination.NewMeta(42, pagination.ListQuery{Page: 1, Limit: 20}))
		}, 200, `{"success":true,"data":[{"id":1,"name":"a"},{"id":2,"name":"b"}],"meta":{"total":42,"page":1,"limit":20,"total_page":3}}`},
		{"nil list is []", func(c *fiber.Ctx) error {
			return List[item](c, nil, pagination.NewMeta(0, pagination.ListQuery{Page: 1, Limit: 20}))
		}, 200, `{"success":true,"data":[],"meta":{"total":0,"page":1,"limit":20,"total_page":0}}`},
	}
	for _, c := range cases {
		status, body, _ := get(t, c.h)
		if status != c.status || body != c.body {
			t.Errorf("%s:\n got %d %s\nwant %d %s", c.name, status, body, c.status, c.body)
		}
	}
	_, _, hdr := get(t, func(c *fiber.Ctx) error { return Created(c, "/api/v1/x/7", nil) })
	if hdr["Location"][0] != "/api/v1/x/7" {
		t.Errorf("Location: %v", hdr["Location"])
	}
}
