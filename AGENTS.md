# AGENTS.md

<!-- BEGIN CONNECTMAC AWS RULES -->
# ConnectMac AWS AI Rules

Use the connectmac-aws skill for any request involving cm aws, AWS Mac Dedicated Hosts, Mac virtual machines, 提包机, Apple-account-based Mac access, or opening/creating/releasing/destroying AWS Mac resources.

Follow these rules:

1. Require an explicit Apple account email for AI-driven open/create/destroy requests. Never infer the email from conversation context.
2. If the user does not provide an Apple email, list configured accounts first and ask the user to choose.
3. Preview before every AWS mutation. Do not pass --confirm or confirm=true until the user explicitly approves that exact operation.
4. Destroy/release workflows must never release Elastic IP allocations. They may only disassociate EIP from the managed instance, terminate managed EC2, and release managed Dedicated Hosts.
5. Do not create AWS key pairs or change security group ingress unless the user explicitly asks for that setup step.
6. Do not SSH-probe a newly launched Mac until AWS readiness checks pass.
7. Treat ready as "the managed Mac is already usable." Do not describe ready as needing to wait, create, or open a new AWS resource.
8. For blocked decisions, stop and explain the blocking reason instead of continuing automatically.

Preferred MCP tools:

- cm_list_profiles
- cm_find_profile_by_apple
- cm_aws_open_mac_by_email
- cm_aws_destroy_mac_by_email
- cm_aws_status
- cm_aws_wait_ready

CLI fallback:

```bash
cm profile accounts
cm profile find <apple-email>
cm aws status <profile-or-apple-email>
cm aws open <profile-or-apple-email>
cm aws destroy <profile-or-apple-email>
```

Only after explicit approval:

```bash
cm aws open <apple-email> --confirm
cm aws destroy <apple-email> --confirm
```
<!-- END CONNECTMAC AWS RULES -->
