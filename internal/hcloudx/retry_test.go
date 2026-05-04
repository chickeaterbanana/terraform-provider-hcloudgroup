package hcloudx_test

import (
	"context"
	"errors"
	"net"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

func TestRetry_FirstSuccessReturnsImmediately(t *testing.T) {
	calls := 0
	err := hcloudx.Retry(context.Background(), func(_ context.Context) error {
		calls++
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 1, calls)
}

func TestRetry_RetriesUntilSuccess_OnRetryableHcloudCodes(t *testing.T) {
	cases := []hcloud.ErrorCode{
		hcloud.ErrorCodeRateLimitExceeded,
		hcloud.ErrorCodeConflict,
		hcloud.ErrorCodeLocked,
	}
	for _, code := range cases {
		t.Run(string(code), func(t *testing.T) {
			calls := 0
			err := hcloudx.Retry(context.Background(), func(_ context.Context) error {
				calls++
				if calls < 2 {
					return hcloud.Error{Code: code, Message: "transient"}
				}
				return nil
			})
			require.NoError(t, err)
			require.Equal(t, 2, calls, "must retry until the closure returns nil")
		})
	}
}

func TestRetry_TerminatesImmediately_OnNonRetryableHcloudCodes(t *testing.T) {
	cases := []hcloud.ErrorCode{
		hcloud.ErrorCodeNotFound,
		hcloud.ErrorCodeForbidden,
		hcloud.ErrorCodeInvalidInput,
	}
	for _, code := range cases {
		t.Run(string(code), func(t *testing.T) {
			calls := 0
			start := time.Now()
			err := hcloudx.Retry(context.Background(), func(_ context.Context) error {
				calls++
				return hcloud.Error{Code: code, Message: "permanent"}
			})
			require.Error(t, err)
			require.Equal(t, 1, calls, "non-retryable hcloud codes must not be retried")
			require.Less(t, time.Since(start), 200*time.Millisecond, "no backoff should have run")
		})
	}
}

func TestRetry_RetriesOnNetOpError(t *testing.T) {
	calls := 0
	err := hcloudx.Retry(context.Background(), func(_ context.Context) error {
		calls++
		if calls < 2 {
			return &net.OpError{Op: "dial", Err: errors.New("transient")}
		}
		return nil
	})
	require.NoError(t, err)
	require.Equal(t, 2, calls)
}

func TestRetry_TerminatesOnGenericError(t *testing.T) {
	calls := 0
	err := hcloudx.Retry(context.Background(), func(_ context.Context) error {
		calls++
		return errors.New("not retryable")
	})
	require.Error(t, err)
	require.Equal(t, 1, calls, "generic errors are terminal")
}

func TestRetry_HonorsContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	calls := 0
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	err := hcloudx.Retry(ctx, func(_ context.Context) error {
		calls++
		return &net.OpError{Op: "dial", Err: errors.New("transient")}
	})
	require.Error(t, err)
	require.True(t, errors.Is(err, context.Canceled), "must surface ctx.Err on cancellation")
}

// We don't drive Retry to its 5-minute budget exhaustion (the backoff
// would dominate test wall-clock). The deterministic wall-clock cap
// integration is exercised by retry_integration_test in the reconciler
// package via an injected fake.
