package actions

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

func TestNull_RunReturnsZeroResult(t *testing.T) {
	res := Null{}.Run(context.Background(), slotctx.SlotContext{})
	require.Equal(t, Result{}, res)
	require.NoError(t, res.Err)
	require.False(t, res.TimedOut)
	require.Equal(t, 0, res.ExitCode)
	require.Empty(t, res.Stdout)
	require.Empty(t, res.Stderr)
}

// Compile-time assertion: Null implements Action. If this stops compiling,
// the interface contract changed and this test pins it.
var _ Action = Null{}
