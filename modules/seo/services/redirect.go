package services

import (
	"context"
	"errors"
	"strings"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/modules/seo/models"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
)

// ErrRedirectNotFound: no active rule matches the path (or the chain is a loop).
var ErrRedirectNotFound = errors.New("seo: no redirect")

const (
	resRedirect = "redirect"
	// MaxRedirectHops is how many rules a chain may follow (BR-26: the admin refuses longer chains; the limit is
	// here as well because the data may have been edited under the check).
	MaxRedirectHops = 3
	// NegativeRedirectTTL is how long "no redirect for this path" is cached, so that 404 traffic does not reach
	// the database once per request.
	NegativeRedirectTTL = 30 * time.Second
)

// MaxPathLen is the longest path (after normalising) that is looked up.
const MaxPathLen = 512

// NormalizePath brings a request path to the form of cms_redirects.source_path: the query string and fragment
// are dropped, the text is lower-cased and a trailing slash is removed (the root stays "/"). The result is not
// validated; the caller checks that it starts with "/" and that it is not too long.
func NormalizePath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	p = strings.ToLower(p)
	if strings.HasPrefix(p, "/") {
		p = strings.TrimRight(p, "/")
		if p == "" {
			p = "/"
		}
	}
	return p
}

func isAbsoluteURL(t string) bool {
	l := strings.ToLower(t)
	return strings.HasPrefix(l, "https://") || strings.HasPrefix(l, "http://")
}

// Redirect answers P-17 for a normalised path in a locale. Rules of the locale win over rules for all locales;
// internal targets are followed on the server up to MaxRedirectHops; a chain that comes back to a path it has
// already visited (a loop) answers not found, because sending the visitor to its first hop would loop the
// browser; a chain longer than the limit stops at the last allowed hop (the visitor's next request continues).
// Found and not-found answers are cached under the tag "redirects".
func (s *seoService) Redirect(ctx context.Context, locale, path string) (*models.RedirectData, error) {
	out, _, err := publiccache.Get(ctx, s.d.Cache,
		publiccache.Spec{
			Resource: resRedirect, Locale: locale, Params: map[string]string{"path": path},
			TTLFor: func(v any) time.Duration {
				if o, ok := v.(models.RedirectOutcome); ok && !o.Found {
					return NegativeRedirectTTL
				}
				return 0
			},
		},
		func(ctx context.Context) (models.RedirectOutcome, []string, error) {
			d, err := s.follow(ctx, locale, path)
			if errors.Is(err, ErrRedirectNotFound) {
				return models.RedirectOutcome{}, []string{"redirects"}, nil
			}
			if err != nil {
				return models.RedirectOutcome{}, nil, err
			}
			return models.RedirectOutcome{Found: true, Data: d}, []string{"redirects"}, nil
		})
	if err != nil {
		return nil, err
	}
	if !out.Found || out.Data == nil {
		return nil, ErrRedirectNotFound
	}
	return out.Data, nil
}

func (s *seoService) follow(ctx context.Context, locale, start string) (*models.RedirectData, error) {
	visited := map[string]bool{start: true}
	cur := start
	var last *models.RedirectData
	for hops := 0; hops < MaxRedirectHops; {
		r, err := s.d.SEO.Redirect(ctx, cur, locale)
		if errors.Is(err, repositories.ErrNotFound) {
			break
		}
		if err != nil {
			return nil, err
		}
		hops++
		last = &models.RedirectData{Target: r.Target, StatusCode: r.StatusCode, Hops: hops}
		if isAbsoluteURL(r.Target) {
			break
		}
		next := NormalizePath(r.Target)
		if visited[next] {
			return nil, ErrRedirectNotFound
		}
		visited[next] = true
		cur = next
	}
	if last == nil {
		return nil, ErrRedirectNotFound
	}
	return last, nil
}
