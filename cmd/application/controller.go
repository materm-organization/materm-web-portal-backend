package application

import (
	systemcontroller "github.com/materm-organization/materm-web-portal-backend/modules/system/controller"
)

// Controllers holds the HTTP controllers of every module.
type Controllers struct {
	System systemcontroller.SystemCtrl
}

// SetupControllers builds the controllers from the services.
func SetupControllers(s *Services) *Controllers {
	return &Controllers{System: systemcontroller.NewSystemCtrl(s.System)}
}
