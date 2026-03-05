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
Dolt host — auto-derived when internal, otherwise from values.
*/}}
{{- define "railyard.doltHost" -}}
{{- if .Values.dolt.internal }}
{{- printf "%s-dolt" (include "railyard.fullname" .) }}
{{- else }}
{{- .Values.dolt.host }}
{{- end }}
{{- end }}

{{/*
Dolt database name — defaults to railyard_{project}.
*/}}
{{- define "railyard.doltDatabase" -}}
{{- if .Values.dolt.database }}
{{- .Values.dolt.database }}
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
Auth volume — mounts claude.json ConfigMap when using oauth_token method.
Include in pod spec volumes.
*/}}
{{- define "railyard.authVolumes" -}}
{{- if eq .Values.auth.method "oauth_token" }}
- name: claude-config
  configMap:
    name: {{ include "railyard.fullname" . }}-claude-config
{{- end }}
{{- if and (eq .Values.auth.method "vertex") .Values.auth.vertex.credentialsSecret }}
- name: gcp-credentials
  secret:
    secretName: {{ .Values.auth.vertex.credentialsSecret }}
{{- end }}
{{- end }}

{{/*
Auth volume mounts — mounts claude.json and GCP credentials into containers.
Include in container volumeMounts.
*/}}
{{- define "railyard.authVolumeMounts" -}}
{{- if eq .Values.auth.method "oauth_token" }}
- name: claude-config
  mountPath: /home/railyard/.claude.json
  subPath: claude.json
  readOnly: true
{{- end }}
{{- if and (eq .Values.auth.method "vertex") .Values.auth.vertex.credentialsSecret }}
- name: gcp-credentials
  mountPath: /var/secrets/google
  readOnly: true
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
