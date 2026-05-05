// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0

// Package reconciler owns the per-slot lifecycle and the multi-phase
// group orchestration. It is the only package that holds simultaneous
// references to the hcloud client, the action runner, and the user_data
// template renderer.
package reconciler

import (
	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/actions"
)

// SlotStatus values stored in tofu state. Anything other than ready or
// failed is invalid; reconciler functions may transiently use other values
// in-memory but never persist them.
const (
	StatusReady  = "ready"
	StatusFailed = "failed"
)

// ActionSet wires the lifecycle hooks. A nil entry is treated identically
// to an actions.Null - the reconciler does not branch on nil so there's
// only ever one no-op path. Callers should populate any unset hook with
// actions.Null before invoking the reconciler.
type ActionSet struct {
	BeforeCreate  actions.Action
	PostCreate    actions.Action
	BeforeReplace actions.Action
	PostReplace   actions.Action
	BeforeRemove  actions.Action
	PostRemove    actions.Action
}

// Group is the desired-state input to the reconciler.
type Group struct {
	Name             string
	Count            int
	Image            string
	ServerType       string
	Location         string
	NetworkID        int64
	SSHKeyNames      []string
	UserLabels       map[string]string
	UserDataTemplate string

	HashFull   string
	HashPrefix string

	Actions        ActionSet
	ReadinessProbe *actions.ReadinessProbe
}

// SlotState mirrors a single entry of the resource's `slots` computed
// attribute. The reconciler reads this from prior state to know which
// servers it created last apply and what hash they were created with.
type SlotState struct {
	SlotID      int
	ServerID    int64
	ServerName  string
	Generation  int
	ReplaceHash string
	PrivateIP   string
	Status      string
	LastError   string
}

// State is the in-memory view the reconciler maintains across phases. It
// is the source of truth for what's been written to tofu state at any
// point during Apply; partial-progress reporting writes a snapshot of this
// after each slot transition.
type State struct {
	Slots []SlotState
}

// SlotByID returns a pointer into State.Slots for the given slot id, or
// nil if the slot is not present.
func (s *State) SlotByID(id int) *SlotState {
	for i := range s.Slots {
		if s.Slots[i].SlotID == id {
			return &s.Slots[i]
		}
	}
	return nil
}

// Upsert replaces the entry for sl.SlotID, appending if not present.
// Slots are kept in slot-id order.
func (s *State) Upsert(sl SlotState) {
	for i := range s.Slots {
		if s.Slots[i].SlotID == sl.SlotID {
			s.Slots[i] = sl
			return
		}
	}
	s.Slots = append(s.Slots, sl)
	for i := len(s.Slots) - 1; i > 0 && s.Slots[i].SlotID < s.Slots[i-1].SlotID; i-- {
		s.Slots[i], s.Slots[i-1] = s.Slots[i-1], s.Slots[i]
	}
}

// RemoveSlot drops the entry for slotID, if present.
func (s *State) RemoveSlot(slotID int) {
	for i := range s.Slots {
		if s.Slots[i].SlotID == slotID {
			s.Slots = append(s.Slots[:i], s.Slots[i+1:]...)
			return
		}
	}
}
