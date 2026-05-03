package hcloudx

import (
	"context"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// DefaultActionTimeout caps a single action wait. Hcloud Server.Create
// typically completes in 30-90 seconds; deletion is faster. Five minutes is
// the spec's recommended bound (section 6).
const DefaultActionTimeout = 5 * time.Minute

// WaitFor wraps Client.WaitForAction with a context bounded by
// DefaultActionTimeout. Returns the original context's error if it expires
// first, or the action error if hcloud reports failure.
func WaitFor(ctx context.Context, c Client, action *hcloud.Action) error {
	if action == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, DefaultActionTimeout)
	defer cancel()
	return c.WaitForAction(ctx, action)
}
