package resource_server_group

import (
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
)

// actionSchemaBlock returns the SingleNestedBlock used for every
// before_*/post_* lifecycle hook. The block contains a single nested
// `command` block; absence of the outer block is the schema's
// representation of a null action.
func actionSchemaBlock(desc string) schema.Block {
	return schema.SingleNestedBlock{
		Description: desc,
		Blocks: map[string]schema.Block{
			"command": commandSchemaBlock(),
		},
	}
}

// commandSchemaBlock describes a shell command and its execution
// parameters. Used inside every action block.
func commandSchemaBlock() schema.Block {
	return schema.SingleNestedBlock{
		Description: "Shell command run via /bin/sh -c with a clean environment.",
		Attributes: map[string]schema.Attribute{
			"command": schema.StringAttribute{
				Required:    true,
				Description: "Shell command passed to /bin/sh -c. No template interpolation; dynamic values flow via env.",
			},
			"env": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Description: "Full environment for the command. Keys must not start with HCLOUDGROUP_.",
				Validators:  envKeyNamespaceValidators(),
			},
			"stdin": schema.StringAttribute{
				Optional:    true,
				Description: "Bytes fed to the command's stdin.",
			},
			"working_dir": schema.StringAttribute{
				Optional:    true,
				Description: "Working directory. Defaults to a per-action ephemeral tempdir.",
			},
			"expected_exit": schema.SetAttribute{
				Optional:    true,
				ElementType: types.Int64Type,
				Description: "Exit codes that count as success. Defaults to {0}.",
			},
			"timeout": schema.StringAttribute{
				Required:    true,
				Description: "Per-attempt timeout, e.g. 30s, 5m. Parsed by Go time.ParseDuration.",
				Validators:  durationStringValidators(),
			},
		},
	}
}

// readinessProbeSchemaBlock is the wrapper that adds polling parameters
// (interval, success_threshold, total_timeout) on top of a command.
func readinessProbeSchemaBlock() schema.Block {
	return schema.SingleNestedBlock{
		Description: "Polled command that determines when a freshly-created server is ready.",
		Blocks: map[string]schema.Block{
			"command": probeCommandSchemaBlock(),
		},
	}
}

func probeCommandSchemaBlock() schema.Block {
	return schema.SingleNestedBlock{
		Description: "Probe command and its polling configuration.",
		Attributes: map[string]schema.Attribute{
			"command": schema.StringAttribute{
				Required:    true,
				Description: "Probe command. Same execution semantics as action commands.",
			},
			"env": schema.MapAttribute{
				Optional:    true,
				ElementType: types.StringType,
				Validators:  envKeyNamespaceValidators(),
			},
			"stdin":       schema.StringAttribute{Optional: true},
			"working_dir": schema.StringAttribute{Optional: true},
			"expected_exit": schema.SetAttribute{
				Optional:    true,
				ElementType: types.Int64Type,
			},
			"timeout": schema.StringAttribute{
				Required:    true,
				Description: "Per-attempt timeout.",
				Validators:  durationStringValidators(),
			},
			"interval": schema.StringAttribute{
				Required:    true,
				Description: "Wait between attempts.",
				Validators:  durationStringValidators(),
			},
			"success_threshold": schema.Int64Attribute{
				Optional:    true,
				Description: "Consecutive successful attempts required. Defaults to 1.",
			},
			"total_timeout": schema.StringAttribute{
				Required:    true,
				Description: "Overall deadline; if exceeded, the probe fails.",
				Validators:  durationStringValidators(),
			},
		},
	}
}
