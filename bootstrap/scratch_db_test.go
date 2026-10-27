//go:build dbtest

package bootstrap

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/dbtest"
	"github.com/materm-organization/materm-web-portal-backend/pkg/testutil/redistest"
)

// connFor turns the URL-form scratch DSN into a DBConnection for user/password.
func connFor(t *testing.T, d *dbtest.Database, user, password string) DBConnection {
	t.Helper()
	u, err := url.Parse(d.DSN)
	if err != nil {
		t.Fatal(err)
	}
	port, _ := strconv.Atoi(u.Port())
	if user == "" {
		user = u.User.Username()
		password, _ = u.User.Password()
	}
	return DBConnection{Host: u.Hostname(), Port: port, User: user, Password: password, DBName: d.Name, SSLMode: "disable"}
}

// CheckRuntimeRole on the scratch cluster: a superuser is refused outside dev and only warned about in dev;
// a plain role passes, and a role that can read the draft table cms_pages is refused outside dev.
func TestCheckRuntimeRole(t *testing.T) {
	d := dbtest.New(t)
	ctx := context.Background()

	super, err := NewPostgresConnection(connFor(t, d, "", ""), PoolConfig{MaxOpenConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = super.Close() })

	if _, err = CheckRuntimeRole(ctx, super.Open(), true); err == nil || !strings.Contains(err.Error(), "superuser") {
		t.Errorf("a superuser must be refused outside dev, got %v", err)
	}
	if err != nil && strings.Contains(err.Error(), "password") && strings.Contains(err.Error(), "=") {
		t.Errorf("the error looks like it carries a value: %v", err)
	}
	if w, err2 := CheckRuntimeRole(ctx, super.Open(), false); err2 != nil || len(w) == 0 {
		t.Errorf("a superuser in dev must be a warning, got err=%v warnings=%v", err2, w)
	}

	// a login role with no privileges
	role := "materm_test_role_" + strings.ToLower(fmt.Sprint(time.Now().UnixNano()))
	pw := "pw" + strconv.FormatInt(time.Now().UnixNano(), 36)
	if _, err = d.SQL.ExecContext(ctx, `create role `+role+` login password '`+pw+`'`); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = super.Open().Exec(`drop owned by ` + role).Error
		_ = super.Open().Exec(`drop role if exists ` + role).Error
	})
	if err = super.Open().Exec(`grant connect on database ` + d.Name + ` to ` + role).Error; err != nil {
		t.Fatal(err)
	}
	low, err := NewPostgresConnection(connFor(t, d, role, pw), PoolConfig{MaxOpenConns: 2})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = low.Close() })

	w, err := CheckRuntimeRole(ctx, low.Open(), true)
	if err != nil {
		t.Fatalf("a plain read-only-ish role must pass: %v", err)
	}
	if len(w) != 1 || !strings.Contains(w[0], PortalRole) {
		t.Errorf("a role other than %s must be warned about, got %v", PortalRole, w)
	}

	// grant SELECT on a cms_pages table: now it can read drafts
	if err = super.Open().Exec(`create table public.cms_pages (id int)`).Error; err != nil {
		t.Fatal(err)
	}
	if err = super.Open().Exec(`grant usage on schema public to ` + role + `; grant select on public.cms_pages to ` + role).Error; err != nil {
		t.Fatal(err)
	}
	if _, err = CheckRuntimeRole(ctx, low.Open(), true); err == nil || !strings.Contains(err.Error(), "cms_pages") {
		t.Errorf("a role that reads cms_pages must be refused outside dev, got %v", err)
	}
}

func redisCfgFor(t *testing.T, r *redistest.Redis) RedisConfig {
	t.Helper()
	opt := r.Client.Options()
	host, portStr, _ := strings.Cut(opt.Addr, ":")
	port, _ := strconv.Atoi(portStr)
	return RedisConfig{Host: host, Port: port, Username: opt.Username, Password: opt.Password, DB: opt.DB}
}

// redis.clearOnStart deletes the keys of THIS service's prefix and nothing else (the Redis is shared), and only
// outside strict mode.
func TestInitializeRedisClearOnStartOwnPrefixOnly(t *testing.T) {
	r := redistest.New(t)
	ctx := context.Background()
	own := "materm-portal:" + "t-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	foreign := r.Key("foreign-admin-like")
	adminKey := "materm-admin:test-" + strconv.FormatInt(time.Now().UnixNano(), 36)
	for _, k := range []string{own, foreign, adminKey} {
		if err := r.Client.Set(ctx, k, "v", time.Minute).Err(); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { r.Client.Del(context.Background(), own, adminKey) })

	cfg := redisCfgFor(t, r)
	cfg.ClearOnStart = true
	rdb, err := InitializeRedis(cfg, true) // strict: no clear
	if err != nil || rdb == nil {
		t.Fatalf("connect: %v", err)
	}
	if n := r.Client.Exists(ctx, own).Val(); n != 1 {
		t.Error("clearOnStart must not run in strict mode")
	}
	_ = rdb.Close()

	rdb, err = InitializeRedis(cfg, false)
	if err != nil || rdb == nil {
		t.Fatalf("connect: %v", err)
	}
	defer func() { _ = rdb.Close() }()
	if r.Client.Exists(ctx, own).Val() != 0 {
		t.Error("a key under materm-portal: must be cleared")
	}
	if r.Client.Exists(ctx, foreign, adminKey).Val() != 2 {
		t.Error("keys outside the service prefix (the admin backend's included) must survive")
	}
}

// Unreachable Redis: strict is an error, dev degrades to (nil, nil).
func TestInitializeRedisUnreachable(t *testing.T) {
	cfg := RedisConfig{Host: "127.0.0.1", Port: 1} // nothing listens on port 1
	if rdb, err := InitializeRedis(cfg, true); err == nil || rdb != nil {
		t.Errorf("strict: want an error, got rdb=%v err=%v", rdb, err)
	}
	if rdb, err := InitializeRedis(cfg, false); err != nil || rdb != nil {
		t.Errorf("dev: want (nil, nil), got rdb=%v err=%v", rdb, err)
	}
}
