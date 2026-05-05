// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package hcloudx_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

// stubWaitClient implements hcloudx.Client just enough to drive
// WaitForAction. Other methods panic to surface unexpected calls.
type stubWaitClient struct {
	waitErr   error
	waitDelay time.Duration
}

func (s *stubWaitClient) ListByGroup(context.Context, string) ([]*hcloud.Server, error) {
	panic("unexpected ListByGroup")
}
func (s *stubWaitClient) GetServer(context.Context, int64) (*hcloud.Server, error) {
	panic("unexpected GetServer")
}
func (s *stubWaitClient) CreateServer(context.Context, hcloud.ServerCreateOpts) (*hcloud.Server, *hcloud.Action, error) {
	panic("unexpected CreateServer")
}
func (s *stubWaitClient) DeleteServer(context.Context, int64) (*hcloud.Action, error) {
	panic("unexpected DeleteServer")
}
func (s *stubWaitClient) UpdateServerLabels(context.Context, int64, map[string]string) (*hcloud.Server, error) {
	panic("unexpected UpdateServerLabels")
}
func (s *stubWaitClient) ResolveSSHKeys(context.Context, []string) ([]*hcloud.SSHKey, error) {
	panic("unexpected ResolveSSHKeys")
}
func (s *stubWaitClient) WaitForAction(ctx context.Context, _ *hcloud.Action) error {
	if s.waitDelay > 0 {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.waitDelay):
		}
	}
	return s.waitErr
}

func TestWaitFor_NilActionReturnsNil(t *testing.T) {
	require.NoError(t, hcloudx.WaitFor(context.Background(), &stubWaitClient{}, nil))
}

func TestWaitFor_PassesThroughClientError(t *testing.T) {
	want := errors.New("hcloud action failed")
	c := &stubWaitClient{waitErr: want}
	got := hcloudx.WaitFor(context.Background(), c, &hcloud.Action{ID: 1})
	require.ErrorIs(t, got, want)
}

func TestWaitFor_HonorsParentContext(t *testing.T) {
	c := &stubWaitClient{waitDelay: 5 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	err := hcloudx.WaitFor(ctx, c, &hcloud.Action{ID: 1})
	require.Error(t, err)
	require.True(t, errors.Is(err, context.DeadlineExceeded), "parent deadline must propagate")
	require.Less(t, time.Since(start), 1*time.Second, "must not wait the full waitDelay")
}
