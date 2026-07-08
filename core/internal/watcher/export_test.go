package watcher

import (
	"context"
	"time"
)

// Poll exposes one scan cycle to tests, bypassing the ticker.
func (w *Watcher) Poll(ctx context.Context, now time.Time) { w.poll(ctx, now) }
