package constants

import "time"

const (
	// AppKeyPrefix is the root Redis key prefix for this service; every cache
	// key must be built on it to avoid collisions on a shared Redis instance.
	AppKeyPrefix = "materm-portal:"
	RootKey      = "$"

	ExpireTime     = 1 * 24 * time.Hour // Expire time 1 day
	DBQueryTimeout = 30 * time.Second
)

type Keys struct {
	Key             string
	ItemKeyTemplate string
	EntityID        string
	EntityType      string
	Prefix          []string
	Attempts        int
	RetryDelay      time.Duration
	Expiration      time.Duration
}
