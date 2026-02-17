## Validation Report: Spec 012 — GitHub Actions CI + Arch Linux Packaging

**Date**: 2026-02-17 21:40
**Commit**: (pending)
**Status**: PASSED

### Phase 3: Tests

- Test suite: `make test` (`go test -race -short -v ./...`)
- Results: 128 passing, 0 failing
- No Go code changes — tests verify no regressions from fps-ctl modifications
- Status: PASSED

### Phase 4: Code Quality

- Dead code: None found
- Duplication: None found
- Encapsulation: `is_packaged()` helper cleanly isolates package detection logic
- Refactorings: fps-ctl `do_status()` version check fixed (`fpsd version` not `--version`)
- Status: PASSED

### Phase 5: Security Review

- Dependencies: No new Go dependencies added
- OWASP Top 10: N/A (no new Go code, only bash scripts and CI config)
- Bash scripts: No credential handling, no user input processing in PKGBUILD/install hooks
- CI workflow: Only uses `GITHUB_TOKEN` (auto-provided), no custom secrets
- AUR publish: Manual script with local SSH key, no credentials in CI
- Config as example: Dashboard credentials not installed world-readable (in /usr/share/doc/ as example, not /etc/)
- Anti-patterns: None found
- Status: PASSED

### Phase 5.5: Release Safety

- Change type: Code (fps-ctl bash script) + packaging files + CI config
- Rollback plan: `git revert` removes all packaging files. fps-ctl changes are additive (new `is_packaged()` branch) — removing them has no effect when binary is not package-managed
- Status: PASSED

### Lint

- Tool: golangci-lint v2.9.0
- Result: 0 issues
- Status: PASSED

### Dogfood Lifecycle

Full cycle completed on CachyOS (Arch Linux derivative):

1. **fps-ctl uninstall**: dev service stopped, binary removed, config/data preserved
2. **makepkg -sf --noconfirm**: built `fpsd-git-1.1.2.r31.g8cb743b-1-x86_64.pkg.tar.zst` (4.7 MB)
3. **sudo pacman -U**: installed, post_install hook printed setup instructions
4. **Verify operation**:
   - `fpsd version` → `fpsd 1.1.2`
   - `systemctl --user enable --now fpsd` → active (running)
   - `curl http://localhost:18737/fps/heartbeat` → 200 OK, version 1.1.2, all features active
   - Dashboard at /fps/dashboard → 200 OK
   - `fps-ctl status` → detected package-managed binary, showed package name
   - `fps-ctl install --no-transparent --yes` → correctly skipped binary/service, only tproxy
5. **sudo pacman -R fpsd-git**: uninstalled cleanly
   - /usr/bin/fpsd: removed
   - /usr/bin/fps-ctl: removed
   - /usr/lib/systemd/user/fpsd.service: removed
   - /usr/share/doc/fpsd-git/: removed
6. **fps-ctl install --no-transparent --yes**: dev environment restored, heartbeat OK

**Issue found and fixed during dogfood**:
- `pre_remove` hook runs as root, so `systemctl --user` targets root's session. Fixed with `--machine=$SUDO_USER@` targeting
- `fpsd --version` doesn't work (binary uses `version` subcommand). Fixed fps-ctl to use `fpsd version`

### Package Contents Verified

```
usr/bin/fpsd
usr/bin/fps-ctl
usr/lib/systemd/user/fpsd.service
usr/share/doc/fpsd-git/fpsd.yml.example
usr/share/doc/fpsd-git/README.md
usr/share/licenses/fpsd-git/LICENSE
```

### Overall

- All gates passed: YES
- Notes: GitHub Actions workflow cannot be verified locally (requires push to GitHub). Workflow follows the same pattern as the verified clockwork-orange CI. The PKGBUILD source URL points to GitHub; local testing used a `file://` URL temporarily switched back to GitHub URL for the final commit.
