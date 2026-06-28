package controller

import (
	"context"
	"time"
)

// Tick exposes the internal tick method for use in package-external tests.
func (c *Controller) Tick(ctx context.Context, now time.Time) {
	c.tick(ctx, now)
}
