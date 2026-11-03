package rediskeys

import (
	"strings"
	"testing"
	"time"

	constants "github.com/materm-organization/materm-web-portal-backend/pkg/constants/services"
)

// TestEveryKeyCarriesThePrefix: the Redis is shared with other projects, so every key this service writes
// starts with the service prefix (materm-portal: by default), whatever the key family.
func TestEveryKeyCarriesThePrefix(t *testing.T) {
	if constants.AppKeyPrefix != "materm-portal:" {
		t.Fatalf("AppKeyPrefix = %q", constants.AppKeyPrefix)
	}
	now := time.Date(2026, 10, 27, 12, 30, 0, 0, time.UTC)
	for _, prefix := range []string{constants.AppKeyPrefix, "materm-portal:test-abc:"} {
		k := New(prefix)
		for name, key := range map[string]string{
			"Gen": k.Gen(), "Inv": k.Inv(), "Cache": k.Cache(3, "page", "en", "abc"), "Tag": k.Tag("pages"), "Locales": k.Locales(),
			"Preview":      k.Preview("deadbeef"),
			"FormWindowRL": k.FormWindowRL("contact", "abc", now), "FormDailyRL": k.FormDailyRL("contact", "abc", now),
			"IPSalt": k.IPSalt(now), "FormKeyMinuteRL": k.FormKeyMinuteRL("abcdef0123456789", now),
			"FormKeyDailyRL": k.FormKeyDailyRL("abcdef0123456789", now), "FormDup": k.FormDup("contact", "i", "h"),
			"PreviewTokenRL": k.PreviewTokenRL("abc", now), "PreviewKeyRL": k.PreviewKeyRL("abcdef0123456789", now),
		} {
			if !strings.HasPrefix(key, prefix) || key == prefix {
				t.Errorf("%s(%q) = %q does not carry the prefix", name, prefix, key)
			}
		}
	}
	if got := New("").Prefix(); got != constants.AppKeyPrefix {
		t.Errorf("the default prefix is %q, want %q", got, constants.AppKeyPrefix)
	}
	if strings.HasPrefix(constants.AppKeyPrefix, "materm-admin:") {
		t.Error("the portal must never share the admin key prefix")
	}
}

func TestKeyShapes(t *testing.T) {
	k := New("")
	now := time.Date(2026, 10, 27, 12, 30, 45, 0, time.UTC)
	want := map[string]string{
		k.Gen():                       "materm-portal:gen",
		k.Cache(3, "page", "la", "h"): "materm-portal:c:3:page:la:h",
		k.Tag("posts:list"):           "materm-portal:tag:posts:list",
		k.Locales():                   "materm-portal:locales",
		k.Inv():                       "materm-portal:inv",
		k.Preview("ab"):               "materm-portal:preview:ab",
		k.FormDailyRL("c", "i", now):  "materm-portal:rl:form:c:i:d:20261027",
		k.IPSalt(now):                 "materm-portal:ipsalt:20261027",
		k.FormKeyMinuteRL("kid", now): "materm-portal:rl:form-key:kid:m:202610271230",
		k.FormKeyDailyRL("kid", now):  "materm-portal:rl:form-key:kid:d:20261027",
		k.FormDup("c", "ip", "h32"):   "materm-portal:rl:form:c:ip:dup:h32",
		k.PreviewTokenRL("h32", now):  "materm-portal:rl:preview:h32:202610271230",
		k.PreviewKeyRL("kid", now):    "materm-portal:rl:preview-key:kid:202610271230",
	}
	for got, exp := range want {
		if got != exp {
			t.Errorf("got %q, want %q", got, exp)
		}
	}
}
