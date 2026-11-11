package entities

import "time"

// ---- site ----------------------------------------------------------------------------------------------------

// PublicLocale is a row of v_public_locales (active locales only).
type PublicLocale struct {
	Code          string  `gorm:"column:code"`
	Name          string  `gorm:"column:name"`
	NativeName    string  `gorm:"column:native_name"`
	ShortLabel    string  `gorm:"column:short_label"`
	Flag          *string `gorm:"column:flag"`
	TextDirection string  `gorm:"column:text_direction"`
	IsDefault     bool    `gorm:"column:is_default"`
	FallbackCode  *string `gorm:"column:fallback_code"`
	DateFormats   JSONB   `gorm:"column:date_formats"`
	SortOrder     int     `gorm:"column:sort_order"`
}

func (PublicLocale) TableName() string { return "v_public_locales" }

// PublicUIString is a row of v_public_ui_strings: one resolved override per key.
type PublicUIString struct {
	RequestedLocale string  `gorm:"column:requested_locale"`
	Key             string  `gorm:"column:key"`
	Namespace       *string `gorm:"column:namespace"`
	Value           string  `gorm:"column:value"`
	ResolvedLocale  string  `gorm:"column:resolved_locale"`
}

func (PublicUIString) TableName() string { return "v_public_ui_strings" }

// PublicSiteSetting is a row of v_public_site_settings. Value is the JSON value (a JSON string for localised
// settings); it is NULL for media settings, whose reference is MediaAssetID.
type PublicSiteSetting struct {
	RequestedLocale string `gorm:"column:requested_locale"`
	GroupKey        string `gorm:"column:group_key"`
	Key             string `gorm:"column:key"`
	ValueType       string `gorm:"column:value_type"`
	Value           JSONB  `gorm:"column:value"`
	MediaAssetID    *int64 `gorm:"column:media_asset_id"`
}

func (PublicSiteSetting) TableName() string { return "v_public_site_settings" }

// PublishedMenuItem is a row of v_published_menu_items. Href is already resolved from the page path (or the
// item's url).
type PublishedMenuItem struct {
	RequestedLocale string  `gorm:"column:requested_locale"`
	MenuKey         string  `gorm:"column:menu_key"`
	ItemID          int64   `gorm:"column:item_id"`
	ParentID        *int64  `gorm:"column:parent_id"`
	SortOrder       int     `gorm:"column:sort_order"`
	LinkType        string  `gorm:"column:link_type"`
	Href            *string `gorm:"column:href"`
	PageID          *int64  `gorm:"column:page_id"`
	OpenInNewTab    bool    `gorm:"column:open_in_new_tab"`
	Icon            *string `gorm:"column:icon"`
	ActiveMatch     string  `gorm:"column:active_match"`
	Label           string  `gorm:"column:label"`
	AriaLabel       *string `gorm:"column:aria_label"`
	ResolvedLocale  string  `gorm:"column:resolved_locale"`
}

func (PublishedMenuItem) TableName() string { return "v_published_menu_items" }

// ---- pages -----------------------------------------------------------------------------------------------------

// PublishedPage is a row of v_published_pages.
type PublishedPage struct {
	RequestedLocale  string     `gorm:"column:requested_locale"`
	PageID           int64      `gorm:"column:page_id"`
	PublicID         string     `gorm:"column:public_id"`
	Key              string     `gorm:"column:key"`
	Template         string     `gorm:"column:template"`
	ParentID         *int64     `gorm:"column:parent_id"`
	IsHome           bool       `gorm:"column:is_home"`
	Noindex          bool       `gorm:"column:noindex"`
	InSitemap        bool       `gorm:"column:in_sitemap"`
	PublishedAt      *time.Time `gorm:"column:published_at"`
	UpdatedAt        time.Time  `gorm:"column:updated_at"`
	Path             string     `gorm:"column:path"`
	ResolvedLocale   string     `gorm:"column:resolved_locale"`
	IsFallback       bool       `gorm:"column:is_fallback"`
	Title            string     `gorm:"column:title"`
	SEOTitle         *string    `gorm:"column:seo_title"`
	SEOTitleAbsolute bool       `gorm:"column:seo_title_absolute"`
	SEODescription   *string    `gorm:"column:seo_description"`
	OGImageID        *int64     `gorm:"column:og_image_id"`
	CanonicalPath    *string    `gorm:"column:canonical_path"`
}

func (PublishedPage) TableName() string { return "v_published_pages" }

// PublishedPageSection is a row of v_published_page_sections. Props is the raw locale-map JSON: collapsing
// it to one language is pkg/localize's job. There is no requested_locale column (props are language-neutral).
type PublishedPageSection struct {
	PageID          int64   `gorm:"column:page_id"`
	SectionID       int64   `gorm:"column:section_id"`
	PublicID        string  `gorm:"column:public_id"`
	SectionKey      string  `gorm:"column:section_key"`
	SectionVersion  int     `gorm:"column:section_version"`
	Variant         *string `gorm:"column:variant"`
	SortOrder       int     `gorm:"column:sort_order"`
	AnchorID        *string `gorm:"column:anchor_id"`
	Props           JSONB   `gorm:"column:props"`
	SharedSectionID *int64  `gorm:"column:shared_section_id"`
}

func (PublishedPageSection) TableName() string { return "v_published_page_sections" }

// ---- taxonomy --------------------------------------------------------------------------------------------------

// PublishedCategory is a row of v_published_categories.
type PublishedCategory struct {
	RequestedLocale string  `gorm:"column:requested_locale"`
	CategoryID      int64   `gorm:"column:category_id"`
	Key             string  `gorm:"column:key"`
	SortOrder       int     `gorm:"column:sort_order"`
	Name            string  `gorm:"column:name"`
	Slug            string  `gorm:"column:slug"`
	Description     *string `gorm:"column:description"`
	ResolvedLocale  string  `gorm:"column:resolved_locale"`
}

func (PublishedCategory) TableName() string { return "v_published_categories" }

// PublishedTag is a row of v_published_tags.
type PublishedTag struct {
	RequestedLocale string `gorm:"column:requested_locale"`
	TagID           int64  `gorm:"column:tag_id"`
	Key             string `gorm:"column:key"`
	Name            string `gorm:"column:name"`
	Slug            string `gorm:"column:slug"`
	ResolvedLocale  string `gorm:"column:resolved_locale"`
}

func (PublishedTag) TableName() string { return "v_published_tags" }

// ---- posts -----------------------------------------------------------------------------------------------------

// PublishedPostCard is v_published_posts WITHOUT the large body column: use it for lists, cards and the
// strip. The repository selects exactly these columns.
type PublishedPostCard struct {
	RequestedLocale string     `gorm:"column:requested_locale"`
	PostID          int64      `gorm:"column:post_id"`
	PublicID        string     `gorm:"column:public_id"`
	CategoryID      *int64     `gorm:"column:category_id"`
	CategoryKey     *string    `gorm:"column:category_key"`
	CategoryName    *string    `gorm:"column:category_name"`
	CategorySlug    *string    `gorm:"column:category_slug"`
	AuthorID        *int64     `gorm:"column:author_id"`
	AuthorName      *string    `gorm:"column:author_name"`
	HeroMediaID     *int64     `gorm:"column:hero_media_id"`
	TeaserMediaID   *int64     `gorm:"column:teaser_media_id"`
	CardMediaID     *int64     `gorm:"column:card_media_id"`
	IsFeatured      bool       `gorm:"column:is_featured"`
	PublishedAt     *time.Time `gorm:"column:published_at"`
	UpdatedAt       time.Time  `gorm:"column:updated_at"`
	ResolvedLocale  string     `gorm:"column:resolved_locale"`
	IsFallback      bool       `gorm:"column:is_fallback"`
	Slug            string     `gorm:"column:slug"`
	Title           string     `gorm:"column:title"`
	Excerpt         *string    `gorm:"column:excerpt"`
	Label           *string    `gorm:"column:label"`
	LocationLabel   *string    `gorm:"column:location_label"`
	// HeroAlt is the alt text of the HERO image (cms_post_translations.hero_alt). It describes the card image
	// only when the card falls back to the hero (TeaserMediaID is nil); the strip and the list use it that way.
	HeroAlt *string `gorm:"column:hero_alt"`
}

func (PublishedPostCard) TableName() string { return "v_published_posts" }

// PublishedPost is the full row of v_published_posts, with the body (rich text, profile full) and the
// detail-only columns. Read it by slug or id, never in a list.
type PublishedPost struct {
	PublishedPostCard
	Body               JSONB   `gorm:"column:body"`
	GalleryHeading     *string `gorm:"column:gallery_heading"`
	GalleryDescription *string `gorm:"column:gallery_description"`
	SEOTitle           *string `gorm:"column:seo_title"`
	SEODescription     *string `gorm:"column:seo_description"`
	OGImageID          *int64  `gorm:"column:og_image_id"`
}

func (PublishedPost) TableName() string { return "v_published_posts" }

// PublishedPostTag is a row of v_published_post_tags.
type PublishedPostTag struct {
	PostID          int64  `gorm:"column:post_id"`
	RequestedLocale string `gorm:"column:requested_locale"`
	TagID           int64  `gorm:"column:tag_id"`
	Key             string `gorm:"column:key"`
	Name            string `gorm:"column:name"`
	Slug            string `gorm:"column:slug"`
}

func (PublishedPostTag) TableName() string { return "v_published_post_tags" }

// PublishedPostSlug is a row of v_published_post_slugs: a real (post, locale) slug, no fallback applied.
type PublishedPostSlug struct {
	PostID     int64  `gorm:"column:post_id"`
	LocaleCode string `gorm:"column:locale_code"`
	Slug       string `gorm:"column:slug"`
}

func (PublishedPostSlug) TableName() string { return "v_published_post_slugs" }

// PublishedPostMedia is a row of v_published_post_media (the gallery at the end of a post).
type PublishedPostMedia struct {
	RequestedLocale string  `gorm:"column:requested_locale"`
	PostID          int64   `gorm:"column:post_id"`
	PostMediaID     int64   `gorm:"column:post_media_id"`
	MediaID         int64   `gorm:"column:media_id"`
	SortOrder       int     `gorm:"column:sort_order"`
	Alt             *string `gorm:"column:alt"`
	Caption         *string `gorm:"column:caption"`
}

func (PublishedPostMedia) TableName() string { return "v_published_post_media" }

// ---- albums, faq, forms, collections ------------------------------------------------------------------------------

// PublishedAlbum is a row of v_published_albums.
type PublishedAlbum struct {
	RequestedLocale string     `gorm:"column:requested_locale"`
	AlbumID         int64      `gorm:"column:album_id"`
	PublicID        string     `gorm:"column:public_id"`
	Key             string     `gorm:"column:key"`
	CoverMediaID    *int64     `gorm:"column:cover_media_id"`
	PublishedAt     *time.Time `gorm:"column:published_at"`
	Title           string     `gorm:"column:title"`
	Description     *string    `gorm:"column:description"`
	ResolvedLocale  string     `gorm:"column:resolved_locale"`
}

func (PublishedAlbum) TableName() string { return "v_published_albums" }

// PublishedAlbumItem is a row of v_published_album_items.
type PublishedAlbumItem struct {
	RequestedLocale    string  `gorm:"column:requested_locale"`
	AlbumID            int64   `gorm:"column:album_id"`
	AlbumItemID        int64   `gorm:"column:album_item_id"`
	PublicID           string  `gorm:"column:public_id"`
	MediaID            int64   `gorm:"column:media_id"`
	SortOrder          int     `gorm:"column:sort_order"`
	LayoutHint         *string `gorm:"column:layout_hint"`
	CaptionTitle       *string `gorm:"column:caption_title"`
	CaptionDescription *string `gorm:"column:caption_description"`
	Alt                *string `gorm:"column:alt"`
}

func (PublishedAlbumItem) TableName() string { return "v_published_album_items" }

// PublishedFAQItem is a row of v_published_faq_items. Answer is rich text (profile lite) JSON.
type PublishedFAQItem struct {
	RequestedLocale string `gorm:"column:requested_locale"`
	GroupKey        string `gorm:"column:group_key"`
	GroupID         int64  `gorm:"column:group_id"`
	FAQItemID       int64  `gorm:"column:faq_item_id"`
	SortOrder       int    `gorm:"column:sort_order"`
	Question        string `gorm:"column:question"`
	Answer          JSONB  `gorm:"column:answer"`
	ResolvedLocale  string `gorm:"column:resolved_locale"`
}

func (PublishedFAQItem) TableName() string { return "v_published_faq_items" }

// PublishedForm is a row of v_published_forms (no recipients, no retention internals).
type PublishedForm struct {
	RequestedLocale    string      `gorm:"column:requested_locale"`
	FormID             int64       `gorm:"column:form_id"`
	Slug               string      `gorm:"column:slug"`
	JSONSchema         JSONB       `gorm:"column:json_schema"`
	FieldOrder         StringArray `gorm:"column:field_order"`
	HoneypotField      *string     `gorm:"column:honeypot_field"`
	RateLimitMax       *int        `gorm:"column:rate_limit_max"`
	RateLimitWindowS   *int        `gorm:"column:rate_limit_window_s"`
	RateLimitDailyMax  *int        `gorm:"column:rate_limit_daily_max"`
	MinFillSeconds     *int        `gorm:"column:min_fill_seconds"`
	MaxPayloadBytes    *int        `gorm:"column:max_payload_bytes"`
	SuccessRedirectURL *string     `gorm:"column:success_redirect_url"`
	Title              string      `gorm:"column:title"`
	Description        *string     `gorm:"column:description"`
	SubmitLabel        string      `gorm:"column:submit_label"`
	SendingLabel       *string     `gorm:"column:sending_label"`
	SuccessTitle       *string     `gorm:"column:success_title"`
	SuccessDescription *string     `gorm:"column:success_description"`
	SuccessAgainLabel  *string     `gorm:"column:success_again_label"`
	Messages           JSONB       `gorm:"column:messages"`
	ResolvedLocale     string      `gorm:"column:resolved_locale"`
}

func (PublishedForm) TableName() string { return "v_published_forms" }

// PublishedFormField is a row of v_published_form_fields.
type PublishedFormField struct {
	RequestedLocale string  `gorm:"column:requested_locale"`
	FormID          int64   `gorm:"column:form_id"`
	FormSlug        string  `gorm:"column:form_slug"`
	FieldKey        string  `gorm:"column:field_key"`
	Label           string  `gorm:"column:label"`
	Placeholder     *string `gorm:"column:placeholder"`
	Help            *string `gorm:"column:help"`
	OptionLabels    JSONB   `gorm:"column:option_labels"`
	ResolvedLocale  string  `gorm:"column:resolved_locale"`
}

func (PublishedFormField) TableName() string { return "v_published_form_fields" }

// PublishedCollectionEntry is a row of v_published_collection_entries (P2).
type PublishedCollectionEntry struct {
	RequestedLocale string     `gorm:"column:requested_locale"`
	CollectionKey   string     `gorm:"column:collection_key"`
	CollectionID    int64      `gorm:"column:collection_id"`
	EntryID         int64      `gorm:"column:entry_id"`
	PublicID        string     `gorm:"column:public_id"`
	SortOrder       int        `gorm:"column:sort_order"`
	PublishedAt     *time.Time `gorm:"column:published_at"`
	Data            JSONB      `gorm:"column:data"`
	SchemaVersion   int        `gorm:"column:schema_version"`
	Title           string     `gorm:"column:title"`
	Slug            *string    `gorm:"column:slug"`
	LocalizedData   JSONB      `gorm:"column:localized_data"`
	ResolvedLocale  string     `gorm:"column:resolved_locale"`
}

func (PublishedCollectionEntry) TableName() string { return "v_published_collection_entries" }

// ---- seo --------------------------------------------------------------------------------------------------------

// SitemapEntry is a row of v_sitemap. Kind is "page" or "post".
type SitemapEntry struct {
	RequestedLocale string     `gorm:"column:requested_locale"`
	Kind            string     `gorm:"column:kind"`
	EntityID        int64      `gorm:"column:entity_id"`
	Path            string     `gorm:"column:path"`
	Lastmod         *time.Time `gorm:"column:lastmod"`
}

func (SitemapEntry) TableName() string { return "v_sitemap" }

// ActiveRedirect is a row of v_active_redirects. LocaleCode nil = every locale.
type ActiveRedirect struct {
	SourcePath string  `gorm:"column:source_path"`
	LocaleCode *string `gorm:"column:locale_code"`
	Target     string  `gorm:"column:target"`
	StatusCode int     `gorm:"column:status_code"`
}

func (ActiveRedirect) TableName() string { return "v_active_redirects" }

// ---- media ------------------------------------------------------------------------------------------------------

// PublicAsset is a row of v_public_assets (ready, not deleted). Variants is a JSON array
// [{variant, format, width, height, storage_key}].
type PublicAsset struct {
	RequestedLocale string   `gorm:"column:requested_locale"`
	AssetID         int64    `gorm:"column:asset_id"`
	PublicID        string   `gorm:"column:public_id"`
	Kind            string   `gorm:"column:kind"`
	StorageKey      string   `gorm:"column:storage_key"`
	MimeType        string   `gorm:"column:mime_type"`
	Bytes           int64    `gorm:"column:bytes"`
	Width           *int     `gorm:"column:width"`
	Height          *int     `gorm:"column:height"`
	FocalX          *float64 `gorm:"column:focal_x"`
	FocalY          *float64 `gorm:"column:focal_y"`
	Blurhash        *string  `gorm:"column:blurhash"`
	DominantColor   *string  `gorm:"column:dominant_color"`
	Alt             *string  `gorm:"column:alt"`
	Caption         *string  `gorm:"column:caption"`
	Credit          *string  `gorm:"column:credit"`
	Variants        JSONB    `gorm:"column:variants"`
	// DurationMS is the length of a video in milliseconds (NULL for every other kind); appended to the view by release 1 of video support.
	DurationMS *int `gorm:"column:duration_ms"`
}

func (PublicAsset) TableName() string { return "v_public_assets" }
