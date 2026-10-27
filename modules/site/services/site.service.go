// Package services is the logic of the site module: P-01 locales, P-02 settings, P-03 UI strings, P-04 menus
// and P-05 the layout bundle. Each method answers one cached response (pkg/publiccache) and names the public
// tags it depends on (doc/08 section 2.8).
package services

import (
	"context"
	"errors"
	"sort"
	"strings"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/modules/site/models"
	"github.com/materm-organization/materm-web-portal-backend/pkg/assemble"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
)

// ErrNotFound is returned for a menu that has no visible item (an unknown key and an empty menu look the same
// in v_published_menu_items).
var ErrNotFound = errors.New("site: not found")

// LayoutMenus are the menus P-05 bundles.
var LayoutMenus = []string{"main", "footer", "social"}

// SiteService serves the site endpoints. neg is the negotiated locale (localize.Negotiation).
type SiteService interface {
	Locales(ctx context.Context) ([]models.LocaleInfo, error)
	Settings(ctx context.Context, neg localize.Negotiation, groups []string) (*models.SettingsData, error)
	UIStrings(ctx context.Context, neg localize.Negotiation, namespaces []string) (*models.UIStringsData, error)
	Menu(ctx context.Context, neg localize.Negotiation, key string) (*models.MenuResponse, error)
	Site(ctx context.Context, neg localize.Negotiation) (*models.SiteData, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	Locales     repositories.LocaleRepository
	UIStrings   repositories.UIStringRepository
	Settings    repositories.SettingRepository
	Menus       repositories.MenuRepository
	Cache       *publiccache.Cache
	Media       *media.Builder
	MediaLookup media.Lookup
}

type siteService struct{ d Deps }

// NewSiteService builds the service.
func NewSiteService(d Deps) SiteService { return &siteService{d: d} }

// Cache resources (publiccache.Spec.Resource). "locales" itself belongs to the locale chain of the application.
const (
	resLocales  = "locales.public"
	resSettings = "settings"
	resUI       = "ui"
	resMenu     = "menu"
	resSite     = "site"
)

func (s *siteService) Locales(ctx context.Context) ([]models.LocaleInfo, error) {
	v, _, err := publiccache.Get(ctx, s.d.Cache, publiccache.Spec{Resource: resLocales, Locale: "-"},
		func(ctx context.Context) ([]models.LocaleInfo, []string, error) {
			rows, err := s.d.Locales.ListActive(ctx)
			if err != nil {
				return nil, nil, err
			}
			return assemble.LocaleInfos(rows), []string{"locales"}, nil
		})
	return v, err
}

func (s *siteService) Settings(ctx context.Context, neg localize.Negotiation, groups []string) (*models.SettingsData, error) {
	groups = normalise(groups)
	v, _, err := publiccache.Get(ctx, s.d.Cache,
		publiccache.Spec{Resource: resSettings, Locale: neg.Code, Params: map[string]string{"group": strings.Join(groups, ",")}},
		func(ctx context.Context) (*models.SettingsData, []string, error) {
			d, err := s.loadSettings(ctx, neg.Code, groups)
			return d, []string{"settings", "locales"}, err
		})
	return v, err
}

func (s *siteService) loadSettings(ctx context.Context, locale string, groups []string) (*models.SettingsData, error) {
	rows, err := s.d.Settings.List(ctx, locale, groups)
	if err != nil {
		return nil, err
	}
	settings, ids := assemble.BuildSettings(rows)
	m, err := s.d.Media.Resolve(ctx, s.d.MediaLookup, locale, ids)
	if err != nil {
		return nil, err
	}
	return &models.SettingsData{Locale: locale, Settings: settings, Media: m}, nil
}

func (s *siteService) UIStrings(ctx context.Context, neg localize.Negotiation, namespaces []string) (*models.UIStringsData, error) {
	namespaces = normalise(namespaces)
	v, _, err := publiccache.Get(ctx, s.d.Cache,
		publiccache.Spec{Resource: resUI, Locale: neg.Code, Params: map[string]string{"ns": strings.Join(namespaces, ",")}},
		func(ctx context.Context) (*models.UIStringsData, []string, error) {
			d, err := s.loadUI(ctx, neg.Code, namespaces)
			return d, uiTags(neg), err
		})
	return v, err
}

// uiTags: ui:<locale> for every locale of the chain (a string missing in "en" is answered from "la", so a
// change of "la" must also clear "en"), and locales.
func uiTags(neg localize.Negotiation) []string {
	tags := make([]string, 0, len(neg.Chain)+1)
	for _, c := range neg.Chain {
		tags = append(tags, "ui:"+c)
	}
	return append(tags, "locales")
}

func (s *siteService) loadUI(ctx context.Context, locale string, namespaces []string) (*models.UIStringsData, error) {
	rows, err := s.d.UIStrings.List(ctx, locale, namespaces)
	if err != nil {
		return nil, err
	}
	strs := make(map[string]string, len(rows))
	for _, r := range rows {
		strs[r.Key] = r.Value
	}
	return &models.UIStringsData{Locale: locale, Strings: strs}, nil
}

func (s *siteService) Menu(ctx context.Context, neg localize.Negotiation, key string) (*models.MenuResponse, error) {
	v, _, err := publiccache.Get(ctx, s.d.Cache,
		publiccache.Spec{Resource: resMenu, Locale: neg.Code, Params: map[string]string{"key": key}},
		func(ctx context.Context) (*models.MenuResponse, []string, error) {
			rows, err := s.d.Menus.Items(ctx, neg.Code, key)
			if err != nil {
				return nil, nil, err
			}
			if len(rows) == 0 {
				return nil, nil, ErrNotFound
			}
			return &models.MenuResponse{Locale: neg.Code, Key: key, Items: menuItems(rows)}, []string{"menu:" + key, "pages", "locales"}, nil
		})
	return v, err
}

// menuItems maps the top-level items. Menus are flat in P1: nested items (parent_id set) are not part of the
// contract of the frontend's MenuItem and are left out.
func menuItems(rows []entities.PublishedMenuItem) []models.MenuItem {
	items := make([]models.MenuItem, 0, len(rows))
	for _, r := range rows {
		if r.ParentID != nil {
			continue
		}
		rel := []string{}
		if r.OpenInNewTab {
			rel = []string{"noopener", "noreferrer"}
		}
		items = append(items, models.MenuItem{ID: r.ItemID, Label: r.Label, AriaLabel: r.AriaLabel, Href: r.Href, Icon: r.Icon,
			OpenInNewTab: r.OpenInNewTab, Rel: rel, ActiveMatch: r.ActiveMatch})
	}
	return items
}

func (s *siteService) Site(ctx context.Context, neg localize.Negotiation) (*models.SiteData, error) {
	v, _, err := publiccache.Get(ctx, s.d.Cache, publiccache.Spec{Resource: resSite, Locale: neg.Code},
		func(ctx context.Context) (*models.SiteData, []string, error) {
			lrows, err := s.d.Locales.ListActive(ctx)
			if err != nil {
				return nil, nil, err
			}
			st, err := s.loadSettings(ctx, neg.Code, nil)
			if err != nil {
				return nil, nil, err
			}
			ui, err := s.loadUI(ctx, neg.Code, []string{"site"})
			if err != nil {
				return nil, nil, err
			}
			menus := map[string]models.MenuData{}
			for _, key := range LayoutMenus {
				rows, err := s.d.Menus.Items(ctx, neg.Code, key)
				if err != nil {
					return nil, nil, err
				}
				menus[key] = models.MenuData{Key: key, Items: menuItems(rows)}
			}
			tags := append([]string{"settings", "locales", "pages"}, uiTags(neg)...)
			for _, key := range LayoutMenus {
				tags = append(tags, "menu:"+key)
			}
			return &models.SiteData{
				Locale: neg.Code, Locales: assemble.LocaleInfos(lrows), Settings: st.Settings,
				Menus: models.SiteMenus{Main: menus["main"], Footer: menus["footer"], Social: menus["social"]},
				UI:    ui.Strings, Media: st.Media,
			}, tags, nil
		})
	return v, err
}

// normalise sorts and de-duplicates a filter list (the cache key must not depend on the order).
func normalise(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, v := range in {
		if _, dup := seen[v]; !dup {
			seen[v] = struct{}{}
			out = append(out, v)
		}
	}
	sort.Strings(out)
	return out
}
