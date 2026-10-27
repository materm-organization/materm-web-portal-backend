// Package models holds the DTOs of the system module (doc/05 section 4, doc/08 P-20/P-21).
package models

// Readiness states. The probe body shows only one of these, never a dependency name or an error.
const (
	StatusOK       = "ok"
	StatusDegraded = "degraded"
	StatusDown     = "down"
)

// Readiness is the outcome of the readiness probe. Ready decides the HTTP status (200 / 503); Status is the
// only thing the probe body shows: ok, degraded (serving without Redis, in every environment) or down (the database is unreachable).
type Readiness struct {
	Ready  bool
	Status string
}
