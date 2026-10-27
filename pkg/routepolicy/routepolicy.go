// Package routepolicy makes "every route declares who may call it" (NFR-12) a property a test can check.
// Routes are registered ONLY through a Group:
//
//	root := routepolicy.NewRoot(app, reg)                              // probes: Public()
//	api  := routepolicy.NewAPI(app, "/api/v1", portalkey.Middleware(), reg) // everything else: PortalKey()
//	api.Get("/public/site", routepolicy.PortalKey(), ctrl.Site)
//
// The portal API is reached only server-to-server (doc/00 section 10.4), so there are exactly two policies:
//
//   - PortalKey: the caller must send a valid X-Portal-Key. The guard is mounted ONCE on the whole API group
//     (router.Group(prefix, guard)), before any route, so a path that matches no route answers 401 without a
//     key and 404 with one: route names do not leak to a caller without the key.
//   - Public: no credential (the probes /health and /ready at the root, which orchestrators call).
//
// A Group refuses a policy that does not match where it is mounted (Public inside the guarded API group,
// PortalKey outside it): the policy a route declares is therefore always the one enforced. UncoveredRoutes
// compares the Fiber route table with the Registry, so a route added with a raw app.Get fails the coverage
// test.
package routepolicy

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/gofiber/fiber/v2"
)

// Kind is how a route is protected.
type Kind int

const (
	// KindPublic: no credential (probes).
	KindPublic Kind = iota
	// KindPortalKey: a valid X-Portal-Key.
	KindPortalKey
)

// Policy declares the protection of one route.
type Policy struct{ Kind Kind }

// Public is a route without a credential (liveness / readiness probes only).
func Public() Policy { return Policy{Kind: KindPublic} }

// PortalKey is a route that needs a valid X-Portal-Key.
func PortalKey() Policy { return Policy{Kind: KindPortalKey} }

// String renders the policy ("-" or "portal-key").
func (p Policy) String() string {
	if p.Kind == KindPortalKey {
		return "portal-key"
	}
	return "-"
}

// Entry is one registered route.
type Entry struct {
	Method string
	Path   string // full path template, e.g. "/api/v1/public/pages/:id"
	Policy Policy
}

// Registry records the policy of every route registered through a Group.
type Registry struct {
	mu      sync.Mutex
	entries []Entry
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{} }

func (r *Registry) add(e Entry) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.entries = append(r.entries, e)
}

// Entries returns the registered routes sorted by path then method.
func (r *Registry) Entries() []Entry {
	r.mu.Lock()
	out := append([]Entry(nil), r.entries...)
	r.mu.Unlock()
	sort.Slice(out, func(i, j int) bool {
		if out[i].Path != out[j].Path {
			return out[i].Path < out[j].Path
		}
		return out[i].Method < out[j].Method
	})
	return out
}

// Lookup returns the entry of method+path.
func (r *Registry) Lookup(method, path string) (Entry, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	for _, e := range r.entries {
		if e.Method == method && e.Path == path {
			return e, true
		}
	}
	return Entry{}, false
}

// Describe renders the registry as text, one route per line (used in test failures).
func (r *Registry) Describe() string {
	var b strings.Builder
	for _, e := range r.Entries() {
		fmt.Fprintf(&b, "%-6s %-45s %s\n", e.Method, e.Path, e.Policy)
	}
	return b.String()
}

// Group registers routes under a path prefix.
type Group struct {
	router fiber.Router
	prefix string
	reg    *Registry
	// guarded is true inside the API group (the portal key guard is mounted on it).
	guarded bool
}

// NewRoot creates the Group of the unauthenticated root (probes): only Public routes are accepted.
func NewRoot(app fiber.Router, reg *Registry) *Group {
	return &Group{router: app, reg: reg}
}

// NewAPI creates the API Group under prefix ("/api/v1"). guard (the portal key middleware) is mounted on
// the prefix before any route, so it covers every path below it, known or not. Only PortalKey routes are
// accepted. A nil guard panics: an API group without a guard must not exist.
func NewAPI(app fiber.Router, prefix string, guard fiber.Handler, reg *Registry) *Group {
	if guard == nil {
		panic("routepolicy: the API group needs a portal key guard")
	}
	return &Group{router: app.Group(prefix, guard), prefix: prefix, reg: reg, guarded: true}
}

// Group returns a sub-group under prefix (same rules as its parent).
func (g *Group) Group(prefix string) *Group {
	return &Group{router: g.router.Group(prefix), prefix: g.prefix + prefix, reg: g.reg, guarded: g.guarded}
}

// Handle registers one route with its policy. It panics (at start-up, so a test or the first boot fails)
// when the policy does not match the group.
func (g *Group) Handle(method, path string, p Policy, handlers ...fiber.Handler) {
	full := g.prefix + path
	if len(handlers) == 0 {
		panic("routepolicy: route without a handler: " + method + " " + full)
	}
	switch {
	case g.guarded && p.Kind != KindPortalKey:
		panic("routepolicy: " + method + " " + full + " is inside the portal-key group and cannot be Public")
	case !g.guarded && p.Kind != KindPublic:
		panic("routepolicy: " + method + " " + full + " declares PortalKey outside the guarded API group")
	}
	g.router.Add(method, path, handlers...)
	g.reg.add(Entry{Method: method, Path: full, Policy: p})
}

// Get registers a GET route.
func (g *Group) Get(path string, p Policy, h ...fiber.Handler) {
	g.Handle(fiber.MethodGet, path, p, h...)
}

// Post registers a POST route.
func (g *Group) Post(path string, p Policy, h ...fiber.Handler) {
	g.Handle(fiber.MethodPost, path, p, h...)
}

// UncoveredRoutes returns the routes of app (method + path) that are not in the registry: the coverage test
// fails when it is not empty. Middleware-only (USE) routes are not listed by Fiber; HEAD routes that Fiber
// derives from a registered GET are counted as covered by it.
func UncoveredRoutes(app *fiber.App, reg *Registry) []string {
	var out []string
	for _, r := range app.GetRoutes(true) {
		if _, ok := reg.Lookup(r.Method, r.Path); ok {
			continue
		}
		if r.Method == fiber.MethodHead {
			if _, ok := reg.Lookup(fiber.MethodGet, r.Path); ok {
				continue
			}
		}
		out = append(out, r.Method+" "+r.Path)
	}
	sort.Strings(out)
	return out
}
