# Spec 011: Installer / Uninstaller

**Status**: COMPLETE
**Created**: 2026-02-16
**Depends on**: Spec 003 (config file), Spec 010 (transparent proxying)

## Problem Statement

fpsd has no deployment automation. Users build the binary with `make build`, then manually copy it somewhere, create a config file, and run it from the terminal. There is no way to start fpsd at login, restart it after crashes, or manage it as a long-running service without ad-hoc setup.

Transparent proxying (spec 010) adds another layer of manual work: iptables rules must be applied at boot, removed cleanly on shutdown, and matched to the correct network interface and user ID. Getting any of these wrong creates redirect loops, broken networking, or silent failures.

This spec adds an installer that sets up fpsd as a systemd user service, running as the installing user with no admin privileges required. Transparent proxy iptables rules are handled as an optional step that does require admin access, with clear warnings and the ability to undo everything cleanly.

## Approach

### Design Principles

- **Works from the source tree**: Run `./scripts/fps-ctl install` after `make build`. No external package manager, no repository to add.
- **User-level by default**: The fpsd service runs as the current user via `systemctl --user`. No root, no `setcap`, no special user accounts.
- **Admin only for iptables**: Transparent proxy rules require `sudo`. The script prompts, explains what it will do, and handles refusal gracefully (fpsd still installs, just without transparent mode).
- **Idempotent**: Running install twice updates the binary and service file without duplicating data or breaking state.
- **Clean uninstall**: Every file and rule created by the installer can be removed. Uninstall asks before deleting user data (config, databases, CA certs).

### Script

A single Bash script at `scripts/fps-ctl` handles all lifecycle operations:

```
./scripts/fps-ctl install       Install fpsd as a systemd user service
./scripts/fps-ctl uninstall     Remove fpsd service, iptables rules, and optionally data
./scripts/fps-ctl status        Show installation status and service health
```

The Makefile gains thin wrapper targets:

```makefile
install: build
	./scripts/fps-ctl install

uninstall:
	./scripts/fps-ctl uninstall
```

### Directory Layout

The installer follows the [XDG Base Directory Specification](https://specifications.freedesktop.org/basedir-spec/latest/) and [systemd file hierarchy](https://www.freedesktop.org/software/systemd/man/file-hierarchy.html):

| Path | Purpose | Created by |
| ---- | ------- | ---------- |
| `~/.local/bin/fpsd` | Binary | install |
| `~/.config/fpsd/fpsd.yml` | Configuration | install (first time only) |
| `~/.local/share/fpsd/` | Data directory (blocklist.db, stats.db, CA certs) | install |
| `~/.local/share/fpsd/logs/` | Log files | install |
| `~/.config/systemd/user/fpsd.service` | Systemd user service unit | install |
| `/etc/systemd/system/fpsd-tproxy.service` | Systemd system service for iptables rules | install (sudo) |

`~/.local/bin` is on `$PATH` by default on most Linux distributions (systemd sets it via `systemd-environment-d-generator`). The installer verifies this and warns if it is not.

### Install Flow

```
fps-ctl install
  │
  ├─ 1. Preflight checks
  │     ├─ Linux platform? (uname)
  │     ├─ systemd available? (systemctl --user)
  │     ├─ Binary built? (./fpsd exists in source tree)
  │     └─ Any of these fail → error with remediation message
  │
  ├─ 2. Create directories
  │     ├─ ~/.local/bin/
  │     ├─ ~/.config/fpsd/
  │     ├─ ~/.local/share/fpsd/
  │     ├─ ~/.local/share/fpsd/logs/
  │     └─ ~/.config/systemd/user/
  │
  ├─ 3. Copy binary
  │     └─ cp ./fpsd ~/.local/bin/fpsd (overwrite if exists)
  │
  ├─ 4. Copy config (first install only)
  │     ├─ If ~/.config/fpsd/fpsd.yml exists → skip (preserve user edits)
  │     └─ If missing → copy ./fpsd.yml with paths adjusted:
  │           data_dir: ~/.local/share/fpsd
  │           log_dir: ~/.local/share/fpsd/logs
  │
  ├─ 5. Write systemd user service unit
  │     └─ ~/.config/systemd/user/fpsd.service (always overwritten)
  │
  ├─ 6. Enable and start service
  │     ├─ systemctl --user daemon-reload
  │     ├─ systemctl --user enable fpsd.service
  │     ├─ loginctl enable-linger $USER (so service runs without active login session)
  │     └─ systemctl --user start fpsd.service
  │
  ├─ 7. Transparent proxy setup (optional)
  │     ├─ Ask: "Set up transparent proxy iptables rules? [y/N]"
  │     │   └─ Skip if --no-transparent flag or user declines
  │     ├─ Probe iptables support (see Platform Detection below)
  │     ├─ Detect or ask for LAN interface
  │     ├─ Read transparent ports from installed config (or use defaults)
  │     ├─ Show the exact rules and system service that will be created
  │     ├─ Confirm with user
  │     ├─ Request sudo
  │     ├─ Write /etc/systemd/system/fpsd-tproxy.service
  │     ├─ sudo systemctl daemon-reload
  │     ├─ sudo systemctl enable fpsd-tproxy.service
  │     ├─ sudo systemctl start fpsd-tproxy.service
  │     └─ Verify rules applied (iptables -t nat -L -n | grep fpsd ports)
  │
  └─ 8. Summary
        ├─ Service status
        ├─ Config file location
        ├─ Dashboard URL (if dashboard credentials configured)
        ├─ Transparent proxy status
        └─ Next steps (generate CA, configure blocklists, etc.)
```

### Uninstall Flow

```
fps-ctl uninstall
  │
  ├─ 1. Transparent proxy teardown (if installed)
  │     ├─ Check for /etc/systemd/system/fpsd-tproxy.service
  │     ├─ If found:
  │     │   ├─ sudo systemctl stop fpsd-tproxy.service
  │     │   ├─ sudo systemctl disable fpsd-tproxy.service
  │     │   ├─ sudo rm /etc/systemd/system/fpsd-tproxy.service
  │     │   └─ sudo systemctl daemon-reload
  │     └─ Verify iptables rules removed
  │
  ├─ 2. Stop and disable fpsd service
  │     ├─ systemctl --user stop fpsd.service
  │     ├─ systemctl --user disable fpsd.service
  │     ├─ rm ~/.config/systemd/user/fpsd.service
  │     └─ systemctl --user daemon-reload
  │
  ├─ 3. Remove binary
  │     └─ rm ~/.local/bin/fpsd
  │
  ├─ 4. Ask about data and config
  │     ├─ "Remove config (~/.config/fpsd/)? [y/N]"
  │     ├─ "Remove data (~/.local/share/fpsd/)? [y/N]"
  │     │   └─ Data includes: blocklist.db, stats.db, CA certs, logs
  │     ├─ --purge flag skips prompts and removes everything
  │     └─ Default: keep (safe for reinstall)
  │
  └─ 5. Summary
        └─ What was removed, what was kept
```

### Status Command

```
fps-ctl status
  │
  ├─ Installation check
  │     ├─ Binary: ~/.local/bin/fpsd [found/missing] [version]
  │     ├─ Config: ~/.config/fpsd/fpsd.yml [found/missing]
  │     └─ Data:   ~/.local/share/fpsd/ [found/missing] [size]
  │
  ├─ Service check
  │     ├─ fpsd.service: [active/inactive/failed/not installed]
  │     ├─ Uptime: [duration]
  │     └─ Linger: [enabled/disabled]
  │
  ├─ Transparent proxy check
  │     ├─ fpsd-tproxy.service: [active/inactive/not installed]
  │     ├─ iptables rules: [applied/missing]
  │     ├─ IP forwarding: [enabled/disabled]
  │     └─ Interface: [name]
  │
  └─ Connectivity check
        ├─ Heartbeat: curl http://localhost:18737/fps/heartbeat
        └─ [success → show version, uptime, mode] / [fail → service may be starting]
```

### Systemd User Service Unit

```ini
[Unit]
Description=Face Puncher Supreme proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
ExecStart=%h/.local/bin/fpsd --config %h/.config/fpsd/fpsd.yml
Restart=on-failure
RestartSec=5

# Logging — fpsd logs to its own files; journal captures stderr
StandardOutput=journal
StandardError=journal
SyslogIdentifier=fpsd

# Security hardening
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=tmpfs
BindPaths=%h/.config/fpsd:%h/.config/fpsd
BindPaths=%h/.local/share/fpsd:%h/.local/share/fpsd
PrivateTmp=yes

[Install]
WantedBy=default.target
```

**Key choices**:

- `Type=exec`: systemd tracks the main process directly (Go binary, no forking).
- `Restart=on-failure` with `RestartSec=5`: Automatic restart after crashes, with a 5-second backoff to avoid tight restart loops.
- `%h` expands to the user's home directory — avoids hardcoding paths.
- `WantedBy=default.target`: Starts when the user's systemd instance starts (at login, or at boot if lingering is enabled).
- Security hardening: `ProtectSystem=strict` makes the filesystem read-only except for explicitly allowed paths. `ProtectHome=tmpfs` hides the home directory except for the bind-mounted config and data directories. `NoNewPrivileges` prevents privilege escalation. These are defense-in-depth — fpsd does not need privileges, and the unit file enforces that.

### Systemd System Service (Transparent Proxy iptables)

The installer generates this unit file with values baked in at install time:

```ini
[Unit]
Description=Face Puncher Supreme transparent proxy iptables rules
After=network-online.target
Wants=network-online.target
# Informational only — system service cannot depend on user service,
# but rules are harmless without fpsd running (connections just fail).

[Service]
Type=oneshot
RemainAfterExit=yes

# Apply rules
ExecStart=/usr/sbin/iptables -t nat -A PREROUTING -i ${LAN_IF} -p tcp --dport 80  -j REDIRECT --to-port ${THTTP}
ExecStart=/usr/sbin/iptables -t nat -A PREROUTING -i ${LAN_IF} -p tcp --dport 443 -j REDIRECT --to-port ${THTTPS}
ExecStart=/usr/sbin/iptables -t nat -A OUTPUT -m owner --uid-owner ${PROXY_UID} -p tcp --dport 80  -j RETURN
ExecStart=/usr/sbin/iptables -t nat -A OUTPUT -m owner --uid-owner ${PROXY_UID} -p tcp --dport 443 -j RETURN
ExecStart=/usr/sbin/sysctl -q -w net.ipv4.ip_forward=1

# Remove rules (on stop or shutdown)
ExecStop=/usr/sbin/iptables -t nat -D PREROUTING -i ${LAN_IF} -p tcp --dport 80  -j REDIRECT --to-port ${THTTP}
ExecStop=/usr/sbin/iptables -t nat -D PREROUTING -i ${LAN_IF} -p tcp --dport 443 -j REDIRECT --to-port ${THTTPS}
ExecStop=/usr/sbin/iptables -t nat -D OUTPUT -m owner --uid-owner ${PROXY_UID} -p tcp --dport 80  -j RETURN
ExecStop=/usr/sbin/iptables -t nat -D OUTPUT -m owner --uid-owner ${PROXY_UID} -p tcp --dport 443 -j RETURN

[Install]
WantedBy=multi-user.target
```

Where `${LAN_IF}`, `${THTTP}`, `${THTTPS}`, and `${PROXY_UID}` are replaced with actual values at install time (not shell variables — literal values written into the unit file).

**Rule semantics** (from spec 010):

| Rule | Chain | Purpose |
| ---- | ----- | ------- |
| PREROUTING REDIRECT :80 → THTTP | nat | Redirect LAN HTTP traffic to fpsd transparent HTTP port |
| PREROUTING REDIRECT :443 → THTTPS | nat | Redirect LAN HTTPS traffic to fpsd transparent HTTPS port |
| OUTPUT RETURN uid-owner :80 | nat | Prevent fpsd's own outbound HTTP from being redirected (loop prevention) |
| OUTPUT RETURN uid-owner :443 | nat | Prevent fpsd's own outbound HTTPS from being redirected (loop prevention) |

**IP forwarding**: `sysctl -w net.ipv4.ip_forward=1` enables packet forwarding, required for the gateway scenario. ExecStop does not disable forwarding because other services may depend on it.

**Lifecycle**: When `fpsd-tproxy.service` is stopped (manually or at shutdown), ExecStop removes the iptables rules. Traffic resumes flowing directly to its original destination. When fpsd-tproxy is started, ExecStart re-applies them. The rules survive as long as the service is running but do not persist across reboots independently — the systemd service handles that.

### Platform Detection

The install script probes the system before attempting transparent proxy setup:

```
Probe sequence:
  │
  ├─ 1. Check for iptables binary
  │     ├─ which iptables → found
  │     ├─ iptables --version → log version (iptables vs iptables-nft)
  │     └─ Not found → "iptables not available; transparent proxy requires iptables"
  │
  ├─ 2. Check nat table access (requires sudo)
  │     ├─ sudo iptables -t nat -L -n >/dev/null 2>&1
  │     ├─ Success → nat table available
  │     └─ Failure → "Cannot access iptables nat table; check permissions or kernel module"
  │
  ├─ 3. Check owner match module
  │     ├─ sudo iptables -t nat -C OUTPUT -m owner --uid-owner $(id -u) -j RETURN 2>&1
  │     │   (this tests whether -m owner is supported, not whether the rule exists)
  │     ├─ "No chain/target/match by that name" → owner module not available
  │     └─ Other errors → module available (rule just doesn't exist yet, which is expected)
  │
  ├─ 4. Check IP forwarding capability
  │     └─ sysctl net.ipv4.ip_forward → log current value
  │
  └─ 5. Detect LAN interface
        ├─ List non-loopback interfaces with IPv4 addresses:
        │   ip -4 -o addr show scope global | awk '{print $2}' | sort -u
        ├─ If exactly one → use it (show to user for confirmation)
        ├─ If multiple → present numbered list, ask user to choose
        ├─ If none → "No network interfaces with IPv4 addresses found"
        └─ --interface flag overrides auto-detection
```

**Supported platforms**: Any Linux distribution with systemd and iptables (legacy or nft backend). The iptables CLI is the compatibility layer — the script does not care whether the kernel uses xtables or nftables underneath.

**Unsupported platforms**: The script exits with a clear message if:

- Not Linux (`uname -s` is not `Linux`)
- No systemd (`systemctl` not found)
- No `--user` support (`systemctl --user` fails — can happen in containers)

### Config Adjustment

When first installing, the script copies `fpsd.yml` from the source tree but adjusts paths to match the installed layout:

| Source tree default | Installed value | Reason |
| ------------------- | --------------- | ------ |
| `data_dir: "."` | `data_dir: "~/.local/share/fpsd"` | Source tree default is cwd; installed service needs absolute path |
| `log_dir: "logs"` | `log_dir: "~/.local/share/fpsd/logs"` | Same — relative path won't resolve correctly from systemd |
| `listen: ":18737"` | `listen: "0.0.0.0:18737"` | Bind to all interfaces (LAN access for client devices) |

The script uses `sed` for these replacements — the config file is simple YAML with known structure. All other values (blocklists, MITM domains, plugins, dashboard credentials) are preserved as-is.

If the user already has a config at `~/.config/fpsd/fpsd.yml`, the script prints a message and skips the copy entirely. This prevents overwriting customizations on upgrade.

### Upgrade Path

Running `fps-ctl install` on an existing installation performs an upgrade:

1. Stops the service
2. Replaces the binary
3. Overwrites the systemd unit file (picks up any unit file changes)
4. Skips config copy (preserves user edits)
5. Restarts the service
6. If transparent proxy was installed and `--transparent` flag is passed: updates the tproxy unit file and restarts it

The script detects an existing installation by checking for `~/.config/systemd/user/fpsd.service`.

### Non-Interactive Mode

For scripted deployments:

```bash
# Install without prompts, skip transparent proxy
./scripts/fps-ctl install --no-transparent

# Install with transparent proxy, specifying interface
./scripts/fps-ctl install --transparent --interface eth0

# Uninstall everything including data
./scripts/fps-ctl uninstall --purge
```

| Flag | Command | Effect |
| ---- | ------- | ------ |
| `--no-transparent` | install | Skip transparent proxy setup entirely |
| `--transparent` | install | Set up transparent proxy (still needs sudo) |
| `--interface IF` | install | LAN interface for iptables rules (skip auto-detect) |
| `--purge` | uninstall | Remove config and data without asking |
| `--yes` | both | Answer yes to all prompts |

Without these flags, the script runs interactively and asks the user at each decision point.

## File Changes

| File | Change |
| ---- | ------ |
| `scripts/fps-ctl` | New — Main installer/uninstaller/status script (Bash, executable) |
| `Makefile` | Add `install` and `uninstall` targets |

## Acceptance Criteria

### Install

- [ ] `fps-ctl install` creates directory layout (~/.local/bin, ~/.config/fpsd, ~/.local/share/fpsd, logs)
- [ ] Binary copied to `~/.local/bin/fpsd` from source tree
- [ ] Config copied to `~/.config/fpsd/fpsd.yml` with adjusted paths (data_dir, log_dir, listen)
- [ ] Config not overwritten if it already exists (upgrade path)
- [ ] Systemd user service unit written to `~/.config/systemd/user/fpsd.service`
- [ ] Service enabled, started, and lingering activated
- [ ] fpsd starts successfully and responds to heartbeat
- [ ] `make install` builds then runs the installer
- [ ] Installer detects missing binary and shows remediation message

### Transparent Proxy

- [ ] Script probes for iptables binary and nat table access
- [ ] Script probes for owner match module support
- [ ] Script detects LAN interfaces and presents selection (or auto-selects if only one)
- [ ] `--interface` flag overrides auto-detection
- [ ] `--transparent` / `--no-transparent` flags control transparent proxy setup non-interactively
- [ ] iptables rules shown to user before applying
- [ ] System service unit written to `/etc/systemd/system/fpsd-tproxy.service` with correct values baked in
- [ ] PREROUTING REDIRECT rules applied for ports 80 and 443
- [ ] OUTPUT RETURN rules applied for loop prevention with correct UID
- [ ] IP forwarding enabled via sysctl
- [ ] Rules verified after application (`iptables -t nat -L -n` shows expected entries)
- [ ] `sudo systemctl stop fpsd-tproxy` removes all four iptables rules
- [ ] `sudo systemctl start fpsd-tproxy` re-applies all four iptables rules
- [ ] Rules survive reboot (via systemd service auto-start)
- [ ] fpsd-tproxy.service starts at boot before user login (system service)

### Uninstall

- [ ] `fps-ctl uninstall` stops and disables fpsd.service
- [ ] Systemd user service unit removed
- [ ] Binary removed from `~/.local/bin/fpsd`
- [ ] If fpsd-tproxy.service exists: stopped, disabled, unit file removed (requires sudo)
- [ ] iptables rules confirmed removed after tproxy service stop
- [ ] User prompted about config and data removal (default: keep)
- [ ] `--purge` removes config and data without prompting
- [ ] `make uninstall` runs the uninstaller
- [ ] Uninstall on a system where fpsd was never installed produces a clear message (not errors)

### Status

- [ ] `fps-ctl status` shows binary location and version
- [ ] Shows service state (active/inactive/failed/not installed)
- [ ] Shows transparent proxy state (service, iptables rules, IP forwarding, interface)
- [ ] Shows connectivity check (heartbeat probe)
- [ ] Works when fpsd is not installed (shows "not installed" for each component)

### Platform Detection

- [ ] Script fails with clear message on non-Linux
- [ ] Script fails with clear message if systemd not available
- [ ] Script warns if `~/.local/bin` is not on PATH
- [ ] Script warns if iptables not found (transparent proxy skipped, service still installs)
- [ ] Script warns if nat table inaccessible (transparent proxy skipped)
- [ ] Script warns if owner match module unavailable

### Idempotency and Edge Cases

- [ ] Running install twice: binary updated, config preserved, service restarted
- [ ] Running install on a running service: service stopped, updated, restarted
- [ ] Running uninstall twice: second run produces "not installed" message, no errors
- [ ] Source tree binary missing: error with `make build` remediation

## Out of Scope

- Package manager integration (deb, rpm, pacman, AUR)
- Docker/container deployment
- Multi-user installation (system-wide /usr/local/bin)
- macOS launchd service creation
- Automatic CA generation during install (user runs `fpsd generate-ca` separately)
- Config migration between versions (config format is stable; manual adjustments if needed)
- nftables native rules (iptables CLI compatibility layer is sufficient)
- IPv6 / ip6tables rules
- Firewalld integration
- Automatic DHCP server configuration (user configures devices to use fpsd machine as gateway)
- Log rotation configuration (fpsd handles its own log rotation)

## Security Considerations

- **No setuid/setcap**: fpsd runs as a regular user. It does not need `CAP_NET_ADMIN` or any capabilities. Transparent proxying is handled by iptables rules applied by a separate system service.
- **Systemd hardening**: The user service unit uses `NoNewPrivileges`, `ProtectSystem=strict`, `ProtectHome=tmpfs`, and `PrivateTmp` to limit fpsd's filesystem access to its config and data directories.
- **Sudo scope**: The installer only uses sudo for three operations: writing the tproxy unit file, running `systemctl daemon-reload`, and enabling/starting the tproxy service. It never runs fpsd itself as root.
- **iptables rule visibility**: Before applying any iptables rules, the script displays the exact commands it will run and waits for confirmation. The user sees every rule before it touches the firewall.
- **Clean teardown**: `systemctl stop fpsd-tproxy` removes all iptables rules via ExecStop. `fps-ctl uninstall` stops and removes the service. There is no scenario where rules linger after uninstall completes successfully.
- **No ExecStop for ip_forward**: ExecStop does not disable `net.ipv4.ip_forward` because other services (VPN, containers, VMs) may depend on it. Disabling forwarding could break unrelated network functionality.
- **tproxy unit file integrity**: The system service unit file is owned by root in `/etc/systemd/system/`. It contains literal iptables commands, not references to user-owned scripts. A non-root user cannot modify the rules after installation.
