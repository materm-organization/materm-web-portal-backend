//go:build dbtest

package application

import (
	"context"
	"net/url"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/bootstrap"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/rediskeys"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/redistest"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/schemadb"
)

func TestMain(m *testing.M) {
	code := m.Run()
	schemadb.Teardown()
	os.Exit(code)
}

// The whole read path on the real schema: the read-only role, Setup, the locale chain through the response
// cache, and the "locales" invalidation.
func TestLocaleChainThroughTheCacheOnTheRealSchema(t *testing.T) {
	s := schemadb.Shared(t)
	r := redistest.New(t)
	ctx := context.Background()

	u, err := url.Parse(s.RODSN)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	pw, _ := u.User.Password()
	env := &bootstrap.Env{
		Server:   bootstrap.ServerConfig{Port: 3000, Env: bootstrap.EnvDev},
		Database: bootstrap.DatabaseConfig{Admin: bootstrap.DBConnection{Host: u.Hostname(), Port: port, User: u.User.Username(), Password: pw, DBName: s.DB.Name, SSLMode: "disable"}},
		Portal:   bootstrap.PortalConfig{FrontendKeys: []string{testKey}},
		Cache:    bootstrap.CacheConfig{Enabled: false}, // no subscriber here; the cache is swapped for a twin below
		File:     bootstrap.FileConfig{PathIP: "https://files.example.test"},
	}
	env.Defaults()
	app := &bootstrap.Application{Env: env, Fiber: bootstrap.NewFiber(env.Server), Redis: r.Client}
	rt, err := Setup(ctx, app)
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	// The wiring's cache would use the production prefix, which another test clears on the shared scratch
	// Redis: swap in a twin under this test's random prefix (the locale fetcher reads repos.PublicCache at call time).
	rt.Repos.PublicCache = publiccache.New(publiccache.Options{Client: r.Client, Keys: rediskeys.New(r.Prefix), Enabled: true, TTL: time.Minute})

	chain, err := rt.Services.Locales.Chain(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if chain.Default() != "la" || strings.Join(chain.For("en"), ",") != "en,la" || strings.Join(chain.Codes(), ",") != "en,la" {
		t.Errorf("chain: %v", chain)
	}
	n, aerr := chain.Negotiate("lo", "")
	if aerr != nil || n.Code != "la" || n.ContentLanguage != "lo" {
		t.Errorf("%+v %v", n, aerr)
	}

	// it was stored under the generation cache, with the "locales" tag
	members, err := r.Client.SMembers(ctx, rediskeys.New(r.Prefix).Tag("locales")).Result()
	if err != nil || len(members) != 1 || !strings.Contains(members[0], ":locales:-:") {
		t.Fatalf("tag set: %v %v", members, err)
	}

	// media builder and lookup are wired from file.path_ip / media.staticPrefix
	if u, ok := rt.Services.Media.URL("uploads/a.png"); !ok || u != "https://files.example.test/uploads/a.png" {
		t.Errorf("media url %q", u)
	}
	if rt.Services.MediaLookup == nil || rt.Repos.Content == nil || rt.Repos.PublicCache == nil {
		t.Error("PB-3/PB-4 wiring is incomplete")
	}
}
