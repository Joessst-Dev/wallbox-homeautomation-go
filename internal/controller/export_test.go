package controller

import (
	"context"
	"time"
)

// Tick exposes the unexported tick method for white-box controller tests.
func (c *Controller) Tick(ctx context.Context, now time.Time) {
	c.tick(ctx, now)
}
