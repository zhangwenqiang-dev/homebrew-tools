package connectmac

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strings"
)

func mcpTools() []map[string]interface{} {
	return []map[string]interface{}{
		mcpTool("cm_mcp_guide", "Read this first when using cm MCP. Explains stable tool flows, main parameters, preview/confirm rules, and safe AWS Mac handling.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false}),
		mcpTool("cm_guide", "Show step-by-step human guidance. Optional topic: first-use, profile, open, close, sync, vnc, or mcp.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"topic": stringSchema(),
			},
			"additionalProperties": false,
		}),
		mcpTool("cm_list_profiles", "List configured cm profiles. Use when the user has not provided an Apple account email or profile and must choose.", map[string]interface{}{"type": "object", "properties": map[string]interface{}{}, "additionalProperties": false}),
		mcpTool("cm_find_profile_by_apple", "Resolve apple_email to the local profile. apple_email is required for AI-driven open/create/destroy requests; never infer it from old context. Returns structuredContent.", appleEmailSchema()),
		mcpTool("cm_check_profile", "Validate profile without connecting. Main parameter: profile. Returns structuredContent with ok and errors.", profileSchema()),
		mcpTool("cm_profile_show", "Show a profile file by profile name or Apple account email. Main parameter: profile.", profileSchema()),
		mcpTool("cm_profile_add", "Preview or create a local profile file. Main parameter: name. confirm=false previews; confirm=true writes after user approval.", profileAddSchema()),
		mcpTool("cm_profile_remove", "Preview or remove a local profile file only; this does not close AWS Mac resources. Blocks when AWS resources are active unless force_local=true. confirm=true removes after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile":     stringSchema(),
				"force_local": map[string]string{"type": "boolean"},
				"confirm":     map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_profile_rename", "Preview or rename a local profile file. Main parameters: profile, new_name. confirm=true writes after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile":  stringSchema(),
				"new_name": stringSchema(),
				"confirm":  map[string]string{"type": "boolean"},
			},
			"required": []string{"profile", "new_name"},
		}),
		mcpTool("cm_profile_export", "Export a profile file by profile name or Apple account email. Main parameter: profile.", profileSchema()),
		mcpTool("cm_doctor", "Run local ConnectMac diagnostics. Optional fix=true creates missing local support dirs.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"fix": map[string]string{"type": "boolean"},
			},
			"additionalProperties": false,
		}),
		mcpTool("cm_dashboard", "Show local profile/tunnel dashboard. Set aws=true for read-only AWS status, decision, and next-step columns. Returns structuredContent profiles array.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"aws": map[string]string{"type": "boolean"},
			},
			"additionalProperties": false,
		}),
		mcpTool("cm_next", "Recommend the next safe action for a profile or Apple email. Read-only. Returns structuredContent with decision, next, ready, and any fixable errors.", profileSchema()),
		mcpTool("cm_push", "Preview or execute rsync upload. Main parameters: profile, local_path, remote_dir. Optional includes/excludes. confirm=true executes after preview approval. Returns structuredContent.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile":    stringSchema(),
				"local_path": stringSchema(),
				"remote_dir": stringSchema(),
				"includes":   stringArraySchema(),
				"excludes":   stringArraySchema(),
				"confirm":    map[string]string{"type": "boolean"},
			},
			"required": []string{"profile", "local_path", "remote_dir"},
		}),
		mcpTool("cm_pull", "Preview or execute rsync download. Main parameters: profile, remote_path, optional local_dir/includes/excludes. confirm=true executes after preview approval. Returns structuredContent.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile":     stringSchema(),
				"remote_path": stringSchema(),
				"local_dir":   stringSchema(),
				"includes":    stringArraySchema(),
				"excludes":    stringArraySchema(),
				"confirm":     map[string]string{"type": "boolean"},
			},
			"required": []string{"profile", "remote_path"},
		}),
		mcpTool("cm_forget_host", "Preview or remove known_hosts entries for a profile host after rebuild/IP reuse. Main parameter: profile. confirm=true executes after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_aws_plan", "Read-only local plan for AWS Mac Dedicated Host, EC2, and EIP operations. Main parameter: profile.", profileSchema()),
		mcpTool("cm_aws_capacity", "Read-only AWS Mac capacity report: Dedicated Host quotas, active host usage, remaining capacity, and offering AZs. Main parameter: profile.", profileSchema()),
		mcpTool("cm_aws_status", "Read-only status for one profile. Returns text plus structuredContent with profile, apple_email, decision, next, ready, EIP, hosts, and instances.", profileSchema()),
		mcpTool("cm_aws_wait_ready", "Wait until the managed AWS Mac EC2 instance is running, EIP-bound, and AWS status checks are ok. Use only after a confirmed create/open/launch.", profileSchema()),
		mcpTool("cm_aws_create_mac", "Preview or execute AWS Mac creation by profile. Prefer cm_aws_open_mac_by_email for user requests. creator must come from explicit user input when missing; never infer it from context. confirm=true mutates AWS after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"creator": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_aws_open_mac_by_email", "Use for 'open/create/start Mac' requests. Requires explicit apple_email. creator must come from explicit user input when missing; never infer it from context. confirm=false previews decision; confirm=true may create/launch/wait after user approval. Returns structuredContent.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"apple_email": stringSchema(),
				"creator":     stringSchema(),
				"confirm":     map[string]string{"type": "boolean"},
			},
			"required": []string{"apple_email"},
		}),
		mcpTool("cm_aws_adopt_mac", "Preview or tag existing AWS Mac resources as cm-managed. Main parameter: profile. creator must come from explicit user input when missing. confirm=true mutates tags after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"creator": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_aws_adopt_host", "Preview or tag an existing empty AWS Mac Dedicated Host as cm-managed. Main parameters: profile, host_id. creator must come from explicit user input when missing. confirm=true mutates tags after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"host_id": stringSchema(),
				"creator": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile", "host_id"},
		}),
		mcpTool("cm_aws_launch_on_host", "Preview or launch AWS Mac EC2 on an explicit existing Dedicated Host. Main parameters: profile, host_id. creator must come from explicit user input when missing. confirm=true mutates AWS after approval.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"host_id": stringSchema(),
				"creator": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile", "host_id"},
		}),
		mcpTool("cm_aws_destroy_mac", "Preview or execute AWS Mac destruction by profile. Never releases Elastic IP allocations. confirm=true mutates AWS after approval. Returns structuredContent.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"profile": stringSchema(),
				"confirm": map[string]string{"type": "boolean"},
			},
			"required": []string{"profile"},
		}),
		mcpTool("cm_aws_destroy_mac_by_email", "Use for 'release/close Mac' requests. Requires explicit apple_email. Never releases Elastic IP allocations. confirm=false previews; confirm=true mutates AWS after approval. Returns structuredContent.", map[string]interface{}{
			"type": "object",
			"properties": map[string]interface{}{
				"apple_email": stringSchema(),
				"confirm":     map[string]string{"type": "boolean"},
			},
			"required": []string{"apple_email"},
		}),
	}
}
func FormatMCPToolsText() string {
	tools := mcpTools()
	sort.Slice(tools, func(i, j int) bool {
		return fmt.Sprint(tools[i]["name"]) < fmt.Sprint(tools[j]["name"])
	})
	rows := make([][]string, 0, len(tools)+1)
	rows = append(rows, []string{"TOOL", "DESCRIPTION", "REQUIRED", "KEY PARAMS"})
	for _, tool := range tools {
		rows = append(rows, []string{
			fmt.Sprint(tool["name"]),
			fmt.Sprint(tool["description"]),
			strings.Join(mcpToolRequiredParams(tool), ", "),
			strings.Join(mcpToolParamNames(tool), ", "),
		})
	}
	return formatRows(rows)
}
func WriteMCPToolsJSON(out io.Writer) error {
	encoder := json.NewEncoder(out)
	encoder.SetIndent("", "  ")
	return encoder.Encode(map[string]interface{}{"tools": mcpTools()})
}
func mcpToolRequiredParams(tool map[string]interface{}) []string {
	schema, _ := tool["inputSchema"].(map[string]interface{})
	rawRequired, _ := schema["required"].([]string)
	if rawRequired != nil {
		return rawRequired
	}
	rawAny, _ := schema["required"].([]interface{})
	required := make([]string, 0, len(rawAny))
	for _, value := range rawAny {
		required = append(required, fmt.Sprint(value))
	}
	return required
}
func mcpToolParamNames(tool map[string]interface{}) []string {
	schema, _ := tool["inputSchema"].(map[string]interface{})
	properties, _ := schema["properties"].(map[string]interface{})
	params := make([]string, 0, len(properties))
	for key := range properties {
		params = append(params, key)
	}
	sort.Strings(params)
	return params
}
func mcpTool(name, description string, schema map[string]interface{}) map[string]interface{} {
	return map[string]interface{}{"name": name, "description": description, "inputSchema": schema}
}
func profileSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"profile": stringSchema()},
		"required":   []string{"profile"},
	}
}
func appleEmailSchema() map[string]interface{} {
	return map[string]interface{}{
		"type":       "object",
		"properties": map[string]interface{}{"apple_email": stringSchema()},
		"required":   []string{"apple_email"},
	}
}
func profileAddSchema() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"name":                     stringSchema(),
			"description":              stringSchema(),
			"user":                     stringSchema(),
			"host":                     stringSchema(),
			"identity_file":            stringSchema(),
			"apple_email":              stringSchema(),
			"aws_profile":              stringSchema(),
			"region":                   stringSchema(),
			"creator":                  stringSchema(),
			"key_name":                 stringSchema(),
			"security_group_id":        stringSchema(),
			"elastic_ip_allocation_id": stringSchema(),
			"elastic_ip_public_ip":     stringSchema(),
			"availability_zone_ids":    stringArraySchema(),
			"instance_type_priority":   stringArraySchema(),
			"confirm":                  map[string]string{"type": "boolean"},
		},
		"required": []string{"name"},
	}
}
func stringSchema() map[string]string {
	return map[string]string{"type": "string"}
}
func stringArraySchema() map[string]interface{} {
	return map[string]interface{}{"type": "array", "items": stringSchema()}
}
