package constants

import "time"

const (
	// AppKeyPrefix is the root Redis key prefix for this service; every cache
	// key must be built on it to avoid collisions on a shared Redis instance.
	AppKeyPrefix = "materm-portal:"
	RootKey      = "$"

	ExpireTime = 1 * 24 * time.Hour // Expire time 1 day
	// DBQueryTimeout is the deadline of every request context; it matches the statement_timeout of the
	// read-only database role (doc/08 section 2.5/2.6).
	DBQueryTimeout = 3 * time.Second
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
