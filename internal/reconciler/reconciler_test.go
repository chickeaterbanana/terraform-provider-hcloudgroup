package reconciler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/actions"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

const testGroup = "g"
const testNetwork int64 = 1234

// recordingAction is a stub Action that increments a counter and records
// each slot it was invoked against. Used to assert lifecycle hook ordering.
type recordingAction struct {
	calls *int32
	slots *[]int
}

func (r recordingAction) Run(_ context.Context, sc slotctx.SlotContext) actions.Result {
	if r.calls != nil {
		atomic.AddInt32(r.calls, 1)
	}
	if r.slots != nil {
		*r.slots = append(*r.slots, sc.SlotID)
	}
	return actions.Result{}
}

func defaultGroup(count int) Group {
	hi := HashInputs{
		Image: "ubuntu", ServerType: "cx22", Location: "fsn1",
		NetworkID: testNetwork,
	}
	full, prefix := hi.Hash()
	return Group{
		Name: testGroup, Count: count,
		Image: "ubuntu", ServerType: "cx22", Location: "fsn1", NetworkID: testNetwork,
		HashFull: full, HashPrefix: prefix,
		Actions: ActionSet{
			BeforeCreate: actions.Null{}, PostCreate: actions.Null{},
			BeforeReplace: actions.Null{}, PostReplace: actions.Null{},
			BeforeRemove: actions.Null{}, PostRemove: actions.Null{},
		},
	}
}

func TestApply_InitialCreate_SequentialAndAllComplete(t *testing.T) {
	c := newFakeClient()
	r := New(c)
	state, err := r.Apply(context.Background(), defaultGroup(3), State{}, nil)
	require.NoError(t, err)

	require.Len(t, state.Slots, 3)
	for i, sl := range state.Slots {
		require.Equal(t, i, sl.SlotID)
		require.Equal(t, 1, sl.Generation)
		require.Equal(t, StatusReady, sl.Status)
		require.NotEmpty(t, sl.PrivateIP)
	}
	require.Equal(t, []string{"g-0-1", "g-1-1", "g-2-1"}, c.createdNames)

	// Every server must end with complete=true.
	servers, _ := c.ListByGroup(context.Background(), testGroup)
	require.Len(t, servers, 3)
	for _, s := range servers {
		require.Equal(t, "true", s.Labels["hcloudgroup.io/complete"])
	}
}

func TestApply_NoOpWhenHashUnchanged(t *testing.T) {
	c := newFakeClient()
	g := defaultGroup(2)
	prior := State{}
	for i := 0; i < g.Count; i++ {
		c.seedServer(testGroup, i, 1, testNetwork)
		prior.Slots = append(prior.Slots, SlotState{
			SlotID: i, ServerID: int64(i + 1),
			ServerName: ServerName(testGroup, i, 1),
			Generation: 1, ReplaceHash: g.HashFull, Status: StatusReady,
			PrivateIP: "10.0.0." + intToStr(i+11),
		})
	}
	r := New(c)
	_, err := r.Apply(context.Background(), g, prior, nil)
	require.NoError(t, err)
	require.Equal(t, 0, c.createCalls, "no servers should be created on no-op")
	require.Equal(t, 0, c.deleteCalls, "no servers should be deleted on no-op")
}

func TestApply_ReplaceFlow_OnHashChange_RunsHooksAndIncrementsGeneration(t *testing.T) {
	c := newFakeClient()
	g := defaultGroup(2)

	// Seed two existing slots with the OLD hash recorded in tofu state.
	prior := State{}
	for i := 0; i < g.Count; i++ {
		srv := c.seedServer(testGroup, i, 1, testNetwork)
		prior.Slots = append(prior.Slots, SlotState{
			SlotID: i, ServerID: srv.ID,
			ServerName: srv.Name, Generation: 1,
			ReplaceHash: "OLD_HASH", Status: StatusReady,
			PrivateIP: "10.0.0." + intToStr(int(srv.ID)+10),
		})
	}

	var beforeReplaceCalls, postReplaceCalls int32
	g.Actions.BeforeReplace = recordingAction{calls: &beforeReplaceCalls}
	g.Actions.PostReplace = recordingAction{calls: &postReplaceCalls}

	r := New(c)
	state, err := r.Apply(context.Background(), g, prior, nil)
	require.NoError(t, err)

	require.Len(t, state.Slots, 2)
	for _, sl := range state.Slots {
		require.Equal(t, 2, sl.Generation, "generation must be 1 + max observed (which was 1) = 2")
		require.Equal(t, g.HashFull, sl.ReplaceHash)
	}
	require.Equal(t, int32(2), beforeReplaceCalls)
	require.Equal(t, int32(2), postReplaceCalls)
	require.Equal(t, 2, c.deleteCalls, "two old servers deleted")
	require.Equal(t, 2, c.createCalls, "two new servers created")
}

// This is the scale-down regression test. Concern 1 from the advisor:
// in-state out-of-range slots were being destroyed by pre-flight,
// silently bypassing before_remove/post_remove and 404'ing in phaseRemove.
func TestApply_ScaleDown_InvokesRemoveHooks_NotPreflight(t *testing.T) {
	c := newFakeClient()
	g := defaultGroup(5)

	prior := State{}
	for i := 0; i < g.Count; i++ {
		srv := c.seedServer(testGroup, i, 1, testNetwork)
		prior.Slots = append(prior.Slots, SlotState{
			SlotID: i, ServerID: srv.ID,
			ServerName: srv.Name, Generation: 1,
			ReplaceHash: g.HashFull, Status: StatusReady,
			PrivateIP: "10.0.0." + intToStr(int(srv.ID)+10),
		})
	}

	g.Count = 3
	var beforeRemoveCalls, postRemoveCalls int32
	var beforeRemoveSlots []int
	g.Actions.BeforeRemove = recordingAction{calls: &beforeRemoveCalls, slots: &beforeRemoveSlots}
	g.Actions.PostRemove = recordingAction{calls: &postRemoveCalls}

	r := New(c)
	state, err := r.Apply(context.Background(), g, prior, nil)
	require.NoError(t, err)

	require.Len(t, state.Slots, 3, "slots 3 and 4 removed")
	require.Equal(t, int32(2), beforeRemoveCalls, "before_remove must fire for each removed slot")
	require.Equal(t, int32(2), postRemoveCalls, "post_remove must fire for each removed slot")
	require.Equal(t, []int{4, 3}, beforeRemoveSlots, "scale-down walks slots in reverse order")
	require.Equal(t, 2, c.deleteCalls)
	require.Equal(t, 0, c.createCalls)
}

func TestApply_ScaleUp_OnlyCreatesNewSlots(t *testing.T) {
	c := newFakeClient()
	g := defaultGroup(3) // grow to 3
	// existing: 2 slots
	prior := State{}
	for i := 0; i < 2; i++ {
		srv := c.seedServer(testGroup, i, 1, testNetwork)
		prior.Slots = append(prior.Slots, SlotState{
			SlotID: i, ServerID: srv.ID, ServerName: srv.Name,
			Generation: 1, ReplaceHash: g.HashFull, Status: StatusReady,
			PrivateIP: "10.0.0." + intToStr(int(srv.ID)+10),
		})
	}

	r := New(c)
	state, err := r.Apply(context.Background(), g, prior, nil)
	require.NoError(t, err)
	require.Len(t, state.Slots, 3)
	require.Equal(t, 1, c.createCalls)
	require.Equal(t, 0, c.deleteCalls)
}

func TestApply_PreflightCleansOrphans_NotInStateOutOfRange(t *testing.T) {
	c := newFakeClient()
	g := defaultGroup(2)

	// Seed a complete=false orphan for slot 0 - simulates a crashed
	// mid-create from the previous apply.
	c.seedServer(testGroup, 0, 1, testNetwork)
	for id, s := range c.servers {
		s.Labels["hcloudgroup.io/complete"] = "false"
		c.servers[id] = s
	}

	// Seed a healthy slot 5 with NO entry in tofu state - simulates a
	// crashed scale-down from the previous apply.
	c.seedServer(testGroup, 5, 1, testNetwork)

	r := New(c)
	state, err := r.Apply(context.Background(), g, State{}, nil)
	require.NoError(t, err)

	require.Len(t, state.Slots, 2)
	require.Equal(t, 2, c.deleteCalls, "preflight destroys orphan + straggler")
	require.Equal(t, 2, c.createCalls, "two slots freshly created (the orphan was reset)")
}

func TestProgressFn_FiresAfterEachSlot(t *testing.T) {
	c := newFakeClient()
	g := defaultGroup(3)
	var snapshotsLen []int
	progress := func(_ context.Context, snap State) error {
		snapshotsLen = append(snapshotsLen, len(snap.Slots))
		return nil
	}
	r := New(c)
	_, err := r.Apply(context.Background(), g, State{}, progress)
	require.NoError(t, err)
	require.Equal(t, []int{1, 2, 3}, snapshotsLen, "progress must fire after each slot completes")
}

// Defensive: keep our test execution under wall-clock budget. The fake
// client returns instantly so anything taking more than a second
// indicates a deadlock or unintended sleep.
func TestApply_CompletesQuickly(t *testing.T) {
	c := newFakeClient()
	r := New(c)
	done := make(chan struct{})
	go func() {
		_, _ = r.Apply(context.Background(), defaultGroup(5), State{}, nil)
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Apply hung past 2 seconds against fake client")
	}
}
