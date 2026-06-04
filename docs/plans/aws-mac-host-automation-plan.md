# AWS Mac Host Automation Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add safe AWS EC2 Mac Dedicated Host lifecycle automation to `cm`, including host allocation, instance launch, Elastic IP binding, status checks, destruction, and MCP tools.

**Architecture:** Implement AWS support as a dedicated package behind small service interfaces so CLI and MCP share the same validation and execution logic. Use AWS SDK for Go v2 for real operations, with dry-run/preview by default for destructive or costly actions.

**Tech Stack:** Go, AWS SDK for Go v2, YAML config, existing `cm` CLI/MCP patterns.

---

## Product Requirements

The workflow manages a Mac Dedicated Host plus one EC2 macOS instance for a profile.

Creation flow:

1. Allocate a Mac Dedicated Host in a configured AWS region and allowed availability zone IDs matching `*-az[1-4]`.
2. Use this resource name format for Dedicated Host and EC2 instance:
   `xcode-<account-email>`.
   Store the creator separately with a tag such as `cm-creator=Xiao Chen`. AWS already records resource creation and launch times, so `cm` does not write a separate creator-date tag. The account email is the Apple account identifier.
3. Supported Mac instance families:
   `mac1`, `mac2`, `mac2-m1ultra`, `mac2-m2`, `mac2-m2pro`, `mac-m3ultra`, `mac-m4`, `mac-m4max`, `mac-m4pro`.
4. Default `instance_type_priority` should prefer Apple Silicon and cheaper baseline machines before expensive performance machines:
   `mac2.metal`, `mac2-m2.metal`, `mac-m4.metal`, `mac2-m2pro.metal`, `mac-m4pro.metal`, `mac2-m1ultra.metal`, `mac-m4max.metal`, `mac-m3ultra.metal`, `mac1.metal`.
5. Dedicated Host allocation must disable automatic placement and host maintenance:
   `AutoPlacement=off`, `HostMaintenance=off`.
6. Launch EC2 using an AMI that matches the selected instance type architecture. Known AMIs:
   - macOS Sequoia x86 Mac AMI: `ami-0538568e5d3653bea`, architecture `64-bit (Mac)`, only for `mac1.metal`.
   - macOS Sequoia Arm Mac AMI: `ami-036044172ee3c8c3c`, architecture `64-bit (Mac-Arm)`.
   - macOS Tahoe Arm Mac AMI: `ami-063755aadeb97329a`, architecture `64-bit (Mac-Arm)`.
7. EC2 instance type must match the Dedicated Host instance type.
8. Key pair name or ID comes from config. `cm` must not create a key pair unless the user explicitly asks it to create that key pair. If `aws.key_name` does not exist during a preflight/create flow, stop and ask the user whether to create it.
9. Disable auto-assigned public IP.
10. Use configured security group ID. `cm` must not silently open SSH on a security group. For a new AWS account where the default security group lacks SSH ingress, report the missing rule and require user confirmation before any command changes the security group.
11. Placement must target the newly allocated Dedicated Host:
   `Tenancy=host`, `HostId=<host-id>`, `Affinity=host`.
12. Bind a configured Elastic IP allocation ID to the new EC2 instance.
13. Before binding EIP, verify the EIP has a configured tag whose value equals the account email. Supported tag keys should include `Name` or `Apple`, configurable per profile.

Destruction flow:

1. Only destroy resources that match the profile and safety tags.
2. Disassociate Elastic IP if currently associated with the managed instance, but never release the Elastic IP allocation.
3. Terminate the managed instance.
4. Wait until instance termination is visible.
5. Release the managed Dedicated Host when AWS allows release.
6. Never terminate unrelated EC2 instances or release unrelated Dedicated Hosts.
7. Support rerunning the same destroy command after a partial timeout by skipping already terminated instances and already released hosts.

Safety requirements:

1. Costly or destructive operations must support preview mode and require explicit confirmation.
2. MCP tools that create or destroy resources must require `confirm: true`.
3. Created resources must include management tags:
   `cm-managed=true`, `cm-profile=<profile>`, `cm-account-email=<email>`.
4. Confirmed create/adopt/launch mutations must require a non-empty `aws.creator` so `cm-creator` records who initiated the resource ownership.
5. Published examples must not contain real hosts, PEM names, AWS account data, security group IDs, subnet IDs, EIP IDs, or emails.

## Proposed Config

```yaml
defaults:
  user: ec2-user
  identity_file: ~/.ssh/example.pem
  aws:
    creator: "Xiao Chen"
    amis_by_region:
      us-east-1:
        mac_x86: "<us-east-1-x86-mac-ami>"
        mac_arm: "<us-east-1-arm-mac-ami>"
      us-east-2:
        mac_x86: "<us-east-2-x86-mac-ami>"
        mac_arm: "<us-east-2-arm-mac-ami>"
      us-west-2:
        mac_x86: ami-0538568e5d3653bea
        mac_arm: ami-063755aadeb97329a

profiles:
  example:
    description: "Apple account: apple@example.com"
    host: mac-host.example.com

    aws:
      profile: default
      region: us-west-2
      account_email: apple@example.com
      key_name: example-key
      subnet_id: "<subnet-id>"
      subnets_by_az:
        usw2-az1: "<subnet-id-az1>"
        usw2-az2: "<subnet-id-az2>"
        usw2-az3: "<subnet-id-az3>"
        usw2-az4: "<subnet-id-az4>"
      security_group_id: "<security-group-id>"
      elastic_ip_allocation_id: "<elastic-ip-allocation-id>"
      elastic_ip_public_ip: "<elastic-ip-public-ip>"
      elastic_ip_owner_tag:
        key: Apple
        value: apple@example.com
      availability_zone_ids:
        - usw2-az1
        - usw2-az2
        - usw2-az3
        - usw2-az4
      instance_type_priority:
        - mac2.metal
        - mac2-m2.metal
        - mac-m4.metal
        - mac2-m2pro.metal
        - mac-m4pro.metal
        - mac2-m1ultra.metal
        - mac-m4max.metal
        - mac-m3ultra.metal
        - mac1.metal
      allow_intel_fallback: false
```

AMI defaults resolve in this order: profile-level `aws.ami`, region-specific `defaults.aws.amis_by_region[profile.aws.region]`, then legacy global `defaults.aws.ami`.

## Proposed Commands

```bash
cm aws plan <profile> [--config <path>]
cm aws create <profile> [--confirm] [--config <path>]
cm aws status <profile> [--config <path>]
cm aws destroy <profile> [--confirm] [--config <path>]
```

Command behavior:

- `cm aws plan` prints the exact intended Dedicated Host, EC2, and EIP operations without calling create APIs.
- `cm aws create` previews unless `--confirm` is passed.
- `cm aws status` describes matching hosts, instances, and EIP association.
- `cm aws destroy` previews unless `--confirm` is passed.

## Proposed MCP Tools

```text
cm_aws_plan
cm_aws_status
cm_aws_create_mac
cm_aws_destroy_mac
```

MCP safety behavior:

- `cm_aws_plan` and `cm_aws_status` do not need confirmation.
- `cm_aws_create_mac` requires `confirm: true`.
- `cm_aws_destroy_mac` requires `confirm: true`.
- Without confirmation, create/destroy tools return a preview only.

## AWS API Mapping

Dedicated Host allocation:

```bash
aws ec2 allocate-hosts \
  --region us-west-2 \
  --availability-zone-id usw2-az1 \
  --instance-type mac2.metal \
  --quantity 1 \
  --auto-placement off \
  --host-maintenance off \
  --tag-specifications 'ResourceType=dedicated-host,Tags=[{Key=Name,Value=xcode-example-20260603-apple@example.com},{Key=cm-managed,Value=true}]'
```

Instance launch on Dedicated Host:

```bash
aws ec2 run-instances \
  --region us-west-2 \
  --image-id ami-063755aadeb97329a \
  --instance-type mac2.metal \
  --key-name example-key \
  --subnet-id '<subnet-id>' \
  --security-group-ids '<security-group-id>' \
  --placement Tenancy=host,HostId=h-example,Affinity=host \
  --no-associate-public-ip-address \
  --tag-specifications 'ResourceType=instance,Tags=[{Key=Name,Value=xcode-example-20260603-apple@example.com},{Key=cm-managed,Value=true}]'
```

Elastic IP association:

```bash
aws ec2 associate-address \
  --region us-west-2 \
  --allocation-id '<elastic-ip-allocation-id>' \
  --instance-id i-example
```

Destroy:

```bash
aws ec2 terminate-instances \
  --region us-west-2 \
  --instance-ids i-example

aws ec2 release-hosts \
  --region us-west-2 \
  --host-ids h-example
```

## File Structure

- Modify `go.mod`: add AWS SDK dependencies.
- Modify `internal/connectmac/config.go`: add AWS config structs.
- Modify `internal/connectmac/validation.go`: validate AWS config when AWS commands are used.
- Create `internal/connectmac/aws_types.go`: command input/output types and plan representation.
- Create `internal/connectmac/aws_client.go`: AWS SDK client wrapper interface.
- Create `internal/connectmac/aws_service.go`: orchestration for plan/create/status/destroy.
- Create `internal/connectmac/aws_service_test.go`: unit tests using fake AWS client.
- Modify `internal/connectmac/app.go`: add `cm aws ...` commands.
- Modify `internal/connectmac/mcp.go`: add AWS MCP tools.
- Modify `internal/connectmac/mcp_test.go`: test MCP schemas and confirmation behavior.
- Modify `README.md`: document AWS commands and safety model.
- Modify `examples/config.yaml`: add sanitized AWS example.

## Implementation Tasks

### Task 1: Add AWS Config Types

**Files:**
- Modify: `internal/connectmac/config.go`
- Modify: `internal/connectmac/config_test.go`
- Modify: `examples/config.yaml`

- [ ] Add `AWSConfig` to profile config with fields matching the proposed YAML.
- [ ] Add tests that parse `aws.profile`, `region`, `creator`, `account_email`, `aws.ami.mac_x86`, `aws.ami.mac_arm`, `key_name`, `subnet_id`, `subnets_by_az`, `security_group_id`, `elastic_ip_allocation_id`, `elastic_ip_owner_tag`, `availability_zone_ids`, `instance_type_priority`, and `allow_intel_fallback`.
- [ ] Ensure missing `aws` config does not break existing SSH, VNC, push, and pull commands.
- [ ] Add sanitized example config only.
- [ ] Run `go test ./...`.

### Task 2: Add AWS Validation

**Files:**
- Modify: `internal/connectmac/validation.go`
- Modify: `internal/connectmac/validation_test.go`

- [ ] Validate that AWS commands require `region`, `account_email`, `key_name`, `subnet_id` or `subnets_by_az`, `security_group_id`, `elastic_ip_allocation_id`, at least one architecture-compatible AMI, and at least one availability zone ID.
- [ ] Validate availability zone IDs with pattern ending in `-az1`, `-az2`, `-az3`, or `-az4`.
- [ ] Validate supported instance types.
- [ ] Reject `mac1.metal` when `allow_intel_fallback` is false.
- [ ] Validate AMI architecture selection: `mac1.metal` must use `aws.ami.mac_x86`; every Apple Silicon Mac instance type must use `aws.ami.mac_arm`.
- [ ] Validate generated resource name format:
  `xcode-<account-email>`.
- [ ] Run `go test ./...`.

### Task 3: Build Planning Model

**Files:**
- Create: `internal/connectmac/aws_types.go`
- Create: `internal/connectmac/aws_service.go`
- Create: `internal/connectmac/aws_service_test.go`

- [ ] Create a `MacPlan` type containing resource name, region, candidate availability zones, candidate instance types, AMI, key name, subnet, security group, EIP allocation ID, and expected safety tags.
- [ ] Implement `BuildMacPlan(profileName, profile, date)` with deterministic output.
- [ ] Test default priority ordering:
  `mac2.metal`, `mac2-m2.metal`, `mac-m4.metal`, `mac2-m2pro.metal`, `mac-m4pro.metal`, `mac2-m1ultra.metal`, `mac-m4max.metal`, `mac-m3ultra.metal`, `mac1.metal`.
- [ ] Test custom priority overrides default priority.
- [ ] Test that `mac1.metal` plans select `aws.ami.mac_x86` and Apple Silicon plans select `aws.ami.mac_arm`.
- [ ] Run `go test ./...`.

### Task 4: Add AWS SDK Client Interface

**Files:**
- Modify: `go.mod`
- Create: `internal/connectmac/aws_client.go`
- Create: `internal/connectmac/aws_client_test.go`

- [ ] Add AWS SDK for Go v2 dependencies for config and EC2.
- [ ] Define an interface for allocate host, describe hosts, run instances, describe instances, describe addresses, associate address, disassociate address, terminate instances, and release hosts.
- [ ] Implement a real SDK-backed client.
- [ ] Implement fake client fixtures for service tests.
- [ ] Run `go test ./...`.

### Task 5: Implement Create Flow

**Files:**
- Modify: `internal/connectmac/aws_service.go`
- Modify: `internal/connectmac/aws_service_test.go`

- [ ] Implement create preview that returns planned operations without AWS mutations.
- [ ] Implement create execution that tries instance types by priority and availability zones by configured order.
- [ ] Allocate host with `AutoPlacement=off` and `HostMaintenance=off`.
- [ ] Launch instance with `Tenancy=host`, target `HostId`, `Affinity=host`, disabled public IP, configured key, subnet, and security group.
- [ ] Verify EIP owner tag before association.
- [ ] Associate EIP to the new instance.
- [ ] Return host ID, instance ID, public IP, selected availability zone ID, and selected instance type.
- [ ] Run `go test ./...`.

### Task 6: Implement Status Flow

**Files:**
- Modify: `internal/connectmac/aws_service.go`
- Modify: `internal/connectmac/aws_service_test.go`

- [ ] Describe managed Dedicated Hosts by tags.
- [ ] Describe managed instances by tags.
- [ ] Describe configured EIP allocation and show whether it is associated with the managed instance.
- [ ] Return concise text suitable for CLI and structured data suitable for MCP.
- [ ] Run `go test ./...`.

### Task 7: Implement Destroy Flow

**Files:**
- Modify: `internal/connectmac/aws_service.go`
- Modify: `internal/connectmac/aws_service_test.go`

- [ ] Implement destroy preview that lists resources that would be changed.
- [ ] Refuse to destroy resources missing `cm-managed=true`, `cm-profile=<profile>`, or matching account email tag.
- [ ] Disassociate EIP only when it is attached to the managed instance.
- [ ] Terminate the managed instance.
- [ ] Release the managed Dedicated Host only after it is releasable.
- [ ] Return clear errors for AWS Mac host release delay.
- [ ] Run `go test ./...`.

### Task 8: Add CLI Commands

**Files:**
- Modify: `internal/connectmac/app.go`
- Modify: `internal/connectmac/app_test.go`

- [ ] Add `cm aws plan <profile> [--config <path>]`.
- [ ] Add `cm aws create <profile> [--confirm] [--config <path>]`.
- [ ] Add `cm aws status <profile> [--config <path>]`.
- [ ] Add `cm aws destroy <profile> [--confirm] [--config <path>]`.
- [ ] Ensure create/destroy without `--confirm` print preview and exit successfully.
- [ ] Update help text.
- [ ] Run `go test ./...`.

### Task 9: Add MCP Tools

**Files:**
- Modify: `internal/connectmac/mcp.go`
- Modify: `internal/connectmac/mcp_test.go`

- [ ] Add `cm_aws_plan` tool.
- [ ] Add `cm_aws_status` tool.
- [ ] Add `cm_aws_create_mac` tool with required profile and optional `confirm`.
- [ ] Add `cm_aws_destroy_mac` tool with required profile and optional `confirm`.
- [ ] Ensure create/destroy without `confirm: true` return preview only.
- [ ] Run MCP `tools/list` smoke test.
- [ ] Run `go test ./...`.

### Task 10: Documentation and Release

**Files:**
- Modify: `README.md`
- Modify: `examples/config.yaml`
- Modify: `Formula/cm.rb` only if version/tag changes.

- [ ] Document AWS config with placeholders only.
- [ ] Document create/status/destroy command examples.
- [ ] Document cost and 24-hour Dedicated Host release caveat.
- [ ] Run the project sensitive scan before commit. It must check for private keys, real account emails, real EC2 hosts, real PEM names, real security group IDs, real subnet IDs, real EIP allocation IDs, and ignored private script directories.

- [ ] Run `go test ./...`.
- [ ] Commit.
- [ ] Tag release.
- [ ] Update Homebrew Formula tag.
- [ ] Push main and tags.

## Optimization Backlog From 2026-06-03 Robin Flow

The Robin Mac startup flow exposed gaps that should be addressed before the AWS workflow is considered smooth for daily operations. The concrete scenario was:

- A profile needed a Mac for one Apple account.
- Automatic host allocation failed across several instance/AZ combinations because AWS returned `InsufficientHostCapacity`.
- A Dedicated Host was then created manually in another AZ.
- `cm` could not adopt the empty host because adoption currently expects both a host and an instance.
- The configured subnet was in a different AZ from the manually created host, so the remaining launch flow required manual subnet discovery.
- The EC2 key pair and local PEM path had to be aligned after the instance was launched.

### Current Status After 2026-06-04 Updates

Completed:

- `cm aws status` now reports EC2 system status, instance status, and attached EBS status.
- `cm aws wait-ready <profile>` now waits for the managed instance to be `running`, the configured EIP to be bound to that instance, system status to be `ok`, instance status to be `ok`, and optional EBS status to be `ok` when AWS reports it.
- `cm aws create <profile> --confirm` now waits for AWS readiness after EIP association.
- `cm_aws_wait_ready` is available through MCP.
- `cm aws status` can now recover the EC2 instance and Dedicated Host from the configured EIP binding when older or manually managed resources do not have the expected `cm-managed` tags.

Current readiness decision:

- `cm` must not run automatic SSH probes during AWS readiness checks.
- AWS readiness is limited to EC2 state, EIP binding, system status, instance status, and attached EBS status.
- SSH remains a user-triggered action through `cm ssh <profile>` after AWS readiness is complete.
- This avoids touching a Mac EC2 instance before AWS reports all required checks as healthy.

Recommended next implementation step:

- Start with Priority 2, subnet selection by Host AZ.
- Reason: it prevents failed or manual launch flows when a reusable or manually created host is in a different AZ than the configured `subnet_id`.
- After Priority 2, implement Priority 1 using the same AZ-aware subnet selection for `adopt-host` and `launch-on-host`.

### Priority 1: Reuse or Adopt an Existing Empty Dedicated Host

Add support for continuing from a manually created or previously available Dedicated Host.

Proposed commands:

```bash
cm aws adopt-host <profile> --host-id <host-id> [--confirm]
cm aws launch-on-host <profile> --host-id <host-id> [--confirm]
```

Alternative: extend `cm aws create` so it first searches for reusable hosts by explicit `host_id`, `resource_name`, tags, account email, or matching type/AZ before allocating a new host.

Requirements:

- Allow adoption of a Dedicated Host even when no EC2 instance exists yet.
- Tag adopted hosts with `cm-managed=true`, `cm-profile=<profile>`, `cm-account-email=<email>`, and optional `cm-creator=<creator>`.
- Verify adopted host state is usable before launch.
- Verify adopted host has no currently running instances before adoption or launch-on-host.
- Verify adopted host instance type matches the profile-selected instance type.
- Verify adopted host AZ is one of the configured `availability_zone_ids`.
- Keep preview mode by default; require `--confirm` for tagging or launching.

Implemented in progress:

- `cm aws adopt-host <profile> --host-id <host-id>` previews and confirms tagging an existing empty Dedicated Host.
- `cm aws launch-on-host <profile> --host-id <host-id>` previews and confirms launching EC2 on an existing empty Dedicated Host.
- Both commands verify host state, instance type, allowed AZ, and empty host instances before mutation.
- `launch-on-host` reuses `subnets_by_az` and subnet AZ validation before starting EC2.
- MCP mirrors the workflow through `cm_aws_adopt_host` and `cm_aws_launch_on_host`.

### Priority 2: Validate and Auto-Select Subnet by Host AZ

The launch subnet must be in the same AZ as the target Dedicated Host. Today the config has one `subnet_id`, which can silently mismatch when the host is created in another AZ.

Planned improvements:

- During plan/create/launch-on-host, describe the configured subnet and compare its `AvailabilityZoneId` with the selected host AZ.
- If mismatched, fail early with a clear message.
- Add optional config for per-AZ subnet mapping:

```yaml
aws:
  subnets_by_az:
    usw2-az1: <subnet-id>
    usw2-az2: <subnet-id>
    usw2-az3: <subnet-id>
    usw2-az4: <subnet-id>
```

- If `subnets_by_az` is present, automatically choose the subnet for the selected host AZ.
- Optionally support auto-discovery: find a subnet in the same VPC and AZ as the configured base subnet.

Implemented in progress:

- `subnets_by_az` is supported as an optional profile AWS config map.
- `cm aws create` resolves the subnet for each candidate AZ before allocating a Dedicated Host.
- If a selected subnet belongs to a different AWS `AvailabilityZoneId`, that create candidate is skipped with a clear error if no candidate succeeds.
- Existing `subnet_id` remains supported for old configs.

### Priority 3: Better Capacity Retry Matrix

When AWS returns `InsufficientHostCapacity`, operators need a clear attempt matrix rather than only the final failed candidate.

Planned improvements:

- Query AWS instance type offerings for the configured region before allocation so unsupported AZ/type combinations are skipped before `AllocateHosts`.
- Record every attempted `(availability_zone_id, instance_type)` pair.
- Print a concise result table with AZ, instance type, subnet, result, and detail:

```text
usw2-az1 mac2-m2.metal      insufficient capacity
usw2-az2 mac2-m2.metal      insufficient capacity
usw2-az1 mac2-m2pro.metal   insufficient capacity
```

- Add flags:

```bash
cm aws create <profile> --try-all
cm aws create <profile> --az usw2-az3 --instance-type mac2-m2.metal --confirm
```

- Keep cleanup behavior strict: if instance launch or EIP association fails after host allocation, cleanup the resources created by that attempt unless the user explicitly asks to keep them.

Completed in `v0.1.13`:

- `cm aws create` describes instance type offerings and skips unsupported AZ/type candidates before allocating a Dedicated Host.
- Create success and failure output includes a candidate attempt table.
- Failed create no longer hides earlier attempts behind only the last AWS error.

### Priority 4: Resource Name and Account Email Matching

Manual resources can preserve account email case, while generated names may normalize it. This can make adoption miss valid resources.

Planned improvements:

- Decide whether generated `xcode-<account-email>` should preserve configured email case.
- During adoption, support matching by:
  - exact `aws.resource_name`
  - generated `xcode-<account-email>`
  - `cm-account-email`
  - configured EIP owner tag value
  - optional case-insensitive Name fallback
- Always show which matcher found the resource.

### Priority 5: Key Pair and Local PEM Consistency Check

The AWS key pair and local SSH identity file are related but configured separately.

Planned improvements:

- In `cm check`, warn when:
  - `aws.key_name` is set
  - `identity_file` basename does not plausibly match the key name
  - the matching local PEM does not exist under `~/.ssh`
- Do not hard-fail on name mismatch because teams may name PEM files differently.
- Keep hard failures for missing `identity_file`, PEM outside `~/.ssh`, or unsafe permissions.
- Do not create missing AWS key pairs automatically. If a future `ensure-key` command is added, it must be an explicit user action or an explicit confirmation prompt.

### Priority 5.1: Security Group Preflight Without Silent Mutation

New AWS accounts often have a default security group that does not allow SSH from the current operator.

Planned improvements:

- Add a preflight check that reports whether the configured security group allows TCP 22 from the current operator IP or an approved CIDR.
- Do not authorize ingress automatically during create.
- If a helper command is added later, require explicit confirmation before adding or removing security group rules.

### Priority 6: Wait Until the Mac Is AWS-Ready

EC2 Mac can report `running` before the platform is safe to use. Add a readiness command for the post-create wait that uses AWS status only.

Proposed command:

```bash
cm aws wait-ready <profile> [--timeout 45m]
```

Behavior:

- Poll `cm aws status`.
- Wait for the managed instance to be `running`.
- Wait for EIP association to point to the managed instance.
- Wait for EC2 system status to be `ok`.
- Wait for EC2 instance status to be `ok`.
- Wait for attached EBS status to be `ok` when AWS reports EBS status for the instance type.
- Treat missing system/instance status and `initializing`, `insufficient-data`, `impaired`, and `not-applicable` status values as not ready. Treat missing EBS status as not applicable.
- Do not probe SSH automatically.
- Exit successfully only after all AWS readiness checks pass.

Completed in `v0.1.11`:

- The command exists and uses a default 45 minute timeout with 30 second polling.
- `cm aws create <profile> --confirm` runs the AWS readiness wait after EIP association.
- `cm aws status` prints `system_status`, `instance_status`, `ebs_status`, per-instance `ready`, and aggregate `Ready`.
- `cm_aws_wait_ready` mirrors this behavior in MCP.

Completed in `v0.1.14`:

- EBS status is optional for readiness. This supports Mac instance types such as `mac2.metal` that only report system and instance status checks.

### Priority 6.1: Destroy Resume and EIP Retention

EC2 Mac termination can stay in `shutting-down` longer than the waiter timeout. Operators also need clear assurance that Elastic IP allocations are retained.

Completed in `v0.1.13`:

- `cm aws destroy` preview now says the Elastic IP is only disassociated and the allocation is retained.
- Destroy results print the retained Elastic IP allocation and public IP.
- Destroy skips terminal resources (`terminated` instances and `released` hosts), so rerunning the same command continues the remaining cleanup.
- Partial destroy errors return the work completed so far and tell operators to rerun the same destroy command after AWS finishes pending transitions.
- `cm aws status` hides terminal resources by default; `cm aws status <profile> --all` includes terminated instances and released hosts for troubleshooting.

### Priority 6.2: Region-Specific AMI Defaults

macOS AMI IDs are region-specific, so sharing one global AMI across `us-east-1`, `us-east-2`, and `us-west-2` can select an invalid AMI.

Completed in `v0.1.14`:

- `defaults.aws.amis_by_region` supports `mac_x86` and `mac_arm` per AWS region.
- Profile-level `aws.ami` still overrides defaults.
- Legacy `defaults.aws.ami` remains supported as a fallback.

### Priority 7: Update MCP Tools

Mirror the new CLI workflow in MCP so AI can safely continue after partial manual work.

Proposed tools:

```text
cm_aws_adopt_host
cm_aws_launch_on_host
cm_aws_wait_ready
```

MCP safety behavior:

- Adoption and launch previews are allowed without confirmation.
- Any AWS mutation requires `confirm: true`.
- Results should include host ID, instance ID, EIP association ID, selected subnet, selected instance type, and next suggested command.

## Open Questions

1. Should `cm aws create` write the newly created public host into the profile automatically, or only print it for manual update?
2. Should we support one EIP per profile only, or allow a pool of EIPs and choose by matching account email tag?
3. Should AWS credentials use profile-only config, environment variables, or both?
4. Should `cm aws destroy` terminate instances immediately, or require a second typed confirmation for extra safety?

## Current Decision Log

- Prefer AWS SDK for Go v2 over shelling out to AWS CLI.
- Keep AWS CLI examples as reference and debugging aid.
- Prefer Apple Silicon and cheaper instance types first.
- Keep Intel `mac1.metal` as optional fallback only.
- Require explicit confirmation for create/destroy in CLI and MCP.
- Verify EIP ownership tag before binding.
- Do not publish real AWS IDs, PEM names, emails, or hosts.
