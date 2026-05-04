package reconciler

import (
	"context"
	"fmt"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

// Apply is the entry point for an Update operation. The desired state is
// in r.group; the prior tofu state is in r.state on entry. Apply walks
// the four phases in spec section 6.1 order:
//
//  1. pre-flight cleanup of orphans (complete=false) and out-of-range slots
//  2. remove slots beyond desired.Count, walking N-1 down
//  3. replace slots whose recorded hash differs from the new hash
//  4. create new slots beyond the previous count
//
// Each slot transition writes partial state via the runner's progress
// callback so a graceful error preserves work done so far.
func (r *runner) Apply(ctx context.Context) error {
	if err := r.preflight(ctx); err != nil {
		return err
	}
	if err := r.phaseRemove(ctx); err != nil {
		return err
	}
	if err := r.phaseReplace(ctx); err != nil {
		return err
	}
	if err := r.phaseCreate(ctx); err != nil {
		return err
	}
	return nil
}

// preflight destroys orphans and out-of-range slots, then re-fetches the
// observed map so subsequent phases see reality. The re-fetch is critical:
// canonical-picking would otherwise see tombstones that have just been
// deleted but are still in the local cache.
func (r *runner) preflight(ctx context.Context) error {
	toDestroy := r.preflightTargets()
	for _, srv := range toDestroy {
		// WaitFor is outside the Retry closure: a transient WaitFor failure
		// must not re-issue DeleteServer, which would 404 on the second
		// attempt (server already gone) and surface as a terminal error.
		var action *hcloud.Action
		if err := hcloudx.Retry(ctx, func(ctx context.Context) error {
			a, derr := r.client.DeleteServer(ctx, srv.ID)
			if derr != nil {
				return derr
			}
			action = a
			return nil
		}); err != nil {
			return fmt.Errorf("preflight: delete server %d: %w", srv.ID, err)
		}
		if err := hcloudx.WaitFor(ctx, r.client, action); err != nil {
			return fmt.Errorf("preflight: wait delete server %d: %w", srv.ID, err)
		}
	}

	if len(toDestroy) == 0 {
		return nil
	}
	servers, err := r.client.ListByGroup(ctx, r.group.Name)
	if err != nil {
		return fmt.Errorf("preflight: re-list: %w", err)
	}
	r.observed = hcloudx.PartitionBySlot(servers)
	return nil
}

// preflightTargets identifies the servers that pre-flight should destroy.
// Two categories qualify:
//
//  1. Orphans: any server with complete=false. These are residue from a
//     crashed mid-create; they have no associated state record and no
//     before_remove hook should fire.
//  2. Stragglers: out-of-range servers (slot >= new count) that the
//     reconciler does not track in tofu state. These are residue from a
//     crashed prior apply that scaled down without finishing.
//
// Healthy in-state out-of-range servers are NOT destroyed here - those
// are scale-down's job, handled by phaseRemove which runs the operator's
// before_remove and post_remove hooks. Treating them here would silently
// skip those hooks and then 404 in phaseRemove's DeleteServer call.
func (r *runner) preflightTargets() []*hcloudServer {
	inState := map[int]bool{}
	for _, sl := range r.state.Slots {
		inState[sl.SlotID] = true
	}
	out := []*hcloudServer{}
	for slotID, observations := range r.observed {
		for _, obs := range observations {
			isOrphan := !obs.Complete
			isStraggler := slotID >= r.group.Count && !inState[slotID]
			if isOrphan || isStraggler {
				out = append(out, &hcloudServer{ID: obs.Server.ID, Name: obs.Server.Name})
			}
		}
	}
	return out
}

// hcloudServer is a trimmed local view used only by preflightTargets so
// the test fakes don't need to construct full *hcloud.Server values.
type hcloudServer struct {
	ID   int64
	Name string
}

// phaseRemove handles scale-down. Walks slot indices N-1..desired.Count.
// Removing in reverse order is a courtesy: cluster-aware before_remove
// hooks typically prefer to drain the highest-numbered (and usually
// most-recently-added) member first.
func (r *runner) phaseRemove(ctx context.Context) error {
	highestExisting := -1
	for _, sl := range r.state.Slots {
		if sl.SlotID > highestExisting {
			highestExisting = sl.SlotID
		}
	}
	for i := highestExisting; i >= r.group.Count; i-- {
		if r.state.SlotByID(i) == nil {
			continue
		}
		if err := r.RemoveSlot(ctx, i); err != nil {
			return err
		}
	}
	return nil
}

// phaseReplace rolls slots whose recorded hash differs from the new hash.
// Walks 0..desired.Count-1 sequentially so cluster-join templates see a
// consistent peer set during the roll.
func (r *runner) phaseReplace(ctx context.Context) error {
	for i := 0; i < r.group.Count; i++ {
		sl := r.state.SlotByID(i)
		if sl == nil {
			continue // will be created in phaseCreate
		}
		if sl.ReplaceHash == r.group.HashFull && sl.Status == StatusReady {
			continue
		}
		newGen := r.nextGenerationFor(i)
		if err := r.ReplaceSlot(ctx, i, newGen); err != nil {
			return err
		}
	}
	return nil
}

// phaseCreate fills in slots that don't yet exist, walking 0..Count-1.
// Sequential iteration is required so slot K's user_data can reference
// slot K-1's PrivateIP via .Peers.
func (r *runner) phaseCreate(ctx context.Context) error {
	for i := 0; i < r.group.Count; i++ {
		if r.state.SlotByID(i) != nil {
			continue
		}
		gen := r.nextGenerationFor(i)
		if err := r.CreateSlot(ctx, i, gen); err != nil {
			return err
		}
	}
	return nil
}
