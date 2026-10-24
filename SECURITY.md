# Security Policy

## Supported Versions

Currently, only the latest major release branch is actively supported with security updates.

| Version | Supported          |
| ------- | ------------------ |
| 1.0.x   | :white_check_mark: |
| < 1.0   | :x:                |

## Gateway and guest boundaries

- Without an API token, HTTP is restricted to a literal loopback listener and
  rejects untrusted Host/cross-origin browser requests. Local processes are not
  authenticated in this development mode.
- A configured `GOWASMRUNNER_API_TOKEN` is required in a single Authorization
  Bearer header before body reads or execution. It grants access to all cached
  plugins/functions; there is no user, tenant or per-plugin role model.
- Remote and wildcard listeners require both the token and TLS certificate/key
  files. The server does not trust forwarded headers as proof of HTTPS. A proxy
  can use plaintext only on a loopback connection to a token-protected runner.
- Request bodies are capped at 1 MiB. Guest memory and execution deadlines are
  enforced per instance, including cancellation of infinite loops. These limits
  do not impose a total process-memory, CPU or concurrent-request quota.
- Guest configuration does not inherit the host environment or grant filesystem
  mounts/sockets. The API token is not forwarded to Wasm. Guests can read their
  supplied input and write to the configured stdout/stderr bridge.

Tokens are deployment environment values; private TLS keys are unencrypted PEM
files protected by operating-system permissions. They are read at startup, not
hot-reloaded. Rotation requires a process restart and coordinated client updates.
The development TLS script creates seven-day identities and refuses to replace
existing files. Keep the CA private key outside containers and source control.
Do not put credentials in URLs, plugin payloads, images or public reports.

## Reporting a Vulnerability

Security is a primary focus for `gowasmrunner` due to its nature as a runtime for potentially untrusted third-party code.

If you discover a vulnerability, **do not open a public issue.**

Use this repository's private vulnerability-reporting facility when available.
If it is unavailable, establish a private channel with the maintainer before
sharing sensitive details. No functioning security email address is configured
in this repository; do not send reports to an example address.

We aim to acknowledge receipt of vulnerability reports within 48 hours and will provide regular updates on the resolution process. We practice responsible disclosure and ask that you give us a reasonable timeframe to patch the issue before making it public.
