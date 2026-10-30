// Package models holds the DTOs of the gallery module. The shapes are those of the frontend loader
// (src/lib/cms/types.ts: AlbumSummary, AlbumDetail); resolved_locale is additive (FR-LOC-04).
package models

import (
	"github.com/materm-organization/materm-web-portal-backend/pkg/media"
	"github.com/materm-organization/materm-web-portal-backend/pkg/pagination"
)

// ImageRef points into the media map (alt nil = use the asset's own alt).
type ImageRef struct {
	MediaID int64   `json:"media_id"`
	Alt     *string `json:"alt"`
}

// AlbumSummary is an entry of P-11 and the head of P-12.
type AlbumSummary struct {
	ID             int64     `json:"id"`
	Key            string    `json:"key"`
	Title          *string   `json:"title"`
	Description    *string   `json:"description"`
	Cover          *ImageRef `json:"cover"`
	ResolvedLocale string    `json:"resolved_locale"`
}

// AlbumsPage is the data of P-11; the controller writes it as {data: Items, meta: Meta, media: Media}.
type AlbumsPage struct {
	Items []AlbumSummary  `json:"items"`
	Meta  pagination.Meta `json:"meta"`
	Media media.Map       `json:"media"`
}

// AlbumItem is a picture of an album.
type AlbumItem struct {
	ID                 int64   `json:"id"`
	MediaID            int64   `json:"media_id"`
	Alt                *string `json:"alt"`
	CaptionTitle       *string `json:"caption_title"`
	CaptionDescription *string `json:"caption_description"`
	SortOrder          int     `json:"sort_order"`
}

// AlbumDetail is the data of P-12.
type AlbumDetail struct {
	AlbumSummary
	Locale string      `json:"locale"`
	Items  []AlbumItem `json:"items"`
	Media  media.Map   `json:"media"`
}

// AlbumsEnvelope documents the body of P-11 for swagger (swag cannot instantiate readkit.MediaList[T]); the
// handler writes readkit.MediaList[AlbumSummary], which has the same JSON shape.
type AlbumsEnvelope struct {
	Success bool            `json:"success"`
	Data    []AlbumSummary  `json:"data"`
	Meta    pagination.Meta `json:"meta"`
	Media   media.Map       `json:"media"`
}
