package clientip

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
)

func TestCanonical(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"203.0.113.7", "203.0.113.7", true},
		{"  203.0.113.7  ", "203.0.113.7", true},
		{"::ffff:203.0.113.7", "203.0.113.7", true}, // IPv4-mapped IPv6 is the IPv4 address
		{"2001:db8:1:2:aaaa:bbbb:cccc:dddd", "2001:db8:1:2::", true},
		{"2001:DB8:1:2::1", "2001:db8:1:2::", true}, // /64: the interface identifier is dropped
		{"2001:db8:1:2:ffff::9", "2001:db8:1:2::", true},
		{"::1", "::", true},
		// not one address
		{"", "", false},
		{"   ", "", false},
		{"203.0.113.7, 198.51.100.1", "", false}, // a list (X-Forwarded-For style)
		{"203.0.113.7:443", "", false},           // host:port
		{"[2001:db8::1]:443", "", false},
		{"fe80::1%eth0", "", false}, // zone
		{"localhost", "", false},
		{"999.1.1.1", "", false},
		{"1.2.3", "", false},
		{"<script>", "", false},
		{strings.Repeat("1", 70), "", false},
	}
	for _, c := range cases {
		got, ok := Canonical(c.in)
		if ok != c.ok || got != c.want {
			t.Errorf("Canonical(%q) = %q, %v; want %q, %v", c.in, got, ok, c.want, c.ok)
		}
	}
}

func TestPickTrustsTheHeaderOnlyWhenTrusted(t *testing.T) {
	// trusted caller, good header
	if r := Pick(true, "198.51.100.9", "10.0.0.1"); r.Addr != "198.51.100.9" || r.Source != FromHeader {
		t.Errorf("trusted: %+v", r)
	}
	// untrusted caller: the header is ignored
	if r := Pick(false, "198.51.100.9", "10.0.0.1"); r.Addr != "10.0.0.1" || r.Source != FromConnection {
		t.Errorf("untrusted: %+v", r)
	}
	// trusted but garbage: the connection address
	for _, bad := range []string{"", "a, b", "not-an-ip", "1.2.3.4:80"} {
		if r := Pick(true, bad, "10.0.0.1"); r.Addr != "10.0.0.1" || r.Source != FromConnection {
			t.Errorf("trusted, header %q: %+v", bad, r)
		}
	}
	// nothing usable at all
	if r := Pick(true, "", ""); r.Addr != "" || r.Source != FromConnection {
		t.Errorf("empty: %+v", r)
	}
}

// Through Fiber: the Locals flag set by the portal key middleware is what makes the header count.
func TestResolveReadsThePortalKeyFlag(t *testing.T) {
	run := func(internal bool, header, ua string) (Result, string) {
		app := fiber.New()
		var res Result
		var gotUA string
		app.Get("/", func(c *fiber.Ctx) error {
			c.Locals(portalkey.LocalsKey, internal)
			res, gotUA = Resolve(c), UserAgent(c)
			return c.SendStatus(200)
		})
		req := httptest.NewRequest("GET", "/", nil)
		req.Header.Set(Header, header)
		req.Header.Set(UserAgentHeader, ua)
		resp, err := app.Test(req, -1)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		return res, gotUA
	}
	r, ua := run(true, "198.51.100.9", "Mozilla/5.0 (test)")
	if r.Addr != "198.51.100.9" || r.Source != FromHeader || ua != "Mozilla/5.0 (test)" {
		t.Errorf("internal: %+v %q", r, ua)
	}
	r, ua = run(false, "198.51.100.9", "Mozilla/5.0 (test)")
	if r.Source != FromConnection || r.Addr == "198.51.100.9" || ua != "" {
		t.Errorf("not internal: %+v %q", r, ua)
	}
}

func TestCleanUserAgent(t *testing.T) {
	if got := CleanUserAgent("  Mozilla\r\n/5.0\x00 (X)\t"); got != "Mozilla  /5.0  (X)" {
		t.Errorf("controls: %q", got)
	}
	long := strings.Repeat("a", 1000)
	if got := CleanUserAgent(long); len(got) != MaxUserAgent {
		t.Errorf("not cut: %d", len(got))
	}
	// the cut is by characters, never inside one
	if got := CleanUserAgent(strings.Repeat("ກ", 400)); len([]rune(got)) != MaxUserAgent {
		t.Errorf("rune cut: %d", len([]rune(got)))
	}
}
