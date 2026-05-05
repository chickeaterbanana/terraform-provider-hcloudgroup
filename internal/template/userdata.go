// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0

// Package template renders the user_data_template for a slot. The surface
// is intentionally narrow: stdlib text/template only, no third-party func
// maps. Spec section 9.
package template

import (
	"bytes"
	"fmt"
	"text/template"
	"time"

	"github.com/chickeaterbanana/terraform-provider-hcloudgroup/internal/slotctx"
)

// TemplatePeer mirrors slotctx.Peer with PascalCase field names so the
// template surface matches Go convention (.PrivateIP, not .private_ip).
type TemplatePeer struct {
	SlotID     int
	ServerName string
	PrivateIP  string
	Generation int
	Status     string
}

// TemplateData is the value passed to text/template Execute. Spec section 9.1.
type TemplateData struct {
	GroupName  string
	SlotID     int
	ServerName string
	Generation int
	Peers      []TemplatePeer
	Now        string
}

// Parse parses templateSrc without rendering. Used at plan time
// (ValidateConfig) so syntactic errors fail fast instead of after the
// reconciler has already started creating servers.
func Parse(templateSrc string) error {
	if templateSrc == "" {
		return nil
	}
	if _, err := template.New("user_data").Parse(templateSrc); err != nil {
		return fmt.Errorf("user_data_template parse: %w", err)
	}
	return nil
}

// Render parses and executes templateSrc with the slot-derived data. Peers
// is the list of *other* slots; the slot's own ID/IP is not included
// because user_data renders before the server exists.
func Render(templateSrc string, sc slotctx.SlotContext) (string, error) {
	if templateSrc == "" {
		return "", nil
	}
	tmpl, err := template.New("user_data").Parse(templateSrc)
	if err != nil {
		return "", fmt.Errorf("user_data_template parse: %w", err)
	}

	peers := make([]TemplatePeer, 0, len(sc.Peers))
	for _, p := range sc.Peers {
		peers = append(peers, TemplatePeer{
			SlotID:     p.SlotID,
			ServerName: p.ServerName,
			PrivateIP:  p.PrivateIP,
			Generation: p.Generation,
		})
	}

	now := sc.Now
	if now.IsZero() {
		now = time.Now()
	}

	data := TemplateData{
		GroupName:  sc.GroupName,
		SlotID:     sc.SlotID,
		ServerName: sc.ServerName,
		Generation: sc.Generation,
		Peers:      peers,
		Now:        now.UTC().Format("2006-01-02T15:04:05Z07:00"),
	}

	var buf bytes.Buffer
	if err := tmpl.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("user_data_template execute: %w", err)
	}
	return buf.String(), nil
}
