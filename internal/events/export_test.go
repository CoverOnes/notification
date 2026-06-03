// export_test.go exposes unexported methods for whitebox testing within the events package.
// This file is only compiled during tests (suffix _test.go).
package events

import (
	"context"

	"github.com/redis/go-redis/v9"
)

// HandleForTest calls the unexported handle method so external test packages can
// exercise the consumer payload processing logic without starting a real Redis loop.
func (c *Consumer) HandleForTest(ctx context.Context, msg *redis.Message) {
	c.handle(ctx, msg)
}
