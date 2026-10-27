package services

import (
	"context"
	"errors"
	"testing"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/pkg/localize"
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
)

type menus map[string][]entities.PublishedMenuItem

func (m menus) Items(_ context.Context, _, key string) ([]entities.PublishedMenuItem, error) {
	return m[key], nil
}

type uiRepo struct{ gotNS []string }

func (u *uiRepo) List(_ context.Context, _ string, ns []string) ([]entities.PublicUIString, error) {
	u.gotNS = ns
	return []entities.PublicUIString{{Key: "site.a", Value: "A"}, {Key: "site.b", Value: "B"}}, nil
}

type noLocales struct{}

func (noLocales) ListActive(context.Context) ([]entities.PublicLocale, error) { return nil, nil }

type noSettings struct{}

func (noSettings) List(context.Context, string, []string) ([]entities.PublicSiteSetting, error) {
	return nil, nil
}

func newSvc(m menus, ui *uiRepo) SiteService {
	return NewSiteService(Deps{Locales: noLocales{}, UIStrings: ui, Settings: noSettings{}, Menus: m,
		Cache: publiccache.New(publiccache.Options{}), Media: media.NewBuilder(media.Config{}), MediaLookup: nil})
}

func neg() localize.Negotiation {
	return localize.Negotiation{Code: "en", Chain: []string{"en", "la"}, ContentLanguage: "en"}
}

func TestMenuItemsAreFlatAndRelFollowsNewTab(t *testing.T) {
	href := "https://x.example"
	parent := int64(1)
	rows := []entities.PublishedMenuItem{
		{ItemID: 1, Label: "Top", Href: &href, OpenInNewTab: true, ActiveMatch: "prefix"},
		{ItemID: 2, Label: "Child", ParentID: &parent},
		{ItemID: 3, Label: "Plain", ActiveMatch: "exact"},
	}
	svc := newSvc(menus{"main": rows}, &uiRepo{})
	m, err := svc.Menu(context.Background(), neg(), "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(m.Items) != 2 || m.Items[0].Label != "Top" || m.Items[1].Label != "Plain" {
		t.Fatalf("items: %+v", m.Items)
	}
	if len(m.Items[0].Rel) != 2 || m.Items[0].Rel[0] != "noopener" || m.Items[1].Rel == nil || len(m.Items[1].Rel) != 0 {
		t.Errorf("rel: %v %v", m.Items[0].Rel, m.Items[1].Rel)
	}
	if m.Locale != "en" || m.Key != "main" {
		t.Errorf("menu: %+v", m)
	}
}

func TestAnEmptyMenuIsNotFoundButSiteBundlesItEmpty(t *testing.T) {
	svc := newSvc(menus{}, &uiRepo{})
	if _, err := svc.Menu(context.Background(), neg(), "nope"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v", err)
	}
	site, err := svc.Site(context.Background(), neg())
	if err != nil {
		t.Fatal(err)
	}
	if site.Menus.Main.Key != "main" || site.Menus.Main.Items == nil || len(site.Menus.Social.Items) != 0 {
		t.Errorf("menus: %+v", site.Menus)
	}
	if site.Settings == nil || site.UI["site.a"] != "A" || site.Media == nil || site.Locales == nil {
		t.Errorf("site must never carry null maps: %+v", site)
	}
}

func TestFiltersAreNormalised(t *testing.T) {
	ui := &uiRepo{}
	svc := newSvc(menus{}, ui)
	if _, err := svc.UIStrings(context.Background(), neg(), []string{"forms", "blog", "forms"}); err != nil {
		t.Fatal(err)
	}
	if len(ui.gotNS) != 2 || ui.gotNS[0] != "blog" || ui.gotNS[1] != "forms" {
		t.Errorf("ns: %v", ui.gotNS)
	}
	if got := uiTags(neg()); len(got) != 3 || got[0] != "ui:en" || got[1] != "ui:la" || got[2] != "locales" {
		t.Errorf("ui tags: %v", got)
	}
	if normalise(nil) != nil {
		t.Error("no filter stays nil")
	}
}
