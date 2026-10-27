//go:build dbtest

package application

import (
	"context"
	"net/url"
	"strconv"
	"strings"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/dbtest"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/redistest"
)

func scratchEnv(t *testing.T, d *dbtest.Database, envName string) *bootstrap.Env {
	t.Helper()
	u, err := url.Parse(d.DSN)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	pw, _ := u.User.Password()
	e := &bootstrap.Env{
		Server:   bootstrap.ServerConfig{Port: 3000, Env: envName},
		Database: bootstrap.DatabaseConfig{Admin: bootstrap.DBConnection{Host: u.Hostname(), Port: port, User: u.User.Username(), Password: pw, DBName: d.Name, SSLMode: "disable"}},
		Portal:   bootstrap.PortalConfig{FrontendKeys: []string{testKey}},
	}
	e.Defaults()
	return e
}

// Boot on the scratch cluster in dev: the real wiring connects, /ready is 200, the key still guards /api/v1.
// With Redis nil the answer is degraded (dev); with the scratch Redis it is ok.
func TestBootOnScratch(t *testing.T) {
	d := dbtest.New(t)
	r := redistest.New(t)
	ctx := context.Background()

	for name, tc := range map[string]struct {
		redis bool
		want  string
	}{"redis up": {true, "ok"}, "redis not connected": {false, "degraded"}} {
		t.Run(name, func(t *testing.T) {
			env := scratchEnv(t, d, bootstrap.EnvDev)
			app := &bootstrap.Application{Env: env, Fiber: bootstrap.NewFiber(env.Server)}
			if tc.redis {
				app.Redis = r.Client
			}
			rt, err := Setup(ctx, app)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(rt.Close)

			status, body := req(t, rt.Fiber, "GET", "/ready", nil)
			data, _ := body["data"].(map[string]any)
			if status != 200 || data["status"] != tc.want {
				t.Errorf("/ready: %d %v, want %s", status, body, tc.want)
			}
			if status, _ = req(t, rt.Fiber, "GET", "/api/v1/x", nil); status != 401 {
				t.Errorf("/api/v1 without key: %d", status)
			}
			if status, _ = req(t, rt.Fiber, "GET", "/api/v1/x", map[string]string{portalkey.Header: testKey}); status != 404 {
				t.Errorf("/api/v1 with key: %d", status)
			}
		})
	}
}

// Outside dev the scratch superuser is not an acceptable runtime role: Setup refuses and leaves nothing open.
func TestBootRefusesSuperuserOutsideDev(t *testing.T) {
	d := dbtest.New(t)
	env := scratchEnv(t, d, bootstrap.EnvProd)
	app := &bootstrap.Application{Env: env, Fiber: bootstrap.NewFiber(env.Server)}
	rt, err := Setup(context.Background(), app)
	if err == nil {
		rt.Close()
		t.Fatal("Setup must refuse a superuser role in prod")
	}
	if !strings.Contains(err.Error(), "database role") {
		t.Errorf("unexpected error: %v", err)
	}
}

// The portal does not own the schema: booting against an empty database creates nothing.
func TestBootCreatesNothing(t *testing.T) {
	d := dbtest.New(t)
	env := scratchEnv(t, d, bootstrap.EnvDev)
	app := &bootstrap.Application{Env: env, Fiber: bootstrap.NewFiber(env.Server)}
	rt, err := Setup(context.Background(), app)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	var n int
	if err = d.SQL.QueryRowContext(context.Background(),
		`select count(*) from pg_class c join pg_namespace n on n.oid = c.relnamespace where n.nspname = 'public'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Errorf("the portal created %d relations in public; it must never migrate", n)
	}
}
