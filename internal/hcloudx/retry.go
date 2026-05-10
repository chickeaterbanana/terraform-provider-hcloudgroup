// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
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

// NotFoundRetryBudget bounds how long RetryIncludingNotFound will keep
// retrying on hcloud's NotFound (404) — far shorter than RetryBudget
// because a true "doesn't exist" must not waste five minutes.
//
// Hetzner's eventual-consistency window between a successful CreateServer
// and the subsequent GetServer(id) becoming visible is typically <2s in
// practice; 30s leaves comfortable headroom for backoff jitter without
// hiding a genuine "this server really isn't there" for long.
const NotFoundRetryBudget = 30 * time.Second

// Retry runs fn under exponential backoff until it succeeds, fn returns
// a non-retryable error, the budget would be exceeded by the next
// attempt's sleep, or ctx is cancelled. Backoff is 500ms doubling up to
// 30s. The "would be exceeded" check (line 32) means Retry stops before
// it could overrun RetryBudget — the budget is the wall-clock cap on
// total elapsed time, not on time remaining at the final attempt.
func Retry(ctx context.Context, fn func(context.Context) error) error {
	return retry(ctx, fn, false)
}

// RetryIncludingNotFound is Retry plus a bounded retry on hcloud
// NotFound. Use it for read-after-write races where the object was just
// created and Hetzner's API hasn't propagated visibility yet — most
// notably the post-CreateServer GetServer call in the slot loop.
//
// Do NOT use it for paths where NotFound is the *intended* terminal
// state (e.g. polling that a delete really happened), or you'll waste
// 30s of backoff before surfacing the expected outcome.
func RetryIncludingNotFound(ctx context.Context, fn func(context.Context) error) error {
	return retry(ctx, fn, true)
}

func retry(ctx context.Context, fn func(context.Context) error, allowNotFound bool) error {
	deadline := time.Now().Add(RetryBudget)
	notFoundDeadline := time.Now().Add(NotFoundRetryBudget)
	delay := 500 * time.Millisecond
	const maxDelay = 30 * time.Second

	for {
		err := fn(ctx)
		if err == nil {
			return nil
		}
		retryable := isRetryable(err)
		if !retryable && allowNotFound && isNotFound(err) {
			// NotFound has its own (shorter) sub-budget — once that's blown,
			// surface the 404 even though the outer RetryBudget hasn't elapsed.
			if time.Now().After(notFoundDeadline) {
				return err
			}
			retryable = true
		}
		if !retryable {
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
	return errors.As(err, &netErr)
}

func isNotFound(err error) bool {
	if err == nil {
		return false
	}
	var apiErr hcloud.Error
	if errors.As(err, &apiErr) {
		return apiErr.Code == hcloud.ErrorCodeNotFound
	}
	return false
}
