// Package controller holds the HTTP handlers of the system module.
package controller

import (
	"github.com/gofiber/fiber/v2"

	"github.com/materm-organization/materm-web-portal-backend/modules/system/models"
	"github.com/materm-organization/materm-web-portal-backend/modules/system/services"
	"github.com/materm-organization/materm-web-portal-backend/pkg/apperror"
	"github.com/materm-organization/materm-web-portal-backend/pkg/requestid"
	"github.com/materm-organization/materm-web-portal-backend/pkg/response"
)

// SystemCtrl is the HTTP surface of the system module: the two probes, mounted on the root without a
// credential.
type SystemCtrl interface {
	Health(c *fiber.Ctx) error
	Ready(c *fiber.Ctx) error
}

type systemCtrl struct{ svc services.SystemService }

// NewSystemCtrl builds the controller.
func NewSystemCtrl(svc services.SystemService) SystemCtrl { return &systemCtrl{svc: svc} }

type probeData struct {
	Status string `json:"status"`
}

// Health is the liveness probe: 200 while the process answers. It touches no dependency and says nothing
// about the host or the version.
func (h *systemCtrl) Health(c *fiber.Ctx) error {
	c.Set(fiber.HeaderCacheControl, "no-store")
	return response.OK(c, probeData{Status: models.StatusOK})
}

// Ready is the readiness probe. 200 {status: ok|degraded} when the database answers (degraded = Redis is down:
// reads fall back to the database), 503 {status: down} when it does not. The body never names a dependency or carries an error: the probe needs no credential, so it
// must not tell an attacker what the stack is made of.
func (h *systemCtrl) Ready(c *fiber.Ctx) error {
	r := h.svc.Readiness(c.UserContext())
	c.Set(fiber.HeaderCacheControl, "no-store")
	if r.Ready {
		return response.OK(c, probeData{Status: r.Status})
	}
	lang := apperror.NegotiateLang(c.Get(fiber.HeaderAcceptLanguage))
	return c.Status(fiber.StatusServiceUnavailable).JSON(fiber.Map{
		"success":    false,
		"message":    apperror.Message(apperror.CodeServiceUnavailable, lang),
		"code":       apperror.CodeServiceUnavailable,
		"data":       probeData{Status: models.StatusDown},
		"request_id": requestid.FromFiber(c),
	})
}
