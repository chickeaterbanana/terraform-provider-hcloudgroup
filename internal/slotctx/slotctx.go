// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
// Package slotctx defines the per-slot context shared by the actions runner,
// the user_data template renderer, and the reconciler. Living in its own
// package breaks an otherwise-circular import graph between those three.
package slotctx

import "time"

// Peer is one entry in the peer list passed to user_data templates and to
// action commands via HCLOUDGROUP_PEERS_JSON.
type Peer struct {
	SlotID     int    `json:"slot_id"`
	ServerName string `json:"server_name"`
	PrivateIP  string `json:"private_ip"`
	Generation int    `json:"generation"`
}

// SlotContext carries everything an action or template needs to render or
// execute for a specific slot. Fields are unset (zero) when not yet
// applicable - notably ServerID and PrivateIP before Server.Create has
// returned.
type SlotContext struct {
	GroupName  string
	SlotID     int
	Generation int
	ServerName string
	ServerID   int64
	PrivateIP  string
	Peers      []Peer
	Now        time.Time
}
