package reconciler

import (
	"context"
	"fmt"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

// Reconciler is the public façade of this package. It owns the hcloud
// client and exposes Apply, Destroy, and Observe. It is constructed once
// per CRUD invocation; there's no long-lived state.
type Reconciler struct {
	Client hcloudx.Client
}

// New returns a Reconciler bound to the given client.
func New(c hcloudx.Client) *Reconciler { return &Reconciler{Client: c} }

// Apply reconciles desired (group) against observed reality and the
// supplied prior state. Returns the new state regardless of whether an
// error was raised, so the caller can write partial progress to tofu
// state before surfacing the diagnostic.
func (rc *Reconciler) Apply(ctx context.Context, group Group, prior State, onProgress ProgressFn) (State, error) {
	servers, err := rc.Client.ListByGroup(ctx, group.Name)
	if err != nil {
		return prior, fmt.Errorf("list servers: %w", err)
	}
	resolved, err := rc.Client.ResolveSSHKeys(ctx, group.SSHKeyNames)
	if err != nil {
		return prior, fmt.Errorf("resolve ssh keys: %w", err)
	}
	r := &runner{
		client:      rc.Client,
		group:       group,
		state:       &State{Slots: append([]SlotState(nil), prior.Slots...)},
		observed:    hcloudx.PartitionBySlot(servers),
		resolvedSSH: resolved,
		onProgress:  onProgress,
	}
	err = r.Apply(ctx)
	return *r.state, err
}

// Destroy walks every slot through the REMOVE FLOW. It does not run
// pre-flight, but does sweep up orphans (complete=false servers) at the
// end so an interrupted Destroy converges on retry.
func (rc *Reconciler) Destroy(ctx context.Context, group Group, prior State, onProgress ProgressFn) (State, error) {
	servers, err := rc.Client.ListByGroup(ctx, group.Name)
	if err != nil {
		return prior, fmt.Errorf("list servers: %w", err)
	}
	r := &runner{
		client:     rc.Client,
		group:      group,
		state:      &State{Slots: append([]SlotState(nil), prior.Slots...)},
		observed:   hcloudx.PartitionBySlot(servers),
		onProgress: onProgress,
	}
	for i := highestSlotID(r.state) ; i >= 0; i-- {
		if r.state.SlotByID(i) == nil {
			continue
		}
		if err := r.RemoveSlot(ctx, i); err != nil {
			return *r.state, err
		}
	}

	// Sweep any orphans that bypassed our state (e.g., crashed mid-create).
	for slotID, list := range r.observed {
		for _, obs := range list {
			if obs.Server == nil {
				continue
			}
			if err := hcloudx.Retry(ctx, func(ctx context.Context) error {
				action, derr := rc.Client.DeleteServer(ctx, obs.Server.ID)
				if derr != nil {
					return derr
				}
				return hcloudx.WaitFor(ctx, rc.Client, action)
			}); err != nil {
				return *r.state, fmt.Errorf("destroy: sweep slot %d server %d: %w", slotID, obs.Server.ID, err)
			}
		}
	}
	return *r.state, nil
}

// Observe rebuilds State from hcloud labels, preserving the ReplaceHash
// values from prior state since the full hash isn't stored on the server
// (only the 12-char prefix is). Slots whose canonical server is missing
// are dropped from the result; this produces a plan diff that triggers a
// re-create on the next apply.
func (rc *Reconciler) Observe(ctx context.Context, group Group, prior State) (State, error) {
	servers, err := rc.Client.ListByGroup(ctx, group.Name)
	if err != nil {
		return prior, fmt.Errorf("list servers: %w", err)
	}
	observed := hcloudx.PartitionBySlot(servers)
	priorBySlot := map[int]SlotState{}
	for _, sl := range prior.Slots {
		priorBySlot[sl.SlotID] = sl
	}

	out := State{}
	for slot := 0; slot < group.Count; slot++ {
		canonical, ok := hcloudx.PickCanonical(observed[slot])
		if !ok {
			continue
		}
		entry := SlotState{
			SlotID:     slot,
			ServerID:   canonical.Server.ID,
			ServerName: canonical.Server.Name,
			Generation: canonical.Generation,
			PrivateIP:  findPrivateIP(canonical.Server, group.NetworkID),
			Status:     StatusReady,
		}
		if old, found := priorBySlot[slot]; found {
			entry.ReplaceHash = old.ReplaceHash
		}
		out.Slots = append(out.Slots, entry)
	}
	return out, nil
}

func highestSlotID(s *State) int {
	max := -1
	for _, sl := range s.Slots {
		if sl.SlotID > max {
			max = sl.SlotID
		}
	}
	return max
}
