{{/*
Common template helpers for the mindmesh-service chart.
*/}}

{{/* Short name (chart name unless overridden). */}}
{{- define "mindmesh-service.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Fully qualified app name. The umbrella sets fullnameOverride per alias so each
service gets a stable, collision-free Service DNS name (identity, collection, ...).
*/}}
{{- define "mindmesh-service.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{- define "mindmesh-service.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* Selector labels — stable across upgrades, must not include version. */}}
{{- define "mindmesh-service.selectorLabels" -}}
app.kubernetes.io/name: {{ include "mindmesh-service.fullname" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/* Full label set for metadata. */}}
{{- define "mindmesh-service.labels" -}}
helm.sh/chart: {{ include "mindmesh-service.chart" . }}
{{ include "mindmesh-service.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: mindmesh
{{- end -}}

{{- define "mindmesh-service.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "mindmesh-service.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/* Container image ref, tag defaulting to the chart appVersion. */}}
{{- define "mindmesh-service.image" -}}
{{- $tag := .Values.image.tag | default .Chart.AppVersion -}}
{{- if .Values.image.registry -}}
{{- printf "%s/%s:%s" .Values.image.registry .Values.image.repository $tag -}}
{{- else -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}
{{- end -}}

{{/* Migration image ref. */}}
{{- define "mindmesh-service.migrateImage" -}}
{{- $tag := .Values.migrations.image.tag | default .Chart.AppVersion -}}
{{- if .Values.migrations.image.registry -}}
{{- printf "%s/%s:%s" .Values.migrations.image.registry .Values.migrations.image.repository $tag -}}
{{- else -}}
{{- printf "%s:%s" .Values.migrations.image.repository $tag -}}
{{- end -}}
{{- end -}}

{{/*
Merged non-secret env: global.env (shared, from the umbrella) as the base, with
this service's .Values.env taking precedence. Returns a YAML map.
*/}}
{{- define "mindmesh-service.mergedEnv" -}}
{{- $global := dict -}}
{{- if .Values.global -}}
{{- if .Values.global.env -}}
{{- $global = .Values.global.env -}}
{{- end -}}
{{- end -}}
{{- $merged := merge (deepCopy (default (dict) .Values.env)) $global -}}
{{- toYaml $merged -}}
{{- end -}}

{{/* Name of the env Secret this service reads (created here or by ESO). */}}
{{- define "mindmesh-service.secretName" -}}
{{- printf "%s-env" (include "mindmesh-service.fullname" .) -}}
{{- end -}}

{{/* Whether an env Secret exists to envFrom (either templated or from ESO). */}}
{{- define "mindmesh-service.hasEnvSecret" -}}
{{- if or (and .Values.secret.create .Values.secret.data) .Values.externalSecrets.enabled -}}true{{- end -}}
{{- end -}}
