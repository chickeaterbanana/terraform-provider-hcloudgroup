// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package hcloudx

import (
	"context"
	"fmt"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// Client is the subset of *hcloud.Client the provider depends on. The
// interface exists so the reconciler can be unit-tested with a fake.
type Client interface {
	ListByGroup(ctx context.Context, group string) ([]*hcloud.Server, error)
	GetServer(ctx context.Context, id int64) (*hcloud.Server, error)
	CreateServer(ctx context.Context, opts hcloud.ServerCreateOpts) (*hcloud.Server, *hcloud.Action, error)
	DeleteServer(ctx context.Context, id int64) (*hcloud.Action, error)
	UpdateServerLabels(ctx context.Context, id int64, labels map[string]string) (*hcloud.Server, error)
	WaitForAction(ctx context.Context, action *hcloud.Action) error
	ResolveSSHKeys(ctx context.Context, names []string) ([]*hcloud.SSHKey, error)
}

// Real wraps an *hcloud.Client behind the Client interface.
type Real struct {
	HC *hcloud.Client
}

// NewReal returns a Client backed by an *hcloud.Client. Pass an
// already-configured client (token, endpoint, retry behavior).
func NewReal(hc *hcloud.Client) *Real {
	return &Real{HC: hc}
}

// ListByGroup uses the hcloud label selector to scope the query to servers
// managed by this provider for the given group. Pagination is handled by
// AllWithOpts.
func (r *Real) ListByGroup(ctx context.Context, group string) ([]*hcloud.Server, error) {
	selector := fmt.Sprintf("%s=%s,%s=%s",
		LabelManagedBy, ManagedByValue,
		LabelGroup, group,
	)
	opts := hcloud.ServerListOpts{
		ListOpts: hcloud.ListOpts{LabelSelector: selector, PerPage: 50},
	}
	return r.HC.Server.AllWithOpts(ctx, opts)
}

// GetServer fetches a server by id. Used by UpdateServerLabels to perform
// the read half of read-modify-write.
func (r *Real) GetServer(ctx context.Context, id int64) (*hcloud.Server, error) {
	srv, _, err := r.HC.Server.GetByID(ctx, id)
	if err != nil {
		return nil, err
	}
	if srv == nil {
		return nil, fmt.Errorf("server %d not found", id)
	}
	return srv, nil
}

// CreateServer issues a Server.Create call. The returned Action is async;
// callers must wait on it via WaitForAction before considering the server
// usable.
func (r *Real) CreateServer(ctx context.Context, opts hcloud.ServerCreateOpts) (*hcloud.Server, *hcloud.Action, error) {
	res, _, err := r.HC.Server.Create(ctx, opts)
	if err != nil {
		return nil, nil, err
	}
	return res.Server, res.Action, nil
}

// DeleteServer issues a Server.DeleteWithResult call; callers wait for the
// returned Action.
func (r *Real) DeleteServer(ctx context.Context, id int64) (*hcloud.Action, error) {
	res, _, err := r.HC.Server.DeleteWithResult(ctx, &hcloud.Server{ID: id})
	if err != nil {
		return nil, err
	}
	return res.Action, nil
}

// UpdateServerLabels performs the read-modify-write needed to flip a single
// label without clobbering the rest of the map. Hcloud's Server.Update
// replaces the labels map wholesale.
func (r *Real) UpdateServerLabels(ctx context.Context, id int64, labels map[string]string) (*hcloud.Server, error) {
	updated, _, err := r.HC.Server.Update(ctx, &hcloud.Server{ID: id}, hcloud.ServerUpdateOpts{
		Labels: labels,
	})
	return updated, err
}

// ResolveSSHKeys looks up SSH keys by name. Hcloud's Server.Create requires
// numeric IDs, so each name from the operator's ssh_keys list must be
// translated. An unknown name produces an error rather than silently being
// dropped.
func (r *Real) ResolveSSHKeys(ctx context.Context, names []string) ([]*hcloud.SSHKey, error) {
	if len(names) == 0 {
		return nil, nil
	}
	out := make([]*hcloud.SSHKey, 0, len(names))
	for _, name := range names {
		key, _, err := r.HC.SSHKey.GetByName(ctx, name)
		if err != nil {
			return nil, err
		}
		if key == nil {
			return nil, fmt.Errorf("ssh_key %q not found", name)
		}
		out = append(out, key)
	}
	return out, nil
}

// WaitForAction blocks until the action reaches a terminal state. Callers
// pass a context with the appropriate deadline (the action_wait helper
// builds one with a 5-minute cap). hcloud-go's WaitForFunc polls the API
// internally; we pass nil for the handleUpdate callback because we only
// care about the terminal state.
func (r *Real) WaitForAction(ctx context.Context, action *hcloud.Action) error {
	if action == nil {
		return nil
	}
	return r.HC.Action.WaitForFunc(ctx, nil, action)
}
