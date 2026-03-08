# Security Policy

## Reporting a Vulnerability

If you discover a security vulnerability in Railyard, please report it responsibly.

Please email the repository maintainer directly. You can find contact information on the maintainer's GitHub profile.

### What to include

- Description of the vulnerability
- Steps to reproduce
- Affected version(s) or commit hash
- Impact assessment (if known)

### What to expect

This is an open-source project maintained on a best-effort basis. There are no guaranteed response times or SLAs. That said, we take security seriously and will do our best to:

1. Acknowledge your report promptly.
2. Investigate and assess the impact.
3. Coordinate disclosure timing with you.
4. Credit you in the fix (unless you prefer anonymity).

### Scope

The following are in scope:
- The Railyard CLI (`ry`) and all subcommands
- The dashboard web interface
- Helm chart templates and default configurations
- Database connectivity and credential handling
- Agent subprocess management

The following are out of scope:
- Third-party AI provider APIs (Claude, Codex, Gemini, OpenCode)
- Kubernetes cluster security (unless caused by Railyard's RBAC/config)
- Vulnerabilities in dependencies (please report these upstream)

## Security Documentation

For the full security posture document covering SOC 2 compliance mappings, OWASP Top 10 coverage, and the shared responsibility model, see [docs/security-posture.md](docs/security-posture.md).
