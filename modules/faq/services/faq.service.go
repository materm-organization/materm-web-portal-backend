// Package services holds the logic of the faq module (P-13).
package services

import (
	"context"
	"strconv"

	"github.com/materm-organization/materm-web-portal-backend/modules/content/entities"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/readkit"
	"github.com/materm-organization/materm-web-portal-backend/modules/content/repositories"
	"github.com/materm-organization/materm-web-portal-backend/modules/faq/models"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/publiccache"
	"github.com/materm-organization/materm-web-portal-backend/pkg/richtext"
)

// FaqService serves P-13. locale is already negotiated.
type FaqService interface {
	// Group returns the visible items of a published group; 404 when the key names no published group - or a
	// published group without a visible item, which the view cannot tell apart from an unknown key.
	Group(ctx context.Context, locale, key string) (*models.FaqGroup, error)
}

// Deps are the collaborators of the service.
type Deps struct {
	Content *repositories.Set
	Cache   *publiccache.Cache
}

type faqService struct{ d Deps }

// NewFaqService builds the service.
func NewFaqService(d Deps) FaqService { return &faqService{d: d} }

func (s *faqService) Group(ctx context.Context, locale, key string) (*models.FaqGroup, error) {
	if !readkit.KeyPattern.MatchString(key) {
		return nil, apperror.NotFound()
	}
	g, _, err := publiccache.Get(ctx, s.d.Cache,
		publiccache.Spec{Resource: "faq", Locale: locale, Params: map[string]string{"key": key}},
		func(ctx context.Context) (*models.FaqGroup, []string, error) {
			rows, err := s.d.Content.FAQ.Items(ctx, locale, key)
			if err != nil {
				return nil, nil, err
			}
			if len(rows) == 0 {
				return nil, nil, apperror.NotFound()
			}
			docs := make([]any, 0, len(rows))
			for _, r := range rows {
				docs = append(docs, readkit.DecodeJSON(r.Answer.Bytes()))
			}
			links, err := readkit.ResolveLinks(ctx, s.d.Content, locale, docs...)
			if err != nil {
				return nil, nil, err
			}
			group := &models.FaqGroup{Locale: locale, GroupKey: key, ResolvedLocale: groupLocale(rows, locale), Items: make([]models.FaqItem, 0, len(rows))}
			for _, r := range rows {
				res := richtext.ResolveJSON(r.Answer.Bytes(), richtext.Lite, links.MapResolver)
				group.Items = append(group.Items, models.FaqItem{ID: r.FAQItemID, Question: r.Question, Answer: res.Doc})
			}
			tags := []string{"faq:" + strconv.FormatInt(rows[0].GroupID, 10), "locales"}
			if links.UsedPages {
				tags = append(tags, "pages")
			}
			if links.UsedPosts {
				tags = append(tags, "posts:list")
			}
			return group, tags, nil
		})
	return g, err
}

func groupLocale(rows []entities.PublishedFAQItem, requested string) string {
	for _, r := range rows {
		if r.ResolvedLocale == requested {
			return requested
		}
	}
	return rows[0].ResolvedLocale
}
