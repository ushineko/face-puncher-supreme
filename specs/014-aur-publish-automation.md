# Spec 014: AUR Publish Automation

**Status**: COMPLETE
**Priority**: Medium
**Type**: CI/CD
**Scope**: GitHub Actions workflow + secret management

---

## Overview

Automate publishing the `fpsd-git` AUR package from GitHub Actions when a version tag is pushed. Currently, the AUR repo at `ssh://aur@aur.archlinux.org/fpsd-git.git` is updated manually from a local `.aur-repo/` clone. This spec adds a CI job that handles it automatically.

---

## Current State

- **PKGBUILD** lives in the main repo root (checked into GitHub)
- **fpsd.install** lives in the main repo root (checked into GitHub)
- **.aur-repo/** is a local clone of the AUR git repo (gitignored)
- AUR updates require: copy PKGBUILD + fpsd.install, regenerate `.SRCINFO`, commit, push via SSH
- AUR remote: `ssh://aur@aur.archlinux.org/fpsd-git.git`
- AUR uses SSH key authentication (key registered at https://aur.archlinux.org account settings)

---

## SSH Key Security Model

### How GitHub Actions Secrets Work

- Secrets are stored encrypted at rest in GitHub's infrastructure
- They are injected as environment variables at workflow runtime
- GitHub automatically **masks** secret values in all log output — if the secret appears in stdout/stderr, it is replaced with `***`
- Secrets are **not passed** to workflows triggered by forks (pull requests from forks cannot access secrets)
- Secrets are **not exposed** in the Actions UI, API responses, or workflow definition files
- Only repository admins/owners can create or update secrets
- Secrets are scoped to the repository (not shared across repos unless using organization secrets)

### Key Management Plan

1. **Generate a dedicated SSH key pair** for AUR publishing (not your personal key):
   ```
   ssh-keygen -t ed25519 -C "fpsd-git-aur-deploy" -f ~/.ssh/aur_deploy
   ```

2. **Register the public key** at https://aur.archlinux.org → My Account → SSH Public Key
   - This replaces any existing key. If you use the same AUR account for manual pushes, you'll need to use this deploy key locally too, or register it as an additional key (AUR supports one key per account — consider this)

3. **Store the private key** as a GitHub repository secret named `AUR_SSH_KEY`:
   - Go to repo → Settings → Secrets and variables → Actions → New repository secret
   - Name: `AUR_SSH_KEY`
   - Value: entire contents of `~/.ssh/aur_deploy` (the private key file, including `-----BEGIN/END-----` lines)

4. **Delete the local private key** after storing it in GitHub:
   ```
   rm ~/.ssh/aur_deploy
   ```
   The public key (`~/.ssh/aur_deploy.pub`) can be kept for reference.

### Risk Assessment

| Risk | Mitigation |
|------|------------|
| Key leaked from GitHub secret | GitHub encrypts at rest, masks in logs, restricts access to repo admins. Rotate key if compromise suspected. |
| Fork PR accesses the key | GitHub does not pass secrets to fork PR workflows. The AUR job only runs on tag pushes to main anyway. |
| Key has broad permissions | AUR SSH keys only grant push access to packages you maintain. No shell access, no access to other users' packages. |
| Single AUR key constraint | AUR allows one SSH key per account. The deploy key replaces any existing key. For manual pushes, either use the same key locally or set up a dedicated AUR account for CI. |
| Workflow modified to exfiltrate key | Only repo admins can modify workflows on protected branches. Review workflow changes in PRs. |

### Alternative: Dedicated AUR Account

If you want to keep your personal AUR SSH key for manual use:

1. Create a second AUR account (e.g., `ushineko-ci`) as a co-maintainer of `fpsd-git`
2. Register the deploy key on that account
3. This way your personal key is unaffected and CI uses its own identity

This is optional — a single account with the deploy key works fine for a personal project.

---

## Implementation

### New Workflow Job

Add a `publish-aur` job to `.github/workflows/release.yml` that runs after the build job, only on tag pushes.

```yaml
publish-aur:
  name: Publish to AUR
  needs: [build-arch]
  if: startsWith(github.ref, 'refs/tags/v')
  runs-on: ubuntu-latest

  steps:
  - name: Checkout Code
    uses: actions/checkout@v4

  - name: Configure SSH for AUR
    run: |
      mkdir -p ~/.ssh
      echo "${{ secrets.AUR_SSH_KEY }}" > ~/.ssh/aur_deploy
      chmod 600 ~/.ssh/aur_deploy
      cat >> ~/.ssh/config <<EOF
      Host aur.archlinux.org
        IdentityFile ~/.ssh/aur_deploy
        User aur
        StrictHostKeyChecking accept-new
      EOF

  - name: Clone AUR Repo
    run: |
      git clone ssh://aur@aur.archlinux.org/fpsd-git.git aur-pkg

  - name: Update AUR Package
    run: |
      cp PKGBUILD fpsd.install aur-pkg/

      # Generate .SRCINFO from PKGBUILD
      # makepkg --printsrcinfo requires being in an Arch environment,
      # but .SRCINFO is a simple format we can generate directly
      cd aur-pkg
      cat > .SRCINFO <<SRCINFO
      pkgbase = fpsd-git
        pkgdesc = Content-aware HTTPS interception proxy for ad blocking
        pkgver = $(grep -oP '(?<=^pkgver=).*' PKGBUILD)
        pkgrel = $(grep -oP '(?<=^pkgrel=).*' PKGBUILD)
        url = https://github.com/ushineko/face-puncher-supreme
        install = fpsd.install
        arch = x86_64
        license = MIT
        makedepends = git
        makedepends = go
        makedepends = npm
        makedepends = nodejs
        depends = glibc
        provides = fpsd
        conflicts = fpsd
        source = face-puncher-supreme::git+https://github.com/ushineko/face-puncher-supreme.git
        sha256sums = SKIP

      pkgname = fpsd-git
      SRCINFO

  - name: Commit and Push to AUR
    run: |
      cd aur-pkg
      git config user.name "ushineko"
      git config user.email "ushineko@users.noreply.github.com"

      git add PKGBUILD fpsd.install .SRCINFO
      if git diff --cached --quiet; then
        echo "No changes to publish"
      else
        VERSION=$(grep -oP '(?<=^pkgver=).*' PKGBUILD)
        git commit -m "Update to ${VERSION}"
        git push
      fi
```

### .SRCINFO Generation

The `.SRCINFO` file is normally generated by `makepkg --printsrcinfo`, which requires an Arch Linux environment. Since the CI job runs on `ubuntu-latest` (not the Arch container from the build job), we generate it directly. The format is static for this package — the only dynamic fields are `pkgver` and `pkgrel`, both read from the PKGBUILD.

**Alternative**: Run the AUR publish job in the same `archlinux:latest` container and use `makepkg --printsrcinfo`. This is more correct but requires the SSH key setup inside the container. Either approach works; the inline generation is simpler and sufficient for a package with no complex metadata.

### Trigger Conditions

The job runs only when:
- A tag matching `v*` is pushed (same trigger as the release job)
- The `build-arch` job succeeds (tests + lint + package build all passed)
- The `AUR_SSH_KEY` secret exists (if missing, the SSH step fails gracefully)

### PKGBUILD pkgver Sync

The PKGBUILD committed to GitHub already has `pkgver` set by prior commits (we update it alongside VERSION bumps). The `pkgver()` function in the PKGBUILD recalculates the version at build time from the Makefile VERSION + git rev count, but the static `pkgver=` field in the file is what AUR displays on the package page. This is already kept in sync by our workflow (bump VERSION → update PKGBUILD pkgver → commit → tag → push).

---

## Setup Steps (One-Time, Manual)

These steps must be done by the repo owner before the workflow will work:

1. [x] Generate passwordless ed25519 SSH key pair: `ssh-keygen -t ed25519 -C "fpsd-git-aur-deploy" -f ~/.ssh/aur_deploy -N ""`
2. [x] Register public key at https://aur.archlinux.org → My Account → SSH Public Key
3. [x] Add private key as GitHub secret `AUR_SSH_KEY` at repo → Settings → Secrets → Actions
4. [ ] Delete local private key: `rm ~/.ssh/aur_deploy` (kept locally for manual use)
5. [x] (Optional) Test by pushing a tag and watching the workflow run

---

## Acceptance Criteria

- [x] `publish-aur` job added to `.github/workflows/release.yml`
- [x] Job runs only on `v*` tag pushes, after the build succeeds
- [x] Job clones AUR repo, copies PKGBUILD + fpsd.install, generates .SRCINFO, commits, and pushes
- [x] Job is a no-op if PKGBUILD hasn't changed (no empty commits)
- [x] SSH key is loaded from `AUR_SSH_KEY` secret, never hardcoded
- [x] SSH key is configured with `StrictHostKeyChecking accept-new` (trust on first use for AUR host)
- [x] Setup steps documented for SSH key generation and secret creation
- [x] Existing local `.aur-repo/` workflow continues to work for manual pushes if needed

---

## Out of Scope

- Automated pkgver bumping (already handled in our release workflow)
- AUR package deletion or ownership transfer
- Multi-architecture support (x86_64 only)
- Automated testing of the built Arch package (already covered by the `build-arch` job)
