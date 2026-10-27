package entities

// All returns one zero value of every entity (for the convention test, the column drift test and tooling).
// PublishedPostCard and PublishedPost read the same view; together they cover all of its columns.
func All() []any {
	return []any{
		PublicLocale{}, PublicUIString{}, PublicSiteSetting{}, PublishedMenuItem{},
		PublishedPage{}, PublishedPageSection{},
		PublishedCategory{}, PublishedTag{},
		PublishedPostCard{}, PublishedPost{}, PublishedPostTag{}, PublishedPostSlug{}, PublishedPostMedia{},
		PublishedAlbum{}, PublishedAlbumItem{}, PublishedFAQItem{},
		PublishedForm{}, PublishedFormField{}, PublishedCollectionEntry{},
		SitemapEntry{}, ActiveRedirect{}, PublicAsset{},
	}
}
