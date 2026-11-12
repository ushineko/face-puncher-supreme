# Security

fpsd is an HTTPS interception proxy. It holds a private CA key that can sign certificates trusted by every client that installed its CA, terminates TLS for the domains it intercepts, and sees the plaintext of those connections, including session cookies and tokens. Issues that expose any of that are in scope.

## Reporting a vulnerability

Report it privately through GitHub: the repository's **Security** tab, **Report a vulnerability**. Please do not open a public issue for it.

Include what you found, how to reproduce it, and the fpsd version (`fpsd version`) and platform. A proof of concept helps.

Expect a reply within a week. A confirmed finding is fixed in a release with a regression test and credited in the changelog unless you ask otherwise. Until that release is out, the finding stays in the private advisory.

## In scope

- CA key and leaf certificate handling (`internal/mitm`), including upstream certificate verification;
- the proxy listeners (explicit, transparent) and what they forward or block;
- the dashboard and management endpoints (`/fps/`), their authentication and session handling;
- content filter plugins and the traffic-capture plugin's on-disk output;
- the installer (`scripts/fps-ctl`) and the Arch package.

## Out of scope

- Deployments that expose the dashboard or proxy port to untrusted networks; fpsd is built for a trusted LAN.
- Findings that require an attacker who already controls the host running fpsd.

## Verifying a release

Each release publishes `SHA256SUMS` and build provenance attestations:

```bash
sha256sum -c SHA256SUMS
gh attestation verify <file> -R ushineko/face-puncher-supreme
```
