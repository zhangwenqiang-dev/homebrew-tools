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
8. Key pair name or ID comes from config.
9. Disable auto-assigned public IP.
10. Use configured security group ID.
11. Placement must target the newly allocated Dedicated Host:
   `Tenancy=host`, `HostId=<host-id>`, `Affinity=host`.
12. Bind a configured Elastic IP allocation ID to the new EC2 instance.
13. Before binding EIP, verify the EIP has a configured tag whose value equals the account email. Supported tag keys should include `Name` or `Apple`, configurable per profile.

Destruction flow:

1. Only destroy resources that match the profile and safety tags.
2. Disassociate Elastic IP if currently associated with the managed instance.
3. Terminate the managed instance.
4. Wait until instance termination is visible.
5. Release the managed Dedicated Host when AWS allows release.
6. Never terminate unrelated EC2 instances or release unrelated Dedicated Hosts.

Safety requirements:

1. Costly or destructive operations must support preview mode and require explicit confirmation.
2. MCP tools that create or destroy resources must require `confirm: true`.
3. Created resources must include management tags:
   `cm-managed=true`, `cm-profile=<profile>`, `cm-account-email=<email>`.
4. Published examples must not contain real hosts, PEM names, AWS account data, security group IDs, subnet IDs, EIP IDs, or emails.

## Proposed Config

```yaml
profiles:
  example:
    description: "Apple account: apple@example.com"
    user: user
    host: mac-host.example.com
    identity_file: ~/.ssh/example.pem

    aws:
      profile: default
      region: us-west-2
      creator: "Xiao Chen"
      account_email: apple@example.com
      ami:
        mac_x86: ami-0538568e5d3653bea
        mac_arm: ami-063755aadeb97329a
      key_name: example-key
      subnet_id: "<subnet-id>"
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
- [ ] Add tests that parse `aws.profile`, `region`, `creator`, `account_email`, `aws.ami.mac_x86`, `aws.ami.mac_arm`, `key_name`, `subnet_id`, `security_group_id`, `elastic_ip_allocation_id`, `elastic_ip_owner_tag`, `availability_zone_ids`, `instance_type_priority`, and `allow_intel_fallback`.
- [ ] Ensure missing `aws` config does not break existing SSH, VNC, push, and pull commands.
- [ ] Add sanitized example config only.
- [ ] Run `go test ./...`.

### Task 2: Add AWS Validation

**Files:**
- Modify: `internal/connectmac/validation.go`
- Modify: `internal/connectmac/validation_test.go`

- [ ] Validate that AWS commands require `region`, `account_email`, `key_name`, `subnet_id`, `security_group_id`, `elastic_ip_allocation_id`, at least one architecture-compatible AMI, and at least one availability zone ID.
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
