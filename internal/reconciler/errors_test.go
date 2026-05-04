package reconciler_test

import (
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/reconciler"
)

func TestSlotError_Error_FormatsSlotPhaseAndCause(t *testing.T) {
	cause := errors.New("ssh dial timeout")
	err := &reconciler.SlotError{SlotID: 2, Phase: "readiness_probe", Cause: cause}
	require.Equal(t, "slot 2 readiness_probe: ssh dial timeout", err.Error())
}

func TestSlotError_Error_NilSafe(t *testing.T) {
	var err *reconciler.SlotError
	require.Equal(t, "", err.Error())
}

func TestSlotError_Unwrap_ReturnsCause(t *testing.T) {
	cause := errors.New("boom")
	err := &reconciler.SlotError{SlotID: 0, Phase: "before_create", Cause: cause}
	require.True(t, errors.Is(err, cause), "errors.Is should find the wrapped cause")
}

func TestSlotError_Unwrap_PassesAsThrough(t *testing.T) {
	cause := errors.New("inner")
	err := &reconciler.SlotError{Cause: cause}
	require.Same(t, cause, err.Unwrap())
}
