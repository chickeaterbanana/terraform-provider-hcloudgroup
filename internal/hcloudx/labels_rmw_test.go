package hcloudx_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx"
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/hcloudx/hcloudxtest"
)

func TestSetProviderLabel_PreservesUserAndProviderLabels(t *testing.T) {
	c := hcloudxtest.NewFake()
	srv := c.SeedServer("g", 0, 1, 12)
	// Add a user label so the test can prove RMW preserves it.
	srv.Labels["env"] = "prod"
	srv.Labels[hcloudx.LabelComplete] = "false"

	err := hcloudx.SetProviderLabel(context.Background(), c, srv.ID, hcloudx.LabelComplete, "true")
	require.NoError(t, err)
	require.Equal(t, "true", c.Servers[srv.ID].Labels[hcloudx.LabelComplete])
	require.Equal(t, "prod", c.Servers[srv.ID].Labels["env"], "user label must survive")
	require.Equal(t, hcloudx.ManagedByValue, c.Servers[srv.ID].Labels[hcloudx.LabelManagedBy], "other provider labels must survive")
	require.Equal(t, 1, c.GetServerCalls, "RMW must read once")
	require.Equal(t, 1, c.UpdateLabelsCalls, "RMW must write once")
}

func TestSetProviderLabel_AddsNewKeyWhenAbsent(t *testing.T) {
	c := hcloudxtest.NewFake()
	srv := c.SeedServer("g", 0, 1, 12)
	delete(srv.Labels, hcloudx.LabelComplete)

	err := hcloudx.SetProviderLabel(context.Background(), c, srv.ID, hcloudx.LabelComplete, "true")
	require.NoError(t, err)
	require.Equal(t, "true", c.Servers[srv.ID].Labels[hcloudx.LabelComplete])
}

func TestSetProviderLabel_PropagatesGetError(t *testing.T) {
	c := hcloudxtest.NewFake()
	want := errors.New("server not found")
	c.FailGetServerErr = want

	err := hcloudx.SetProviderLabel(context.Background(), c, 999, hcloudx.LabelComplete, "true")
	require.ErrorIs(t, err, want)
	require.Equal(t, 0, c.UpdateLabelsCalls, "must not write when read fails")
}

func TestSetProviderLabel_PropagatesUpdateError(t *testing.T) {
	c := hcloudxtest.NewFake()
	srv := c.SeedServer("g", 0, 1, 12)
	c.FailOnUpdateLabelsID = srv.ID
	c.FailOnUpdateLabelsErr = errors.New("update failed")

	err := hcloudx.SetProviderLabel(context.Background(), c, srv.ID, hcloudx.LabelComplete, "true")
	require.Error(t, err)
}
