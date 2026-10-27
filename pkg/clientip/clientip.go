// Package clientip finds the address of the VISITOR for rate limiting and for the daily IP hash of a form
// submission (doc/08 sections 2.10 and 6.3).
//
// The portal API is called by the portal-frontend server, so the connection address is always the same
// (Netlify's egress). The visitor's address travels in the X-Client-IP header that the server action sets.
// That header is trusted ONLY because the caller proved it holds the portal key (portalkey.Internal): a
// caller without the key never reaches a handler, and the check is repeated here so that the package is safe
// on its own. A header that is not exactly one IP address (a list, a port, text, an oversize value) is
// ignored and the connection address is used instead; the answer never fails.
//
// An IPv6 address is reduced to its /64 network: one subscriber controls a whole /64, so counting full
// addresses would let a client dodge a rate limit by rotating the interface identifier.
package clientip

import (
	"net/netip"
	"strings"

	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/portalkey"
)

// Header carries the visitor's address, set by the portal-frontend server.
const Header = "X-Client-IP"

// UserAgentHeader carries the visitor's User-Agent, set by the portal-frontend server.
const UserAgentHeader = "X-Client-User-Agent"

// maxHeaderLen bounds what is parsed (the longest textual IPv6 address is 45 bytes).
const maxHeaderLen = 64

// MaxUserAgent is the longest user agent that is kept (the database column holds 300 characters).
const MaxUserAgent = 300

// Source says where the address came from.
type Source string

const (
	// FromHeader: X-Client-IP of a caller that holds the portal key.
	FromHeader Source = "header"
	// FromConnection: the connection address (or the one Fiber derives through server.trustedProxies).
	FromConnection Source = "connection"
)

// Result is the visitor's address in canonical form and where it came from.
type Result struct {
	// Addr is canonical: IPv4 dotted, IPv6 as the /64 network address, IPv4-mapped IPv6 unmapped. Empty only
	// when even the connection address is unusable.
	Addr   string
	Source Source
}

// Resolve returns the visitor's address for the request.
func Resolve(c *fiber.Ctx) Result {
	return Pick(portalkey.Internal(c), c.Get(Header), c.IP())
}

// Pick is Resolve over plain values: trusted says whether the header may be believed.
func Pick(trusted bool, header, connection string) Result {
	if trusted {
		if a, ok := Canonical(header); ok {
			return Result{Addr: a, Source: FromHeader}
		}
	}
	a, _ := Canonical(connection)
	return Result{Addr: a, Source: FromConnection}
}

// Canonical parses exactly one IP address and returns its canonical form (see the package comment). ok=false
// for anything else: empty, a list, a host:port, a zone, text.
func Canonical(s string) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || len(s) > maxHeaderLen {
		return "", false
	}
	a, err := netip.ParseAddr(s)
	if err != nil || a.Zone() != "" {
		return "", false
	}
	a = a.Unmap()
	if a.Is6() {
		p, err := a.Prefix(64)
		if err != nil {
			return "", false
		}
		return p.Addr().String(), true
	}
	return a.String(), true
}

// UserAgent returns the visitor's user agent from the trusted header, cut to MaxUserAgent characters with
// control characters replaced by spaces. "" when absent or the caller is not trusted.
func UserAgent(c *fiber.Ctx) string {
	if !portalkey.Internal(c) {
		return ""
	}
	return CleanUserAgent(c.Get(UserAgentHeader))
}

// CleanUserAgent is the sanitising part of UserAgent.
func CleanUserAgent(s string) string {
	var b strings.Builder
	n := 0
	for _, r := range s {
		if n >= MaxUserAgent {
			break
		}
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r < 0xa0) {
			r = ' '
		}
		b.WriteRune(r)
		n++
	}
	return strings.TrimSpace(b.String())
}
