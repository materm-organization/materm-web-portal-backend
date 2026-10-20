package circuitbreaker

import (
	"fmt"
	"time"

	"go-fiber/pkg/logs"

	"github.com/sony/gobreaker"
)

// New creates a circuit breaker for a named dependency (e.g. "primary-db", "redis").
// Opens after 5 consecutive failures; waits 30s before half-open probe.
func New(name string) *gobreaker.CircuitBreaker {
	return gobreaker.NewCircuitBreaker(gobreaker.Settings{
		Name:        name,
		MaxRequests: 1,
		Interval:    60 * time.Second,
		Timeout:     30 * time.Second,
		ReadyToTrip: func(counts gobreaker.Counts) bool {
			return counts.ConsecutiveFailures >= 5
		},
		OnStateChange: func(name string, from, to gobreaker.State) {
			logs.Info(fmt.Sprintf("[circuit-breaker] %s: %s → %s", name, from, to))
		},
	})
}

// Execute wraps a typed function with the circuit breaker.
// Returns gobreaker.ErrOpenState immediately when the circuit is open
// (no DB round-trip, no 30s wait).
func Execute[T any](cb *gobreaker.CircuitBreaker, fn func() (T, error)) (T, error) {
	result, err := cb.Execute(func() (any, error) {
		return fn()
	})
	if err != nil {
		var zero T
		return zero, err
	}
	return result.(T), nil
}
