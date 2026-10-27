package services

import (
	"testing"
	"time"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
)

func TestItemFromCardAltAndDates(t *testing.T) {
	hero, teaser, alt := int64(5), int64(6), "Hero alt"
	published := time.Date(2026, 9, 5, 20, 0, 0, 0, time.UTC) // 6 Sep in Vientiane
	fm := readkit.DateFormats{Short: "{month}. {day}.{year}", Months: []string{"Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"}}

	// the card falls back to the hero (no teaser): the hero's alt describes it
	c := entities.PublishedPostCard{PostID: 1, Slug: "a-b", HeroMediaID: &hero, CardMediaID: &hero, HeroAlt: &alt, PublishedAt: &published}
	it := ItemFromCard(c, fm)
	if it.Image == nil || it.Image.MediaID != 5 || it.Image.Alt == nil || *it.Image.Alt != alt {
		t.Errorf("hero card: %+v", it.Image)
	}
	if it.Href != "/blog/a-b" || it.Date != "Sep. 06.2026" || it.DateTime != "2026-09-05T20:00:00Z" {
		t.Errorf("href/date: %q %q %q", it.Href, it.Date, it.DateTime)
	}

	// a teaser is a different picture: the hero's alt does not describe it (the asset's own alt is used)
	c.TeaserMediaID, c.CardMediaID = &teaser, &teaser
	if it = ItemFromCard(c, fm); it.Image == nil || it.Image.MediaID != 6 || it.Image.Alt != nil {
		t.Errorf("teaser card: %+v", it.Image)
	}

	// no picture at all
	c.CardMediaID, c.TeaserMediaID, c.HeroMediaID = nil, nil, nil
	if it = ItemFromCard(c, fm); it.Image != nil {
		t.Errorf("no picture: %+v", it.Image)
	}
}
