{{- define "rota.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "rota.fullname" -}}
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

{{- define "rota.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{- define "rota.labels" -}}
helm.sh/chart: {{ include "rota.chart" . }}
app.kubernetes.io/name: {{ include "rota.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{- define "rota.coreSelectorLabels" -}}
app.kubernetes.io/name: {{ include "rota.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: core
{{- end }}

{{- define "rota.dashboardSelectorLabels" -}}
app.kubernetes.io/name: {{ include "rota.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/component: dashboard
{{- end }}

{{- define "rota.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "rota.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/* Secret holding the chart-managed credentials. */}}
{{- define "rota.secretName" -}}
{{- include "rota.fullname" . }}
{{- end }}

{{- define "rota.coreImage" -}}
{{- printf "%s:%s" .Values.core.image.repository (default .Chart.AppVersion .Values.core.image.tag) }}
{{- end }}

{{- define "rota.dashboardImage" -}}
{{- printf "%s:%s" .Values.dashboard.image.repository (default .Chart.AppVersion .Values.dashboard.image.tag) }}
{{- end }}

{{/* Settings the chart stores in its own Secret (only non-empty ones). */}}
{{- define "rota.secretData" -}}
{{- if not .Values.secrets.existingSecret }}
{{- with .Values.secrets.encryptionKey }}
ROTA_ENCRYPTION_KEY: {{ . | b64enc | quote }}
{{- end }}
{{- with .Values.secrets.encryptionKeysPrevious }}
ROTA_ENCRYPTION_KEYS_PREVIOUS: {{ . | b64enc | quote }}
{{- end }}
{{- with .Values.secrets.jwtSecret }}
JWT_SECRET: {{ . | b64enc | quote }}
{{- end }}
{{- with .Values.secrets.adminUser }}
ROTA_ADMIN_USER: {{ . | b64enc | quote }}
{{- end }}
{{- with .Values.secrets.adminPassword }}
ROTA_ADMIN_PASSWORD: {{ . | b64enc | quote }}
{{- end }}
{{- with .Values.secrets.metricsToken }}
METRICS_TOKEN: {{ . | b64enc | quote }}
{{- end }}
{{- end }}
{{- if and .Values.database.password (not .Values.database.existingSecret) }}
DB_PASSWORD: {{ .Values.database.password | b64enc | quote }}
{{- end }}
{{- if and .Values.redis.url (not .Values.redis.existingSecret) }}
REDIS_URL: {{ .Values.redis.url | b64enc | quote }}
{{- end }}
{{- end }}

{{- define "rota.redisEnabled" -}}
{{- if or .Values.redis.url .Values.redis.existingSecret }}true{{ end }}
{{- end }}
