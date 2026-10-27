package redistest

import (
	"errors"
	"testing"
)

func TestGuardRefusesSharedRedis(t *testing.T) {
	for _, u := range []string{
		"redis://:p@127.0.0.1:6379/0",
		"redis://:p@localhost:6379",
		"redis://:p@cache.internal:6379/1",
		"redis://:p@127.0.0.1/0", // implied 6379
		"",
		"not a url",
	} {
		if err := Guard(u); !errors.Is(err, ErrRefused) {
			t.Fatalf("Guard accepted %q: %v", u, err)
		}
	}
	if err := Guard("redis://:p@127.0.0.1:56380/0"); err != nil {
		t.Fatalf("Guard refused the scratch URL: %v", err)
	}
}
