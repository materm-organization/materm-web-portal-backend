//go:build dbtest

package redistest

import (
	"context"
	"testing"
)

func TestNewIsolatesByPrefix(t *testing.T) {
	r := New(t)
	ctx := context.Background()
	if err := r.Client.Set(ctx, r.Key("k"), "v", 0).Err(); err != nil {
		t.Fatal(err)
	}
	if v, err := r.Client.Get(ctx, r.Key("k")).Result(); err != nil || v != "v" {
		t.Fatalf("round trip failed: %v %q", err, v)
	}
}
