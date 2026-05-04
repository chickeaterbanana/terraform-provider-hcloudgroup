// Package hcloudxtest provides an in-memory hcloudx.Client implementation
// for unit tests of any package that talks to hcloud through the
// reconciler/CRUD layer. Tests run sequentially; the fake is not safe for
// concurrent use.
package hcloudxtest

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sort"
	"strconv"
	"sync/atomic"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

// ErrFakeNetwork is a *net.OpError so hcloudx.Retry classifies it as
// retryable. Use it for fault injection that should be transient.
var ErrFakeNetwork = &net.OpError{Op: "dial", Err: errors.New("fake network error")}

// ErrNotFound is a 404-style hcloud.Error so hcloudx.Retry classifies it
// as terminal. Use it for fault injection that should not be retried.
var ErrNotFound = hcloud.Error{Code: hcloud.ErrorCodeNotFound, Message: "not found"}

// Fake is an in-memory hcloudx.Client. Servers are stored in a map keyed
// by id; labels are full-replaced by UpdateServerLabels (matching real
// hcloud semantics); PrivateNet is populated at create time so
// findPrivateIP works without a separate attach step.
type Fake struct {
	Servers      map[int64]*hcloud.Server
	nextServerID int64

	CreateCalls       int
	DeleteCalls       int
	UpdateLabelsCalls int
	WaitForCalls      int
	ListByGroupCalls  int
	GetServerCalls    int

	DeletedIDs   []int64
	CreatedNames []string

	// Fault-injection knobs. Zero values disable injection.

	// FailOnCreateName, if non-empty, makes CreateServer return
	// FailOnCreateErr (default ErrFakeNetwork) when the requested name
	// matches.
	FailOnCreateName string
	FailOnCreateErr  error

	// FailOnDeleteID, if non-zero, makes DeleteServer return
	// FailOnDeleteErr (default ErrFakeNetwork) when the requested id
	// matches.
	FailOnDeleteID  int64
	FailOnDeleteErr error

	// FailOnUpdateLabelsID, if non-zero, makes UpdateServerLabels return
	// FailOnUpdateLabelsErr (default ErrFakeNetwork) when the requested
	// id matches.
	FailOnUpdateLabelsID  int64
	FailOnUpdateLabelsErr error

	// UpdateLabelsFailures: the next N UpdateServerLabels calls return
	// FailOnUpdateLabelsErr (default ErrFakeNetwork) before succeeding.
	// Use this to verify retry behavior at the complete=true label flip.
	UpdateLabelsFailures int

	// FailListByGroupErr, if non-nil, is returned from ListByGroup.
	FailListByGroupErr error
	// FailGetServerErr, if non-nil, is returned from GetServer.
	FailGetServerErr error
	// GetServerFailures: the next N GetServer calls return FailGetServerErr
	// (default ErrFakeNetwork) before succeeding. Use this to verify
	// retry behavior at the post-create re-read.
	GetServerFailures int

	// WaitForFailures: the next N WaitForAction calls return
	// FailWaitForErr (default ErrFakeNetwork) before succeeding.
	WaitForFailures int
	FailWaitForErr  error

	// ResolveSSHKeysErr, if non-nil, is returned from ResolveSSHKeys.
	ResolveSSHKeysErr error
}

var _ hcloudx.Client = (*Fake)(nil)

// NewFake constructs an empty fake.
func NewFake() *Fake { return &Fake{Servers: map[int64]*hcloud.Server{}} }

// ListByGroup returns all servers labeled hcloudgroup.io/group=group, sorted
// by id for stable test assertions.
func (f *Fake) ListByGroup(_ context.Context, group string) ([]*hcloud.Server, error) {
	f.ListByGroupCalls++
	if f.FailListByGroupErr != nil {
		return nil, f.FailListByGroupErr
	}
	out := make([]*hcloud.Server, 0)
	for _, s := range f.Servers {
		if s.Labels == nil {
			continue
		}
		if s.Labels[hcloudx.LabelGroup] == group {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

// GetServer returns the server by id, or ErrNotFound. Returns a clone so
// tests can mutate without affecting Fake state.
func (f *Fake) GetServer(_ context.Context, id int64) (*hcloud.Server, error) {
	f.GetServerCalls++
	if f.GetServerFailures > 0 {
		f.GetServerFailures--
		err := f.FailGetServerErr
		if err == nil {
			err = ErrFakeNetwork
		}
		return nil, err
	}
	if f.FailGetServerErr != nil {
		return nil, f.FailGetServerErr
	}
	srv, ok := f.Servers[id]
	if !ok {
		return nil, ErrNotFound
	}
	return cloneServer(srv), nil
}

// CreateServer records the call, optionally fails, and otherwise inserts a
// new server with a private IP allocated from 10.0.0.0/24.
func (f *Fake) CreateServer(_ context.Context, opts hcloud.ServerCreateOpts) (*hcloud.Server, *hcloud.Action, error) {
	f.CreateCalls++
	f.CreatedNames = append(f.CreatedNames, opts.Name)
	if f.FailOnCreateName != "" && opts.Name == f.FailOnCreateName {
		err := f.FailOnCreateErr
		if err == nil {
			err = ErrFakeNetwork
		}
		return nil, nil, err
	}
	id := atomic.AddInt64(&f.nextServerID, 1)
	var netID int64
	if len(opts.Networks) > 0 && opts.Networks[0] != nil {
		netID = opts.Networks[0].ID
	}
	srv := &hcloud.Server{
		ID:     id,
		Name:   opts.Name,
		Labels: cloneLabels(opts.Labels),
		PrivateNet: []hcloud.ServerPrivateNet{{
			Network: &hcloud.Network{ID: netID},
			IP:      net.ParseIP("10.0.0." + strconv.Itoa(int(id+10))),
		}},
	}
	f.Servers[id] = srv
	return cloneServer(srv), nil, nil
}

// DeleteServer removes the server by id from Servers. The fake returns nil
// for the *hcloud.Action; WaitForAction tolerates nil actions.
func (f *Fake) DeleteServer(_ context.Context, id int64) (*hcloud.Action, error) {
	f.DeleteCalls++
	f.DeletedIDs = append(f.DeletedIDs, id)
	if f.FailOnDeleteID != 0 && id == f.FailOnDeleteID {
		err := f.FailOnDeleteErr
		if err == nil {
			err = ErrFakeNetwork
		}
		return nil, err
	}
	delete(f.Servers, id)
	return nil, nil
}

// UpdateServerLabels replaces a server's full label map (matching the
// real hcloud API semantics).
func (f *Fake) UpdateServerLabels(_ context.Context, id int64, labels map[string]string) (*hcloud.Server, error) {
	f.UpdateLabelsCalls++
	if f.UpdateLabelsFailures > 0 {
		f.UpdateLabelsFailures--
		err := f.FailOnUpdateLabelsErr
		if err == nil {
			err = ErrFakeNetwork
		}
		return nil, err
	}
	if f.FailOnUpdateLabelsID != 0 && id == f.FailOnUpdateLabelsID {
		err := f.FailOnUpdateLabelsErr
		if err == nil {
			err = ErrFakeNetwork
		}
		return nil, err
	}
	srv, ok := f.Servers[id]
	if !ok {
		return nil, ErrNotFound
	}
	srv.Labels = cloneLabels(labels)
	return cloneServer(srv), nil
}

// WaitForAction is a no-op except for the WaitForFailures counter that
// lets tests inject transient failures.
func (f *Fake) WaitForAction(_ context.Context, _ *hcloud.Action) error {
	f.WaitForCalls++
	if f.WaitForFailures > 0 {
		f.WaitForFailures--
		err := f.FailWaitForErr
		if err == nil {
			err = ErrFakeNetwork
		}
		return err
	}
	return nil
}

// ResolveSSHKeys returns synthetic *hcloud.SSHKey values - one per name,
// with id = index+1. Empty names → empty slice. Tests don't depend on
// the actual key content.
func (f *Fake) ResolveSSHKeys(_ context.Context, names []string) ([]*hcloud.SSHKey, error) {
	if f.ResolveSSHKeysErr != nil {
		return nil, f.ResolveSSHKeysErr
	}
	out := make([]*hcloud.SSHKey, 0, len(names))
	for i, n := range names {
		out = append(out, &hcloud.SSHKey{ID: int64(i + 1), Name: n})
	}
	return out, nil
}

// SeedServer pretends a fully-completed server is already in hcloud for
// (slotID, generation). Used to construct prior-state scenarios.
func (f *Fake) SeedServer(group string, slotID, generation int, networkID int64) *hcloud.Server {
	id := atomic.AddInt64(&f.nextServerID, 1)
	srv := &hcloud.Server{
		ID:   id,
		Name: fmt.Sprintf("%s-%d-%d", group, slotID, generation),
		Labels: map[string]string{
			hcloudx.LabelManagedBy:   hcloudx.ManagedByValue,
			hcloudx.LabelGroup:       group,
			hcloudx.LabelSlot:        strconv.Itoa(slotID),
			hcloudx.LabelGeneration:  strconv.Itoa(generation),
			hcloudx.LabelReplaceHash: "seed12345678",
			hcloudx.LabelComplete:    "true",
		},
		PrivateNet: []hcloud.ServerPrivateNet{{
			Network: &hcloud.Network{ID: networkID},
			IP:      net.ParseIP("10.0.0." + strconv.Itoa(int(id+10))),
		}},
	}
	f.Servers[id] = srv
	return srv
}

// SeedOrphan inserts a complete=false orphan server for (slotID, generation).
// The pre-flight cleanup phase is expected to destroy it.
func (f *Fake) SeedOrphan(group string, slotID, generation int, networkID int64) *hcloud.Server {
	srv := f.SeedServer(group, slotID, generation, networkID)
	srv.Labels[hcloudx.LabelComplete] = "false"
	return srv
}

// SeedPrivateIP returns the private IP that SeedServer would assign for a
// freshly-seeded server. Tests use it to populate prior-state slots
// without re-deriving the formula.
func SeedPrivateIP(serverID int64) string {
	return "10.0.0." + strconv.Itoa(int(serverID+10))
}

func cloneServer(s *hcloud.Server) *hcloud.Server {
	if s == nil {
		return nil
	}
	out := *s
	out.Labels = cloneLabels(s.Labels)
	out.PrivateNet = append([]hcloud.ServerPrivateNet(nil), s.PrivateNet...)
	return &out
}

func cloneLabels(in map[string]string) map[string]string {
	if in == nil {
		return nil
	}
	out := make(map[string]string, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}
