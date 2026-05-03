package hcloudx

import (
	"context"
	"errors"
	"net"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// RetryBudget caps how long Retry will keep retrying a single operation
// across all backoff attempts. Five minutes per spec section 11.
const RetryBudget = 5 * time.Minute

// Retry runs fn under exponential backoff until it succeeds, fn returns a
// non-retryable error, or the budget expires. Backoff is 500ms doubling
// up to 30s.
func Retry(ctx context.Context, fn func(context.Context) error) error {
	deadline := time.Now().Add(RetryBudget)
	delay := 500 * time.Millisecond
	const maxDelay = 30 * time.Second

	for {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		if !isRetryable(err) {
			return err
		}
		if time.Now().Add(delay).After(deadline) {
			return err
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
		delay *= 2
		if delay > maxDelay {
			delay = maxDelay
		}
	}
}

func isRetryable(err error) bool {
	if err == nil {
		return false
	}
	var apiErr hcloud.Error
	if errors.As(err, &apiErr) {
		// 4xx-class errors from hcloud are permanent. The hcloud-go SDK
		// surfaces these via typed Code values; we treat rate-limit as
		// retryable and everything else 4xx as terminal.
		switch apiErr.Code {
		case hcloud.ErrorCodeRateLimitExceeded,
			hcloud.ErrorCodeConflict,
			hcloud.ErrorCodeLocked:
			return true
		}
		return false
	}
	var netErr net.Error
	if errors.As(err, &netErr) {
		return true
	}
	return false
}
