// Package response writes the success envelopes of the API (doc/05 section 1.3, doc/00 section 3):
//
//	{ "success": true, "message"?: "...", "data": ... }
//	{ "success": true, "data": [ ... ], "meta": { "total", "page", "limit", "total_page" } }
//
// Errors are written by pkg/apperror (Handler) from the error a handler returns; handlers do not build
// error bodies. There is no 204: every response is JSON so the frontend can always read it; an action
// without a result answers 200 with data null.
package response

import (
	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/pkg/pagination"
)

// Envelope is the body of a success response.
type Envelope struct {
	Success bool   `json:"success"`
	Message string `json:"message,omitempty"`
	Data    any    `json:"data"`
}

// ListEnvelope is the body of a list response; Meta sits next to Data, never inside it.
type ListEnvelope struct {
	Success bool            `json:"success"`
	Message string          `json:"message,omitempty"`
	Data    any             `json:"data"`
	Meta    pagination.Meta `json:"meta"`
}

// OK answers 200 with data (nil data is written as null).
func OK(c *fiber.Ctx, data any) error {
	return c.Status(fiber.StatusOK).JSON(Envelope{Success: true, Data: data})
}

// OKMessage answers 200 with an optional human message.
func OKMessage(c *fiber.Ctx, message string, data any) error {
	return c.Status(fiber.StatusOK).JSON(Envelope{Success: true, Message: message, Data: data})
}

// Created answers 201 and, when location is not empty, sets the Location header.
func Created(c *fiber.Ctx, location string, data any) error {
	if location != "" {
		c.Set(fiber.HeaderLocation, location)
	}
	return c.Status(fiber.StatusCreated).JSON(Envelope{Success: true, Data: data})
}

// List answers 200 with a page of items plus meta. A nil slice is written as [] (never null).
func List[T any](c *fiber.Ctx, items []T, meta pagination.Meta) error {
	if items == nil {
		items = []T{}
	}
	return c.Status(fiber.StatusOK).JSON(ListEnvelope{Success: true, Data: items, Meta: meta})
}
