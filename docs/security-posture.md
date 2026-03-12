# Railyard Security Document

**Audience:** Security engineers, DevOps/SecOps, compliance reviewers (SOC 2, OWASP)
**Last updated:** 2026-03-08
**Scope:** Railyard — an AI agent orchestration platform that spawns, monitors, and coordinates coding agents (Claude, Codex, Gemini, OpenCode) against a MySQL database and optional pgvector instance.

---

## Shared Responsibility Model

Railyard follows a shared responsibility model. **Part 1** covers controls built into the product. **Part 2** covers controls the operator must configure for their deployment environment.

| Layer | Owner | Examples |
|-------|-------|----------|
| Application code, security headers, credential sanitization | Railyard (inherent) | CSP, SQL injection prevention, log redaction |
| Authentication, network segmentation, encryption at rest | Operator (inherited) | OAuth2 Proxy, NetworkPolicy, volume encryption |

---

## Part 1: Inherent Controls

Controls provided by Railyard out of the box. Each control references the relevant SOC 2 Trust Service Criteria and OWASP Top 10 category where applicable.

### 1.1 Injection Prevention

**SOC 2:** CC6.1 (Logical Access) | **OWASP:** A03:2021 Injection

Railyard uses parameterized queries and identifier quoting across all database interactions to prevent SQL injection.

**Go (MySQL via GORM):**
All database operations use GORM's parameterized query interface. No raw string interpolation is used in SQL statements. The `internal/db/` package handles all database connectivity.

**Python (pgvector via psycopg2):**
Dynamic table names use `psycopg2.sql.Identifier()` quoting. The `cocoindex/` package includes regression tests (`injection_security_test.py`) that verify malicious engine IDs, track names, and table names cannot bypass quoting.

**Evidence:**
- `internal/engine/injection_security_test.go` — Go-side injection regression tests
- `internal/messaging/injection_security_test.go` — messaging layer injection tests
- `cocoindex/injection_security_test.py` — Python-side injection regression tests

### 1.2 Transport Security

**SOC 2:** CC6.1, CC6.7 (Encryption in Transit) | **OWASP:** A02:2021 Cryptographic Failures

The dashboard server supports TLS termination directly. When TLS certificates are provided, the server:

- Serves over HTTPS
- Sets `Strict-Transport-Security: max-age=63072000; includeSubDomains` (HSTS)
- Detects TLS vs. plaintext and adjusts output accordingly

**Security response headers** (applied to every response via `securityHeaders()` middleware in `internal/dashboard/server.go`):

| Header | Value | Purpose |
|--------|-------|---------|
| `X-Content-Type-Options` | `nosniff` | Prevents MIME-type sniffing |
| `X-Frame-Options` | `DENY` | Prevents clickjacking via iframes |
| `Referrer-Policy` | `strict-origin-when-cross-origin` | Limits referrer leakage |
| `Permissions-Policy` | `camera=(), microphone=(), geolocation=()` | Disables unnecessary browser APIs |
| `Content-Security-Policy` | `default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; connect-src 'self'` | Restricts resource loading origins |

**Evidence:**
- `internal/dashboard/transport_security_test.go` — verifies all headers are set and TLS behavior

### 1.3 Credential Protection

**SOC 2:** CC6.1, CC6.5 (Credential Management)

Railyard actively prevents credential leakage through multiple layers:

**Database error sanitization:**
The `sanitizeDBError()` function in `internal/db/connect.go` strips credentials from database error messages before they reach logs or CLI output. This handles:
- Explicit password replacement
- Regex-based `user:password@host` DSN pattern scrubbing
- URL-encoded password handling (`%40`, `%23`, etc.)
- Multiple DSN formats (MySQL `tcp()`, PostgreSQL `host:port`, Unix sockets)

**Agent log redaction:**
The engine's log writer redacts sensitive token patterns from agent subprocess output before writing to the `agent_logs` table:
- Anthropic API keys (`sk-ant-*`)
- OpenAI API keys (`sk-*`)
- Slack bot/user tokens (`xoxb-*`, `xoxp-*`)
- Discord bot tokens
- AWS credentials (`AKIA*`)
- Generic bearer tokens

**Evidence:**
- `internal/db/secrets_security_test.go` — DSN sanitization edge cases
- `internal/db/auth_security_test.go` — credential handling regression suite
- `internal/engine/log_sanitization_security_test.go` — token redaction tests

### 1.4 Access Control (Kubernetes RBAC)

**SOC 2:** CC6.2, CC6.3 (Role-Based Access)

The Helm chart creates a least-privilege RBAC configuration:

- **Dedicated ServiceAccount** per release (`charts/railyard/templates/serviceaccount.yaml`)
- **Namespace-scoped Role** (not ClusterRole) with permissions limited to:
  - `configmaps`: get, list, watch
  - `secrets`: get, list, watch
- **RoleBinding** scoped to the release namespace only

No write access to secrets, no cluster-wide permissions, no pod exec capabilities.

**Evidence:**
- `charts/railyard/templates/rbac.yaml`
- `charts/railyard/templates/serviceaccount.yaml`

### 1.5 Resource Protection

**SOC 2:** CC6.1 (Availability)

**SSE connection limiting:**
The dashboard's Server-Sent Events endpoint enforces a maximum concurrent connection limit to prevent resource exhaustion. When the limit is reached, new connections receive `503 Service Unavailable`.

**Read-only dashboard:**
The dashboard registers only GET handlers. POST, PUT, DELETE, and PATCH requests return 404/405. This eliminates the CSRF attack surface entirely — there are no state-changing operations exposed via the web interface.

**Evidence:**
- `internal/dashboard/auth_security_test.go` — `TestSSEConnectionLimit`, `TestNoCSRFProtection`

### 1.6 Subprocess Isolation

**SOC 2:** CC7.2 (System Monitoring)

Agent subprocesses are spawned with:
- **Cryptographically random session IDs** (via `crypto/rand`) for traceability
- **Context-scoped lifecycle** — cancelling the parent context terminates the subprocess
- **Process group isolation** — subprocesses receive their own process group via `syscall.SysProcAttr`
- **Captured stdout/stderr** — all output is buffered, redacted, and persisted to the `agent_logs` table

**Evidence:**
- `internal/engine/subprocess.go`
- `internal/engine/subprocess_test.go`

### 1.7 Complete I/O Logging

**SOC 2:** CC7.2, CC7.3 (Monitoring and Detection)

Every agent interaction is logged to the `agent_logs` table:

| Column | Content |
|--------|---------|
| `engine_id` | Which engine processed the work |
| `session_id` | Unique per subprocess spawn |
| `car_id` | The work unit being processed |
| `direction` | `in` (prompt) or `out` (response) |
| `content` | Full text (redacted of credentials) |
| `created_at` | Timestamp |

The database provides a persistent audit trail of all agent interactions.

### 1.8 OWASP Top 10 Regression Suite

**OWASP mapping of existing security test files:**

| OWASP Category | Test File(s) | Coverage |
|----------------|-------------|----------|
| A03: Injection | `engine/injection_security_test.go`, `messaging/injection_security_test.go`, `cocoindex/injection_security_test.py` | SQL injection via parameterized queries and identifier quoting |
| A05: Security Misconfiguration | `engine/misconfig_security_test.go`, `dispatch/misconfig_security_test.go` | Default config validation, safe defaults |
| A07: Identification & Auth Failures | `db/auth_security_test.go`, `dashboard/auth_security_test.go` | Credential handling, auth boundary documentation |
| A09: Security Logging & Monitoring Failures | `engine/log_sanitization_security_test.go`, `db/secrets_security_test.go` | Log redaction, credential scrubbing |

### 1.9 Authentication Methods

**SOC 2:** CC6.1, CC6.2

Railyard supports multiple authentication methods for AI provider credentials, documented in `docs/k8s-authentication.md`:

| Method | Credential Storage | Rotation | Use Case |
|--------|-------------------|----------|----------|
| API Key | Kubernetes Secret | Manual | Enterprise, CI/CD |
| OAuth Token | Kubernetes Secret | Annual | Small teams, Max plan |
| AWS Bedrock | Kubernetes Secret (IAM creds) | IAM rotation | AWS-native |
| Google Vertex AI | Mounted SA JSON | SA key rotation | GCP-native |
| Azure AI Foundry | Kubernetes Secret | Portal rotation | Azure-native |
| Vault Helper | Dynamic via `ANTHROPIC_API_KEY_HELPER` | Automatic | Strict rotation policies |

All credentials are stored in Kubernetes Secrets and injected via `envFrom` — never baked into container images or ConfigMaps.

---

## Part 2: Operator Responsibilities

Controls that require operator configuration. These are not gaps in the product — they are deployment decisions that depend on the operator's environment and compliance requirements.

### 2.1 Dashboard Authentication

**SOC 2:** CC6.1, CC6.2 | **Status:** Operator-configured (by design)

The dashboard does not include built-in authentication middleware. This is a deliberate architectural decision — authentication is delegated to the infrastructure layer to avoid coupling the dashboard to a specific identity provider.

**Required action:**

For any network-exposed deployment, configure one of:

1. **OAuth2 Proxy sidecar** (recommended) — The Helm chart includes built-in support:
   ```yaml
   dashboard:
     oauth2proxy:
       enabled: true
       provider: github  # or google, oidc, etc.
       clientID: "..."
       clientSecret: "..."
   ```

2. **Ingress-level authentication** — Use your ingress controller's auth annotations (e.g., nginx `auth-url`, Traefik ForwardAuth).

3. **Network isolation** — For development, use `kubectl port-forward` which restricts access to the operator's machine.

**What is exposed without authentication:**
- All dashboard routes (`/`, `/cars`, `/messages`, `/logs`, `/sessions`)
- SSE event stream (`/api/events`) — real-time escalation alerts
- Agent messages and full I/O logs (classified HIGH sensitivity)

### 2.2 Network Segmentation

**SOC 2:** CC6.6 (Network Security) | **Status:** Available (opt-in)

The Helm chart includes optional NetworkPolicy resources gated by `networkPolicy.enabled`. When enabled, traffic is restricted to required paths only:

| Source | Destination | Port | Purpose |
|--------|-------------|------|---------|
| Dashboard | MySQL | 3306 | Query operational data |
| Engine | MySQL | 3306 | Read/write cars, logs |
| Engine | AI Provider APIs | 443 | Outbound API calls |
| Engine | pgvector | 5432 | Overlay reads/writes |
| Configured CIDR / namespace | Dashboard | 8080 | User access |
| Yardmaster | MySQL | 3306 | Health checks, rebalancing |
| All pods | kube-dns | 53 | DNS resolution |

All other intra-namespace and cross-namespace traffic is blocked.

**Evidence:**
- `charts/railyard/templates/networkpolicy.yaml` — per-component NetworkPolicy resources
- `charts/railyard/ci/test-values-networkpolicy.yaml` — CI test values with policies enabled

### 2.3 Secrets Management

**SOC 2:** CC6.1, CC6.5 | **Status:** Operator-configured

**Required actions:**

1. **Never commit credentials in values files.** Use `--set` flags or external secret management.
2. **Use `auth.existingSecret`** to reference pre-created Kubernetes Secrets managed by:
   - Sealed Secrets
   - External Secrets Operator
   - HashiCorp Vault (via Vault Secrets Operator)
3. **Enable Vault-based rotation** for automated credential lifecycle:
   ```yaml
   auth:
     method: api_key
     apiKeyHelper: "vault read -field=key secret/anthropic"
   ```

### 2.4 Encryption at Rest

**SOC 2:** CC6.1, CC6.7 | **Status:** Operator-configured

Railyard stores data in MySQL and optionally pgvector (PostgreSQL). Neither database encrypts data at rest by default.

**Required action:**

- Enable volume encryption on the underlying storage (AWS EBS encryption, GCP PD encryption, Azure Disk encryption)
- For managed database services, enable the provider's encryption-at-rest feature
- Consider MySQL's built-in backup capabilities for encrypted offsite backups

### 2.5 Monitoring and Alerting

**SOC 2:** CC7.2, CC7.3 | **Status:** Partially implemented

Railyard writes agent logs to the `agent_logs` table and emits structured audit events for configuration changes.

**Implemented:**

- **Audit event logging** (`internal/audit/`): Config load/reload, track seeding, config seeding, and credential status changes are recorded to the `audit_events` table and emitted as structured JSON to stderr for SIEM ingestion.
  - Event types: `config.loaded`, `config.seed_tracks`, `config.seed_config`, `credentials.default_detected`
  - JSON output includes `"audit": true` marker for easy filtering
  - Each event captures: event_type, actor, resource, detail, timestamp

**Required action:**

1. Deploy a log shipper (Fluentd, Vector, Promtail) to forward structured audit JSON from stderr to your SIEM
2. Configure alerts on:
   - `credentials.default_detected` events (insecure defaults)
   - Engine stall detection (agent subprocess timeouts)
   - Escalation events (yardmaster escalations indicate agent failures)
   - Failed database connections (may indicate credential issues)
3. Use standard MySQL audit logging for data-layer audit trails

### 2.6 Dashboard Bind Address

**SOC 2:** CC6.6 | **Status:** Operator-configured

The dashboard binds to `0.0.0.0` (all interfaces) by default. In local development environments without a firewall, the dashboard is accessible from any machine on the network.

**Required action for local development:**
- Use firewall rules to restrict access to the dashboard port
- Or use `kubectl port-forward` which binds to localhost only

In Kubernetes deployments, this is not a concern — the pod's network interface is isolated by the cluster network, and access is controlled via Service/Ingress configuration.

---

## Part 3: Known Gaps and Roadmap

Gaps documented honestly with their current status. Items marked **By Design** are architectural decisions with documented mitigations. Items marked **Roadmap** are planned improvements.

| Gap | Status | Mitigation | Tracking |
|-----|--------|------------|----------|
| No built-in dashboard authentication | By Design | Delegate to OAuth2 Proxy sidecar or ingress-level auth | N/A — see Section 2.1 |
| No dashboard rate limiting | Roadmap | Network-level rate limiting via ingress controller | `railyard-uqy` |
| No audit trail for config changes | Roadmap | Database audit logging provides partial coverage | `railyard-bsk` |
| No NetworkPolicy templates | Roadmap | Operator defines custom policies | `railyard-795` |
| Dashboard binds to 0.0.0.0 | By Design | K8s pod networking isolates; local dev uses port-forward | N/A — see Section 2.6 |
| No encryption at rest (application layer) | By Design | Delegated to infrastructure (volume encryption) | N/A — see Section 2.4 |

---

## Compliance Framework Mapping

### SOC 2 Trust Service Criteria

| Criteria | Category | Railyard Coverage | Section |
|----------|----------|-------------------|---------|
| CC6.1 | Logical Access / Security | Credential sanitization, security headers, K8s Secrets, TLS | 1.2, 1.3, 1.9 |
| CC6.2 | Role-Based Access | K8s RBAC, ServiceAccount, auth delegation | 1.4, 2.1 |
| CC6.3 | Least Privilege | Namespace-scoped Role, read-only permissions | 1.4 |
| CC6.5 | Credential Management | Redaction, Vault helper, existingSecret support | 1.3, 2.3 |
| CC6.6 | Network Security | Operator-configured NetworkPolicy, ingress controls | 2.2, 2.6 |
| CC6.7 | Encryption | TLS in transit (inherent), at rest (operator) | 1.2, 2.4 |
| CC7.1 | Vulnerability Management | OWASP regression test suite, CI lint gates | 1.8 |
| CC7.2 | System Monitoring | Complete I/O logging, subprocess traceability | 1.6, 1.7 |
| CC7.3 | Detection & Response | Agent log persistence, escalation alerting | 1.7, 2.5 |

### OWASP Top 10 (2021)

| Category | Status | Evidence |
|----------|--------|----------|
| A01: Broken Access Control | Operator-configured (OAuth2 Proxy) | Section 2.1 |
| A02: Cryptographic Failures | Mitigated (TLS, HSTS, credential redaction) | Section 1.2, 1.3 |
| A03: Injection | Mitigated (parameterized queries, identifier quoting) | Section 1.1 |
| A04: Insecure Design | Mitigated (read-only dashboard, least-privilege RBAC) | Section 1.4, 1.5 |
| A05: Security Misconfiguration | Tested (misconfig regression tests, safe defaults) | Section 1.8 |
| A06: Vulnerable Components | CI lint gates, dependency scanning (operator responsibility) | N/A |
| A07: Identification & Auth Failures | Tested (auth boundary documentation, credential handling) | Section 1.8 |
| A08: Software & Data Integrity Failures | Database constraints and GORM migrations provide data integrity guarantees | Section 1.7 |
| A09: Security Logging Failures | Mitigated (complete I/O logging, log redaction tests) | Section 1.7, 1.8 |
| A10: Server-Side Request Forgery | Low risk (no user-controlled URL fetching) | N/A |

### PCI-DSS Applicability

Railyard does not process, store, or transmit cardholder data. PCI-DSS is not directly applicable to Railyard itself. If Railyard is deployed within a Cardholder Data Environment (CDE), the operator is responsible for ensuring the deployment meets PCI-DSS requirements for that environment.

---

## Security Testing

### Running the security regression suite

All security tests follow the naming convention `*_security_test.go` (Go) and `*_security_test.py` (Python):

```bash
# Go security tests
go test ./internal/... -run Security -v

# Python security tests
cd cocoindex && python -m pytest *_security_test.py -v
```

### CI enforcement

Security tests run as part of the standard CI pipeline (`.github/workflows/ci.yml`). Lint gates (`golangci-lint`) run before tests to catch static analysis issues.
