package reconciler

import (
	"context"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
)

// CreateSlot runs the full CREATE FLOW (spec section 6) for a slot at the
// given generation. The slot's State entry is upserted as it progresses
// so partial-progress callers see the latest known fields.
func (r *runner) CreateSlot(ctx context.Context, slotID, generation int) error {
	if err := r.innerCreate(ctx, slotID, generation); err != nil {
		return err
	}
	return r.reportProgress(ctx)
}

// RemoveSlot runs the full REMOVE FLOW for a slot. The slot is dropped
// from State on success.
func (r *runner) RemoveSlot(ctx context.Context, slotID int) error {
	if err := r.innerRemove(ctx, slotID); err != nil {
		return err
	}
	r.state.RemoveSlot(slotID)
	return r.reportProgress(ctx)
}

// ReplaceSlot runs the full REPLACE FLOW: before_replace, the inner
// remove, the inner create at newGeneration, post_replace.
func (r *runner) ReplaceSlot(ctx context.Context, slotID, newGeneration int) error {
	prior := r.state.SlotByID(slotID)
	if prior == nil {
		return r.markFailed(slotID, "before_replace", errSlotInactive, "", "")
	}

	srv, _ := r.serverFor(slotID)
	scBefore := r.buildSlotCtx(slotID, prior.Generation, srv)
	if res := runAction(ctx, r.group.Actions.BeforeReplace, scBefore); res.Err != nil {
		return r.markFailed(slotID, "before_replace", res.Err, res.Stdout, res.Stderr)
	}

	if err := r.innerRemove(ctx, slotID); err != nil {
		return err
	}
	if err := r.innerCreate(ctx, slotID, newGeneration); err != nil {
		return err
	}

	created := r.state.SlotByID(slotID)
	srvAfter, _ := r.serverFor(slotID)
	scAfter := r.buildSlotCtx(slotID, created.Generation, srvAfter)
	if res := runAction(ctx, r.group.Actions.PostReplace, scAfter); res.Err != nil {
		return r.markFailed(slotID, "post_replace", res.Err, res.Stdout, res.Stderr)
	}

	return r.reportProgress(ctx)
}

// innerCreate is the CREATE FLOW shared between fresh creation and the
// create-half of a replace. On success the slot's State entry has
// Status=ready and complete=true on the hcloud server.
func (r *runner) innerCreate(ctx context.Context, slotID, generation int) error {
	scBefore := r.buildSlotCtx(slotID, generation, nil)
	if res := runAction(ctx, r.group.Actions.BeforeCreate, scBefore); res.Err != nil {
		return r.markFailed(slotID, "before_create", res.Err, res.Stdout, res.Stderr)
	}

	userData, err := r.renderUserData(scBefore)
	if err != nil {
		return r.markFailed(slotID, "user_data_render", err, "", "")
	}

	labels := hcloudx.MergeForCreate(r.group.UserLabels, r.group.Name, slotID, generation, r.group.HashPrefix)
	opts := hcloud.ServerCreateOpts{
		Name:       ServerName(r.group.Name, slotID, generation),
		ServerType: &hcloud.ServerType{Name: r.group.ServerType},
		Image:      &hcloud.Image{Name: r.group.Image},
		Location:   &hcloud.Location{Name: r.group.Location},
		Networks:   []*hcloud.Network{{ID: r.group.NetworkID}},
		SSHKeys:    r.resolvedSSH,
		UserData:   userData,
		Labels:     labels,
	}

	var (
		srv    *hcloud.Server
		action *hcloud.Action
	)
	err = hcloudx.Retry(ctx, func(ctx context.Context) error {
		var cerr error
		srv, action, cerr = r.client.CreateServer(ctx, opts)
		return cerr
	})
	if err != nil {
		return r.markFailed(slotID, "server_create", err, "", "")
	}
	if err := hcloudx.WaitFor(ctx, r.client, action); err != nil {
		return r.markFailed(slotID, "server_create_wait", err, "", "")
	}

	// Re-fetch so PrivateNet is populated. The Create response does not
	// always contain network attachments synchronously.
	if reread, gerr := r.client.GetServer(ctx, srv.ID); gerr == nil && reread != nil {
		srv = reread
	}
	r.recordObserved(slotID, srv, generation, false)

	created := SlotState{
		SlotID:      slotID,
		ServerID:    srv.ID,
		ServerName:  srv.Name,
		Generation:  generation,
		ReplaceHash: r.group.HashFull,
		PrivateIP:   findPrivateIP(srv, r.group.NetworkID),
		Status:      StatusReady,
	}
	r.state.Upsert(created)

	scProbe := r.buildSlotCtx(slotID, generation, srv)
	if r.group.ReadinessProbe != nil {
		if res := r.group.ReadinessProbe.Run(ctx, scProbe); res.Err != nil {
			return r.markFailed(slotID, "readiness_probe", res.Err, res.Stdout, res.Stderr)
		}
	}

	if res := runAction(ctx, r.group.Actions.PostCreate, scProbe); res.Err != nil {
		return r.markFailed(slotID, "post_create", res.Err, res.Stdout, res.Stderr)
	}

	if err := hcloudx.SetProviderLabel(ctx, r.client, srv.ID, hcloudx.LabelComplete, "true"); err != nil {
		return r.markFailed(slotID, "label_complete", err, "", "")
	}
	r.recordObserved(slotID, srv, generation, true)

	return nil
}

// innerRemove is the REMOVE FLOW shared between scale-down and the
// remove-half of a replace.
func (r *runner) innerRemove(ctx context.Context, slotID int) error {
	prior := r.state.SlotByID(slotID)
	if prior == nil || prior.ServerID == 0 {
		return r.markFailed(slotID, "before_remove", errSlotInactive, "", "")
	}

	srv, _ := r.serverFor(slotID)
	scBefore := r.buildSlotCtx(slotID, prior.Generation, srv)
	if res := runAction(ctx, r.group.Actions.BeforeRemove, scBefore); res.Err != nil {
		return r.markFailed(slotID, "before_remove", res.Err, res.Stdout, res.Stderr)
	}

	var action *hcloud.Action
	err := hcloudx.Retry(ctx, func(ctx context.Context) error {
		var derr error
		action, derr = r.client.DeleteServer(ctx, prior.ServerID)
		return derr
	})
	if err != nil {
		return r.markFailed(slotID, "server_delete", err, "", "")
	}
	if err := hcloudx.WaitFor(ctx, r.client, action); err != nil {
		return r.markFailed(slotID, "server_delete_wait", err, "", "")
	}

	r.dropObserved(slotID, prior.ServerID)

	scAfter := r.buildSlotCtx(slotID, prior.Generation, nil)
	if res := runAction(ctx, r.group.Actions.PostRemove, scAfter); res.Err != nil {
		return r.markFailed(slotID, "post_remove", res.Err, res.Stdout, res.Stderr)
	}

	return nil
}

// serverFor returns the canonical observed *hcloud.Server for slotID, if
// one exists. The reconciler keeps the observed map in sync as it
// creates and destroys servers, so this is cheap.
func (r *runner) serverFor(slotID int) (*hcloud.Server, bool) {
	for _, obs := range r.observed[slotID] {
		if obs.Complete {
			return obs.Server, true
		}
	}
	if len(r.observed[slotID]) > 0 {
		return r.observed[slotID][0].Server, true
	}
	return nil, false
}

// recordObserved updates the in-memory observed map after a successful
// create or label flip. Keeps subsequent peer-list builds and serverFor
// lookups consistent.
func (r *runner) recordObserved(slotID int, srv *hcloud.Server, generation int, complete bool) {
	obs := hcloudx.Observation{
		Server:     srv,
		SlotID:     slotID,
		Generation: generation,
		Complete:   complete,
		HashPrefix: r.group.HashPrefix,
	}
	list := r.observed[slotID]
	for i, o := range list {
		if o.Server != nil && srv != nil && o.Server.ID == srv.ID {
			list[i] = obs
			r.observed[slotID] = list
			return
		}
	}
	list = append(list, obs)
	r.observed[slotID] = list
}

// dropObserved removes a server from the observed map after a successful
// delete.
func (r *runner) dropObserved(slotID int, serverID int64) {
	list := r.observed[slotID]
	for i, o := range list {
		if o.Server != nil && o.Server.ID == serverID {
			r.observed[slotID] = append(list[:i], list[i+1:]...)
			return
		}
	}
}
