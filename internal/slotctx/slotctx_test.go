// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package slotctx_test

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

// TestPeer_JSONFieldNames pins the snake_case JSON tags on Peer. These
// keys are the operator-facing contract for HCLOUDGROUP_PEERS_JSON
// (env.go in package actions); a regression here silently breaks
// operator scripts that key off the names.
func TestPeer_JSONFieldNames(t *testing.T) {
	p := slotctx.Peer{
		SlotID:     2,
		ServerName: "consul-2-3",
		PrivateIP:  "10.0.0.5",
		Generation: 3,
	}
	b, err := json.Marshal(p)
	require.NoError(t, err)

	got := string(b)
	for _, key := range []string{`"slot_id"`, `"server_name"`, `"private_ip"`, `"generation"`} {
		require.Contains(t, got, key, "missing %s in marshaled output: %s", key, got)
	}
	for _, badKey := range []string{`"SlotID"`, `"ServerName"`, `"PrivateIP"`, `"Generation"`} {
		require.False(t, strings.Contains(got, badKey),
			"PascalCase key %s leaked through to operator-facing JSON: %s", badKey, got)
	}
}

// TestPeer_JSONRoundTrip catches a tag drift that would still pass
// TestPeer_JSONFieldNames if the marshal side broke first.
func TestPeer_JSONRoundTrip(t *testing.T) {
	original := slotctx.Peer{
		SlotID:     1,
		ServerName: "consul-1-2",
		PrivateIP:  "10.0.0.4",
		Generation: 2,
	}
	b, err := json.Marshal(original)
	require.NoError(t, err)

	var decoded slotctx.Peer
	require.NoError(t, json.Unmarshal(b, &decoded))
	require.Equal(t, original, decoded)
}

// TestSlotContext_PeersMarshalAsArray pins the array shape of the
// operator-facing peer list. Empty peer slice must marshal to "[]" not
// "null" so consuming scripts can rely on iterating without nil checks.
func TestSlotContext_PeersMarshalAsArray(t *testing.T) {
	t.Run("empty peers", func(t *testing.T) {
		b, err := json.Marshal([]slotctx.Peer{})
		require.NoError(t, err)
		require.Equal(t, "[]", string(b))
	})
	t.Run("multiple peers", func(t *testing.T) {
		peers := []slotctx.Peer{
			{SlotID: 0, ServerName: "g-0-1", PrivateIP: "10.0.0.1", Generation: 1},
			{SlotID: 2, ServerName: "g-2-1", PrivateIP: "10.0.0.3", Generation: 1},
		}
		b, err := json.Marshal(peers)
		require.NoError(t, err)

		var decoded []slotctx.Peer
		require.NoError(t, json.Unmarshal(b, &decoded))
		require.Equal(t, peers, decoded)
	})
}
