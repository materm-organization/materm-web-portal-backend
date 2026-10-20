package helper

import (
	"fmt"
	"go-fiber/pkg/logs"
	"time"
)

type RetryConfig struct {
	MaxRetries  int
	Delay       time.Duration
	DelayFunc   func(attempt int) time.Duration // overrides Delay if set
	IsRetryable func(err error) bool
}

//USAGE:
/*
user, err := Retry(RetryConfig{
    MaxRetries: 3,
    Delay:      100 * time.Millisecond,
    IsRetryable: func(err error) bool {
        return strings.Contains(err.Error(), "record not found")
    },
}, userID, "GetUser", func() (*repo.User, error) {
    return l.userRepo.GetUser(userID)
})
*/
func Retry[T any](cfg RetryConfig, logKey string, operation string, fn func() (T, error)) (T, error) {
	var result T
	var err error

	for i := 0; i < cfg.MaxRetries; i++ {
		attempt := i + 1
		logs.Info(fmt.Sprintf("key:%s - [%s] attempt %d", logKey, operation, attempt))

		result, err = fn()
		if err == nil {
			return result, nil
		}

		if cfg.IsRetryable != nil && !cfg.IsRetryable(err) {
			return result, err // non-retryable, exit immediately
		}

		if i < cfg.MaxRetries-1 {
			logs.Info(fmt.Sprintf("key:%s - [%s] attempt %d failed with retryable error: %s", logKey, operation, attempt, err.Error()))

			delay := cfg.Delay
			if cfg.DelayFunc != nil {
				delay = cfg.DelayFunc(attempt)
			}
			time.Sleep(delay)
		}
	}

	return result, err
}

/*
USAGE:
err = RetryVoid(RetryConfig{
    MaxRetries: 3,
    DelayFunc: func(attempt int) time.Duration {
        return time.Duration(math.Pow(2, float64(attempt))) * 50 * time.Millisecond
    },
    IsRetryable: func(err error) bool {
        return utilities.IsDeadlockError(err)
    },
}, "UpdateUser", "UpdateUser", func() error {
    logs.Info(fmt.Sprintf("[UpdateUser] - [Request]: %v", req))
    return l.userRepo.UpdateUser(req)
})

*/

func RetryVoid(cfg RetryConfig, logKey string, operation string, fn func() error) error {
	_, err := Retry(cfg, logKey, operation, func() (struct{}, error) {
		return struct{}{}, fn()
	})
	return err
}
