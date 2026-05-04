package reconciler

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/actions"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
	tmpl "github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/template"
)

// ProgressFn is invoked after each slot transition so the resource layer
// can persist partial progress to tofu state. The callback receives a
// snapshot of the current State; if it returns an error the reconciler
// stops and surfaces that error (this is rare - it typically means the
// framework rejected the state write, not a hcloud problem).
type ProgressFn func(ctx context.Context, snapshot State) error

// runner bundles the dependencies a slot transition needs: the hcloud
// client wrapper, the desired group spec, the in-memory state, the
// observed-servers map (slot -> all observations), and the
// progress-reporting callback.
//
// genHighWater holds the maximum generation seen per slot at the moment
// pre-flight ran, including any orphans pre-flight is about to destroy.
// This pins the README §5.4 contract that the next generation must be
// max(observed)+1 across both canonical AND orphan, so a recreated server
// cannot collide with the just-deleted orphan's name (Hetzner refuses
// duplicate server names; the resulting 409 is opaque to the operator).
type runner struct {
	client       hcloudx.Client
	group        Group
	state        *State
	observed     map[int][]hcloudx.Observation
	genHighWater map[int]int
	resolvedSSH  []*hcloud.SSHKey
	onProgress   ProgressFn
}

// reportProgress writes the current state snapshot to the caller. Any
// error from the callback aborts the apply.
func (r *runner) reportProgress(ctx context.Context) error {
	if r.onProgress == nil {
		return nil
	}
	return r.onProgress(ctx, *r.state)
}

// markFailed mutates the in-memory state for a slot to record an error.
// The reconciler then returns the underlying error to the caller; the
// callback fires from reportProgress so the state write is not lost.
func (r *runner) markFailed(slotID int, phase string, cause error, stdout, stderr string) *SlotError {
	se := &SlotError{SlotID: slotID, Phase: phase, Cause: cause, Stdout: stdout, Stderr: stderr}
	if existing := r.state.SlotByID(slotID); existing != nil {
		existing.Status = StatusFailed
		existing.LastError = se.Error()
	} else {
		r.state.Upsert(SlotState{
			SlotID:    slotID,
			Status:    StatusFailed,
			LastError: se.Error(),
		})
	}
	return se
}

// nextGenerationFor returns the next generation number for a slot,
// derived from the maximum generation seen across both canonical and
// orphan servers (spec section 5.4). When the slot is brand-new the
// pre-cleanup high-water entry is zero and the result is 1.
//
// Reads from genHighWater (snapshotted before pre-flight cleanup) rather
// than r.observed: the latter loses orphan generations once pre-flight
// destroys them, which would let a new server collide with a just-deleted
// orphan's name.
func (r *runner) nextGenerationFor(slotID int) int {
	gen := r.genHighWater[slotID]
	if cur := hcloudx.MaxObservedGeneration(r.observed[slotID]); cur > gen {
		gen = cur
	}
	return gen + 1
}

// runAction wraps an Action.Run with normalization: nil becomes Null, and
// the result is mapped to a non-nil error iff the action failed.
func runAction(ctx context.Context, a actions.Action, sc slotctx.SlotContext) actions.Result {
	if a == nil {
		return actions.Null{}.Run(ctx, sc)
	}
	return a.Run(ctx, sc)
}

// findPrivateIP returns the server's IP on the configured network, or ""
// if it is not yet attached to that network. Hcloud reports private IPs
// once the server is created and attached; the IP is stable for the life
// of the server.
func findPrivateIP(srv *hcloud.Server, networkID int64) string {
	if srv == nil {
		return ""
	}
	for _, pn := range srv.PrivateNet {
		if pn.Network != nil && pn.Network.ID == networkID {
			return pn.IP.String()
		}
	}
	return ""
}

// buildSlotCtx materializes a slotctx.SlotContext for the given slot at
// the given generation. peerSlot is the slot whose entry should be
// excluded from .Peers (typically the slot being acted on; for initial
// creation, peers are inferred from already-created lower slots).
func (r *runner) buildSlotCtx(slotID, generation int, srv *hcloud.Server) slotctx.SlotContext {
	sc := slotctx.SlotContext{
		GroupName:  r.group.Name,
		SlotID:     slotID,
		Generation: generation,
		ServerName: ServerName(r.group.Name, slotID, generation),
		Peers:      peersExcluding(r.state, slotID),
		Now:        time.Now().UTC(),
	}
	if srv != nil {
		sc.ServerID = srv.ID
		sc.PrivateIP = findPrivateIP(srv, r.group.NetworkID)
	}
	return sc
}

// renderUserData renders the user_data template against the slot context.
// An empty template yields an empty string and no error.
func (r *runner) renderUserData(sc slotctx.SlotContext) (string, error) {
	if r.group.UserDataTemplate == "" {
		return "", nil
	}
	out, err := tmpl.Render(r.group.UserDataTemplate, sc)
	if err != nil {
		return "", fmt.Errorf("user_data render: %w", err)
	}
	return out, nil
}

// errSlotInactive is returned when a flow tries to operate on a slot
// missing from observed state when the flow expected one. It signals a
// programming bug, not a runtime condition - the caller should never see
// it under correct phase ordering.
var errSlotInactive = errors.New("slot has no observed canonical server")
