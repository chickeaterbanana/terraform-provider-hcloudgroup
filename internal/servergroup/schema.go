// Copyright (c) 2026 The terraform-provider-hcloudgroup Authors
// SPDX-License-Identifier: MPL-2.0
package servergroup

import (
	"context"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-timeouts/resource/timeouts"
	"github.com/hashicorp/terraform-plugin-framework-validators/int64validator"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// Schema declares the HCL-facing attributes and blocks. Splitting this
// out of resource.go keeps the CRUD wiring readable.
func (r *ServerGroupResource) Schema(ctx context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		Description: "A group of Hetzner Cloud servers managed as a single rolling-replace unit.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true,
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"name": schema.StringAttribute{
				Required:    true,
				Description: "Group identifier. Embedded in server names and used as a label selector value.",
				Validators:  groupNameValidators(),
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"replicas": schema.Int64Attribute{
				Required:    true,
				Description: "Number of slots in the group. Must be 1..999. The upper bound pairs with the deterministic server-name budget (group-slot-generation must fit RFC 1123's 63-char limit; see internal/servergroup/validators.go). (Named 'replicas' rather than 'count' because Terraform reserves 'count' as a meta-argument on every resource.)",
				Validators:  []validator.Int64{int64validator.Between(1, 999)},
			},
			"image": schema.StringAttribute{
				Required:    true,
				Description: "Hetzner image name or numeric id (passed verbatim).",
			},
			"server_type": schema.StringAttribute{
				Required:    true,
				Description: "Hetzner server type, e.g. cx22.",
			},
			"location": schema.StringAttribute{
				Required:    true,
				Description: "Hetzner location, e.g. fsn1, nbg1, hel1.",
			},
			"network_id": schema.Int64Attribute{
				Required:    true,
				Description: "ID of the hcloud network to attach each server to.",
			},
			"ssh_keys": schema.ListAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Names of pre-existing hcloud SSH keys to authorize on each server.",
			},
			"labels": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "User labels applied to each server. Must not use the hcloudgroup.io/ prefix.",
				Validators:  userLabelValidators(),
			},
			"user_data_template": schema.StringAttribute{
				Optional:    true,
				Description: "Go text/template source for cloud-init user_data. See package docs for variables.",
			},
			"replace_on_change": schema.SetAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Names of additional attributes that, when changed, trigger a rolling replace.",
			},
			"current_replace_hash": schema.StringAttribute{
				Computed:    true,
				Description: "SHA-256 of the current desired hash inputs. For drift inspection.",
				PlanModifiers: []planmodifier.String{
					newReplaceHashPlanModifier(),
				},
			},
			"slots": schema.ListNestedAttribute{
				Computed:    true,
				Description: "One entry per slot, populated after CRUD operations.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"slot_id":      schema.Int64Attribute{Computed: true},
						"server_id":    schema.Int64Attribute{Computed: true},
						"server_name":  schema.StringAttribute{Computed: true},
						"generation":   schema.Int64Attribute{Computed: true},
						"replace_hash": schema.StringAttribute{Computed: true},
						"ip_private":   schema.StringAttribute{Computed: true},
						"status":       schema.StringAttribute{Computed: true},
						"last_error":   schema.StringAttribute{Computed: true},
					},
				},
			},
		},
		Blocks: map[string]schema.Block{
			"before_create":   actionSchemaBlock("Pre-create lifecycle hook."),
			"post_create":     actionSchemaBlock("Post-create lifecycle hook."),
			"before_replace":  actionSchemaBlock("Pre-replace cluster-level hook."),
			"post_replace":    actionSchemaBlock("Post-replace cluster-level hook."),
			"before_remove":   actionSchemaBlock("Pre-remove lifecycle hook."),
			"post_remove":     actionSchemaBlock("Post-remove lifecycle hook."),
			"readiness_probe": readinessProbeSchemaBlock(),
			"timeouts": timeouts.Block(ctx, timeouts.Opts{
				Create: true, Update: true, Delete: true,
				CreateDescription: "Default 60m. Tune up for larger groups or longer probes.",
				UpdateDescription: "Default 90m.",
				DeleteDescription: "Default 30m.",
			}),
		},
	}
}

// DefaultTimeouts returns the per-operation defaults applied when the
// HCL omits a `timeouts` block.
func DefaultTimeouts() (create, update, deletion time.Duration) {
	return 60 * time.Minute, 90 * time.Minute, 30 * time.Minute
}
