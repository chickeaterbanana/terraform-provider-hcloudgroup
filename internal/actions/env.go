// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package actions

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

// HCLOUDGROUPPrefix is the reserved env-var namespace. Operator env keys
// starting with this prefix are rejected (at plan time and re-checked at
// exec time as defense in depth).
const HCLOUDGROUPPrefix = "HCLOUDGROUP_"

// BuildEnv assembles the final KEY=VALUE slice passed to the child shell.
// Order: provider-populated HCLOUDGROUP_* first, operator's env second.
// Operator entries with the reserved prefix are dropped (and the caller
// can detect that via the returned shadowed list).
func BuildEnv(sc slotctx.SlotContext, operatorEnv map[string]string) (env []string, shadowed []string) {
	provider := providerVars(sc)
	for _, k := range sortedKeys(provider) {
		env = append(env, k+"="+provider[k])
	}
	for _, k := range sortedKeys(operatorEnv) {
		if strings.HasPrefix(k, HCLOUDGROUPPrefix) {
			shadowed = append(shadowed, k)
			continue
		}
		env = append(env, k+"="+operatorEnv[k])
	}
	return env, shadowed
}

func providerVars(sc slotctx.SlotContext) map[string]string {
	v := map[string]string{
		HCLOUDGROUPPrefix + "GROUP_NAME":  sc.GroupName,
		HCLOUDGROUPPrefix + "SLOT_ID":     fmt.Sprintf("%d", sc.SlotID),
		HCLOUDGROUPPrefix + "GENERATION":  fmt.Sprintf("%d", sc.Generation),
		HCLOUDGROUPPrefix + "SERVER_NAME": sc.ServerName,
		HCLOUDGROUPPrefix + "NOW":         sc.Now.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}
	if sc.ServerID != 0 {
		v[HCLOUDGROUPPrefix+"SERVER_ID"] = fmt.Sprintf("%d", sc.ServerID)
	}
	if sc.PrivateIP != "" {
		v[HCLOUDGROUPPrefix+"PRIVATE_IP"] = sc.PrivateIP
	}

	peersJSON, err := json.Marshal(sc.Peers)
	if err != nil {
		// Peer is a flat struct of string/int; json.Marshal cannot fail
		// here. If it ever does, fail loud rather than silently emit "[]".
		panic(fmt.Sprintf("env: marshal peers: %v", err))
	}
	v[HCLOUDGROUPPrefix+"PEERS_JSON"] = string(peersJSON)

	ips := make([]string, 0, len(sc.Peers))
	for _, p := range sc.Peers {
		ips = append(ips, p.PrivateIP)
	}
	v[HCLOUDGROUPPrefix+"PEER_PRIVATE_IPS"] = strings.Join(ips, " ")

	return v
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}
