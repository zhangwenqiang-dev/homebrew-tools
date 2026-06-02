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

Example profile:

```yaml
profiles:
  xcode-vnc:
    description: Apple account: user@example.com
    user: user
    host: mac-host.example.com
    identity_file: ~/.ssh/example.pem
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

## Safety Checks

Before starting SSH, `cm` checks:

- The named profile exists.
- `user`, `host`, and `identity_file` are configured.
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
