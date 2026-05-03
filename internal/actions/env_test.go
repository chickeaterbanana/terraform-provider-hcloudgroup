package actions

import (
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

func TestBuildEnv_PopulatesProviderNamespace(t *testing.T) {
	sc := slotctx.SlotContext{
		GroupName:  "consul",
		SlotID:     2,
		Generation: 5,
		ServerName: "consul-2-5",
		ServerID:   42,
		PrivateIP:  "10.0.0.42",
		Now:        time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC),
		Peers: []slotctx.Peer{
			{SlotID: 0, ServerName: "consul-0-1", PrivateIP: "10.0.0.10", Generation: 1},
			{SlotID: 1, ServerName: "consul-1-1", PrivateIP: "10.0.0.11", Generation: 1},
		},
	}
	env, shadowed := BuildEnv(sc, map[string]string{"PATH": "/bin"})
	require.Empty(t, shadowed)

	got := envMap(env)
	require.Equal(t, "consul", got["HCLOUDGROUP_GROUP_NAME"])
	require.Equal(t, "2", got["HCLOUDGROUP_SLOT_ID"])
	require.Equal(t, "5", got["HCLOUDGROUP_GENERATION"])
	require.Equal(t, "consul-2-5", got["HCLOUDGROUP_SERVER_NAME"])
	require.Equal(t, "42", got["HCLOUDGROUP_SERVER_ID"])
	require.Equal(t, "10.0.0.42", got["HCLOUDGROUP_PRIVATE_IP"])
	require.Equal(t, "/bin", got["PATH"])
	require.Equal(t, "10.0.0.10 10.0.0.11", got["HCLOUDGROUP_PEER_PRIVATE_IPS"])
	require.Contains(t, got["HCLOUDGROUP_PEERS_JSON"], `"private_ip":"10.0.0.10"`)
}

func TestBuildEnv_OmitsServerVarsBeforeCreate(t *testing.T) {
	sc := slotctx.SlotContext{
		GroupName: "g", SlotID: 0, Generation: 1, ServerName: "g-0-1",
		Now: time.Now(),
	}
	env, _ := BuildEnv(sc, nil)
	got := envMap(env)
	_, hasID := got["HCLOUDGROUP_SERVER_ID"]
	_, hasIP := got["HCLOUDGROUP_PRIVATE_IP"]
	require.False(t, hasID, "SERVER_ID must be unset before server exists")
	require.False(t, hasIP, "PRIVATE_IP must be unset before server exists")
}

func TestBuildEnv_RejectsShadowingOperatorEnv(t *testing.T) {
	sc := slotctx.SlotContext{GroupName: "g", Now: time.Now()}
	env, shadowed := BuildEnv(sc, map[string]string{
		"HCLOUDGROUP_SLOT_ID": "999",
		"PATH":                "/bin",
	})
	require.Equal(t, []string{"HCLOUDGROUP_SLOT_ID"}, shadowed)
	got := envMap(env)
	require.NotEqual(t, "999", got["HCLOUDGROUP_SLOT_ID"], "operator must not shadow reserved namespace")
	require.Equal(t, "/bin", got["PATH"])
}

func envMap(env []string) map[string]string {
	out := map[string]string{}
	for _, kv := range env {
		idx := strings.IndexByte(kv, '=')
		if idx < 0 {
			continue
		}
		out[kv[:idx]] = kv[idx+1:]
	}
	return out
}
