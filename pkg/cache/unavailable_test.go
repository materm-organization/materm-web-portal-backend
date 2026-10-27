package cache

import (
	"context"
	"testing"
	"time"
)

func TestUnavailableCacheAlwaysFails(t *testing.T) {
	c := NewRedisRepository(nil) // Redis unreachable in dev
	ctx := context.Background()
	if _, err := c.Get(ctx, "k", "$"); err != ErrUnavailable {
		t.Errorf("Get: %v", err)
	}
	if err := c.Set(ctx, "k", "$", 1, time.Second); err != ErrUnavailable {
		t.Errorf("Set: %v", err)
	}
	if err := c.Delete(ctx, "k"); err != ErrUnavailable {
		t.Errorf("Delete: %v", err)
	}
	if _, err := c.Exists(ctx, "k"); err != ErrUnavailable {
		t.Errorf("Exists: %v", err)
	}
	if _, err := c.Increment(ctx, "k"); err != ErrUnavailable {
		t.Errorf("Increment: %v", err)
	}
	if err := c.DeleteAll(ctx); err != ErrUnavailable {
		t.Errorf("DeleteAll: %v", err)
	}
	if c.GetClient() != nil {
		t.Error("the degraded cache has no client")
	}
}
