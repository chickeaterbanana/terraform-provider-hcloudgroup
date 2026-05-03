package reconciler

import (
	"context"
	"net"
	"sort"
	"sync/atomic"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// fakeClient is a minimal in-memory implementation of hcloudx.Client used
// by the reconciler tests. It is single-goroutine-only - tests run
// sequentially.
type fakeClient struct {
	servers      map[int64]*hcloud.Server
	nextServerID int64
	createCalls  int
	deleteCalls  int
	deletedIDs   []int64
	createdNames []string
	failOnCreate string // server name that should fail hcloud Create
}

func newFakeClient() *fakeClient {
	return &fakeClient{servers: map[int64]*hcloud.Server{}}
}

func (f *fakeClient) ListByGroup(_ context.Context, group string) ([]*hcloud.Server, error) {
	out := make([]*hcloud.Server, 0)
	for _, s := range f.servers {
		if s.Labels == nil {
			continue
		}
		if s.Labels["hcloudgroup.io/group"] == group {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out, nil
}

func (f *fakeClient) GetServer(_ context.Context, id int64) (*hcloud.Server, error) {
	srv, ok := f.servers[id]
	if !ok {
		return nil, hcloudErrorNotFound()
	}
	return cloneServer(srv), nil
}

func (f *fakeClient) CreateServer(_ context.Context, opts hcloud.ServerCreateOpts) (*hcloud.Server, *hcloud.Action, error) {
	f.createCalls++
	f.createdNames = append(f.createdNames, opts.Name)
	if opts.Name == f.failOnCreate {
		return nil, nil, &net.OpError{Op: "dial", Err: errFakeNetwork{}}
	}
	id := atomic.AddInt64(&f.nextServerID, 1)
	srv := &hcloud.Server{
		ID:     id,
		Name:   opts.Name,
		Labels: cloneLabels(opts.Labels),
		PrivateNet: []hcloud.ServerPrivateNet{{
			Network: &hcloud.Network{ID: opts.Networks[0].ID},
			IP:      net.ParseIP("10.0.0." + intToStr(int(id+10))),
		}},
	}
	f.servers[id] = srv
	return cloneServer(srv), nil, nil
}

func (f *fakeClient) DeleteServer(_ context.Context, id int64) (*hcloud.Action, error) {
	f.deleteCalls++
	f.deletedIDs = append(f.deletedIDs, id)
	delete(f.servers, id)
	return nil, nil
}

func (f *fakeClient) UpdateServerLabels(_ context.Context, id int64, labels map[string]string) (*hcloud.Server, error) {
	srv, ok := f.servers[id]
	if !ok {
		return nil, hcloudErrorNotFound()
	}
	srv.Labels = cloneLabels(labels)
	return cloneServer(srv), nil
}

func (f *fakeClient) WaitForAction(_ context.Context, _ *hcloud.Action) error {
	return nil
}

func (f *fakeClient) ResolveSSHKeys(_ context.Context, names []string) ([]*hcloud.SSHKey, error) {
	out := make([]*hcloud.SSHKey, 0, len(names))
	for i, n := range names {
		out = append(out, &hcloud.SSHKey{ID: int64(i + 1), Name: n})
	}
	return out, nil
}

// seedServer pretends a fully-completed server is already in hcloud for a
// given slot/generation. Used to set up "prior state" in tests.
func (f *fakeClient) seedServer(group string, slotID, generation int, networkID int64) *hcloud.Server {
	id := atomic.AddInt64(&f.nextServerID, 1)
	srv := &hcloud.Server{
		ID:   id,
		Name: ServerName(group, slotID, generation),
		Labels: map[string]string{
			"hcloudgroup.io/managed-by":   "hcloudgroup-provider",
			"hcloudgroup.io/group":        group,
			"hcloudgroup.io/slot":         intToStr(slotID),
			"hcloudgroup.io/generation":   intToStr(generation),
			"hcloudgroup.io/replace-hash": "seed12345678",
			"hcloudgroup.io/complete":     "true",
		},
		PrivateNet: []hcloud.ServerPrivateNet{{
			Network: &hcloud.Network{ID: networkID},
			IP:      net.ParseIP("10.0.0." + intToStr(int(id+10))),
		}},
	}
	f.servers[id] = srv
	return srv
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

func intToStr(i int) string {
	const digits = "0123456789"
	if i == 0 {
		return "0"
	}
	neg := i < 0
	if neg {
		i = -i
	}
	buf := [20]byte{}
	pos := len(buf)
	for i > 0 {
		pos--
		buf[pos] = digits[i%10]
		i /= 10
	}
	if neg {
		pos--
		buf[pos] = '-'
	}
	return string(buf[pos:])
}

// errFakeNetwork is a net.OpError-compatible error type used in tests
// that need a "transient" failure. The retry layer treats net.OpError as
// retryable; tests that expect a permanent failure should construct an
// hcloud.Error instead.
type errFakeNetwork struct{}

func (errFakeNetwork) Error() string { return "fake network error" }

// hcloudErrorNotFound returns a 404-style hcloud.Error - useful when a
// test needs the retry layer to treat the error as permanent.
func hcloudErrorNotFound() error {
	return hcloud.Error{Code: hcloud.ErrorCodeNotFound, Message: "not found"}
}
