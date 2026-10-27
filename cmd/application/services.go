package application

import (
	systemservices "github.com/materm-organization/materm-web-portal-backend/modules/system/services"
)

// Services holds the services of every module.
type Services struct {
	System systemservices.SystemService
}

// SetupServices builds the services from the repositories. strict is staging/prod (a Redis outage makes
// /ready answer 503).
func SetupServices(repos *Repositories, strict bool) *Services {
	return &Services{
		System: systemservices.NewSystemService(systemservices.Deps{
			PingDB:    systemservices.DBPinger(repos.DB),
			PingRedis: systemservices.RedisPinger(repos.Redis),
			Strict:    strict,
		}),
	}
}
