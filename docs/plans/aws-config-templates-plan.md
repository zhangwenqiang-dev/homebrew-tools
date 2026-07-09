# AWS Config Templates Plan

## Goal

Move repeatable AWS/profile defaults into database-managed configuration templates, then let profiles reference a template. This avoids copying AMI IDs, key names, security groups, subnet maps, identity file paths, and instance type priority into every profile.

## Core Idea

Add a managed configuration type, for example `aws_config_template`, stored in the ConnectMac backend database.

Profiles keep only profile-specific values:

- Profile name.
- Apple account email.
- Description.
- Resource name.
- Elastic IP allocation ID and public IP.
- Optional per-profile overrides.
- A reference to one AWS config template.

The template keeps reusable values:

- `user`
- `identity_file`
- `aws_profile`
- `region`
- `key_name`
- `security_group_id`
- `availability_zone_ids`
- `subnet_id`
- `subnets_by_az`
- `instance_type_priority`
- `allow_intel_fallback`
- `amis_by_region`

When `cm` loads remote profiles, it should produce an effective profile by merging:

1. Global/default config.
2. Selected AWS config template.
3. Profile-specific fields and overrides.

## Why This Is Better

- AMI changes are edited once on the template instead of one profile at a time.
- Profiles become shorter and easier to review.
- Admins can create profile records faster from the Web UI.
- Member assignments remain profile-based, while infrastructure defaults remain template-based.
- Old full YAML profiles can continue working during migration.

## Web UI Plan

Add a `配置模板` management area for admins:

- List templates.
- Add template.
- Edit template.
- Disable/delete template after checking references.

In the profile add/edit modal:

- Add a `配置模板` dropdown.
- Show inherited values from the selected template.
- Keep profile-specific fields editable.
- Optionally allow explicit overrides for special cases.

## API Plan

Add APIs such as:

```text
GET  /api/aws-config-templates
POST /api/aws-config-template/save
POST /api/aws-config-template/delete
```

Profile save/load APIs should include the selected template reference and return effective profile data for status/open/release operations.

## Compatibility

- Existing profiles with full `profile_yaml` must continue to work.
- New profiles can use template references.
- Migration can be gradual: admins can create templates first, then update profiles to reference them.
- Do not store PEM file contents, AWS secret keys, VNC passwords, or webhook secrets in templates.

## Open Problem To Handle

Remote database profiles currently need to work as normal `cm` profiles when the Web UI, CLI, and MCP call status/open/release. If a profile stores only a template reference, any code path that reads profiles must use the merged effective profile, not the raw minimal profile.

This affects:

- `cm list`
- `cm profile show`
- Web profile list/status.
- AWS open/release/status actions.
- MCP profile tools.
- Local fallback behavior when remote profile loading fails.

The implementation must avoid a split-brain state where the Web UI sees template-backed profiles but CLI/MCP still says `unknown profile` or `missing aws config`.

## Recommended Implementation Order

1. Add storage structs and database table for AWS config templates.
2. Add template CRUD APIs.
3. Add effective-profile merge logic in one shared service function.
4. Update all profile loading paths to use the same effective-profile function.
5. Add Web UI template management and profile template dropdown.
6. Add tests for old full YAML profiles and new template-backed profiles.
7. Add a migration helper later if manual migration becomes tedious.
