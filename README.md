# ConnectMac

ConnectMac is an internal CLI for managing SSH local port-forwarding profiles. It is built for commands like VNC tunnels, where a small typo in the host, key, or port can connect to the wrong place or silently fail.

The binary command is `cm`.

## Build

```bash
go build -o bin/cm ./cmd/cm
```

For internal installation, copy `bin/cm` to a shared tool path such as `/usr/local/bin/cm`.

## Quick Start

Create the default config:

```bash
cm init
```

Then edit the config manually and point `identity_file` at a PEM file inside your own `~/.ssh/` directory.

The default config path is:

```text
~/.connectmac/config.yaml
```

For many profiles, keep shared or important entries in `config.yaml` and put additional files under:

```text
~/.connectmac/profiles/*.yaml
```

Each file uses the same `profiles:` structure. `cm` loads `config.yaml` first, then all `.yaml` and `.yml` files in `profiles/` by filename. Duplicate profile names are rejected.
Shared `user`, `identity_file`, and `aws.creator` values can be placed in top-level `defaults:`. Profile values override defaults.

Example profile:

```yaml
defaults:
  user: ec2-user
  identity_file: ~/.ssh/example.pem
  aws:
    creator: "Xiao Chen"

profiles:
  xcode-vnc:
    description: Apple account: user@example.com
    host: mac-host.example.com
    sync:
      push:
        excludes:
          - xcuserdata
          - .svn
          - .git
          - .DS_Store
      pull:
        excludes: []
    vnc:
      username: mac-user
    aws:
      profile: cm-xcode
      region: us-west-2
      resource_name: ""
      account_email: user@example.com
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
        value: user@example.com
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
      allow_intel_fallback: false
    tunnels:
      - local_port: 5900
        remote_host: localhost
        remote_port: 5900
```

## Commands

List configured profiles:

```bash
cm list
```

Check a profile before connecting:

```bash
cm check xcode-vnc
```

Start a foreground tunnel:

```bash
cm connect xcode-vnc
```

Open an interactive SSH shell:

```bash
cm ssh xcode-vnc
```

Start a managed background tunnel:

```bash
cm start xcode-vnc
```

Show managed background tunnels:

```bash
cm status
```

Stop a managed background tunnel:

```bash
cm stop xcode-vnc
```

Remove old SSH host fingerprints for a profile after the remote machine has been rebuilt:

```bash
cm forget-host xcode-vnc
```

Open macOS Screen Sharing for a VNC tunnel:

```bash
cm open-vnc xcode-vnc
```

Pull a remote file or directory into the current directory:

```bash
cm pull xcode-vnc ~/Desktop/App.ipa
```

Upload a local file or directory to a remote directory:

```bash
cm push xcode-vnc ./MyProject ~/Downloads/
```

When pushing a directory, `cm` uploads it directly with rsync and applies the profile's push exclude rules.

Use a custom config path:

```bash
cm list --config ./examples/config.yaml
```

Start the MCP server for AI clients:

```bash
cm mcp
```

Preview AWS Mac Dedicated Host automation:

```bash
cm aws plan xcode-vnc
cm aws status xcode-vnc
cm aws wait-ready xcode-vnc
cm aws adopt xcode-vnc
cm aws create xcode-vnc
cm aws destroy xcode-vnc
```

`cm aws plan` is local-only and does not call AWS APIs. `cm aws status` uses the configured AWS profile and region to describe managed Dedicated Hosts, EC2 instances, Elastic IP association, and EC2 system, instance, and EBS status checks. `cm aws wait-ready` waits until the managed EC2 instance is running, the Elastic IP is bound to that instance, and all three status checks are `ok`. `cm aws create` and `cm aws destroy` preview by default; pass `--confirm` to execute the AWS mutations. After a confirmed create, `cm` waits for AWS readiness checks before reporting the Mac ready; it does not run SSH probes during this wait.

Use `aws.creator` to tag who originally created the Mac. AWS already records resource creation and launch times, so `cm` does not write a separate creator-date tag. Use `aws.account_email` for the Apple account email. Leave `aws.resource_name` empty for new resources so `cm` generates `xcode-<account-email>`. Set `aws.resource_name` only when adopting resources that were created before `cm` managed them.

AWS credentials are read through the normal AWS SDK credential chain. Keep access keys in `~/.aws/credentials`, AWS SSO, environment variables, or IAM roles. Do not put AWS secret keys in `~/.connectmac/config.yaml`.

## Safety Checks

Before starting SSH, `cm` checks:

- The named profile exists.
- `user`, `host`, and `identity_file` are configured directly or through `defaults:`.
- The private key path is under `~/.ssh/`.
- The private key file exists.
- The private key file is not group/world-readable on Unix-like systems.
- The local tunnel port is available.
- The system `ssh` executable is available.
- The system `rsync` executable is available for `pull` and `push`.

The generated SSH command includes:

```bash
-o ExitOnForwardFailure=yes
-o ServerAliveInterval=30
-o ServerAliveCountMax=3
```

`ExitOnForwardFailure=yes` is especially important: if local forwarding fails, SSH exits instead of pretending the tunnel is usable.

## Rebuilt Remote Hosts

If a remote machine is destroyed and recreated, SSH may reject the connection because the old host fingerprint in `~/.ssh/known_hosts` no longer matches.

Use:

```bash
cm forget-host xcode-vnc
```

This runs:

```bash
ssh-keygen -R <profile-host>
```

Then connect again and confirm the new fingerprint when SSH prompts you. This command is explicit on purpose; normal `connect`, `start`, `pull`, and `push` commands do not silently remove host identity checks.

## Opening VNC

`cm open-vnc <profile>` opens macOS Screen Sharing using the first configured tunnel's local port.

```bash
cm open-vnc xcode-vnc
```

With this config:

```yaml
vnc:
  username: mac-user
tunnels:
  - local_port: 5900
    remote_host: localhost
    remote_port: 5900
```

It runs:

```bash
open "vnc://mac-user@localhost:5900"
```

Do not put VNC passwords in the config. Let macOS Screen Sharing and Keychain remember the password after the first successful login.

## PEM File Rule

Store PEM files only under `~/.ssh/`:

```bash
mkdir -p ~/.ssh
mv example.pem ~/.ssh/
chmod 600 ~/.ssh/example.pem
```

Do not keep PEM files in project folders, Desktop, Downloads, shared folders, or repositories. `cm check`, `cm connect`, `cm start`, `cm pull`, and `cm push` reject `identity_file` paths outside `~/.ssh/`.

## Rsync Transfers

`cm pull` reads the SSH target from the selected profile and downloads into the current directory:

```bash
cm pull xcode-vnc ~/Desktop/file.zip
```

This runs rsync against:

```text
user@<profile-host>:~/Desktop/file.zip -> .
```

`cm push` uploads a file directly:

```bash
cm push xcode-vnc ./build.zip ~/Downloads/
```

For directories, `cm push` uploads the directory directly with rsync. By default, these paths are excluded:

```text
xcuserdata
.svn
.git
.DS_Store
```

You can configure push and pull excludes separately per profile:

```yaml
profiles:
  xcode-vnc:
    sync:
      push:
        excludes:
          - xcuserdata
          - .svn
          - .git
          - .DS_Store
          - docs
          - "*.md"
      pull:
        excludes:
          - .DS_Store
```

## MCP Server

`cm mcp` starts a stdio MCP server for AI clients.

Available tools:

```text
cm_list_profiles
cm_check_profile
cm_push
cm_pull
cm_forget_host
```

Tools with side effects require `confirm: true`. Without confirmation, they return a preview only.

Example tool arguments:

```json
{
  "profile": "xcode-vnc",
  "local_path": "/Users/example/project",
  "remote_dir": "~/Documents/",
  "confirm": true
}
```

`cm_ssh`, `cm_start`, `cm_stop`, and `cm_open_vnc` are intentionally not exposed through MCP.
