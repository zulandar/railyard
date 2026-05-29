{{/*
Expand the name of the chart.
*/}}
{{- define "railyard.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
We truncate at 63 chars because some Kubernetes name fields are limited to this.
If release name contains chart name it will be used as a full name.
*/}}
{{- define "railyard.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Create chart name and version as used by the chart label.
*/}}
{{- define "railyard.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels applied to all resources.
*/}}
{{- define "railyard.labels" -}}
helm.sh/chart: {{ include "railyard.chart" . }}
{{ include "railyard.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels used in matchLabels and service selectors.
*/}}
{{- define "railyard.selectorLabels" -}}
app.kubernetes.io/name: {{ include "railyard.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Service account name.
*/}}
{{- define "railyard.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "railyard.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Container image with tag defaulting to appVersion.
*/}}
{{- define "railyard.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag }}
{{- printf "%s:%s" .Values.image.repository $tag }}
{{- end }}

{{/*
Track-level engine image. Falls back to the global railyard.image when
the track does not specify image.repository.
Usage: include "railyard.trackImage" (dict "track" . "global" $)
*/}}
{{- define "railyard.trackImage" -}}
{{- if and .track.image .track.image.repository -}}
  {{- $tag := default (default .global.Chart.AppVersion .global.Values.image.tag) .track.image.tag -}}
  {{- printf "%s:%s" .track.image.repository $tag -}}
{{- else -}}
  {{- include "railyard.image" .global -}}
{{- end -}}
{{- end }}

{{/*
Config checksum annotation — forces pod rollout when the configmap changes.
Include in pod template metadata.annotations for any deployment that mounts
the railyard-config configmap.
*/}}
{{- define "railyard.configChecksum" -}}
checksum/config: {{ include (print $.Template.BasePath "/configmap.yaml") . | sha256sum }}
{{- end }}

{{/*
Database host — auto-derived when internal, otherwise from values.
*/}}
{{- define "railyard.dbHost" -}}
{{- if .Values.database.internal }}
{{- printf "%s-mysql" (include "railyard.fullname" .) }}
{{- else }}
{{- .Values.database.host }}
{{- end }}
{{- end }}

{{/*
Database name — defaults to railyard_{project}.
*/}}
{{- define "railyard.dbDatabase" -}}
{{- if .Values.database.database }}
{{- .Values.database.database }}
{{- else }}
{{- printf "railyard_%s" .Values.project }}
{{- end }}
{{- end }}

{{/*
pgvector host — auto-derived when internal, otherwise from values.
*/}}
{{- define "railyard.pgvectorHost" -}}
{{- if .Values.pgvector.internal }}
{{- printf "%s-pgvector" (include "railyard.fullname" .) }}
{{- else }}
{{- .Values.pgvector.host }}
{{- end }}
{{- end }}

{{/*
Auth secret name — uses existingSecret if set, otherwise generated name.
*/}}
{{- define "railyard.authSecretName" -}}
{{- if .Values.auth.existingSecret }}
{{- .Values.auth.existingSecret }}
{{- else }}
{{- printf "%s-auth" (include "railyard.fullname" .) }}
{{- end }}
{{- end }}

{{/*
Auth environment variables — renders envFrom for the auth secret.
Include this in any pod spec that needs agent credentials.
*/}}
{{- define "railyard.authEnvFrom" -}}
- secretRef:
    name: {{ include "railyard.authSecretName" . }}
{{- end }}

{{/*
Auth volume — provides volumes for OAuth credentials and GCP service accounts.
When using oauth_token, provides:
  - claude-credentials: Secret volume (read-only source for credentials)
  - claude-config: ConfigMap with claude.json
  - claude-home: writable emptyDir for /home/railyard/.claude
Claude Code requires ~/.claude/ to be writable — direct secret mounts fail silently.
Include in pod spec volumes.
*/}}
{{- define "railyard.authVolumes" -}}
{{- if eq .Values.auth.method "oauth_token" }}
- name: claude-credentials
  secret:
    secretName: {{ include "railyard.authSecretName" . }}
- name: claude-config
  configMap:
    name: {{ include "railyard.fullname" . }}-claude-config
- name: claude-home
  emptyDir: {}
{{- end }}
{{- if and (eq .Values.auth.method "vertex") .Values.auth.vertex.credentialsSecret }}
- name: gcp-credentials
  secret:
    secretName: {{ .Values.auth.vertex.credentialsSecret }}
{{- end }}
{{- end }}

{{/*
Auth volume mounts — mounts writable claude home and GCP credentials into containers.
Include in container volumeMounts.
*/}}
{{- define "railyard.authVolumeMounts" -}}
{{- if eq .Values.auth.method "oauth_token" }}
- name: claude-home
  mountPath: /home/railyard/.claude
{{- end }}
{{- if and (eq .Values.auth.method "vertex") .Values.auth.vertex.credentialsSecret }}
- name: gcp-credentials
  mountPath: /var/secrets/google
  readOnly: true
{{- end }}
{{- end }}

{{/*
Auth init containers — copies OAuth credentials into writable claude-home emptyDir.
Claude Code silently hangs if ~/.claude/ is not writable.
Include in pod spec initContainers.
*/}}
{{- define "railyard.authInitContainers" -}}
{{- if eq .Values.auth.method "oauth_token" }}
- name: copy-credentials
  image: {{ include "railyard.image" . }}
  command: ["sh", "-c"]
  args:
    - |
      mkdir -p /home/railyard/.claude
      cp /secrets/CLAUDE_CODE_OAUTH_TOKEN /home/railyard/.claude/.credentials.json 2>/dev/null || true
      cp /claude-config/claude.json /home/railyard/.claude/.claude.json 2>/dev/null || true
      chmod 600 /home/railyard/.claude/.credentials.json 2>/dev/null || true
      chmod 600 /home/railyard/.claude/.claude.json 2>/dev/null || true
  volumeMounts:
    - name: claude-credentials
      mountPath: /secrets
      readOnly: true
    - name: claude-config
      mountPath: /claude-config
      readOnly: true
    - name: claude-home
      mountPath: /home/railyard/.claude
{{- end }}
{{- end }}

{{/*
Auth extra env vars — adds provider-specific env vars beyond the secret.
Include in container env.
*/}}
{{- define "railyard.authExtraEnv" -}}
{{- if and (eq .Values.auth.method "vertex") .Values.auth.vertex.credentialsSecret }}
- name: GOOGLE_APPLICATION_CREDENTIALS
  value: /var/secrets/google/credentials.json
{{- end }}
{{- end }}

{{/*
DNS egress rule — allows DNS resolution via kube-system kube-dns.
Include in NetworkPolicy egress rules.
*/}}
{{- define "railyard.dnsEgress" -}}
- to:
    - namespaceSelector:
        matchLabels:
          kubernetes.io/metadata.name: kube-system
      podSelector:
        matchLabels:
          k8s-app: kube-dns
  ports:
    - protocol: UDP
      port: 53
    - protocol: TCP
      port: 53
{{- end }}
