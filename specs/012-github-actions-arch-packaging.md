# Spec 012: GitHub Actions CI + Arch Linux Packaging

**Status**: COMPLETE
**Created**: 2026-02-17
**Depends on**: Spec 011 (installer/uninstaller)

## Problem Statement

fpsd has no CI pipeline and no distribution packaging. Users must clone the repo, install Go and Node.js, and run `make build` manually. There is no automated way to verify that the project builds cleanly on push, no packaged binary for Arch Linux users, and no presence on AUR.

This spec adds a GitHub Actions workflow that builds an Arch Linux package on every push to main, publishes GitHub Releases on version tags, and provides an AUR publish script for manual AUR updates. Only linux-amd64 is targeted.

## Approach

### Version Source

The Makefile `VERSION` variable is the single source of truth for release versions. The PKGBUILD `pkgver()` function reads it and appends git metadata for the Arch-compatible version string:

```
Makefile:         VERSION := 1.1.2
PKGBUILD pkgver:  1.1.2.r185.g297d17f
```

Format: `{version}.r{commit_count}.g{short_sha}` — standard for AUR `-git` packages.

### GitHub Actions Workflow

Single workflow file at `.github/workflows/build.yml` with two jobs:

**Job 1: build-arch** (runs on every push/PR to main)
- Container: `archlinux:latest`
- Installs: `base-devel`, `git`, `go`, `npm`, `nodejs`
- Creates non-root `builder` user (makepkg refuses to run as root)
- Runs `makepkg -s --noconfirm` to produce `fpsd-git-*.pkg.tar.zst`
- Uploads package as workflow artifact
- Also runs `make test` and `make lint` inside the container as a CI gate

**Job 2: release** (runs only on `v*` tag push)
- Downloads the Arch package artifact from job 1
- Creates a GitHub Release via `softprops/action-gh-release@v1`
- Attaches the `.pkg.tar.zst` to the release

Workflow triggers:
- `push` to `main` branch
- `pull_request` to `main` branch
- `v*` tags (triggers release job)
- `workflow_dispatch` (manual trigger)

### PKGBUILD

Package name: `fpsd-git` (AUR `-git` convention for VCS packages).

```
pkgname=fpsd-git
arch=('x86_64')
license=('MIT')
depends=('glibc')
makedepends=('git' 'go' 'npm' 'nodejs')
```

Key design choices:

- **`arch=('x86_64')`**: Compiled Go binary, not `any`. Per user scope: linux-amd64 only.
- **`depends=('glibc')`**: Pure Go binary with no CGO deps (zombiezen.com/go/sqlite is pure Go). Only needs glibc.
- **`makedepends`**: Go compiler and Node.js/npm for the Vite frontend build.
- **No runtime deps on go/npm**: These are build-time only — the final binary embeds everything.
- **`source=("face-puncher-supreme::git+https://github.com/ushineko/face-puncher-supreme.git")`**: Standard AUR -git source. Named explicitly because the repo name differs from the package name.

#### build()

```bash
build() {
    cd face-puncher-supreme
    make build
}
```

`make build` handles the full chain: npm ci, vite build, go build with ldflags version injection.

#### package()

Install layout follows Arch packaging guidelines:

| Source | Destination | Notes |
| ------ | ----------- | ----- |
| `fpsd` | `/usr/bin/fpsd` | Compiled binary |
| `scripts/fps-ctl` | `/usr/bin/fps-ctl` | Tproxy management script |
| `fpsd.yml` | `/usr/share/doc/fpsd-git/fpsd.yml.example` | Reference config (user copies to `~/.config/fpsd/`) |
| `LICENSE` | `/usr/share/licenses/fpsd-git/LICENSE` | MIT license |
| `README.md` | `/usr/share/doc/fpsd-git/README.md` | Documentation |
| generated unit | `/usr/lib/systemd/user/fpsd.service` | Systemd user service |

The config is shipped as a reference example in `/usr/share/doc/`, not installed to `/etc/`. This avoids exposing dashboard credentials (stored in plaintext) as world-readable. Users copy and edit the example into their home directory (`~/.config/fpsd/fpsd.yml`), where standard home directory permissions protect it.

The systemd user unit installed by the package differs from the fps-ctl-generated one:

```ini
[Unit]
Description=Face Puncher Supreme proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=exec
ExecStart=/usr/bin/fpsd -c %h/.config/fpsd/fpsd.yml
WorkingDirectory=%h/.local/share/fpsd
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=fpsd
NoNewPrivileges=yes
ProtectSystem=strict
PrivateTmp=yes

[Install]
WantedBy=default.target
```

Key differences from fps-ctl unit:

- Binary path: `/usr/bin/fpsd` (pacman-managed) vs `%h/.local/bin/fpsd` (fps-ctl-managed)
- Config path: same (`%h/.config/fpsd/fpsd.yml`) — per-user config in both cases
- No `ProtectHome=tmpfs` or `BindPaths` — the fps-ctl unit hides the home directory and selectively bind-mounts config/data/binary paths back in. The packaged unit skips this because the binary is at `/usr/bin` (outside home), so only config and data dirs need home access. Simpler to leave `ProtectHome` at its default than to set up tmpfs + bind mounts.
- `WorkingDirectory=%h/.local/share/fpsd` — sets process CWD (fallback if data_dir is left as `.` in config, though absolute paths are recommended)

#### .install hooks

```bash
post_install() {
    echo ":: First-time setup:"
    echo "::   mkdir -p ~/.config/fpsd ~/.local/share/fpsd/logs"
    echo "::   cp /usr/share/doc/fpsd-git/fpsd.yml.example ~/.config/fpsd/fpsd.yml"
    echo "::   Edit ~/.config/fpsd/fpsd.yml — set data_dir to ~/.local/share/fpsd"
    echo ":: Enable the service: systemctl --user enable --now fpsd"
    echo ":: Generate CA for MITM: fpsd generate-ca (safe — refuses to overwrite existing CA)"
    echo ":: Transparent proxy setup: sudo fps-ctl install --transparent --interface <IF>"
}

post_upgrade() {
    echo ":: Restart the service: systemctl --user restart fpsd"
}

pre_remove() {
    # pacman hooks run as root — use --machine to target the installing user's session
    if [ -n "$SUDO_USER" ]; then
        systemctl --user --machine="$SUDO_USER@" stop fpsd.service 2>/dev/null || true
        systemctl --user --machine="$SUDO_USER@" disable fpsd.service 2>/dev/null || true
    fi
    echo ":: If fpsd is still running: systemctl --user stop fpsd"
}
```

#### Config adjustments

The `.install` hook's `post_install` instructs the user to copy the reference config and edit it. The reference config uses source-tree defaults (`data_dir: "."`, `listen: ":18737"`). The user should adjust `data_dir` and `listen` after copying — same as they would with fps-ctl.

Alternatively, a future enhancement could have `post_install` run sed to pre-adjust the copy (like fps-ctl does), but that adds complexity to a pacman hook. For now, the reference config documents the expected values:

| Field | Source tree default | Recommended package value | Rationale |
| ----- | ------------------- | ------------------------- | --------- |
| `data_dir` | `.` | `~/.local/share/fpsd` | Absolute path so `fpsd generate-ca` and other CLI commands work from any directory |
| `log_dir` | `logs` | `~/.local/share/fpsd/logs` | Absolute path, same reason |
| `listen` | `:18737` | `0.0.0.0:18737` | Bind all interfaces for LAN access |

Using absolute paths for `data_dir` is important: CA cert paths are resolved as `filepath.Join(data_dir, ca_cert)`. With `data_dir: "."`, running `fpsd generate-ca` from a random directory would write certs to the wrong place. The service unit's `WorkingDirectory` only applies when systemd starts the process — not when the user runs CLI commands directly.

#### CA certificate handling

CA certs (`ca-cert.pem`, `ca-key.pem`) live in the data directory (`~/.local/share/fpsd/`). This directory is user data — not owned by the package and never touched during install, upgrade, or removal.

`fpsd generate-ca` refuses to overwrite existing CA files unless `--force` is passed. This means:

- Installing the package over an existing fps-ctl setup: CA certs preserved (data dir untouched)
- Upgrading the package: CA certs preserved
- Uninstalling the package: CA certs preserved (data dir is not package-managed)
- The dogfood cycle (uninstall fps-ctl → install package → uninstall package → restore fps-ctl): CA certs survive the full cycle because no step touches the data directory

### fps-ctl compatibility

fps-ctl currently assumes it manages the binary (copies from source tree to ~/.local/bin). With a package-managed binary at /usr/bin, fps-ctl needs to detect this:

- **Package install detected** (binary at `/usr/bin/fpsd`): skip binary copy, skip service unit management, only handle tproxy iptables setup
- **Source tree install** (no package): current behavior unchanged

Detection: check if `/usr/bin/fpsd` exists and is managed by pacman (`pacman -Qo /usr/bin/fpsd`).

For this spec, fps-ctl's tproxy functionality is the main value-add for packaged installs — iptables rules are not managed by the package (they require root and are site-specific).

### AUR Publish Script

`scripts/update_aur.sh` — same pattern as clockwork-orange:

1. Clone or fetch `ssh://aur@aur.archlinux.org/fpsd-git.git` into `.aur-repo/`
2. Copy `PKGBUILD` and `fpsd.install` to `.aur-repo/`
3. Generate `.SRCINFO` via `makepkg --printsrcinfo`
4. Show diff, prompt for confirmation
5. Commit and push to AUR

Requires SSH key registered with AUR account. The `.aur-repo/` directory is gitignored.

### Dogfood Testing Procedure

Before committing, verify the full package lifecycle locally:

```
1. Uninstall current fps-ctl service (preserve data)
   └─ fps-ctl uninstall --yes
   └─ Keeps ~/.config/fpsd/ and ~/.local/share/fpsd/ (config, CA certs, databases)

2. Build Arch package
   └─ makepkg -sf --noconfirm

3. Install package
   └─ sudo pacman -U fpsd-git-*.pkg.tar.zst

4. Verify operation
   ├─ systemctl --user enable --now fpsd
   ├─ curl http://localhost:18737/fps/heartbeat → 200 OK, correct version
   ├─ fps-ctl status → shows binary, service, heartbeat
   └─ Dashboard loads at /fps/dashboard

5. Uninstall package
   └─ sudo pacman -R fpsd-git

6. Verify clean system
   ├─ /usr/bin/fpsd gone
   ├─ /usr/bin/fps-ctl gone
   ├─ /usr/lib/systemd/user/fpsd.service gone
   ├─ /usr/share/doc/fpsd-git/ gone
   ├─ systemctl --user status fpsd → not found
   └─ No fpsd process running

7. Restore development service
   └─ make build && fps-ctl install --no-transparent
   └─ Verify heartbeat OK
```

This cycle proves: package builds, installs cleanly, runs correctly, uninstalls without residue, and doesn't break the dev environment.

## File Changes

| File | Change |
| ---- | ------ |
| `.github/workflows/build.yml` | New — GitHub Actions workflow (build-arch + release jobs) |
| `PKGBUILD` | New — Arch Linux package build script |
| `fpsd.install` | New — Arch pacman install hooks (post_install, post_upgrade, pre_remove) |
| `scripts/update_aur.sh` | New — AUR publish script (interactive, SSH-based) |
| `.gitignore` | Add `.aur-repo/`, `*.pkg.tar.zst`, `pkg/`, `src/` (makepkg artifacts) |
| `scripts/fps-ctl` | Modify — detect package-managed binary, skip binary/service management when packaged |

## Acceptance Criteria

### PKGBUILD

- [x] `makepkg -s --noconfirm` builds successfully on Arch/CachyOS
- [x] `pkgver()` produces correct version from Makefile VERSION + git metadata
- [x] Package contains: `/usr/bin/fpsd`, `/usr/bin/fps-ctl`, reference config in `/usr/share/doc/fpsd-git/`, systemd user unit, license, docs
- [x] `fpsd version` inside package reports correct version
- [x] Reference config installed to `/usr/share/doc/fpsd-git/fpsd.yml.example`
- [x] `.install` hooks print config copy instructions on install and restart hint on upgrade
- [x] `pre_remove` stops and disables the user service

### GitHub Actions

- [x] Workflow triggers on push to main, PRs, v* tags, and manual dispatch
- [x] build-arch job: builds package in archlinux:latest container
- [x] build-arch job: runs `make test` and `make lint` as CI gates
- [x] build-arch job: uploads `.pkg.tar.zst` as artifact
- [x] release job: creates GitHub Release on v* tag with package attached
- [x] `fetch-depth: 0` used for full git history (required for pkgver)

### AUR Publishing

- [x] `scripts/update_aur.sh` clones/updates AUR repo, copies PKGBUILD, generates .SRCINFO
- [x] Script shows diff and prompts before pushing
- [x] `.aur-repo/` gitignored
- [x] AUR package name is `fpsd-git`

### fps-ctl Package Awareness

- [x] fps-ctl detects package-managed binary at `/usr/bin/fpsd`
- [x] When packaged: fps-ctl skips binary copy and service unit management
- [x] When packaged: fps-ctl still handles tproxy iptables setup
- [x] When not packaged: fps-ctl behaves exactly as before (no regression)

### Dogfood Lifecycle

- [x] fps-ctl uninstall removes dev service cleanly
- [x] Package builds locally with makepkg
- [x] Package installs with pacman -U
- [x] fpsd starts, heartbeat responds, dashboard loads
- [x] Package uninstalls cleanly (no orphaned files or services)
- [x] fps-ctl install restores dev environment after package removal
- [x] Full cycle documented in validation report

### Build Artifacts

- [x] `.gitignore` excludes makepkg artifacts (*.pkg.tar.zst, pkg/, src/, .aur-repo/)
- [x] Package size is reasonable (binary + embedded UI, ~5 MB compressed / ~12 MB installed)

## Out of Scope

- Debian/RPM/other distro packaging (Arch only per project scope)
- ARM builds (x86_64 only)
- Automated AUR publishing from CI (manual script only — AUR SSH key should not be in GitHub secrets)
- Docker images
- Reproducible builds
- Package signing (unsigned for now)
- Multi-architecture support
- Windows/macOS builds

## Security Considerations

- **No secrets in workflow**: The build job uses only `GITHUB_TOKEN` (auto-provided) for release creation. No SSH keys or deployment credentials stored in GitHub.
- **AUR publish is manual**: The `update_aur.sh` script runs locally with the user's SSH key. This avoids storing AUR credentials in CI.
- **Container isolation**: The Arch build runs in an ephemeral `archlinux:latest` container. No persistent state between builds.
- **Package checksums**: AUR -git packages use `sha256sums=('SKIP')` for git sources (standard practice — the source is fetched fresh each time).
- **systemd hardening**: The packaged service unit retains security directives (NoNewPrivileges, ProtectSystem, PrivateTmp).
