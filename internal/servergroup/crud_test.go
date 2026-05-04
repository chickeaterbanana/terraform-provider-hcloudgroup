package servergroup

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/reconciler"
)

// TestShouldRemoveResource_Matrix guards Finding 7: Read should call
// State.RemoveResource only when prior tofu state had servers and the
// current observation finds none. The guard on prior.Slots prevents a
// freshly-imported resource (where slots is not yet populated) from
// being silently removed before the framework's first refresh runs.
func TestShouldRemoveResource_Matrix(t *testing.T) {
	withSlot := reconciler.State{Slots: []reconciler.SlotState{{SlotID: 0}}}
	empty := reconciler.State{}

	tests := []struct {
		name     string
		observed reconciler.State
		prior    reconciler.State
		want     bool
	}{
		{name: "all servers deleted out-of-band → remove", observed: empty, prior: withSlot, want: true},
		{name: "fresh import, slots not yet populated → keep", observed: empty, prior: empty, want: false},
		{name: "normal refresh with servers → keep", observed: withSlot, prior: withSlot, want: false},
		{name: "post-create, prior empty, observed populated → keep", observed: withSlot, prior: empty, want: false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, shouldRemoveResource(tc.observed, tc.prior))
		})
	}
}
